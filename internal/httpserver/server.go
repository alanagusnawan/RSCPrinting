package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	"github.com/rscprinting/print-agent/internal/version"
	"github.com/rscprinting/print-agent/internal/wsclient"
	"github.com/rscprinting/print-agent/pkg/apitypes"
	"golang.org/x/exp/slog"
)

type Server struct {
	cfg          config.Config
	client       *wsclient.Client
	printer      printer.Printer
	queueManager *queue.Manager
	httpServer   *http.Server
}

func New(cfg config.Config, c *wsclient.Client, p printer.Printer, qm *queue.Manager) *Server {
	s := &Server{
		cfg:          cfg,
		client:       c,
		printer:      p,
		queueManager: qm,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/printers", s.handlePrinters)
	mux.HandleFunc("/jobs/", s.handleJobs)
	mux.HandleFunc("/print", s.handlePrint)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.httpServer.Handler.ServeHTTP(w, r)
}

func (s *Server) Start() error {
	slog.Info("starting local debug http server", "addr", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status, detail := s.client.Status()
	printersList, _ := s.printer.List(r.Context())

	resp := map[string]interface{}{
		"status":    "ok",
		"wsStatus":  status,
		"wsDetail":  detail,
		"agentId":   s.cfg.Agent.ID,
		"version":   version.Version,
		"printers":  printersList,
		"mode":      s.cfg.Mode,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, version.Get())
}

func (s *Server) handlePrinters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	list, err := s.printer.List(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get printers: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	// Status stub untuk endpoint /jobs/{id}
	jobID := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if jobID == "" {
		http.Error(w, "job id required", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobId":  jobID,
		"status": "processed",
	})
}

func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request) {
	// Diizinkan hanya jika mode=standalone atau server.enable_local_print_endpoint: true
	if !s.cfg.Server.EnableLocalPrintEndpoint && !strings.EqualFold(s.cfg.Mode, "standalone") {
		http.Error(w, "local print endpoint is disabled in production agent mode", http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Multipart form: "printer", "file"
	err := r.ParseMultipartForm(32 << 20) // 32MB max
	if err != nil {
		http.Error(w, "parse multipart form error", http.StatusBadRequest)
		return
	}

	targetPrinter := r.FormValue("printer")
	if targetPrinter == "" {
		targetPrinter = s.cfg.Printers.DefaultName
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file in form", http.StatusBadRequest)
		return
	}
	defer file.Close()

	tmp, err := os.CreateTemp("", "local-print-*.pdf")
	if err != nil {
		http.Error(w, "failed to create temp file", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, file); err != nil {
		_ = tmp.Close()
		http.Error(w, "failed to write temp file", http.StatusInternalServerError)
		return
	}
	_ = tmp.Close()

	jobID := fmt.Sprintf("local-%d", time.Now().UnixNano())
	printErr := s.queueManager.Execute(r.Context(), targetPrinter, func(execCtx context.Context) error {
		return s.printer.Print(execCtx, printer.Job{
			JobID:    jobID,
			Printer:  targetPrinter,
			FilePath: tmpName,
			Template: apitypes.PrintTemplate{Copies: 1},
		})
	})

	if printErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   printErr.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"jobId":   jobID,
	})
}

func writeJSON(w http.ResponseWriter, code int, val interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(val)
}
