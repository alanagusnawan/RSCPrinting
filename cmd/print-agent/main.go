package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/httpserver"
	"github.com/rscprinting/print-agent/internal/logger"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	agentsvc "github.com/rscprinting/print-agent/internal/service"
	"github.com/rscprinting/print-agent/internal/storage"
	"github.com/rscprinting/print-agent/internal/tray"
	"github.com/rscprinting/print-agent/internal/updater"
	"github.com/rscprinting/print-agent/internal/version"
	"github.com/rscprinting/print-agent/internal/wsclient"
	"github.com/rscprinting/print-agent/pkg/apitypes"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		cmdServe(args)
	case "printers":
		cmdPrinters(args)
	case "test":
		cmdTest(args)
	case "service":
		cmdService(args)
	case "update":
		cmdUpdate(args)
	case "version":
		cmdVersion(args)
	case "status":
		cmdStatus(args)
	case "-v", "--version":
		cmdVersion(nil)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Print Agent - Client-Only WebSocket Print Agent

Usage:
  print-agent <command> [arguments]

Commands:
  serve       Jalankan agent print listener
  printers    Daftar printer lokal
  test        Cetak halaman test ke printer tertentu
  service     Kelola background service (install, uninstall, start, stop, status)
  update      Cek atau terapkan update binary dari GitHub
  version     Tampilkan versi dan build info
  status      Tampilkan ringkasan status agent`)
}

func cmdVersion(_ []string) {
	v := version.Get()
	fmt.Printf("Print Agent %s (commit: %s, built: %s)\n", v.Version, v.GitCommit, v.BuildDate)
}

func cmdPrinters(args []string) {
	fs := flag.NewFlagSet("printers", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi")
	_ = fs.Parse(args)

	cfg, _ := config.Load(*configPath)
	p, err := printer.New(cfg.Ghostscript.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error init printer backend: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := p.List(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error list printers: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d printer(s):\n", len(list))
	for _, pr := range list {
		defStr := ""
		if pr.IsDefault {
			defStr = " [DEFAULT]"
		}
		fmt.Printf(" - %s (%s)%s\n", pr.Name, pr.Status, defStr)
	}
}

func cmdTest(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: print-agent test <printer_name>")
		os.Exit(1)
	}
	printerName := args[0]

	cfg, _ := config.Load("")
	p, err := printer.New(cfg.Ghostscript.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error init printer backend: %v\n", err)
		os.Exit(1)
	}

	// Minimal PDF content
	testPDF := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/MediaBox[0 0 595 842]/Parent 2 0 R/Resources<<>>>>endobj\nxref\n0 4\n0000000000 65535 f \n0000000009 00000 n \n0000000052 00000 n \n0000000101 00000 n \ntrailer<</Size 4/Root 1 0 R>>\nstartxref\n178\n%%EOF\n"

	tmp, err := os.CreateTemp("", "test-print-*.pdf")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp file failed: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.WriteString(testPDF)
	_ = tmp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("Printing test page to %s...\n", printerName)
	err = p.Print(ctx, printer.Job{
		JobID:    "test-page",
		Printer:  printerName,
		FilePath: tmp.Name(),
		Template: apitypes.PrintTemplate{Copies: 1},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Print test failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Test page sent to spooler successfully.")
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi")
	_ = fs.Parse(args)

	cfg, _ := config.Load(*configPath)
	fmt.Printf("Agent ID:    %s\n", cfg.Agent.ID)
	fmt.Printf("Display:     %s\n", cfg.Agent.DisplayName)
	fmt.Printf("Hostname:    %s\n", cfg.Agent.Hostname)
	fmt.Printf("WS URL:      %s\n", cfg.WSServer.URL)
	fmt.Printf("Local HTTP:  http://%s:%d\n", cfg.Server.Host, cfg.Server.Port)
	fmt.Printf("Version:     %s\n", version.Version)
}

func cmdUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi")
	apply := fs.Bool("apply", false, "Unduh dan terapkan update jika ada")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config error: %v\n", err)
		os.Exit(1)
	}

	up := updater.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if *apply {
		applied, newVer, err := up.CheckAndApply(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update error: %v\n", err)
			os.Exit(1)
		}
		if applied {
			fmt.Printf("Successfully updated to %s. Please restart agent.\n", newVer)
		} else {
			fmt.Println("No newer release found.")
		}
	} else {
		token := up.ResolveToken()
		if token == "" {
			fmt.Println("Warning: No GitHub token configured (PRINT_AGENT_GH_TOKEN or updater.token). Rate limit may apply.")
		}
		fmt.Println("Checking for latest release...")
		applied, newVer, err := up.CheckAndApply(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "check error: %v\n", err)
			os.Exit(1)
		}
		if applied {
			fmt.Printf("Update available: %s (applied)\n", newVer)
		} else {
			fmt.Println("Up to date.")
		}
	}
}

func cmdService(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: print-agent service install|uninstall|start|stop|status [--autorun=service|user]")
		os.Exit(1)
	}

	action := args[0]
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	autorunMode := fs.String("autorun", "service", "Mode autorun: service (system) atau user (user login)")
	_ = fs.Parse(args[1:])

	if *autorunMode == "user" {
		switch action {
		case "install":
			if err := agentsvc.InstallUserAutorun(); err != nil {
				fmt.Fprintf(os.Stderr, "install user autorun failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("User autorun installed successfully.")
		case "uninstall":
			if err := agentsvc.UninstallUserAutorun(); err != nil {
				fmt.Fprintf(os.Stderr, "uninstall user autorun failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("User autorun uninstalled successfully.")
		default:
			fmt.Printf("Action %s not applicable for user autorun mode.\n", action)
		}
		return
	}

	svcConfig := agentsvc.GetServiceConfig([]string{"serve"})
	program := &serviceProgram{}
	s, err := service.New(program, svcConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init service failed: %v\n", err)
		os.Exit(1)
	}

	switch action {
	case "install":
		if err := s.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "install service failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service installed successfully.")
	case "uninstall":
		if err := s.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall service failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service uninstalled successfully.")
	case "start":
		if err := s.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "start service failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service started.")
	case "stop":
		if err := s.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "stop service failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service stopped.")
	case "status":
		status, err := s.Status()
		if err != nil {
			fmt.Fprintf(os.Stderr, "get status failed: %v\n", err)
			os.Exit(1)
		}
		statusStr := "Unknown"
		switch status {
		case service.StatusRunning:
			statusStr = "Running"
		case service.StatusStopped:
			statusStr = "Stopped"
		}
		fmt.Printf("Service Status: %s\n", statusStr)
	default:
		fmt.Fprintf(os.Stderr, "unknown service action: %s\n", action)
		os.Exit(1)
	}
}

type serviceProgram struct {
	cancel context.CancelFunc
}

func (p *serviceProgram) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go runAgent(ctx, "configs/config.yaml")
	return nil
}

func (p *serviceProgram) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi YAML")
	mode := fs.String("mode", "", "Mode runtime: agent | standalone")
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runAgentWithMode(ctx, *configPath, *mode)
}

func runAgent(ctx context.Context, configPath string) {
	runAgentWithMode(ctx, configPath, "")
}

func runAgentWithMode(ctx context.Context, configPath, modeOverride string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	if modeOverride != "" {
		cfg.Mode = modeOverride
	}

	// Setup logging & secret redaction
	if cfg.WSServer.Token != "" {
		logger.RegisterSecret(cfg.WSServer.Token)
	}
	if cfg.Updater.Token != "" {
		logger.RegisterSecret(cfg.Updater.Token)
	}
	log := logger.Setup(cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.File)
	log.Info("starting print agent", "version", version.Version, "agentId", cfg.Agent.ID, "mode", cfg.Mode)

	// Watchdog check
	up := updater.New(cfg)
	up.CheckWatchdogRollback()

	// Storage BoltDB
	store, err := storage.Open(cfg.DataDir)
	if err != nil {
		log.Error("failed to open boltdb storage", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	// Printer backend
	p, err := printer.New(cfg.Ghostscript.Path)
	if err != nil {
		log.Error("failed to init printer backend", "err", err)
		os.Exit(1)
	}

	// Queue manager
	qm := queue.NewManager(cfg.Queue.MaxConcurrentPerPrinter, cfg.Queue.MaxSizePerPrinter, cfg.Queue.JobTimeout)

	// WebSocket Client
	wsClient := wsclient.New(cfg, p, qm, store)

	// Local HTTP Server
	httpSrv := httpserver.New(cfg, wsClient, p, qm)
	go func() {
		if err := httpSrv.Start(); err != nil {
			log.Warn("local http server stopped", "err", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	up.MarkStartupHealthy()

	// Start auto-update check if enabled
	up.StartPeriodicCheck(ctx, func(newVer string) {
		log.Info("auto-updater applied new version", "version", newVer)
	})

	// Start WS Client loop
	go func() {
		if err := wsClient.Run(ctx); err != nil {
			log.Error("websocket client stopped with error", "err", err)
		}
	}()

	// System Tray (jika enabled)
	if cfg.Tray.Enabled {
		t := tray.New(cfg, wsClient, func() {
			log.Info("restart requested from tray")
		}, func() {
			log.Info("manual update check requested from tray")
			checkCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_, _, _ = up.CheckAndApply(checkCtx)
		}, func() {
			log.Info("shutdown requested from tray")
			store.Close()
			os.Exit(0)
		})
		t.Run(ctx)
	} else {
		<-ctx.Done()
	}
}
