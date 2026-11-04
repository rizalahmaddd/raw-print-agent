package api

import (
	"errors"
	"testing"

	"github.com/rizalahmaddd/raw-print-agent/internal/config"
)

const self = "http://127.0.0.1:18310"

// The settings page calls .join on this list, so null breaks a fresh install
// that has no origins yet.
func TestAdminStateOriginsNeverNull(t *testing.T) {
	e := newEnv(t, false)
	e.store.Update(func(c *config.Config) error { c.AllowedOrigins = nil; return nil })
	if _, m := e.do("GET", "/admin/state", self, "", nil); m["allowed_origins"] == nil {
		t.Fatal("allowed_origins is null")
	}
}

func TestAutoStartHiddenWhenNotAService(t *testing.T) {
	e := newEnv(t, false)
	_, m := e.do("GET", "/admin/state", self, "", nil)
	if _, ok := m["auto_start"]; ok {
		t.Fatalf("auto_start should be absent outside a service: %v", m["auto_start"])
	}
	if w, _ := e.do("PUT", "/admin/auto-start", self, "", map[string]bool{"enabled": false}); w.Code != 404 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestAutoStartToggle(t *testing.T) {
	e := newEnv(t, false)
	on := true
	e.srv.AutoStart = &AutoStart{
		Enabled: func() (bool, error) { return on, nil },
		Set:     func(v bool) error { on = v; return nil },
	}
	if _, m := e.do("GET", "/admin/state", self, "", nil); m["auto_start"] != true {
		t.Fatalf("got %v", m["auto_start"])
	}
	w, m := e.do("PUT", "/admin/auto-start", self, "", map[string]bool{"enabled": false})
	if w.Code != 200 || m["auto_start"] != false || on {
		t.Fatalf("got %d %v on=%v", w.Code, m["auto_start"], on)
	}
	if w, _ := e.do("PUT", "/admin/auto-start", self, "", map[string]any{}); w.Code != 400 || on {
		t.Fatalf("missing enabled: got %d on=%v", w.Code, on)
	}
	if w, _ := e.do("PUT", "/admin/auto-start", app, "", map[string]bool{"enabled": true}); w.Code != 403 || on {
		t.Fatalf("web app must not change it: got %d on=%v", w.Code, on)
	}
}

func TestAutoStartErrors(t *testing.T) {
	e := newEnv(t, false)
	boom := errors.New("access denied")
	e.srv.AutoStart = &AutoStart{
		Enabled: func() (bool, error) { return false, boom },
		Set:     func(bool) error { return boom },
	}
	if _, m := e.do("GET", "/admin/state", self, "", nil); m["auto_start_error"] != boom.Error() {
		t.Fatalf("got %v", m["auto_start_error"])
	}
	if w, _ := e.do("PUT", "/admin/auto-start", self, "", map[string]bool{"enabled": true}); w.Code != 500 {
		t.Fatalf("got %d", w.Code)
	}
}
