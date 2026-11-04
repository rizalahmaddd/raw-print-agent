//go:build windows

package spool

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winspool              = windows.NewLazySystemDLL("winspool.drv")
	procOpenPrinterW      = winspool.NewProc("OpenPrinterW")
	procClosePrinter      = winspool.NewProc("ClosePrinter")
	procStartDocPrinterW  = winspool.NewProc("StartDocPrinterW")
	procEndDocPrinter     = winspool.NewProc("EndDocPrinter")
	procStartPagePrinter  = winspool.NewProc("StartPagePrinter")
	procEndPagePrinter    = winspool.NewProc("EndPagePrinter")
	procWritePrinter      = winspool.NewProc("WritePrinter")
	procEnumPrintersW     = winspool.NewProc("EnumPrintersW")
	procGetDefaultPrinter = winspool.NewProc("GetDefaultPrinterW")
	procGetJobW           = winspool.NewProc("GetJobW")
)

const (
	printerEnumLocal       = 0x2
	printerEnumConnections = 0x4

	printerAttrWorkOffline = 0x400

	printerStatusPaused     = 0x1
	printerStatusError      = 0x2
	printerStatusPaperJam   = 0x8
	printerStatusPaperOut   = 0x10
	printerStatusOffline    = 0x80
	printerStatusNotAvail   = 0x1000
	printerStatusDoorOpen   = 0x400000
	printerStatusUserInterv = 0x100000

	jobStatusPaused     = 0x1
	jobStatusError      = 0x2
	jobStatusDeleting   = 0x4
	jobStatusSpooling   = 0x8
	jobStatusPrinting   = 0x10
	jobStatusOffline    = 0x20
	jobStatusPaperOut   = 0x40
	jobStatusPrinted    = 0x80
	jobStatusDeleted    = 0x100
	jobStatusBlocked    = 0x200
	jobStatusUserInterv = 0x400
	jobStatusComplete   = 0x1000
)

type docInfo1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

type printerInfo2 struct {
	ServerName         *uint16
	PrinterName        *uint16
	ShareName          *uint16
	PortName           *uint16
	DriverName         *uint16
	Comment            *uint16
	Location           *uint16
	DevMode            uintptr
	SepFile            *uint16
	PrintProcessor     *uint16
	Datatype           *uint16
	Parameters         *uint16
	SecurityDescriptor uintptr
	Attributes         uint32
	Priority           uint32
	DefaultPriority    uint32
	StartTime          uint32
	UntilTime          uint32
	Status             uint32
	Jobs               uint32
	AveragePPM         uint32
}

type jobInfo1 struct {
	JobID        uint32
	PrinterName  *uint16
	MachineName  *uint16
	UserName     *uint16
	Document     *uint16
	Datatype     *uint16
	StatusText   *uint16
	Status       uint32
	Priority     uint32
	Position     uint32
	TotalPages   uint32
	PagesPrinted uint32
	Submitted    windows.Systemtime
}

type WinSpooler struct{}

func NewSystem() (Spooler, error) {
	if err := winspool.Load(); err != nil {
		return nil, err
	}
	return WinSpooler{}, nil
}

// alignedBuf keeps the buffer 8-byte aligned so the struct casts below are valid.
func alignedBuf(n uint32) []byte {
	words := make([]uint64, (n+7)/8+1)
	return unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
}

