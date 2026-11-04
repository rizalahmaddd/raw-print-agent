package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rizalahmaddd/raw-print-agent/internal/config"
	"github.com/rizalahmaddd/raw-print-agent/internal/spool"
)

const app = "https://pos.example.com"

type env struct {
	t     *testing.T
	srv   *Server
	h     http.Handler
	store *config.Store
	dir   string
}

func newEnv(t *testing.T, auto bool) *env {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) error {
		c.AllowedOrigins = []string{app}
		c.AutoApprovePairing = auto
		c.DefaultPrinterMatch = "lx-310"
		c.MaxPayloadBytes = 1024
		return nil
	})
	sp := spool.NewFileSpooler(filepath.Join(dir, "out"), "EPSON LX-310", "Microsoft Print to PDF")
	srv, err := New(store, sp, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, srv: srv, h: srv.Handler(), store: store, dir: dir}
}

func (e *env) do(method, path, origin, token string, body any, hdr ...string) (*httptest.ResponseRecorder, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:18310"+path, rd)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if token != "" {
		r.Header.Set("X-Agent-Token", token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func (e *env) pair() string {
	e.t.Helper()
	w, m := e.do("POST", "/pair", app, "", map[string]string{"name": "POS"})
	if w.Code != 200 || m["token"] == "" {
		e.t.Fatalf("auto pair failed: %d %s", w.Code, w.Body)
	}
	return m["token"].(string)
}

func TestRejectsForeignOriginAndHost(t *testing.T) {
	e := newEnv(t, true)
	if w, _ := e.do("GET", "/health", "https://evil.example", "", nil); w.Code != 403 {
		t.Fatalf("foreign origin: got %d", w.Code)
	}
	r := httptest.NewRequest("GET", "http://attacker.example:18310/health", nil)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("rebinding host: got %d", w.Code)
	}
}

func TestPreflightPrivateNetwork(t *testing.T) {
	e := newEnv(t, true)
	w, _ := e.do("OPTIONS", "/print", app, "", nil,
		"Access-Control-Request-Method", "POST", "Access-Control-Request-Private-Network", "true")
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Private-Network") != "true" ||
		w.Header().Get("Access-Control-Allow-Origin") != app {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
}

func TestPrintRequiresToken(t *testing.T) {
	e := newEnv(t, true)
	if w, _ := e.do("POST", "/print", app, "", map[string]any{"job_id": "a", "data_base64": "QQ=="}); w.Code != 401 {
		t.Fatalf("got %d", w.Code)
	}
	if w, _ := e.do("POST", "/print", app, "bogus", map[string]any{"job_id": "a", "data_base64": "QQ=="}); w.Code != 401 {
		t.Fatalf("bogus token: got %d", w.Code)
	}
}

func TestTokenBoundToOrigin(t *testing.T) {
	e := newEnv(t, true)
	tok := e.pair()
	e.store.Update(func(c *config.Config) error {
		c.AllowedOrigins = append(c.AllowedOrigins, "https://other.example.com")
		return nil
	})
	if w, _ := e.do("GET", "/printers", "https://other.example.com", tok, nil); w.Code != 401 {
		t.Fatalf("token reused from another origin: got %d", w.Code)
	}
	if w, _ := e.do("GET", "/printers", app, tok, nil); w.Code != 200 {
		t.Fatalf("own origin: got %d", w.Code)
	}
}

func TestManualPairingFlow(t *testing.T) {
	e := newEnv(t, false)
	w, m := e.do("POST", "/pair", app, "", map[string]string{"name": "Gudang 1"})
	if w.Code != 202 || len(m["code"].(string)) != 6 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	id := m["request_id"].(string)

	if _, m := e.do("GET", "/pair/"+id, app, "", nil); m["status"] != "pending" {
		t.Fatalf("expected pending, got %v", m)
	}
	if w, _ := e.do("GET", "/pair/"+id, "https://other.example.com", "", nil); w.Code != 403 {
		t.Fatalf("other origin must not see the request: %d", w.Code)
	}
	// a remote page cannot approve its own request
	if w, _ := e.do("POST", "/admin/pair/"+id+"/approve", app, "", nil); w.Code != 403 {
		t.Fatalf("remote approve: got %d", w.Code)
	}
	if w, _ := e.do("POST", "/admin/pair/"+id+"/approve", "http://127.0.0.1:18310", "", nil); w.Code != 200 {
		t.Fatalf("local approve: %d %s", w.Code, w.Body)
	}
	_, m = e.do("GET", "/pair/"+id, app, "", nil)
	tok, _ := m["token"].(string)
	if m["status"] != "approved" || tok == "" {
		t.Fatalf("approved poll: %v", m)
	}
	if w, _ := e.do("GET", "/pair/"+id, app, "", nil); w.Code != 404 {
		t.Fatalf("token must be handed out once, got %d", w.Code)
	}
	if w, _ := e.do("GET", "/printers", app, tok, nil); w.Code != 200 {
		t.Fatalf("token works: %d", w.Code)
	}
	raw, _ := os.ReadFile(e.store.Path())
	if strings.Contains(string(raw), tok) {
		t.Fatal("plaintext token stored in config")
	}
}

func TestAdminBlocksCrossSite(t *testing.T) {
	e := newEnv(t, true)
	if w, _ := e.do("GET", "/admin/state", "", "", nil, "Sec-Fetch-Site", "cross-site"); w.Code != 403 {
		t.Fatalf("cross-site GET: %d", w.Code)
	}
	if w, _ := e.do("GET", "/admin/state", "", "", nil, "Sec-Fetch-Site", "same-origin"); w.Code != 200 {
		t.Fatalf("same-origin GET: %d", w.Code)
	}
	if w, _ := e.do("PUT", "/admin/settings", app, "", map[string]any{"allowed_origins": []string{"https://evil.example"}}); w.Code != 403 {
		t.Fatalf("remote settings change: %d", w.Code)
	}
}

func TestPrintDedupAndFileOutput(t *testing.T) {
	e := newEnv(t, true)
	tok := e.pair()
	data := []byte("\x1b@HELLO\r\n\x0c")
	body := map[string]any{"job_id": "si-1", "doc_name": "SI-0001", "data_base64": base64.StdEncoding.EncodeToString(data), "copies": 2}

	w, m := e.do("POST", "/print", app, tok, body)
	if w.Code != 202 || m["duplicate"] != false {
		t.Fatalf("print: %d %s", w.Code, w.Body)
	}
	job := m["job"].(map[string]any)
	if job["printer"] != "EPSON LX-310" {
		t.Fatalf("printer match: %v", job["printer"])
	}
	if _, m := e.do("POST", "/print", app, tok, body); m["duplicate"] != true {
		t.Fatalf("second submit must be deduplicated: %v", m)
	}
	files, _ := filepath.Glob(filepath.Join(e.dir, "out", "*.prn"))
	if len(files) != 1 {
		t.Fatalf("expected 1 spooled file, got %d", len(files))
	}
	got, _ := os.ReadFile(files[0])
	if !bytes.Equal(got, append(append([]byte{}, data...), data...)) {
		t.Fatalf("payload altered: %q", got)
	}
	if _, m := e.do("GET", "/jobs/si-1", app, tok, nil); m["job"].(map[string]any)["state"] != "done" {
		t.Fatalf("job state: %v", m)
	}
}

func TestPayloadLimitAndUnknownPrinter(t *testing.T) {
	e := newEnv(t, true)
	tok := e.pair()
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("A"), 2000))
	if w, _ := e.do("POST", "/print", app, tok, map[string]any{"job_id": "x", "data_base64": big}); w.Code != 413 {
		t.Fatalf("limit: %d", w.Code)
	}
	if w, _ := e.do("POST", "/print", app, tok, map[string]any{"job_id": "y", "printer": "Nope", "data_base64": "QQ=="}); w.Code != 404 {
		t.Fatalf("unknown printer: %d", w.Code)
	}
}

func TestProfileRoundTrip(t *testing.T) {
	e := newEnv(t, true)
	tok := e.pair()
	p := map[string]any{"page_height_mm": 279.4, "top_margin_lines": 2, "left_margin_cols": 1, "quality": "draft", "cpi": 12}
	if w, _ := e.do("PUT", "/profile", app, tok, map[string]any{"profile": p}); w.Code != 200 {
		t.Fatalf("put: %d", w.Code)
	}
	_, m := e.do("GET", "/profile", app, tok, nil)
	if m["profile"].(map[string]any)["top_margin_lines"] != float64(2) {
		t.Fatalf("profile: %v", m)
	}
	bad := map[string]any{"page_height_mm": 279.4, "top_margin_lines": 2, "left_margin_cols": 1, "quality": "best", "cpi": 12}
	if w, _ := e.do("PUT", "/profile", app, tok, map[string]any{"profile": bad}); w.Code != 400 {
		t.Fatalf("validation: %d", w.Code)
	}
}
