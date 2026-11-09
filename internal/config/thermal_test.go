package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rscprinting/print-agent/internal/config"
)

func TestThermalConfigParsingAndResolvers(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("PRINT_AGENT_DATA_DIR", tempDir)
	defer os.Unsetenv("PRINT_AGENT_DATA_DIR")

	path := filepath.Join(tempDir, "config.yaml")
	content := "printers:\n" +
		"  thermal:\n" +
		"    - name: \"Thermal\"\n" +
		"      widthMm: 58\n" +
		"      dpi: 203\n" +
		"      cut: false\n" +
		"    - name: \"Thermal 80\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	th, ok := cfg.Printers.ThermalFor("thermal")
	if !ok {
		t.Fatal("ThermalFor(\"thermal\") not found (case-insensitive expected)")
	}
	if th.ResolvedWidthMm() != 58 {
		t.Errorf("width = %d, want 58", th.ResolvedWidthMm())
	}
	if th.ResolvedCut() {
		t.Errorf("cut = true, want false")
	}

	def, ok := cfg.Printers.ThermalFor("Thermal 80")
	if !ok {
		t.Fatal("ThermalFor(\"Thermal 80\") not found")
	}
	if def.ResolvedWidthMm() != 80 {
		t.Errorf("default width = %d, want 80", def.ResolvedWidthMm())
	}
	if def.ResolvedDPI() != 203 {
		t.Errorf("default dpi = %d, want 203", def.ResolvedDPI())
	}
	if !def.ResolvedCut() {
		t.Errorf("default cut = false, want true")
	}

	if _, ok := cfg.Printers.ThermalFor("Nope"); ok {
		t.Error("ThermalFor(\"Nope\") unexpectedly found")
	}
}

func TestProvisionConfigDefaultsAndKeyValues(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("PRINT_AGENT_DATA_DIR", tempDir)
	defer os.Unsetenv("PRINT_AGENT_DATA_DIR")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Provision.DriverSource != "github" {
		t.Errorf("default driverSource = %q, want github", cfg.Provision.DriverSource)
	}

	path := filepath.Join(tempDir, "config.yaml")
	content := "provision:\n" +
		"  driverSource: \"url\"\n" +
		"  url: \"https://example.com/APD.exe\"\n" +
		"  thermalDriverPattern: \"*TM-T82*\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg2, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	if cfg2.Provision.DriverSource != "url" || cfg2.Provision.URL != "https://example.com/APD.exe" {
		t.Fatalf("provision not parsed: %+v", cfg2.Provision)
	}

	kvs := cfg2.Provision.KeyValues()
	found := map[string]string{}
	for _, kv := range kvs {
		found[kv[0]] = kv[1]
	}
	if found["url"] != "https://example.com/APD.exe" {
		t.Errorf("KeyValues url = %q", found["url"])
	}
	if _, ok := found["thermalDriverPattern"]; !ok {
		t.Errorf("KeyValues missing thermalDriverPattern: %v", found)
	}
}
