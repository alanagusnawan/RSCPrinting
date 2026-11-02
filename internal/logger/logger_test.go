package logger_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rscprinting/print-agent/internal/logger"
	"golang.org/x/exp/slog"
)

func TestLoggerRedaction(t *testing.T) {
	secretToken := "secret-jwt-token-12345"
	logger.RegisterSecret(secretToken)

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, nil)
	// Wrap or test Redact directly
	redactedMsg := logger.Redact("connecting with token " + secretToken + " now")
	if strings.Contains(redactedMsg, secretToken) {
		t.Errorf("expected secret to be redacted, got %s", redactedMsg)
	}
	if !strings.Contains(redactedMsg, "<redacted>") {
		t.Errorf("expected <redacted> in text, got %s", redactedMsg)
	}

	_ = handler
}
