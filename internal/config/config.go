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
	Office      OfficeConfig      `yaml:"office"`
	Document    DocumentConfig    `yaml:"document"`
	Logging     LoggingConfig     `yaml:"logging"`
	Queue       QueueConfig       `yaml:"queue"`
	Updater     UpdaterConfig     `yaml:"updater"`
	Tray        TrayConfig        `yaml:"tray"`
	Provision   ProvisionConfig   `yaml:"provision"`
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
	DefaultName  string                 `yaml:"defaultName"`
	Allowlist    []string               `yaml:"allowlist"`
	PollInterval time.Duration          `yaml:"pollInterval"`
	Thermal      []ThermalPrinterConfig `yaml:"thermal"`
	Share        ShareConfig            `yaml:"share"`
}

// ShareConfig mengatur pembagian printer lokal agar bisa dipasang di komputer
// lain (cetak lintas-komputer).
type ShareConfig struct {
	Enabled     bool   `yaml:"enabled"`     // aktifkan auto-share saat agent start
	Protocol    string `yaml:"protocol"`    // auto | smb | ipp
	NamePattern string `yaml:"namePattern"` // pola nama share, mis. "RSC-%s"
	// Relay (Model 2) untuk cross-OS: agent membuka listener agar device lain
	// bisa mencetak lewat agent ini.
	RelayEnabled    bool `yaml:"relayEnabled"`
	RelayIPPEnabled bool `yaml:"relayIppEnabled"` // listener IPP 1 port (multi-printer)
	RelayPort       int  `yaml:"relayPort"`       // port IPP (default 19100)
	RelayRawEnabled bool `yaml:"relayRawEnabled"` // listener RAW per printer
	RelayRawBase    int  `yaml:"relayRawBase"`    // port dasar RAW (default 19101)
}

// ThermalPrinterConfig mengatur printer ESC/POS yang dicetak dengan jalur raster
// thermal (render ke bitmap lalu kirim perintah GS v 0), melewati driver OS.
type ThermalPrinterConfig struct {
	Name    string `yaml:"name"`    // nama queue, dicocokkan tanpa peduli huruf besar/kecil
	WidthMm int    `yaml:"widthMm"` // lebar kertas (58 atau 80); kosong = 80
	DPI     int    `yaml:"dpi"`     // resolusi render; kosong = 203
	Cut     *bool  `yaml:"cut"`     // auto-cut di akhir; kosong = true
	Trim    *bool  `yaml:"trim"`    // pangkas margin putih; kosong = true
	Feed    int    `yaml:"feed"`    // feed (dot) sebelum cut; kosong = 150
}

// ResolvedWidthMm mengembalikan lebar kertas efektif (default 80mm).
func (t ThermalPrinterConfig) ResolvedWidthMm() int {
	if t.WidthMm <= 0 {
		return 80
	}
	return t.WidthMm
}

// ResolvedDPI mengembalikan DPI render efektif (default 203).
func (t ThermalPrinterConfig) ResolvedDPI() int {
	if t.DPI <= 0 {
		return 203
	}
	return t.DPI
}

// ResolvedCut mengembalikan preferensi auto-cut efektif (default true).
func (t ThermalPrinterConfig) ResolvedCut() bool {
	if t.Cut == nil {
		return true
	}
	return *t.Cut
}

// ResolvedTrim mengembalikan preferensi pangkas margin efektif (default true).
func (t ThermalPrinterConfig) ResolvedTrim() bool {
	if t.Trim == nil {
		return true
	}
	return *t.Trim
}

// ResolvedFeed mengembalikan feed sebelum cut (dot) efektif (default 150).
func (t ThermalPrinterConfig) ResolvedFeed() int {
	if t.Feed <= 0 {
		return 150
	}
	return t.Feed
}

// ThermalFor mencari konfigurasi thermal untuk printer bernama name.
func (c PrintersConfig) ThermalFor(name string) (ThermalPrinterConfig, bool) {
	for _, t := range c.Thermal {
		if strings.EqualFold(strings.TrimSpace(t.Name), strings.TrimSpace(name)) {
			return t, true
		}
	}
	return ThermalPrinterConfig{}, false
}

type GhostscriptConfig struct {
	Path string `yaml:"path"`
}

type OfficeConfig struct {
	Enabled bool   `yaml:"enabled"` // aktifkan konversi Office via LibreOffice
	Path    string `yaml:"path"`    // kosong = auto-detect soffice
}

type DocumentConfig struct {
	AllowRaw       *bool         `yaml:"allowRaw"`       // izinkan raw/zpl/escpos (default true)
	ConvertTimeout time.Duration `yaml:"convertTimeout"` // timeout konversi Office
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

// ProvisionConfig mengatur provisioning driver (dipakai skrip install/deploy).
// Nilai ini juga dipakai sebagai default CLI; script membaca via `config get-provision`.
type ProvisionConfig struct {
	DriverSource         string `yaml:"driverSource"` // github | url | local
	GithubRepo           string `yaml:"githubRepo"`   // owner/repo
	GithubTag            string `yaml:"githubTag"`    // tag rilis berisi aset driver
	AssetPattern         string `yaml:"assetPattern"` // pola nama aset (wildcard)
	URL                  string `yaml:"url"`          // bila driverSource=url
	SHA256               string `yaml:"sha256"`       // opsional
	ApdDir               string `yaml:"apdDir"`       // folder installer lokal (mis. "dist")
	ThermalDriverPattern string `yaml:"thermalDriverPattern"`
}

// KeyValues mengembalikan pasangan kunci-nilai stabil untuk dikonsumsi skrip
// (mis. PowerShell) tanpa perlu parsing YAML.
func (p ProvisionConfig) KeyValues() [][2]string {
	return [][2]string{
		{"driverSource", p.DriverSource},
		{"githubRepo", p.GithubRepo},
		{"githubTag", p.GithubTag},
		{"assetPattern", p.AssetPattern},
		{"url", p.URL},
		{"sha256", p.SHA256},
		{"apdDir", p.ApdDir},
		{"thermalDriverPattern", p.ThermalDriverPattern},
	}
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
			Share: ShareConfig{
				Enabled:         true,
				Protocol:        "auto",
				NamePattern:     "RSC-%s",
				RelayIPPEnabled: true,
				RelayPort:       19100,
				RelayRawEnabled: true,
				RelayRawBase:    19101,
			},
		},
		Ghostscript: GhostscriptConfig{
			Path: "",
		},
		Office: OfficeConfig{
			Enabled: true,
			Path:    "",
		},
		Document: DocumentConfig{
			AllowRaw:       boolPtr(true),
			ConvertTimeout: 60 * time.Second,
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
		Provision: ProvisionConfig{
			DriverSource:         "github",
			GithubRepo:           "alanagusnawan/RSCPrinting",
			GithubTag:            "drivers",
			AssetPattern:         "*APD*TM-T82X*.exe",
			ThermalDriverPattern: "*TM-T82*",
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

func boolPtr(b bool) *bool { return &b }

func generateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
