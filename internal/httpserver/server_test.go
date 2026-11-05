package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/httpserver"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	"github.com/rscprinting/print-agent/internal/wsclient"
)

func TestLocalHTTPServerEndpoints(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 18080
	cfg.Server.EnableLocalPrintEndpoint = false

	mockP := printer.NewMockPrinter(nil)
	qm := queue.NewManager(1, 10, 5*time.Second)
	client := wsclient.New(cfg, mockP, qm, nil)

	srv := httpserver.New(cfg, client, mockP, qm)

	// Test GET /health
	reqHealth := httptest.NewRequest(http.MethodGet, "/health", nil)
	rrHealth := httptest.NewRecorder()
	srv.ServeHTTP(rrHealth, reqHealth)

	if rrHealth.Code != http.StatusOK {
		t.Fatalf("health endpoint returned %d", rrHealth.Code)
	}

	var healthResp map[string]interface{}
	if err := json.Unmarshal(rrHealth.Body.Bytes(), &healthResp); err != nil {
		t.Fatalf("unmarshal health resp error: %v", err)
	}
	if healthResp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", healthResp["status"])
	}

	// Test GET /version
	reqVer := httptest.NewRequest(http.MethodGet, "/version", nil)
	rrVer := httptest.NewRecorder()
	srv.ServeHTTP(rrVer, reqVer)
	if rrVer.Code != http.StatusOK {
		t.Fatalf("version endpoint returned %d", rrVer.Code)
	}

	// Test GET /printers
	reqPrinters := httptest.NewRequest(http.MethodGet, "/printers", nil)
	rrPrinters := httptest.NewRecorder()
	srv.ServeHTTP(rrPrinters, reqPrinters)
	if rrPrinters.Code != http.StatusOK {
		t.Fatalf("printers endpoint returned %d", rrPrinters.Code)
	}

	// Test GET /docs
	reqDocs := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rrDocs := httptest.NewRecorder()
	srv.ServeHTTP(rrDocs, reqDocs)
	if rrDocs.Code != http.StatusOK {
		t.Fatalf("docs endpoint returned %d", rrDocs.Code)
	}

	// Test GET /swagger.json
	reqSwagger := httptest.NewRequest(http.MethodGet, "/swagger.json", nil)
	rrSwagger := httptest.NewRecorder()
	srv.ServeHTTP(rrSwagger, reqSwagger)
	if rrSwagger.Code != http.StatusOK {
		t.Fatalf("swagger.json endpoint returned %d", rrSwagger.Code)
	}

	// Test POST /print is forbidden when disabled
	reqPrint := httptest.NewRequest(http.MethodPost, "/print", nil)
	rrPrint := httptest.NewRecorder()
	srv.ServeHTTP(rrPrint, reqPrint)
	if rrPrint.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for disabled /print endpoint, got %d", rrPrint.Code)
	}
}
