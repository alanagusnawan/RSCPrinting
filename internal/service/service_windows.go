//go:build windows

package service

import (
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// registryCommand menyusun perintah autorun dengan path absolut + sembunyikan
// konsol, sehingga berjalan benar tanpa peduli working directory.
func registryCommand(exe, configPath, dataDir string) string {
	parts := []string{`"` + exe + `"`, "serve"}
	if configPath != "" {
		parts = append(parts, "--config", `"`+configPath+`"`)
	}
	if dataDir != "" {
		parts = append(parts, "--data-dir", `"`+dataDir+`"`)
	}
	parts = append(parts, "--hidden")
	return strings.Join(parts, " ")
}

func installWindowsRegistryAutorun(exe, configPath, dataDir string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	return k.SetStringValue("PrintAgent", registryCommand(exe, configPath, dataDir))
}

func uninstallWindowsRegistryAutorun() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	return k.DeleteValue("PrintAgent")
}

func ensureWindowsSpooler() error {
	// Di Windows 7, 8, 10, 11 nama service sama: "Spooler"
	// 1. Set startup type Automatic
	_ = exec.Command("sc", "config", "Spooler", "start=", "auto").Run()
	// 2. Jalankan service bila belum aktif
	_ = exec.Command("net", "start", "Spooler").Run()
	return nil
}
