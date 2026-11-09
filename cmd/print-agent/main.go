package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/httpserver"
	"github.com/rscprinting/print-agent/internal/instance"
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
	case "run":
		cmdRunService(args)
	case "config":
		cmdConfig(args)
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
	fmt.Println(`RSCPrinting - Client-Only WebSocket Print Agent

Usage:
  print-agent <command> [arguments]

Commands:
  serve       Jalankan agent print listener
  printers    Daftar printer lokal
  test        Cetak halaman test ke printer tertentu
  service     Kelola background service (install, uninstall, start, stop, status)
  config      Kelola konfigurasi (set-printers, get-provision)
  update      Cek atau terapkan update binary dari GitHub
  version     Tampilkan versi dan build info
  status      Tampilkan ringkasan status agent`)
}

func cmdVersion(_ []string) {
	v := version.Get()
	fmt.Printf("RSCPrinting %s (commit: %s, built: %s)\n", v.Version, v.GitCommit, v.BuildDate)
}

func cmdPrinters(args []string) {
	fs := flag.NewFlagSet("printers", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi")
	_ = fs.Parse(args)

	cfg, _ := config.Load(*configPath)
	p, err := printer.New(cfg)
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
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi")
	filePath := fs.String("file", "", "Path file yang akan dicetak (opsional; default halaman uji)")
	format := fs.String("format", "", "Format: auto|pdf|ps|image|text|office|raw|zpl (kosong = tetap PDF/PS)")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Println("Usage: print-agent test <printer_name> [--file path] [--format f]")
		os.Exit(1)
	}
	printerName := fs.Arg(0)

	cfg, _ := config.Load(*configPath)
	p, err := printer.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error init printer backend: %v\n", err)
		os.Exit(1)
	}

	var targetPath string
	requestedFormat := printer.ParseFormat(*format)
	cleanup := func() {}

	if *filePath != "" {
		if _, err := os.Stat(*filePath); err != nil {
			fmt.Fprintf(os.Stderr, "file not found: %v\n", err)
			os.Exit(1)
		}
		targetPath = *filePath
	} else {
		// Halaman uji dalam bentuk PostScript.
		testContent := "%!PS-Adobe-3.0\n" +
			"/Helvetica-Bold findfont 24 scalefont setfont\n" +
			"50 750 moveto\n" +
			"(RSCPrinting Test Page) show\n" +
			"/Helvetica findfont 14 scalefont setfont\n" +
			"50 710 moveto\n" +
			"(Printer: " + printerName + ") show\n" +
			"50 680 moveto\n" +
			"(Status: OK - Print Agent Connected) show\n" +
			"50 660 moveto\n" +
			"500 660 lineto\n" +
			"stroke\n" +
			"showpage\n"

		tmp, err := os.CreateTemp("", "test-print-*.ps")
		if err != nil {
			fmt.Fprintf(os.Stderr, "create temp file failed: %v\n", err)
			os.Exit(1)
		}
		_, _ = tmp.WriteString(testContent)
		_ = tmp.Close()
		targetPath = tmp.Name()
		cleanup = func() { _ = os.Remove(tmp.Name()) }
		if requestedFormat == printer.FormatAuto {
			requestedFormat = printer.FormatPS
		}
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("Printing %s to %s (format=%q)...\n", targetPath, printerName, string(requestedFormat))
	err = p.Print(ctx, printer.Job{
		JobID:    "test-page",
		Printer:  printerName,
		FilePath: targetPath,
		Format:   requestedFormat,
		FileName: targetPath,
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

func cmdConfig(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: print-agent config set-printers --config <path> --allowlist <a,b,c> [--default <name>]")
		os.Exit(1)
	}

	switch args[0] {
	case "set-printers":
		cmdConfigSetPrinters(args[1:])
	case "get-provision":
		cmdConfigGetProvision(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config action: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdConfigGetProvision(args []string) {
	fs := flag.NewFlagSet("config get-provision", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}
	for _, kv := range cfg.Provision.KeyValues() {
		fmt.Printf("%s=%s\n", kv[0], kv[1])
	}
}

func cmdConfigSetPrinters(args []string) {
	fs := flag.NewFlagSet("config set-printers", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi")
	allowlist := fs.String("allowlist", "", "Daftar nama printer dipisah koma")
	allowlistFile := fs.String("allowlist-file", "", "File berisi daftar nama printer (satu per baris)")
	defaultName := fs.String("default", "", "Nama printer default")
	thermal := fs.String("thermal", "", "Daftar nama printer thermal ESC/POS dipisah koma")
	_ = fs.Parse(args)

	names, err := resolveAllowlist(*allowlist, *allowlistFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := config.SetPrinters(*configPath, names, *defaultName); err != nil {
		fmt.Fprintf(os.Stderr, "update config failed: %v\n", err)
		os.Exit(1)
	}

	thermalCount := 0
	if *thermal != "" {
		thermalNames, terr := resolveAllowlist(*thermal, "")
		if terr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", terr)
			os.Exit(1)
		}
		if err := config.EnsureThermalPrinters(*configPath, thermalNames); err != nil {
			fmt.Fprintf(os.Stderr, "update thermal config failed: %v\n", err)
			os.Exit(1)
		}
		thermalCount = len(thermalNames)
	}

	fmt.Printf("Updated %s: %d printer(s) in allowlist, default=%q, thermal=%d\n", *configPath, len(names), *defaultName, thermalCount)
}

func resolveAllowlist(inline, file string) ([]string, error) {
	if inline == "" && file == "" {
		return nil, fmt.Errorf("tidak ada allowlist yang diberikan (gunakan --allowlist atau --allowlist-file)")
	}

	var raw []string
	if inline != "" {
		raw = append(raw, strings.Split(inline, ",")...)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read allowlist file failed: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			raw = append(raw, line)
		}
	}

	seen := make(map[string]bool)
	var names []string
	for _, n := range raw {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	return names, nil
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
		fmt.Println("Usage: print-agent service install|uninstall|start|stop|status [--autorun=service|user|both] [--config path]")
		os.Exit(1)
	}

	action := args[0]
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	autorunMode := fs.String("autorun", "service", "Mode autorun: service (system) atau user (user login)")
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi (dipakai service)")
	_ = fs.Parse(args[1:])

	if *autorunMode == "user" {
		switch action {
		case "install":
			configAbs, _ := filepath.Abs(*configPath)
			dataAbs := ""
			if cfg, lerr := config.Load(configAbs); lerr == nil {
				dd := cfg.DataDir
				if dd != "" && !filepath.IsAbs(dd) {
					if a, aerr := filepath.Abs(dd); aerr == nil {
						dd = a
					}
				}
				dataAbs = dd
			}
			if err := agentsvc.InstallUserAutorun(configAbs, dataAbs); err != nil {
				fmt.Fprintf(os.Stderr, "install user autorun failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("User autorun (registry) installed successfully (config: %s).\n", configAbs)
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

	svcArgs, configAbs, envVars, err := resolveServiceRunArgs(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve service args failed: %v\n", err)
		os.Exit(1)
	}

	svcConfig := agentsvc.GetServiceConfig(svcArgs, envVars)
	program := &serviceProgram{configPath: configAbs}
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
	cancel     context.CancelFunc
	configPath string
}

func (p *serviceProgram) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	cp := p.configPath
	if cp == "" {
		cp = "configs/config.yaml"
	}
	go runAgent(ctx, cp)
	return nil
}

func (p *serviceProgram) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// resolveServiceRunArgs menyusun argumen service (dijalankan ulang oleh SCM)
// dengan path absolut, serta environment agar data dir absolut. Ini penting
// karena working directory service Windows = C:\Windows\System32.
func resolveServiceRunArgs(configPath string) (svcArgs []string, configAbs string, envVars map[string]string, err error) {
	configAbs, err = filepath.Abs(configPath)
	if err != nil {
		return nil, "", nil, err
	}

	dataDir := "./data"
	if cfg, lerr := config.Load(configAbs); lerr == nil {
		if cfg.DataDir != "" {
			dataDir = cfg.DataDir
		}
	}
	if !filepath.IsAbs(dataDir) {
		dataDir, err = filepath.Abs(dataDir)
		if err != nil {
			return nil, "", nil, err
		}
	}

	envVars = map[string]string{
		"PRINT_AGENT_DATA_DIR": dataDir,
	}
	svcArgs = []string{"run", "--config", configAbs}
	return svcArgs, configAbs, envVars, nil
}

// cmdRunService dijalankan oleh Service Control Manager (SCM). Ia memakai
// kardianos service.Run agar service melaporkan status ke SCM dengan benar.
func cmdRunService(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi YAML")
	dataDir := fs.String("data-dir", "", "Override direktori data (absolut)")
	hidden := fs.Bool("hidden", false, "Sembunyikan jendela konsol")
	_ = fs.Parse(args)

	if *hidden {
		hideConsole()
	}
	if *dataDir != "" {
		_ = os.Setenv("PRINT_AGENT_DATA_DIR", *dataDir)
	}

	program := &serviceProgram{configPath: *configPath}
	s, err := service.New(program, agentsvc.GetServiceConfig([]string{"run"}, nil))
	if err != nil {
		fmt.Fprintf(os.Stderr, "init service runtime failed: %v\n", err)
		os.Exit(1)
	}

	if err := s.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "service runtime failed: %v\n", err)
		os.Exit(1)
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "configs/config.yaml", "Path ke file konfigurasi YAML")
	mode := fs.String("mode", "", "Mode runtime: agent | standalone")
	dataDir := fs.String("data-dir", "", "Override direktori data (absolut)")
	hidden := fs.Bool("hidden", false, "Sembunyikan jendela konsol (untuk autorun)")
	_ = fs.Parse(args)

	if *hidden {
		hideConsole()
	}
	if *dataDir != "" {
		_ = os.Setenv("PRINT_AGENT_DATA_DIR", *dataDir)
	}

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

	// Single-instance: cegah service + autorun registry menjalankan dua agent.
	release, already, lockErr := instance.Acquire("RSCPrinting-Agent")
	if lockErr != nil {
		log.Warn("single-instance lock tidak tersedia; lanjut tanpa lock", "err", lockErr)
	} else if already {
		log.Warn("instance RSCPrinting lain sudah berjalan; keluar dari instance ini")
		return
	} else {
		defer release()
	}

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
	p, err := printer.New(cfg)
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
			store.Close()
			exe, err := os.Executable()
			if err == nil {
				cmd := exec.Command(exe, os.Args[1:]...)
				_ = cmd.Start()
			}
			os.Exit(0)
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
