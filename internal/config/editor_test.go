package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rscprinting/print-agent/internal/config"
)

func TestSetPrintersPreservesCommentsAndFields(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("PRINT_AGENT_DATA_DIR", tempDir)
	defer os.Unsetenv("PRINT_AGENT_DATA_DIR")

	path := filepath.Join(tempDir, "config.yaml")
	original := "mode: agent # mode tetap\n" +
		"\n" +
		"printers:\n" +
		"  defaultName: \"\"\n" +
		"  allowlist: []\n" +
		"  pollInterval: 5m # deteksi perubahan\n" +
		"\n" +
		"document:\n" +
		"  allowRaw: true # izinkan raw\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	names := []string{"EPSON TM-T82X Kasir 1", "EPSON TM-T82X Kasir 2"}
	if err := config.SetPrinters(path, names, names[0]); err != nil {
		t.Fatalf("SetPrinters: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	text := string(out)

	for _, comment := range []string{"# mode tetap", "# deteksi perubahan", "# izinkan raw"} {
		if !strings.Contains(text, comment) {
			t.Errorf("comment %q tidak dipertahankan. output:\n%s", comment, text)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load result: %v", err)
	}
	if cfg.Printers.DefaultName != names[0] {
		t.Errorf("defaultName = %q, want %q", cfg.Printers.DefaultName, names[0])
	}
	if len(cfg.Printers.Allowlist) != len(names) {
		t.Fatalf("allowlist len = %d, want %d", len(cfg.Printers.Allowlist), len(names))
	}
	for i, n := range names {
		if cfg.Printers.Allowlist[i] != n {
			t.Errorf("allowlist[%d] = %q, want %q", i, cfg.Printers.Allowlist[i], n)
		}
	}
}

func TestSetPrintersCreatesMissingPrintersBlock(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "config.yaml")
	if err := os.WriteFile(path, []byte("mode: agent\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := config.SetPrinters(path, []string{"Printer A"}, "Printer A"); err != nil {
		t.Fatalf("SetPrinters: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !strings.Contains(string(out), "printers:") {
		t.Errorf("expected printers block, got:\n%s", string(out))
	}
}

func TestEnsureThermalPrintersMergesAndPreserves(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("PRINT_AGENT_DATA_DIR", tempDir)
	defer os.Unsetenv("PRINT_AGENT_DATA_DIR")

	path := filepath.Join(tempDir, "config.yaml")
	original := "printers:\n" +
		"  allowlist: []\n" +
		"  thermal:\n" +
		"    - name: \"Thermal\" # keep me\n" +
		"      widthMm: 58\n" +
		"      dpi: 203\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := config.EnsureThermalPrinters(path, []string{"Thermal", "Kasir Thermal"}); err != nil {
		t.Fatalf("EnsureThermalPrinters: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "# keep me") {
		t.Errorf("existing comment lost:\n%s", text)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	th, ok := cfg.Printers.ThermalFor("Thermal")
	if !ok {
		t.Fatal("Thermal entry lost")
	}
	if th.ResolvedWidthMm() != 58 {
		t.Errorf("existing widthMm not preserved: %d", th.ResolvedWidthMm())
	}
	if _, ok := cfg.Printers.ThermalFor("Kasir Thermal"); !ok {
		t.Error("new thermal entry not added")
	}
	if len(cfg.Printers.Thermal) != 2 {
		t.Errorf("thermal count = %d, want 2", len(cfg.Printers.Thermal))
	}
}