func (WinSpooler) Printers() ([]Printer, error) {
	var needed, returned uint32
	flags := uintptr(printerEnumLocal | printerEnumConnections)
	procEnumPrintersW.Call(flags, 0, 2, 0, 0, uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
	if needed == 0 {
		return []Printer{}, nil
	}
	buf := alignedBuf(needed)
	r, _, e := procEnumPrintersW.Call(flags, 0, 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(needed),
		uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
	if r == 0 {
		return nil, fmt.Errorf("EnumPrinters: %w", e)
	}
	infos := unsafe.Slice((*printerInfo2)(unsafe.Pointer(&buf[0])), returned)
	out := make([]Printer, 0, returned)
	for _, in := range infos {
		offline := in.Attributes&printerAttrWorkOffline != 0 || in.Status&(printerStatusOffline|printerStatusNotAvail) != 0
		out = append(out, Printer{
			Name:    windows.UTF16PtrToString(in.PrinterName),
			Driver:  windows.UTF16PtrToString(in.DriverName),
			Port:    windows.UTF16PtrToString(in.PortName),
			Status:  printerStatusText(in.Status, offline),
			Offline: offline,
			Jobs:    int(in.Jobs),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func printerStatusText(s uint32, offline bool) string {
	var parts []string
	if offline {
		parts = append(parts, "offline")
	}
	for _, f := range []struct {
		bit  uint32
		text string
	}{
		{printerStatusPaperOut, "paper out"},
		{printerStatusPaperJam, "paper jam"},
		{printerStatusDoorOpen, "door open"},
		{printerStatusUserInterv, "needs attention"},
		{printerStatusPaused, "paused"},
		{printerStatusError, "error"},
	} {
		if s&f.bit != 0 {
			parts = append(parts, f.text)
		}
	}
	if len(parts) == 0 {
		return "ready"
	}
	return strings.Join(parts, ", ")
}

func (WinSpooler) SystemDefault() (string, error) {
	var n uint32
	procGetDefaultPrinter.Call(0, uintptr(unsafe.Pointer(&n)))
	if n == 0 {
		return "", errors.New("no default printer")
	}
	buf := make([]uint16, n)
	r, _, e := procGetDefaultPrinter.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return "", fmt.Errorf("GetDefaultPrinter: %w", e)
	}
	return windows.UTF16ToString(buf), nil
}

func openPrinter(name string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var h windows.Handle
	r, _, e := procOpenPrinterW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&h)), 0)
	if r == 0 {
		if errors.Is(e, windows.ERROR_INVALID_PRINTER_NAME) {
			return 0, fmt.Errorf("%w: %s", ErrPrinterNotFound, name)
		}
		return 0, fmt.Errorf("OpenPrinter %q: %w", name, e)
	}
	return h, nil
}

func (WinSpooler) Submit(printer, docName string, data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, errors.New("empty payload")
	}
	h, err := openPrinter(printer)
	if err != nil {
		return 0, err
	}
	defer procClosePrinter.Call(uintptr(h))

	dn, _ := windows.UTF16PtrFromString(docName)
	dt, _ := windows.UTF16PtrFromString("RAW")
	di := docInfo1{DocName: dn, Datatype: dt}
	r, _, e := procStartDocPrinterW.Call(uintptr(h), 1, uintptr(unsafe.Pointer(&di)))
	if r == 0 {
		return 0, fmt.Errorf("StartDocPrinter (RAW): %w; v4/XPS drivers do not accept RAW, use the v3 Epson driver or Generic / Text Only", e)
	}
	jobID := uint32(r)
	ok := false
	defer func() {
		if !ok {
			procEndDocPrinter.Call(uintptr(h))
		}
	}()

	if r, _, e := procStartPagePrinter.Call(uintptr(h)); r == 0 {
		return 0, fmt.Errorf("StartPagePrinter: %w", e)
	}
	for off := 0; off < len(data); {
		var written uint32
		r, _, e := procWritePrinter.Call(uintptr(h), uintptr(unsafe.Pointer(&data[off])),
			uintptr(len(data)-off), uintptr(unsafe.Pointer(&written)))
		if r == 0 {
			return 0, fmt.Errorf("WritePrinter: %w", e)
		}
		if written == 0 {
			return 0, errors.New("WritePrinter wrote 0 bytes")
		}
		off += int(written)
	}
	procEndPagePrinter.Call(uintptr(h))
	ok = true
	if r, _, e := procEndDocPrinter.Call(uintptr(h)); r == 0 {
		return jobID, fmt.Errorf("EndDocPrinter: %w", e)
	}
	return jobID, nil
}

func (WinSpooler) Job(printer string, id uint32) (JobInfo, error) {
	h, err := openPrinter(printer)
	if err != nil {
		return JobInfo{}, err
	}
	defer procClosePrinter.Call(uintptr(h))

	var needed uint32
	procGetJobW.Call(uintptr(h), uintptr(id), 1, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed == 0 {
		// The spooler drops finished jobs from the queue, so "unknown job id"
		// after a successful submit means it went through.
		return JobInfo{State: StateDone}, nil
	}
	buf := alignedBuf(needed)
	r, _, e := procGetJobW.Call(uintptr(h), uintptr(id), 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(needed),
		uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		if errors.Is(e, windows.ERROR_INVALID_PARAMETER) {
			return JobInfo{State: StateDone}, nil
		}
		return JobInfo{}, fmt.Errorf("GetJob: %w", e)
	}
	ji := (*jobInfo1)(unsafe.Pointer(&buf[0]))
	return jobStateFrom(ji.Status, windows.UTF16PtrToString(ji.StatusText)), nil
}

func jobStateFrom(s uint32, text string) JobInfo {
	switch {
	case s&jobStatusPaperOut != 0:
		return JobInfo{State: StateError, Detail: "paper out"}
	case s&jobStatusOffline != 0:
		return JobInfo{State: StateError, Detail: "printer offline"}
	case s&jobStatusUserInterv != 0:
		return JobInfo{State: StateError, Detail: "printer needs attention"}
	case s&(jobStatusError|jobStatusBlocked) != 0:
		d := "printer error"
		if text != "" {
			d = text
		}
		return JobInfo{State: StateError, Detail: d}
	case s&(jobStatusDeleted|jobStatusDeleting) != 0:
		return JobInfo{State: StateError, Detail: "job was cancelled"}
	case s&(jobStatusPrinted|jobStatusComplete) != 0:
		return JobInfo{State: StateDone}
	case s&jobStatusPrinting != 0:
		return JobInfo{State: StatePrinting}
	case s&jobStatusPaused != 0:
		return JobInfo{State: StateQueued, Detail: "paused"}
	case s&jobStatusSpooling != 0:
		return JobInfo{State: StateQueued, Detail: "spooling"}
	}
	return JobInfo{State: StateQueued}
}
