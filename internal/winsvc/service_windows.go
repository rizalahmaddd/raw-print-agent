//go:build windows

package winsvc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	Name        = "RawPrintAgent"
	DisplayName = "RawPrint Agent"
	Description = "Forwards RAW print jobs (ESC/P, ESC/POS, ZPL) from allowlisted web applications to local printers."
)

func IsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type handler struct {
	run func(ctx context.Context) error
}

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				// non-zero exit code makes the SCM apply the restart recovery actions
				return true, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			}
		}
	}
}

func RunService(run func(ctx context.Context) error) error {
	return svc.Run(Name, handler{run: run})
}

func Install(exe string, args ...string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as Administrator): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err == nil {
		cfg, cerr := s.Config()
		if cerr == nil {
			cfg.BinaryPathName = syscallQuote(exe, args)
			// Upgrades run install again, so keep a choice made on the settings page.
			if cfg.StartType == mgr.StartDisabled {
				cfg.StartType = mgr.StartAutomatic
			}
			cfg.DisplayName = DisplayName
			cfg.Description = Description
			cerr = s.UpdateConfig(cfg)
		}
		if cerr != nil {
			s.Close()
			return cerr
		}
	} else {
		s, err = m.CreateService(Name, exe, mgr.Config{
			DisplayName: DisplayName, Description: Description, StartType: mgr.StartAutomatic,
		}, args...)
		if err != nil {
			return err
		}
	}
	defer s.Close()
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, uint32((24 * time.Hour).Seconds())); err != nil {
		return err
	}
	// Without this the SCM only recovers from crashes, so a clean stop with an
	// error, such as the port being busy at boot, would leave the service down.
	return s.SetRecoveryActionsOnNonCrashFailures(true)
}

func syscallQuote(exe string, args []string) string {
	parts := []string{windows.EscapeArg(exe)}
	for _, a := range args {
		parts = append(parts, windows.EscapeArg(a))
	}
	return strings.Join(parts, " ")
}

func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return nil
	}
	defer s.Close()
	stop(s)
	return s.Delete()
}

func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return err
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State == svc.Running {
		return nil
	}
	return s.Start()
}

func AutoStartEnabled() (bool, error) {
	m, err := mgr.Connect()
	if err != nil {
		return false, err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return false, err
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return false, err
	}
	return cfg.StartType == mgr.StartAutomatic, nil
}

// SetAutoStart uses Manual rather than Disabled when turned off, so the service
// can still be started by hand and the failure recovery keeps working.
func SetAutoStart(enabled bool) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return err
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	cfg.StartType = mgr.StartManual
	if enabled {
		cfg.StartType = mgr.StartAutomatic
	}
	return s.UpdateConfig(cfg)
}

func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return err
	}
	defer s.Close()
	return stop(s)
}

func stop(s *mgr.Service) error {
	st, err := s.Control(svc.Stop)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for st.State != svc.Stopped && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return err
		}
	}
	return nil
}

// AllowLocalNetworkAccess adds the origins to the Chrome and Edge
// LocalNetworkAccessAllowedForUrls policy so users are not prompted.
func AllowLocalNetworkAccess(origins []string) error {
	for _, base := range []string{`SOFTWARE\Policies\Google\Chrome`, `SOFTWARE\Policies\Microsoft\Edge`} {
		if err := appendPolicyList(base+`\LocalNetworkAccessAllowedForUrls`, origins); err != nil {
			return fmt.Errorf("%s: %w", base, err)
		}
	}
	return nil
}

func appendPolicyList(path string, values []string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	names, err := k.ReadValueNames(0)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	max := 0
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil {
			have[strings.ToLower(v)] = true
		}
		if i, err := strconv.Atoi(n); err == nil && i > max {
			max = i
		}
	}
	for _, v := range values {
		if have[strings.ToLower(v)] {
			continue
		}
		max++
		if err := k.SetStringValue(strconv.Itoa(max), v); err != nil {
			return err
		}
		have[strings.ToLower(v)] = true
	}
	return nil
}
