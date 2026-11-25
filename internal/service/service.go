package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/kardianos/service"
)

type Config struct {
	Name        string
	DisplayName string
	Description string
	Arguments   []string
}

func GetServiceConfig(args []string, envVars map[string]string) *service.Config {
	return &service.Config{
		Name:        "RSCPrinting",
		DisplayName: "RSCPrinting",
		Description: "Client-only WebSocket print agent for local printing",
		Arguments:   args,
		EnvVars:     envVars,
		Option: service.KeyValue{
			"DelayedAutoStart": false,
		},
	}
}

func InstallService(svc service.Service) error {
	return svc.Install()
}

func UninstallService(svc service.Service) error {
	return svc.Uninstall()
}

func StartService(svc service.Service) error {
	return svc.Start()
}

func StopService(svc service.Service) error {
	return svc.Stop()
}

func RestartService(svc service.Service) error {
	return svc.Restart()
}

func StatusService(svc service.Service) (service.Status, error) {
	return svc.Status()
}

func EnsureSpoolerService() error {
	return ensureWindowsSpooler()
}

func InstallUserAutorun(configPath, dataDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "windows":
		return installWindowsRegistryAutorun(exe, configPath, dataDir)
	case "linux":
		return installLinuxDesktopAutorun(exe)
	case "darwin":
		return installDarwinLaunchAgent(exe)
	default:
		return fmt.Errorf("user autorun not supported on %s", runtime.GOOS)
	}
}

func UninstallUserAutorun() error {
	switch runtime.GOOS {
	case "windows":
		return uninstallWindowsRegistryAutorun()
	case "linux":
		return uninstallLinuxDesktopAutorun()
	case "darwin":
		return uninstallDarwinLaunchAgent()
	default:
		return fmt.Errorf("user autorun not supported on %s", runtime.GOOS)
	}
}

func installLinuxDesktopAutorun(exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ".config", "autostart")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Exec=%s serve
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
Name=Print Agent
Comment=Local Print Agent
`, exe)

	return os.WriteFile(filepath.Join(dir, "print-agent.desktop"), []byte(content), 0644)
}

func uninstallLinuxDesktopAutorun() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(home, ".config", "autostart", "print-agent.desktop"))
}

func installDarwinLaunchAgent(exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.rscprinting.printagent</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
`, exe)

	return os.WriteFile(filepath.Join(dir, "com.rscprinting.printagent.plist"), []byte(plist), 0644)
}

func uninstallDarwinLaunchAgent() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(home, "Library", "LaunchAgents", "com.rscprinting.printagent.plist"))
}
