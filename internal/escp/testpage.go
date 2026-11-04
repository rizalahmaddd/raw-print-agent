// Package escp builds the calibration page for Epson ESC/P dot matrix printers.
package escp

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rizalahmaddd/raw-print-agent/internal/config"
)

const (
	esc = 0x1b
	ff  = 0x0c
	si  = 0x0f
)

// Columns returns the printable width in characters for an 8 inch carriage.
func Columns(cpi int) int {
	switch cpi {
	case 10:
		return 80
	case 17:
		return 137
	default:
		return 96
	}
}

// PageLayout converts the physical page height to 1/6" lines plus the leftover
// in 1/216" units. Page lengths that are not a whole number of lines cannot use
// ESC C + FF without drifting, so the remainder is fed with ESC J.
func PageLayout(heightMM float64) (lines int, remainder216 int) {
	units := int(math.Round(heightMM / 25.4 * 216))
	return units / 36, units % 36
}

func Init(p config.Profile) []byte {
	var b bytes.Buffer
	b.Write([]byte{esc, '@'})
	if p.Quality == "nlq" {
		b.Write([]byte{esc, 'x', 1})
	} else {
		b.Write([]byte{esc, 'x', 0})
	}
	b.Write([]byte{esc, '2'})
	if lines, rem := PageLayout(p.PageHeightMM); rem == 0 && lines > 0 && lines < 128 {
		b.Write([]byte{esc, 'C', byte(lines)})
	}
	switch p.CPI {
	case 10:
		b.Write([]byte{esc, 'P'})
	case 17:
		b.Write([]byte{esc, 'P', si})
	default:
		b.Write([]byte{esc, 'M'})
	}
	return b.Bytes()
}

// EndPage moves the paper to the next perforation after `used` lines.
func EndPage(p config.Profile, used int) []byte {
	lines, rem := PageLayout(p.PageHeightMM)
	if rem == 0 && lines < 128 {
		return []byte{ff}
	}
	left := (lines-used)*36 + rem
	var b bytes.Buffer
	for left > 0 {
		n := min(left, 255)
		b.Write([]byte{esc, 'J', byte(n)})
		left -= n
	}
	return b.Bytes()
}

// TestPage prints a column ruler and numbered lines so the operator can check
// the left offset, the top offset and that the page ends on the perforation.
func TestPage(p config.Profile, printer string, now time.Time) []byte {
	cols := Columns(p.CPI) - p.LeftMarginCols
	lines, _ := PageLayout(p.PageHeightMM)
	pad := strings.Repeat(" ", p.LeftMarginCols)

	var b bytes.Buffer
	b.Write(Init(p))
	used := 0
	line := func(s string) {
		if len(s) > cols {
			s = s[:cols]
		}
		b.WriteString(pad + s + "\r\n")
		used++
	}
	for i := 0; i < p.TopMarginLines; i++ {
		b.WriteString("\r\n")
		used++
	}

	line(fmt.Sprintf("RAWPRINT TEST PAGE  %s", now.Format("2006-01-02 15:04")))
	line("Printer: " + ascii(printer))
	line(fmt.Sprintf("Height %.1f mm (%d lines)  CPI %d (%d cols)  %s  top %d  left %d",
		p.PageHeightMM, lines, p.CPI, Columns(p.CPI), strings.ToUpper(p.Quality), p.TopMarginLines, p.LeftMarginCols))

	var tens, units strings.Builder
	for c := 1; c <= cols; c++ {
		if c%10 == 0 {
			tens.WriteString(fmt.Sprint((c / 10) % 10))
		} else {
			tens.WriteByte(' ')
		}
		units.WriteString(fmt.Sprint(c % 10))
	}
	line(tens.String())
	line(units.String())
	line(strings.Repeat("-", cols))

	last := lines - 1
	for used < last {
		n := used + 1
		s := fmt.Sprintf("%02d", n)
		if n == last {
			s += "  last line: the next line feed should land on the perforation"
		}
		if len(s) < cols {
			s += strings.Repeat(" ", cols-len(s)-1) + "|"
		}
		line(s)
	}
	b.Write(EndPage(p, used))
	return b.Bytes()
}

func ascii(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}
