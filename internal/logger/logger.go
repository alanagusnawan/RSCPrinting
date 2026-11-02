package logger

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/exp/slog"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	secretMu sync.RWMutex
	secrets  []string
)

func RegisterSecret(secret string) {
	if s := strings.TrimSpace(secret); s != "" && len(s) >= 4 {
		secretMu.Lock()
		defer secretMu.Unlock()
		secrets = append(secrets, s)
	}
}

func Redact(text string) string {
	secretMu.RLock()
	defer secretMu.RUnlock()
	res := text
	for _, s := range secrets {
		if s != "" {
			res = strings.ReplaceAll(res, s, "<redacted>")
		}
	}
	return res
}

type redactingHandler struct {
	next slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Message = Redact(r.Message)

	var newAttrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		newAttrs = append(newAttrs, sanitizeAttr(a))
		return true
	})

	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	newRecord.AddAttrs(newAttrs...)
	return h.next.Handle(ctx, newRecord)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		sanitized[i] = sanitizeAttr(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(sanitized)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func sanitizeAttr(a slog.Attr) slog.Attr {
	keyLower := strings.ToLower(a.Key)
	if strings.Contains(keyLower, "token") || strings.Contains(keyLower, "secret") || strings.Contains(keyLower, "auth") {
		return slog.String(a.Key, "<redacted>")
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, Redact(a.Value.String()))
	}
	return a
}

func Setup(levelStr, format, logFile string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var writer io.Writer = os.Stdout
	if logFile != "" {
		_ = os.MkdirAll(filepath.Dir(logFile), 0755)
		rotator := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    50, // MB
			MaxBackups: 3,
			MaxAge:     14, // days
			Compress:   true,
		}
		writer = io.MultiWriter(os.Stdout, rotator)
	}

	var baseHandler slog.Handler
	if strings.ToLower(format) == "text" {
		baseHandler = slog.NewTextHandler(writer, opts)
	} else {
		baseHandler = slog.NewJSONHandler(writer, opts)
	}

	handler := &redactingHandler{next: baseHandler}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
