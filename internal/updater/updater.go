package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/logger"
	"github.com/rscprinting/print-agent/internal/version"
	"github.com/zalando/go-keyring"
	"golang.org/x/exp/slog"
)

type Updater struct {
	cfg     config.Config
	dataDir string
}

func New(cfg config.Config) *Updater {
	return &Updater{
		cfg:     cfg,
		dataDir: cfg.DataDir,
	}
}

func (u *Updater) ResolveToken() string {
	// Prioritas: 1. OS Keyring, 2. Env var, 3. Config file
	if tok, err := keyring.Get("print-agent", "github-token"); err == nil && tok != "" {
		logger.RegisterSecret(tok)
		return tok
	}

	if envTok := os.Getenv("PRINT_AGENT_GH_TOKEN"); envTok != "" {
		logger.RegisterSecret(envTok)
		return envTok
	}

	if u.cfg.Updater.Token != "" {
		logger.RegisterSecret(u.cfg.Updater.Token)
		return u.cfg.Updater.Token
	}

	return ""
}

func (u *Updater) CheckAndApply(ctx context.Context) (bool, string, error) {
	if !u.cfg.Updater.Enabled && u.ResolveToken() == "" {
		return false, "", nil
	}

	owner := u.cfg.Updater.Owner
	repo := u.cfg.Updater.Repo
	if owner == "" || repo == "" {
		return false, "", fmt.Errorf("updater owner or repo not configured")
	}

	token := u.ResolveToken()
	slug := fmt.Sprintf("%s/%s", owner, repo)

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{
		APIToken: token,
	})
	if err != nil {
		return false, "", fmt.Errorf("create github source failed: %w", err)
	}

	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    source,
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return false, "", fmt.Errorf("init updater failed: %w", err)
	}

	latest, found, err := up.DetectLatest(ctx, selfupdate.ParseSlug(slug))
	if err != nil {
		return false, "", fmt.Errorf("detect latest release failed: %w", err)
	}
	if !found {
		return false, "", nil
	}

	if !latest.GreaterThan(version.Version) {
		slog.Info("agent binary is already up-to-date", "version", version.Version)
		return false, latest.Version(), nil
	}

	exe, err := os.Executable()
	if err != nil {
		return false, latest.Version(), fmt.Errorf("resolve executable failed: %w", err)
	}

	// 1. Tulis update.state watchdog
	stateFile := filepath.Join(u.dataDir, "update.state")
	stateContent := fmt.Sprintf("timestamp=%d\nversion=%s\n", time.Now().Unix(), latest.Version())
	_ = os.WriteFile(stateFile, []byte(stateContent), 0600)

	// 2. Terapkan update
	if err := up.UpdateTo(ctx, latest, exe); err != nil {
		_ = os.Remove(stateFile)
		return false, latest.Version(), fmt.Errorf("update binary failed: %w", err)
	}

	slog.Info("update successfully applied, restart required", "newVersion", latest.Version())
	return true, latest.Version(), nil
}

func (u *Updater) CheckWatchdogRollback() {
	stateFile := filepath.Join(u.dataDir, "update.state")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return
	}

	// Jika file state ada saat startup, periksa crash loop < 30s
	var ts int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "timestamp=") {
			_, _ = fmt.Sscanf(line, "timestamp=%d", &ts)
		}
	}

	exe, err := os.Executable()
	if err == nil && ts > 0 && time.Since(time.Unix(ts, 0)) < 30*time.Second {
		oldExe := exe + ".old"
		if _, err := os.Stat(oldExe); err == nil {
			slog.Warn("watchdog detected crash after update within 30s, rolling back to previous version")
			_ = os.Remove(exe)
			_ = os.Rename(oldExe, exe)
		}
	}

	_ = os.Remove(stateFile)
}

func (u *Updater) MarkStartupHealthy() {
	stateFile := filepath.Join(u.dataDir, "update.state")
	_ = os.Remove(stateFile)
}

func (u *Updater) StartPeriodicCheck(ctx context.Context, onUpdated func(newVer string)) {
	interval := u.cfg.Updater.CheckInterval
	if interval <= 0 {
		interval = 6 * time.Hour
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				applied, newVer, err := u.CheckAndApply(ctx)
				if err != nil {
					slog.Warn("periodic auto-update check error", "err", err)
				} else if applied && onUpdated != nil {
					onUpdated(newVer)
				}
			}
		}
	}()
}

func VerifySHA256(filePath, expectedHash string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	actualHash := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	return nil
}
