package spool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Printer struct {
	Name    string `json:"name"`
	Driver  string `json:"driver,omitempty"`
	Port    string `json:"port,omitempty"`
	Status  string `json:"status"`
	Offline bool   `json:"offline"`
	Jobs    int    `json:"jobs"`
}

type JobState string

const (
	StateQueued   JobState = "queued"
	StatePrinting JobState = "printing"
	StateDone     JobState = "done"
	StateError    JobState = "error"
)

type JobInfo struct {
	State  JobState `json:"state"`
	Detail string   `json:"detail,omitempty"`
}

var ErrPrinterNotFound = errors.New("printer not found")

type Spooler interface {
	Printers() ([]Printer, error)
	SystemDefault() (string, error)
	// Submit sends data unchanged as one RAW job and returns the spooler job id.
	Submit(printer, docName string, data []byte) (uint32, error)
	Job(printer string, id uint32) (JobInfo, error)
}

// FileSpooler writes every job to a .prn file. It backs development and tests
// on machines without a Windows spooler.
type FileSpooler struct {
	Dir      string
	Names    []string
	mu       sync.Mutex
	next     uint32
	finished map[uint32]bool
}

func NewFileSpooler(dir string, printers ...string) *FileSpooler {
	if len(printers) == 0 {
		printers = []string{"File Printer"}
	}
	return &FileSpooler{Dir: dir, Names: printers, finished: map[uint32]bool{}}
}

func (f *FileSpooler) Printers() ([]Printer, error) {
	out := make([]Printer, 0, len(f.Names))
	for _, n := range f.Names {
		out = append(out, Printer{Name: n, Driver: "file", Port: f.Dir, Status: "ready"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *FileSpooler) SystemDefault() (string, error) {
	return f.Names[0], nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (f *FileSpooler) Submit(printer, docName string, data []byte) (uint32, error) {
	if !f.has(printer) {
		return 0, fmt.Errorf("%w: %s", ErrPrinterNotFound, printer)
	}
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.next++
	id := f.next
	f.mu.Unlock()
	name := fmt.Sprintf("%s-%04d-%s.prn", time.Now().Format("20060102-150405"), id,
		strings.Trim(unsafeChars.ReplaceAllString(docName, "_"), "_"))
	if err := os.WriteFile(filepath.Join(f.Dir, name), data, 0o644); err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.finished[id] = true
	f.mu.Unlock()
	return id, nil
}

func (f *FileSpooler) Job(printer string, id uint32) (JobInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finished[id] {
		return JobInfo{State: StateDone}, nil
	}
	return JobInfo{}, fmt.Errorf("job %d not found", id)
}

func (f *FileSpooler) has(name string) bool {
	for _, n := range f.Names {
		if n == name {
			return true
		}
	}
	return false
}

// ResolvePrinter picks the configured printer, then a name/driver substring
// match, then the system default.
func ResolvePrinter(s Spooler, explicit, configured, match string) (string, error) {
	printers, err := s.Printers()
	if err != nil {
		return "", err
	}
	find := func(name string) bool {
		for _, p := range printers {
			if strings.EqualFold(p.Name, name) {
				return true
			}
		}
		return false
	}
	if explicit != "" {
		if find(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("%w: %s", ErrPrinterNotFound, explicit)
	}
	if configured != "" && find(configured) {
		return configured, nil
	}
	if m := strings.ToLower(strings.TrimSpace(match)); m != "" {
		for _, p := range printers {
			if strings.Contains(strings.ToLower(p.Name), m) || strings.Contains(strings.ToLower(p.Driver), m) {
				return p.Name, nil
			}
		}
	}
	if d, err := s.SystemDefault(); err == nil && d != "" && find(d) {
		return d, nil
	}
	if configured != "" {
		return "", fmt.Errorf("%w: %s", ErrPrinterNotFound, configured)
	}
	return "", errors.New("no printer configured and no system default printer")
}
