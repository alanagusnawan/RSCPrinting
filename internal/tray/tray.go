//go:build !headless && (windows || (cgo && (darwin || linux)))

package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/getlantern/systray"
	"github.com/rscprinting/print-agent/assets"
	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/wsclient"
	"golang.org/x/exp/slog"
)

type Tray struct {
	cfg        config.Config
	client     *wsclient.Client
	onRestart  func()
	onUpdate   func()
	onShutdown func()
}

func New(cfg config.Config, c *wsclient.Client, onRestart, onUpdate, onShutdown func()) *Tray {
	return &Tray{
		cfg:        cfg,
		client:     c,
		onRestart:  onRestart,
		onUpdate:   onUpdate,
		onShutdown: onShutdown,
	}
}

func (t *Tray) Run(ctx context.Context) {
	onReady := func() {
		if runtime.GOOS == "windows" && len(assets.IconICO) > 0 {
			systray.SetIcon(assets.IconICO)
		} else if len(assets.IconPNG) > 0 {
			systray.SetIcon(assets.IconPNG)
		}
		systray.SetTitle("RSCPrinting")
		systray.SetTooltip("RSCPrinting")

		mStatus := systray.AddMenuItem(fmt.Sprintf("Status: Running (Port %d)", t.cfg.Server.Port), "Status local server")
		mStatus.Disable()

		mServer := systray.AddMenuItem(fmt.Sprintf("Server: %s — Disconnected", t.cfg.WSServer.URL), "Status WebSocket")
		mServer.Disable()

		mAgent := systray.AddMenuItem(fmt.Sprintf("Agent: %s (%s)", t.cfg.Agent.DisplayName, t.cfg.Agent.ID), "ID Agent")
		mAgent.Disable()

		systray.AddSeparator()

		mDashboard := systray.AddMenuItem("Buka Dashboard", "Buka browser ke local health")
		mDocs := systray.AddMenuItem("Dokumentasi API", "Buka Swagger UI di browser (/docs)")
		mConfig := systray.AddMenuItem("Konfigurasi", "Buka file konfigurasi YAML")
		mLog := systray.AddMenuItem("Lihat Log", "Buka file log")
		mCheckUpdate := systray.AddMenuItem("Cek Update", "Picu cek update dari GitHub")
		mRestart := systray.AddMenuItem("Restart Server", "Restart loop service")

		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Keluar", "Keluar dari aplikasi")

		// Goroutine update label status WS
		go func() {
			statusCh := t.client.StatusChannel()
			for {
				select {
				case <-ctx.Done():
					return
				case s := <-statusCh:
					mServer.SetTitle(fmt.Sprintf("Server: %s — %s", t.cfg.WSServer.URL, s))
				}
			}
		}()

		// Event handlers
		go func() {
			for {
				select {
				case <-mDashboard.ClickedCh:
					url := fmt.Sprintf("http://127.0.0.1:%d/health", t.cfg.Server.Port)
					openBrowser(url)
				case <-mDocs.ClickedCh:
					url := fmt.Sprintf("http://127.0.0.1:%d/docs", t.cfg.Server.Port)
					openBrowser(url)
				case <-mConfig.ClickedCh:
					cfgFile := t.cfg.ConfigPath
					if cfgFile == "" {
						cfgFile = "configs/config.yaml"
					}
					if _, err := os.Stat(cfgFile); err != nil {
						if _, err2 := os.Stat("config.yaml"); err2 == nil {
							cfgFile = "config.yaml"
						}
					}
					openFile(cfgFile)
				case <-mLog.ClickedCh:
					if t.cfg.Logging.File != "" {
						openFile(t.cfg.Logging.File)
					}
				case <-mCheckUpdate.ClickedCh:
					if t.onUpdate != nil {
						t.onUpdate()
					}
				case <-mRestart.ClickedCh:
					if t.onRestart != nil {
						t.onRestart()
					}
				case <-mQuit.ClickedCh:
					slog.Info("quit clicked from tray")
					if t.onShutdown != nil {
						t.onShutdown()
					}
					systray.Quit()
					return
				case <-ctx.Done():
					systray.Quit()
					return
				}
			}
		}()
	}

	onExit := func() {}

	systray.Run(onReady, onExit)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func openFile(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("notepad.exe", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}
