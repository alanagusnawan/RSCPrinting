package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/httpserver"
	"github.com/rscprinting/print-agent/internal/jobs"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	"github.com/rscprinting/print-agent/internal/wsclient"
)

func newTestServer(t *testing.T, mutate bool) *httpserver.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 18080
	cfg.Server.EnableLocalPrintEndpoint = mutate
	if mutate {
		cfg.Mode = "standalone"
	}
	cfg.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg.ConfigPath, []byte("printers:\n  allowlist: []\n"), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	mockP := printer.NewMockPrinter(nil)
	qm := queue.NewManager(1, 10, 5*time.Second)
	reg := jobs.NewRegistry(50)
	client := wsclient.New(cfg, mockP, qm, nil, reg)
	return httpserver.New(cfg, client, mockP, qm, reg)
}

func do(srv *httpserver.Server, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, r)
	return rr
}

func TestSystemEndpoints(t *testing.T) {
	srv := newTestServer(t, false)

	for _, path := range []string{"/health", "/version", "/printers", "/printers/options", "/docs", "/swagger.json", "/config/printers", "/jobs"} {
		rr := do(srv, http.MethodGet, path, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s => %d (%s)", path, rr.Code, rr.Body.String())
		}
	}

	var health map[string]interface{}
	_ = json.Unmarshal(do(srv, http.MethodGet, "/health", "").Body.Bytes(), &health)
	if health["status"] != "ok" {
		t.Errorf("health status = %v", health["status"])
	}
}

func TestPrinterCRUD(t *testing.T) {
	srv := newTestServer(t, true)

	// CREATE
	rr := do(srv, http.MethodPost, "/printers", `{"name":"Kasir-Network","driverName":"Generic / Text Only","type":"IP","portOrUri":"192.168.1.200"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /printers => %d (%s)", rr.Code, rr.Body.String())
	}

	// READ
	rr = do(srv, http.MethodGet, "/printers/Kasir-Network", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /printers/Kasir-Network => %d (%s)", rr.Code, rr.Body.String())
	}

	// UPDATE (rename + default)
	rr = do(srv, http.MethodPut, "/printers/Kasir-Network", `{"newName":"Kasir-Utama","isDefault":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /printers/Kasir-Network => %d (%s)", rr.Code, rr.Body.String())
	}
	if rr = do(srv, http.MethodGet, "/printers/Kasir-Utama", ""); rr.Code != http.StatusOK {
		t.Fatalf("GET renamed printer => %d", rr.Code)
	}

	// SET DEFAULT endpoint
	if rr = do(srv, http.MethodPost, "/printers/Kasir-Utama/default", ""); rr.Code != http.StatusOK {
		t.Fatalf("POST /printers/{name}/default => %d (%s)", rr.Code, rr.Body.String())
	}

	// DELETE
	rr = do(srv, http.MethodDelete, "/printers/Kasir-Utama", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE /printers/Kasir-Utama => %d (%s)", rr.Code, rr.Body.String())
	}
	if rr = do(srv, http.MethodGet, "/printers/Kasir-Utama", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("GET deleted printer => %d, want 404", rr.Code)
	}
}

func TestMutationsForbiddenInAgentMode(t *testing.T) {
	srv := newTestServer(t, false)

	rr := do(srv, http.MethodPost, "/printers", `{"name":"X","type":"USB","portOrUri":"USB001"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST /printers in agent mode => %d, want 403", rr.Code)
	}
	if rr = do(srv, http.MethodDelete, "/printers/HP-LaserJet", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("DELETE in agent mode => %d, want 403", rr.Code)
	}
}

func TestConfigPrintersUpdate(t *testing.T) {
	srv := newTestServer(t, true)

	body := `{"defaultName":"Thermal","allowlist":["Thermal"],"thermal":[{"name":"Thermal","widthMm":80,"dpi":203,"cut":true,"trim":true,"feed":150}]}`
	rr := do(srv, http.MethodPut, "/config/printers", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /config/printers => %d (%s)", rr.Code, rr.Body.String())
	}

	rr = do(srv, http.MethodGet, "/config/printers", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /config/printers => %d", rr.Code)
	}
	var pc config.PrintersConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &pc); err != nil {
		t.Fatalf("unmarshal printers config: %v", err)
	}
	if pc.DefaultName != "Thermal" || len(pc.Thermal) != 1 || pc.Thermal[0].Name != "Thermal" {
		t.Fatalf("printers config not updated: %+v", pc)
	}
}

func TestNotFoundAndBadRequests(t *testing.T) {
	srv := newTestServer(t, true)

	if rr := do(srv, http.MethodGet, "/printers/nope", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("GET unknown printer => %d, want 404", rr.Code)
	}
	if rr := do(srv, http.MethodPost, "/printers", "{bad json"); rr.Code != http.StatusBadRequest {
		t.Fatalf("POST invalid body => %d, want 400", rr.Code)
	}
	if rr := do(srv, http.MethodGet, "/jobs/unknown", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("GET unknown job => %d, want 404", rr.Code)
	}
}

func TestPrintForbiddenWhenDisabled(t *testing.T) {
	srv := newTestServer(t, false)
	rr := do(srv, http.MethodPost, "/print", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST /print disabled => %d, want 403", rr.Code)
	}
}

func TestSwaggerContainsCrudPaths(t *testing.T) {
	srv := newTestServer(t, false)
	body := do(srv, http.MethodGet, "/swagger.json", "").Body.Bytes()
	for _, p := range []string{"/printers/{name}", "/config/printers", "/jobs"} {
		if !bytes.Contains(body, []byte(p)) {
			t.Errorf("swagger missing path %s", p)
		}
	}
}
