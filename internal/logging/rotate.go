package logging

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DailyFile writes to <dir>/agent-YYYY-MM-DD.log and removes files older than
// the retention period whenever the day changes.
type DailyFile struct {
	dir  string
	keep int
	mu   sync.Mutex
	day  string
	f    *os.File
}

func NewDailyFile(dir string, keepDays int) (*DailyFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DailyFile{dir: dir, keep: keepDays}, nil
}

func (d *DailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := time.Now().Format("2006-01-02")
	if d.f == nil || day != d.day {
		if d.f != nil {
			d.f.Close()
		}
		f, err := os.OpenFile(filepath.Join(d.dir, "agent-"+day+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		d.f, d.day = f, day
		d.cleanup()
	}
	return d.f.Write(p)
}

func (d *DailyFile) cleanup() {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -d.keep).Format("2006-01-02")
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		if day := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".log"); day < cutoff {
			os.Remove(filepath.Join(d.dir, name))
		}
	}
}

func (d *DailyFile) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return nil
	}
	return d.f.Close()
}
