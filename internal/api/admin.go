package api

import (
	"net/http"
	"strings"

	"github.com/rizalahmaddd/raw-print-agent/internal/config"
	"github.com/rizalahmaddd/raw-print-agent/internal/spool"
)

type tokenView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Origin     string `json:"origin"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

func (s *Server) handleAdminState(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	printers, perr := s.spool.Printers()
	if printers == nil {
		printers = []spool.Printer{}
	}
	resolved, rerr := s.resolvePrinter("")
	tokens := make([]tokenView, 0, len(cfg.Tokens))
	for _, t := range cfg.Tokens {
		v := tokenView{ID: t.ID, Name: t.Name, Origin: t.Origin, CreatedAt: t.CreatedAt.Local().Format("2006-01-02 15:04")}
		if !t.LastUsedAt.IsZero() {
			v.LastUsedAt = t.LastUsedAt.Local().Format("2006-01-02 15:04")
		}
		tokens = append(tokens, v)
	}
	origins := cfg.AllowedOrigins
	if origins == nil {
		origins = []string{}
	}
	out := map[string]any{
		"version":               s.version,
		"config_path":           s.store.Path(),
		"allowed_origins":       origins,
		"auto_approve_pairing":  cfg.AutoApprovePairing,
		"default_printer":       cfg.DefaultPrinter,
		"default_printer_match": cfg.DefaultPrinterMatch,
		"resolved_printer":      resolved,
		"profile":               cfg.ProfileFor(resolved),
		"default_profile":       config.DefaultProfile(),
		"printers":              printers,
		"tokens":                tokens,
		"pending_pairings":      s.pendingPairs(),
		"recent_jobs":           s.jobs.Recent(20),
	}
	if perr != nil {
		out["printers_error"] = perr.Error()
	}
	if rerr != nil {
		out["resolve_error"] = rerr.Error()
	}
	if s.AutoStart != nil {
		if on, err := s.AutoStart.Enabled(); err != nil {
			out["auto_start_error"] = err.Error()
		} else {
			out["auto_start"] = on
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	if err := s.store.Update(func(c *config.Config) error { found = c.RevokeToken(id); return nil }); err != nil {
		writeErr(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "token_not_found", "unknown token")
		return
	}
	s.log.Info("token revoked", "token_id", id)
	w.WriteHeader(http.StatusNoContent)
}

type settingsReq struct {
	AllowedOrigins      []string `json:"allowed_origins"`
	AutoApprovePairing  bool     `json:"auto_approve_pairing"`
	DefaultPrinter      string   `json:"default_printer"`
	DefaultPrinterMatch string   `json:"default_printer_match"`
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsReq
	if !readJSON(w, r, &req, 16<<10) {
		return
	}
	origins := []string{}
	for _, o := range req.AllowedOrigins {
		if strings.TrimSpace(o) == "" {
			continue
		}
		n, err := config.NormalizeOrigin(o)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_origin", err.Error())
			return
		}
		origins = append(origins, n)
	}
	if err := s.store.Update(func(c *config.Config) error {
		c.AllowedOrigins = origins
		c.AutoApprovePairing = req.AutoApprovePairing
		c.DefaultPrinter = strings.TrimSpace(req.DefaultPrinter)
		c.DefaultPrinterMatch = strings.TrimSpace(req.DefaultPrinterMatch)
		return nil
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}
	s.log.Info("settings updated", "origins", strings.Join(origins, ","), "default_printer", req.DefaultPrinter)
	s.handleAdminState(w, r)
}

func (s *Server) handleAdminAutoStart(w http.ResponseWriter, r *http.Request) {
	if s.AutoStart == nil {
		writeErr(w, http.StatusNotFound, "auto_start_unavailable", "auto start is only available when running as a Windows service")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(w, r, &req, 4<<10) {
		return
	}
	if req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "enabled is required")
		return
	}
	if err := s.AutoStart.Set(*req.Enabled); err != nil {
		s.log.Error("auto start update failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "service_error", err.Error())
		return
	}
	s.log.Info("auto start updated", "enabled", *req.Enabled)
	s.handleAdminState(w, r)
}

func (s *Server) handleAdminProfile(w http.ResponseWriter, r *http.Request) {
	s.saveProfile(w, r)
}

func (s *Server) handleAdminTestPrint(w http.ResponseWriter, r *http.Request) {
	s.testPrint(w, r, "local")
}
