package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode        string            `yaml:"mode"` // agent | standalone
	Server      ServerConfig      `yaml:"server"`
	WSServer    WSServerConfig    `yaml:"ws_server"`
	Agent       AgentConfig       `yaml:"agent"`
	Printers    PrintersConfig    `yaml:"printers"`
	Ghostscript GhostscriptConfig `yaml:"ghostscript"`
	Logging     LoggingConfig     `yaml:"logging"`
	Queue       QueueConfig       `yaml:"queue"`
	Updater     UpdaterConfig     `yaml:"updater"`
	Tray        TrayConfig        `yaml:"tray"`
	DataDir     string            `yaml:"dataDir"`
	ConfigPath  string            `yaml:"-"`
}

type ServerConfig struct {
	Host                     string `yaml:"host"`
	Port                     int    `yaml:"port"`
	EnableLocalPrintEndpoint bool   `yaml:"enable_local_print_endpoint"`
}

type WSServerConfig struct {
	URL                 string        `yaml:"url"`
	Token               string        `yaml:"token"`
	ReconnectBackoffMax time.Duration `yaml:"reconnectBackoffMax"`
	HandshakeTimeout    time.Duration `yaml:"handshakeTimeout"`
	PingInterval        time.Duration `yaml:"pingInterval"`
	WelcomeTimeout      time.Duration `yaml:"welcomeTimeout"`
}

type AgentConfig struct {
	ID          string `yaml:"id"`
	DisplayName string `yaml:"displayName"`
	Hostname    string `yaml:"hostname"`
}

type PrintersConfig struct {
	DefaultName  string        `yaml:"defaultName"`
	Allowlist    []string      `yaml:"allowlist"`
	PollInterval time.Duration `yaml:"pollInterval"`
}

