package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rizalahmaddd/raw-print-agent/internal/api"
	"github.com/rizalahmaddd/raw-print-agent/internal/config"
	"github.com/rizalahmaddd/raw-print-agent/internal/logging"
	"github.com/rizalahmaddd/raw-print-agent/internal/spool"
	"github.com/rizalahmaddd/raw-print-agent/internal/winsvc"
)

var version = "dev"

const usage = `rawprint-agent %s

Usage:
  rawprint-agent run       [-config FILE] [-file-spool DIR] [-file-printers "A,B"]
  rawprint-agent install   [-config FILE] [-origins URL,URL] [-auto-approve] [-printer-match TEXT] [-browser-policy] [-port N]
  rawprint-agent uninstall
  rawprint-agent start | stop
  rawprint-agent config    [-config FILE]
  rawprint-agent version
`

func main() {
	if winsvc.IsService() {
		path := defaultConfigPath()
		err := winsvc.RunService(func(ctx context.Context) error { return serve(ctx, path, "", nil) })
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "install":
		err = cmdInstall(os.Args[2:])
	case "uninstall":
		err = winsvc.Uninstall()
	case "start":
		err = winsvc.Start()
	case "stop":
		err = winsvc.Stop()
	case "config":
		err = cmdConfig(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "RawPrintAgent", "config.json")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "rawprint-agent", "config.json")
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	fileSpool := fs.String("file-spool", "", "write jobs as .prn files to this directory instead of the Windows spooler")
	filePrinters := fs.String("file-printers", "File Printer", "comma separated printer names exposed with -file-spool")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var names []string
	for _, n := range strings.Split(*filePrinters, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return serve(ctx, *cfgPath, *fileSpool, names)
}

func serve(ctx context.Context, cfgPath, fileSpool string, filePrinters []string) error {
	store, err := config.Open(cfgPath)
	if err != nil {
		return err
	}
	cfg := store.Snapshot()

	var out io.Writer = os.Stderr
	logFile, err := logging.NewDailyFile(filepath.Join(filepath.Dir(cfgPath), "logs"), cfg.LogRetentionDays)
	if err == nil {
		defer logFile.Close()
		out = io.MultiWriter(os.Stderr, logFile)
	}
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var sp spool.Spooler
	if fileSpool != "" {
		sp = spool.NewFileSpooler(fileSpool, filePrinters...)
	} else if sp, err = spool.NewSystem(); err != nil {
		return err
	}

	srv, err := api.New(store, sp, log, version)
	if err != nil {
		return err
	}
	if winsvc.IsService() {
		srv.AutoStart = &api.AutoStart{Enabled: winsvc.AutoStartEnabled, Set: winsvc.SetAutoStart}
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Error("listen failed", "addr", cfg.Listen, "err", err)
		return fmt.Errorf("listen on %s: %w (if another program uses this port, change \"listen\" in the config or reinstall with /PORT=)", cfg.Listen, err)
	}
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	log.Info("agent started", "version", version, "addr", cfg.Listen, "config", cfgPath,
		"origins", strings.Join(cfg.AllowedOrigins, ","))

	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		log.Error("server stopped", "err", err)
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	log.Info("agent stopping")
	return hs.Shutdown(shutdownCtx)
}

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	origins := fs.String("origins", "", "comma separated web origins allowed to print, e.g. https://pos.example.com")
	autoApprove := fs.Bool("auto-approve", false, "approve pairing requests from allowed origins without confirmation")
	match := fs.String("printer-match", "", "pick the first printer whose name or driver contains this text, e.g. LX-310")
	policy := fs.Bool("browser-policy", false, "allow the origins in the Chrome/Edge Local Network Access policy")
	port := fs.Int("port", 0, "loopback port to listen on (default keeps the configured one, 18310 on a new install)")
	fs.Parse(args)

	list, err := config.ParseOrigins(*origins)
	if err != nil {
		return err
	}
	store, err := config.Open(*cfgPath)
	if err != nil {
		return err
	}
	err = store.Update(func(c *config.Config) error {
		for _, o := range list {
			if !c.OriginAllowed(o) {
				c.AllowedOrigins = append(c.AllowedOrigins, o)
			}
		}
		if *autoApprove {
			c.AutoApprovePairing = true
		}
		if *match != "" {
			c.DefaultPrinterMatch = *match
		}
		if *port != 0 {
			listen := fmt.Sprintf("127.0.0.1:%d", *port)
			if err := config.ValidateListen(listen); err != nil {
				return err
			}
			c.Listen = listen
		}
		return nil
	})
	if err != nil {
		return err
	}
	if *policy && len(list) > 0 {
		if err := winsvc.AllowLocalNetworkAccess(list); err != nil {
			return fmt.Errorf("browser policy: %w", err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := winsvc.Install(exe); err != nil {
		return err
	}
	if err := winsvc.Start(); err != nil {
		return err
	}
	fmt.Println("RawPrint Agent installed and started. Settings: http://" + store.Snapshot().Listen + "/")
	return nil
}

func cmdConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	fs.Parse(args)
	store, err := config.Open(*cfgPath)
	if err != nil {
		return err
	}
	c := store.Snapshot()
	for i := range c.Tokens {
		c.Tokens[i].Hash = "(hidden)"
	}
	fmt.Println("# " + store.Path())
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(c)
}
