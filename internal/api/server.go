package api

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rizalahmaddd/raw-print-agent/internal/config"
	"github.com/rizalahmaddd/raw-print-agent/internal/escp"
	"github.com/rizalahmaddd/raw-print-agent/internal/jobs"
	"github.com/rizalahmaddd/raw-print-agent/internal/spool"
)

//go:embed ui/index.html
var indexHTML []byte

type Server struct {
	store   *config.Store
	spool   spool.Spooler
	jobs    *jobs.Manager
	pairs   *pairings
	log     *slog.Logger
	version string
	port    string
	now     func() time.Time

	// AutoStart is nil when the agent does not run as a Windows service.
	AutoStart *AutoStart
}

type AutoStart struct {
	Enabled func() (bool, error)
	Set     func(enabled bool) error
}

func New(store *config.Store, sp spool.Spooler, log *slog.Logger, version string) (*Server, error) {
	_, port, err := net.SplitHostPort(store.Snapshot().Listen)
	if err != nil {
		return nil, err
	}
	return &Server{
		store: store, spool: sp, jobs: jobs.NewManager(sp), pairs: newPairings(),
		log: log, version: version, port: port, now: time.Now,
	}, nil
}

type ctxKind int

const (
	kindNone   ctxKind = iota // no Origin: curl or another local program
	kindSelf                  // the agent's own settings page
	kindRemote                // an allowlisted web app
)

type reqCtx struct {
	origin string
	kind   ctxKind
	token  *config.Token
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.app(false, s.handleHealth))
	mux.HandleFunc("POST /pair", s.app(false, s.handlePairStart))
	mux.HandleFunc("GET /pair/{id}", s.app(false, s.handlePairPoll))
	mux.HandleFunc("GET /printers", s.app(true, s.handlePrinters))
	mux.HandleFunc("GET /profile", s.app(true, s.handleProfileGet))
	mux.HandleFunc("PUT /profile", s.app(true, s.handleProfilePut))
	mux.HandleFunc("POST /print", s.app(true, s.handlePrint))
	mux.HandleFunc("GET /jobs/{id}", s.app(true, s.handleJob))
	mux.HandleFunc("POST /test-print", s.app(true, s.handleTestPrint))

	mux.HandleFunc("GET /{$}", s.admin(s.handleIndex))
	mux.HandleFunc("GET /admin/state", s.admin(s.handleAdminState))
	mux.HandleFunc("POST /admin/pair/{id}/approve", s.admin(s.handleAdminApprove))
	mux.HandleFunc("POST /admin/pair/{id}/reject", s.admin(s.handleAdminReject))
	mux.HandleFunc("DELETE /admin/tokens/{id}", s.admin(s.handleAdminRevoke))
	mux.HandleFunc("PUT /admin/settings", s.admin(s.handleAdminSettings))
	mux.HandleFunc("PUT /admin/auto-start", s.admin(s.handleAdminAutoStart))
	mux.HandleFunc("PUT /admin/profile", s.admin(s.handleAdminProfile))
	mux.HandleFunc("POST /admin/test-print", s.admin(s.handleAdminTestPrint))

	return s.guard(mux)
}