type GhostscriptConfig struct {
	Path string `yaml:"path"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	File   string `yaml:"file"`
}

type QueueConfig struct {
	MaxSizePerPrinter       int           `yaml:"maxSizePerPrinter"`
	JobTimeout              time.Duration `yaml:"jobTimeout"`
	MaxConcurrentPerPrinter int           `yaml:"maxConcurrentPerPrinter"`
}

type UpdaterConfig struct {
	Enabled       bool          `yaml:"enabled"`
	Owner         string        `yaml:"owner"`
	Repo          string        `yaml:"repo"`
	Token         string        `yaml:"token"`
	CheckInterval time.Duration `yaml:"checkInterval"`
	Prerelease    bool          `yaml:"prerelease"`
}

type TrayConfig struct {
	Enabled        bool `yaml:"enabled"`
	StartMinimized bool `yaml:"startMinimized"`
}

func DefaultConfig() Config {
	return Config{
		Mode: "agent",
		Server: ServerConfig{
			Host:                     "127.0.0.1",
			Port:                     8080,
			EnableLocalPrintEndpoint: false,
		},
		WSServer: WSServerConfig{
			URL:                 "ws://localhost:5488/agent/ws",
			Token:               "",
			ReconnectBackoffMax: 60 * time.Second,
			HandshakeTimeout:    15 * time.Second,
			PingInterval:        30 * time.Second,
			WelcomeTimeout:      10 * time.Second,
		},
		Agent: AgentConfig{
			ID:          "",
			DisplayName: "",
			Hostname:    "",
		},
		Printers: PrintersConfig{
			DefaultName:  "",
			Allowlist:    []string{},
			PollInterval: 5 * time.Minute,
		},
		Ghostscript: GhostscriptConfig{
			Path: "",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			File:   "",
		},
		Queue: QueueConfig{
			MaxSizePerPrinter:       10,
			JobTimeout:              60 * time.Second,
			MaxConcurrentPerPrinter: 1,
		},
		Updater: UpdaterConfig{
			Enabled:       true,
			Owner:         "alanagusnawan",
			Repo:          "RSCPrinting",
			Token:         "",
			CheckInterval: 6 * time.Hour,
			Prerelease:    false,
		},
		Tray: TrayConfig{
			Enabled:        true,
			StartMinimized: true,
		},
		DataDir: "./data",
	}
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	cfg.ConfigPath = path

	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config yaml failed: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("read config file failed: %w", err)
		}
	}

	loadEnv(&cfg)

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return cfg, fmt.Errorf("create data directory failed: %w", err)
	}

	loadOrSyncAgentConfig(&cfg)

	if cfg.Agent.Hostname == "" {
		if h, err := os.Hostname(); err == nil {
			cfg.Agent.Hostname = h
		} else {
			cfg.Agent.Hostname = "unknown-host"
		}
	}

	return cfg, nil
}

func loadEnv(cfg *Config) {
	if v := os.Getenv("PRINT_AGENT_MODE"); v != "" {
		cfg.Mode = v
	}
	if v := os.Getenv("PRINT_AGENT_WS_URL"); v != "" {
		cfg.WSServer.URL = v
	}
	if v := os.Getenv("PRINT_AGENT_WS_TOKEN"); v != "" {
		cfg.WSServer.Token = v
	}
	if v := os.Getenv("PRINT_AGENT_ID"); v != "" {
		cfg.Agent.ID = v
	}
	if v := os.Getenv("PRINT_AGENT_DISPLAY_NAME"); v != "" {
		cfg.Agent.DisplayName = v
	}
	if v := os.Getenv("PRINT_AGENT_GH_TOKEN"); v != "" {
		cfg.Updater.Token = v
	}
	if v := os.Getenv("PRINT_AGENT_GH_OWNER"); v != "" {
		cfg.Updater.Owner = v
	}
	if v := os.Getenv("PRINT_AGENT_GH_REPO"); v != "" {
		cfg.Updater.Repo = v
	}
	if v := os.Getenv("PRINT_AGENT_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
}

type PersistedAgentConfig struct {
	AgentID string `json:"agentId" yaml:"agentId"`
	WSURL   string `json:"wsUrl" yaml:"wsUrl"`
	WSToken string `json:"wsToken" yaml:"wsToken"`
}

func loadOrSyncAgentConfig(cfg *Config) {
	configFile := filepath.Join(cfg.DataDir, "config")
	legacyIDFile := filepath.Join(cfg.DataDir, "agent-id")

	var persisted PersistedAgentConfig

	// 1. Coba baca ./data/config jika ada
	if data, err := os.ReadFile(configFile); err == nil {
		content := strings.TrimSpace(string(data))
		if strings.HasPrefix(content, "{") {
			_ = json.Unmarshal([]byte(content), &persisted)
		} else {
			_ = yaml.Unmarshal([]byte(content), &persisted)
		}
	} else if dataLegacy, err := os.ReadFile(legacyIDFile); err == nil {
		// 2. Fallback migrasi dari ./data/agent-id lama
		legacyContent := strings.TrimSpace(string(dataLegacy))
		if strings.HasPrefix(legacyContent, "{") {
			_ = json.Unmarshal([]byte(legacyContent), &persisted)
		} else {
			persisted.AgentID = legacyContent
		}
	}

	// 3. Resolusi Agent ID
	if cfg.Agent.ID != "" {
		persisted.AgentID = cfg.Agent.ID
	} else if persisted.AgentID != "" {
		cfg.Agent.ID = persisted.AgentID
	} else {
		persisted.AgentID = generateUUIDv4()
		cfg.Agent.ID = persisted.AgentID
	}

	// 4. Resolusi WebSocket URL
	if cfg.WSServer.URL != "" && cfg.WSServer.URL != "ws://localhost:5488/agent/ws" {
		persisted.WSURL = cfg.WSServer.URL
	} else if persisted.WSURL != "" {
		cfg.WSServer.URL = persisted.WSURL
	} else {
		if cfg.WSServer.URL == "" {
			cfg.WSServer.URL = "ws://localhost:5488/agent/ws"
		}
		persisted.WSURL = cfg.WSServer.URL
	}

	// 5. Resolusi WebSocket Token
	if cfg.WSServer.Token != "" {
		persisted.WSToken = cfg.WSServer.Token
	} else if persisted.WSToken != "" {
		cfg.WSServer.Token = persisted.WSToken
	} else {
		persisted.WSToken = generateSecureToken(32)
		cfg.WSServer.Token = persisted.WSToken
	}

	// 6. Simpan kembali ke ./data/config dalam format JSON rapi
	if outData, err := json.MarshalIndent(persisted, "", "  "); err == nil {
		_ = os.WriteFile(configFile, outData, 0600)
		// Simpan juga agent-id lama untuk backward compatibility
		_ = os.WriteFile(legacyIDFile, []byte(persisted.AgentID), 0600)
	}
}

func generateSecureToken(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
