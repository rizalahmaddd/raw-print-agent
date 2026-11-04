package escp

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rizalahmaddd/raw-print-agent/internal/config"
)

func TestPageLayout(t *testing.T) {
	cases := []struct {
		mm        float64
		lines, rm int
	}{
		{279.4, 66, 0},
		{139.7, 33, 0},
		{158, 37, 12},
	}
	for _, c := range cases {
		l, r := PageLayout(c.mm)
		if l != c.lines || r != c.rm {
			t.Errorf("%.1fmm: got %d lines + %d/216, want %d + %d", c.mm, l, r, c.lines, c.rm)
		}
	}
}

func TestTestPageFillsExactlyOnePage(t *testing.T) {
	p := config.DefaultProfile()
	out := TestPage(p, "EPSON LX-310", time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC))

	if !bytes.HasPrefix(out, []byte{esc, '@'}) || !bytes.Contains(out, []byte{esc, 'C', 66}) {
		t.Fatal("missing init or page length")
	}
	if out[len(out)-1] != ff {
		t.Fatal("11 inch page must end with a form feed")
	}
	if n := bytes.Count(out, []byte("\r\n")); n != 65 {
		t.Fatalf("expected 65 line feeds before FF, got %d", n)
	}
	body := strings.TrimSuffix(string(bytes.TrimPrefix(out, Init(p))), "\f")
	for _, l := range strings.Split(body, "\r\n") {
		if len(l) > 96 {
			t.Fatalf("line wider than 96 columns: %q", l)
		}
	}
}

func TestOddHeightUsesExactFeed(t *testing.T) {
	p := config.DefaultProfile()
	p.PageHeightMM = 158
	out := TestPage(p, "x", time.Now())
	if bytes.Contains(out, []byte{esc, 'C'}) {
		t.Fatal("ESC C must not be used when the height is not a whole number of lines")
	}
	end := EndPage(p, 36)
	if !bytes.Equal(end, []byte{esc, 'J', 48}) {
		t.Fatalf("remainder feed: %v", end)
	}
}
