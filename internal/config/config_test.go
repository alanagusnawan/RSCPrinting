package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rscprinting/print-agent/internal/config"
)

func TestConfigLoadDefaults(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("PRINT_AGENT_DATA_DIR", tempDir)
	defer os.Unsetenv("PRINT_AGENT_DATA_DIR")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}

	if cfg.Mode != "agent" {
		t.Errorf("expected mode agent, got %s", cfg.Mode)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Agent.ID == "" {
		t.Errorf("expected generated agent id, got empty")
	}
	if cfg.WSServer.Token == "" {
		t.Errorf("expected generated ws token, got empty")
	}
	if cfg.WSServer.URL == "" {
		t.Errorf("expected default ws url, got empty")
	}

	// Verify persistence in config file
	configFile := filepath.Join(tempDir, "config")
	dataConfig, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if len(dataConfig) == 0 {
		t.Fatalf("config file is empty")
	}

	// Verify persistence in legacy agent-id file
	idFile := filepath.Join(tempDir, "agent-id")
	data, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatalf("agent-id file not written: %v", err)
	}
	if string(data) != cfg.Agent.ID {
		t.Errorf("mismatch persisted id %s vs %s", string(data), cfg.Agent.ID)
	}
}

func TestConfigEnvOverrides(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("PRINT_AGENT_DATA_DIR", tempDir)
	os.Setenv("PRINT_AGENT_WS_URL", "wss://custom.server.com/ws")
	os.Setenv("PRINT_AGENT_WS_TOKEN", "super-secret-token")
	os.Setenv("PRINT_AGENT_DISPLAY_NAME", "Kasir Satu")
	defer func() {
		os.Unsetenv("PRINT_AGENT_DATA_DIR")
		os.Unsetenv("PRINT_AGENT_WS_URL")
		os.Unsetenv("PRINT_AGENT_WS_TOKEN")
		os.Unsetenv("PRINT_AGENT_DISPLAY_NAME")
	}()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}

	if cfg.WSServer.URL != "wss://custom.server.com/ws" {
		t.Errorf("expected custom ws url, got %s", cfg.WSServer.URL)
	}
	if cfg.WSServer.Token != "super-secret-token" {
		t.Errorf("expected custom ws token, got %s", cfg.WSServer.Token)
	}
	if cfg.Agent.DisplayName != "Kasir Satu" {
		t.Errorf("expected display name Kasir Satu, got %s", cfg.Agent.DisplayName)
	}
}
