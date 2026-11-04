package jobs

import (
	"errors"
	"testing"
	"time"

	"github.com/rizalahmaddd/raw-print-agent/internal/spool"
)

type flaky struct {
	spool.FileSpooler
	fail  bool
	calls int
}

func (f *flaky) Submit(p, d string, b []byte) (uint32, error) {
	f.calls++
	if f.fail {
		return 0, errors.New("printer offline")
	}
	return uint32(f.calls), nil
}

func (f *flaky) Job(string, uint32) (spool.JobInfo, error) {
	return spool.JobInfo{State: spool.StatePrinting}, nil
}

func TestFailedJobCanBeRetriedWithSameID(t *testing.T) {
	sp := &flaky{fail: true}
	m := NewManager(sp)
	if _, _, err := m.Submit(Request{ID: "a", Printer: "p", Data: []byte("x")}); err == nil {
		t.Fatal("expected error")
	}
	sp.fail = false
	j, dup, err := m.Submit(Request{ID: "a", Printer: "p", Data: []byte("x")})
	if err != nil || dup || sp.calls != 2 {
		t.Fatalf("retry after failure must print: dup=%v err=%v calls=%d", dup, err, sp.calls)
	}
	if got, _ := m.Get(j.ID); got.State != spool.StatePrinting {
		t.Fatalf("state not refreshed: %v", got.State)
	}
}

func TestDedupWindowExpires(t *testing.T) {
	sp := &flaky{}
	m := NewManager(sp)
	now := time.Now()
	m.now = func() time.Time { return now }
	m.Submit(Request{ID: "a", Printer: "p", Data: []byte("x")})
	if _, dup, _ := m.Submit(Request{ID: "a", Printer: "p", Data: []byte("x")}); !dup {
		t.Fatal("expected duplicate")
	}
	now = now.Add(DedupWindow + time.Second)
	if _, dup, _ := m.Submit(Request{ID: "a", Printer: "p", Data: []byte("x")}); dup {
		t.Fatal("dedup must expire")
	}
	if sp.calls != 2 {
		t.Fatalf("calls=%d", sp.calls)
	}
}

func TestValidation(t *testing.T) {
	m := NewManager(&flaky{})
	for _, r := range []Request{{Printer: "p", Data: []byte("x")}, {ID: "a", Printer: "p"}, {ID: "a", Printer: "p", Data: []byte("x"), Copies: 11}} {
		if _, _, err := m.Submit(r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: expected ErrInvalid, got %v", r, err)
		}
	}
}