// guard runs before routing: Host check against DNS rebinding, Origin
// allowlist, CORS and Chrome's Private/Local Network Access preflight.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			writeErr(w, http.StatusMisdirectedRequest, "bad_host", "requests must target 127.0.0.1 or localhost")
			return
		}
		origin := r.Header.Get("Origin")
		cfg := s.store.Snapshot()
		allowed := origin != "" && cfg.OriginAllowed(origin)
		if origin != "" && !allowed && !s.isSelf(origin) {
			s.log.Warn("rejected origin", "origin", origin, "path", r.URL.Path)
			writeErr(w, http.StatusForbidden, "origin_not_allowed", "this origin is not in the agent allowlist")
			return
		}
		if allowed {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Content-Type, X-Agent-Token")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				h.Set("Access-Control-Allow-Private-Network", "true")
			}
		}
		if r.Method == http.MethodOptions {
			if !allowed {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostAllowed(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil || p != s.port {
		return false
	}
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func (s *Server) isSelf(origin string) bool {
	for _, h := range []string{"127.0.0.1", "localhost", "[::1]"} {
		if strings.EqualFold(origin, "http://"+h+":"+s.port) {
			return true
		}
	}
	return false
}

func (s *Server) app(needToken bool, fn func(http.ResponseWriter, *http.Request, *reqCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rc := &reqCtx{origin: r.Header.Get("Origin")}
		switch {
		case rc.origin == "":
			rc.kind = kindNone
		case s.isSelf(rc.origin):
			writeErr(w, http.StatusForbidden, "use_admin_api", "the settings page uses /admin endpoints")
			return
		default:
			rc.kind = kindRemote
		}
		if t, ok := s.tokenFor(r, rc.origin); ok {
			rc.token = &t
		}
		if needToken && rc.token == nil {
			writeErr(w, http.StatusUnauthorized, "not_paired", "pair with the agent first (POST /pair)")
			return
		}
		fn(w, r, rc)
	}
}

// admin accepts only the agent's own page (or a non-browser local client).
// Sec-Fetch-Site blocks cross-site GETs that carry no Origin header.
func (s *Server) admin(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		site := r.Header.Get("Sec-Fetch-Site")
		if (origin != "" && !s.isSelf(origin)) || site == "cross-site" || site == "same-site" {
			writeErr(w, http.StatusForbidden, "forbidden", "admin endpoints are only available from the local settings page")
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		fn(w, r)
	}
}

func (s *Server) tokenFor(r *http.Request, origin string) (config.Token, bool) {
	secret := r.Header.Get("X-Agent-Token")
	if secret == "" {
		secret = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if secret == "" {
		return config.Token{}, false
	}
	cfg := s.store.Snapshot()
	i, ok := cfg.FindToken(secret)
	if !ok {
		return config.Token{}, false
	}
	t := cfg.Tokens[i]
	// A token only works from the origin it was issued to, so a leaked token
	// cannot be replayed from another allowlisted site.
	if origin != "" && !strings.EqualFold(origin, t.Origin) {
		return config.Token{}, false
	}
	if s.now().Sub(t.LastUsedAt) > time.Hour {
		_ = s.store.Update(func(c *config.Config) error {
			for k := range c.Tokens {
				if c.Tokens[k].ID == t.ID {
					c.Tokens[k].LastUsedAt = s.now().UTC()
				}
			}
			return nil
		})
	}
	return t, true
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *Server) resolvePrinter(explicit string) (string, error) {
	cfg := s.store.Snapshot()
	return spool.ResolvePrinter(s.spool, explicit, cfg.DefaultPrinter, cfg.DefaultPrinterMatch)
}

func (s *Server) printerInfo(name string) *spool.Printer {
	list, err := s.spool.Printers()
	if err != nil {
		return nil
	}
	for _, p := range list {
		if strings.EqualFold(p.Name, name) {
			cp := p
			return &cp
		}
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	out := map[string]any{"name": "rawprint-agent", "version": s.version, "paired": rc.token != nil}
	if rc.token != nil {
		if name, err := s.resolvePrinter(""); err == nil {
			out["printer"] = s.printerInfo(name)
			out["profile"] = s.store.Snapshot().ProfileFor(name)
		} else {
			out["printer_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePrinters(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	list, err := s.spool.Printers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "spooler_error", err.Error())
		return
	}
	def, _ := s.resolvePrinter("")
	writeJSON(w, http.StatusOK, map[string]any{"printers": list, "default": def})
}

func (s *Server) handleProfileGet(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	name, err := s.resolvePrinter(r.URL.Query().Get("printer"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "printer_not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"printer": name, "profile": s.store.Snapshot().ProfileFor(name)})
}

type profileReq struct {
	Printer string         `json:"printer"`
	Profile config.Profile `json:"profile"`
}

func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var req profileReq
	if !readJSON(w, r, &req, 16<<10) {
		return
	}
	if err := req.Profile.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_profile", err.Error())
		return
	}
	name, err := s.resolvePrinter(req.Printer)
	if err != nil {
		writeErr(w, http.StatusNotFound, "printer_not_found", err.Error())
		return
	}
	if err := s.store.Update(func(c *config.Config) error { c.Profiles[name] = req.Profile; return nil }); err != nil {
		writeErr(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}
	s.log.Info("profile updated", "printer", name)
	writeJSON(w, http.StatusOK, map[string]any{"printer": name, "profile": req.Profile})
}

func (s *Server) handleProfilePut(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.saveProfile(w, r)
}

type printReq struct {
	JobID      string `json:"job_id"`
	Printer    string `json:"printer"`
	DocName    string `json:"doc_name"`
	DataBase64 string `json:"data_base64"`
	Copies     int    `json:"copies"`
}

func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	limit := s.store.Snapshot().MaxPayloadBytes
	var req printReq
	if !readJSON(w, r, &req, limit*4/3+8<<10) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_payload", "data_base64 is not valid base64")
		return
	}
	if int64(len(data)) > limit {
		writeErr(w, http.StatusRequestEntityTooLarge, "payload_too_large", fmt.Sprintf("payload exceeds %d bytes", limit))
		return
	}
	printer, err := s.resolvePrinter(req.Printer)
	if err != nil {
		writeErr(w, http.StatusNotFound, "printer_not_found", err.Error())
		return
	}
	s.submit(w, jobs.Request{ID: req.JobID, Printer: printer, DocName: req.DocName, Data: data, Copies: req.Copies, Origin: rc.origin})
}

func (s *Server) submit(w http.ResponseWriter, jr jobs.Request) {
	job, dup, err := s.jobs.Submit(jr)
	switch {
	case errors.Is(err, jobs.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid_job", err.Error())
	case err != nil:
		s.log.Error("print failed", "job_id", jr.ID, "printer", jr.Printer, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"code": "spooler_error", "message": err.Error()}, "job": job,
		})
	default:
		if !dup {
			s.log.Info("job submitted", "job_id", job.ID, "printer", job.Printer, "bytes", job.Bytes,
				"copies", job.Copies, "spool_id", job.SpoolID, "origin", job.Origin)
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "duplicate": dup})
	}
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "job_not_found", "unknown job_id")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

type testReq struct {
	Printer string `json:"printer"`
}

func (s *Server) testPrint(w http.ResponseWriter, r *http.Request, origin string) {
	var req testReq
	if r.ContentLength != 0 && !readJSON(w, r, &req, 4<<10) {
		return
	}
	printer, err := s.resolvePrinter(req.Printer)
	if err != nil {
		writeErr(w, http.StatusNotFound, "printer_not_found", err.Error())
		return
	}
	prof := s.store.Snapshot().ProfileFor(printer)
	data := escp.TestPage(prof, printer, s.now())
	s.submit(w, jobs.Request{ID: "test-" + config.RandomID(), Printer: printer, DocName: "RawPrint test page", Data: data, Origin: origin})
}

func (s *Server) handleTestPrint(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.testPrint(w, r, rc.origin)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		} else {
			writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
