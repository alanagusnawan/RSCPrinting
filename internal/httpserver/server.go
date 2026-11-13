package httpserver

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/jobs"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	"github.com/rscprinting/print-agent/internal/version"
	"github.com/rscprinting/print-agent/internal/wsclient"
	"github.com/rscprinting/print-agent/pkg/apitypes"
	"golang.org/x/exp/slog"
)

//go:embed swagger.json
var swaggerJSON []byte

type Server struct {
	cfg          config.Config
	client       *wsclient.Client
	printer      printer.Printer
	queueManager *queue.Manager
	jobs         *jobs.Registry
	httpServer   *http.Server
}

func New(cfg config.Config, c *wsclient.Client, p printer.Printer, qm *queue.Manager, jobReg *jobs.Registry) *Server {
	s := &Server{
		cfg:          cfg,
		client:       c,
		printer:      p,
		queueManager: qm,
		jobs:         jobReg,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/printers", s.handlePrinters)
	mux.HandleFunc("/printers/", s.handlePrintersSubtree)
	mux.HandleFunc("/jobs", s.handleJobs)
	mux.HandleFunc("/jobs/", s.handleJobsSubtree)
	mux.HandleFunc("/config/printers", s.handleConfigPrinters)
	mux.HandleFunc("/print", s.handlePrint)
	mux.HandleFunc("/docs", s.handleDocs)
	mux.HandleFunc("/docs/", s.handleDocs)
	mux.HandleFunc("/swagger.json", s.handleSwaggerJSON)

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

// allowMutations memberi izin endpoint mutasi (POST/PUT/DELETE) hanya saat
// mode standalone atau server.enable_local_print_endpoint=true.
func (s *Server) allowMutations(w http.ResponseWriter) bool {
	if s.cfg.Server.EnableLocalPrintEndpoint || strings.EqualFold(s.cfg.Mode, "standalone") {
		return true
	}
	http.Error(w, "mutating endpoints are disabled in production agent mode", http.StatusForbidden)
	return false
}

func (s *Server) notifyPrintersUpdate(ctx context.Context) {
	if s.client == nil {
		return
	}
	if list, err := s.printer.List(ctx); err == nil {
		names := make([]string, 0, len(list))
		for _, p := range list {
			names = append(names, p.Name)
		}
		_ = s.client.NotifyPrintersUpdate(names)
	}
}

// ---------------------------------------------------------------------------
// System
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Printers (CRUD)
// ---------------------------------------------------------------------------

func (s *Server) handlePrinters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.printer.List(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get printers: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		if !s.allowMutations(w) {
			return
		}
		s.createPrinter(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePrintersSubtree(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/printers/"), "/")
	if rest == "" {
		s.handlePrinters(w, r)
		return
	}
	if rest == "options" {
		s.handlePrinterOptions(w, r)
		return
	}
	if rest == "sync" {
		s.handleSyncPrinters(w, r)
		return
	}
	if strings.HasSuffix(rest, "/default") {
		name, _ := url.PathUnescape(strings.Trim(strings.TrimSuffix(rest, "/default"), "/"))
		s.handleSetDefaultPrinter(w, r, name)
		return
	}
	if strings.HasSuffix(rest, "/share") {
		name, _ := url.PathUnescape(strings.Trim(strings.TrimSuffix(rest, "/share"), "/"))
		s.handleSharePrinter(w, r, name)
		return
	}
	if strings.HasSuffix(rest, "/unshare") {
		name, _ := url.PathUnescape(strings.Trim(strings.TrimSuffix(rest, "/unshare"), "/"))
		s.handleUnsharePrinter(w, r, name)
		return
	}
	name, _ := url.PathUnescape(rest)
	s.handlePrinterItem(w, r, name)
}

func (s *Server) handlePrinterOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	opts, err := s.printer.Options(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get printer options: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) createPrinter(w http.ResponseWriter, r *http.Request) {
	var params printer.RegisterPrinterParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if params.Name == "" || params.PortOrURI == "" || params.Type == "" {
		http.Error(w, "name, type, and portOrUri are required", http.StatusBadRequest)
		return
	}

	if err := s.printer.Register(r.Context(), params); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	s.notifyPrintersUpdate(r.Context())
	writeJSON(w, http.StatusCreated, map[string]interface{}{"success": true, "printer": params.Name})
}

func (s *Server) handlePrinterItem(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		http.Error(w, "printer name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		info, err := s.printer.Info(r.Context(), name)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "printer not found", "printer": name})
			return
		}
		writeJSON(w, http.StatusOK, info)
	case http.MethodPut, http.MethodPatch:
		if !s.allowMutations(w) {
			return
		}
		s.updatePrinter(w, r, name)
	case http.MethodDelete:
		if !s.allowMutations(w) {
			return
		}
		if err := s.printer.Remove(r.Context(), name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.notifyPrintersUpdate(r.Context())
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "printer": name})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) updatePrinter(w http.ResponseWriter, r *http.Request, name string) {
	var params printer.UpdatePrinterParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if params.NewName == "" && params.DriverName == "" && params.PortOrURI == "" && params.IsDefault == nil {
		http.Error(w, "no fields to update", http.StatusBadRequest)
		return
	}

	if err := s.printer.Update(r.Context(), name, params); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	s.notifyPrintersUpdate(r.Context())
	target := name
	if params.NewName != "" {
		target = params.NewName
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "printer": target})
}

func (s *Server) handleSetDefaultPrinter(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutations(w) {
		return
	}
	if name == "" {
		http.Error(w, "printer name required", http.StatusBadRequest)
		return
	}
	if err := s.printer.SetDefault(r.Context(), name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "default": name})
}

// handleSharePrinter membagikan printer lokal agar bisa dipasang di device lain.
func (s *Server) handleSharePrinter(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutations(w) {
		return
	}
	if name == "" {
		http.Error(w, "printer name required", http.StatusBadRequest)
		return
	}
	var opts printer.ShareOptions
	_ = json.NewDecoder(r.Body).Decode(&opts) // body opsional

	share, err := s.printer.Share(r.Context(), name, opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "share": share})
}

// handleUnsharePrinter menghentikan pembagian printer lokal.
func (s *Server) handleUnsharePrinter(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutations(w) {
		return
	}
	if name == "" {
		http.Error(w, "printer name required", http.StatusBadRequest)
		return
	}
	if err := s.printer.Unshare(r.Context(), name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "printer": name})
}

// handleSyncPrinters memasang beberapa printer sekaligus (bulk) pada device ini.
func (s *Server) handleSyncPrinters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutations(w) {
		return
	}
	var body struct {
		Printers []printer.RegisterPrinterParams `json:"printers"`
		Prune    bool                            `json:"prune"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	current, _ := s.printer.List(r.Context())
	have := make(map[string]bool, len(current))
	for _, p := range current {
		have[p.Name] = true
	}

	installed := []string{}
	updated := []string{}
	removed := []string{}
	failed := map[string]string{}

	for _, params := range body.Printers {
		if err := s.printer.Register(r.Context(), params); err != nil {
			failed[params.Name] = err.Error()
			continue
		}
		if have[params.Name] {
			updated = append(updated, params.Name)
		} else {
			installed = append(installed, params.Name)
		}
	}

	if body.Prune {
		want := make(map[string]bool, len(body.Printers))
		for _, params := range body.Printers {
			want[params.Name] = true
		}
		for _, p := range current {
			if want[p.Name] {
				continue
			}
			if err := s.printer.Remove(r.Context(), p.Name); err != nil {
				failed[p.Name] = err.Error()
			} else {
				removed = append(removed, p.Name)
			}
		}
	}

	s.notifyPrintersUpdate(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   len(failed) == 0,
		"installed": installed,
		"updated":   updated,
		"removed":   removed,
		"failed":    failed,
	})
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

func (s *Server) handleConfigPrinters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.Printers)
	case http.MethodPut:
		if !s.allowMutations(w) {
			return
		}
		var body config.PrintersConfig
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		if s.cfg.ConfigPath == "" {
			http.Error(w, "config path not set", http.StatusInternalServerError)
			return
		}
		if err := config.SetPrintersConfig(s.cfg.ConfigPath, body); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.cfg.Printers = body
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "printers": body})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.jobs == nil {
		writeJSON(w, http.StatusOK, []jobs.Job{})
		return
	}
	writeJSON(w, http.StatusOK, s.jobs.List())
}

func (s *Server) handleJobsSubtree(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/jobs/"), "/")
	if id == "" {
		http.Error(w, "job id required", http.StatusBadRequest)
		return
	}
	if s.jobs == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "job not found", "jobId": id})
		return
	}

	switch r.Method {
	case http.MethodGet:
		j, ok := s.jobs.Get(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "job not found", "jobId": id})
			return
		}
		writeJSON(w, http.StatusOK, j)
	case http.MethodDelete:
		if !s.allowMutations(w) {
			return
		}
		if !s.jobs.Delete(id) {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "job not found", "jobId": id})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "jobId": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---------------------------------------------------------------------------
// Print
// ---------------------------------------------------------------------------

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

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file in form", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := ""
	contentType := ""
	if header != nil {
		fileName = header.Filename
		contentType = header.Header.Get("Content-Type")
	}

	ext := ".bin"
	if e := filepath.Ext(fileName); e != "" {
		ext = e
	}

	requestedFormat := printer.ParseFormat(r.FormValue("format"))

	tmp, err := os.CreateTemp("", "local-print-*"+ext)
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
	jobCtx, cancelJob := context.WithCancel(r.Context())
	defer cancelJob()
	if s.jobs != nil {
		s.jobs.Create(jobID, targetPrinter, "http")
		s.jobs.SetCancel(jobID, cancelJob)
	}

	printErr := s.queueManager.Execute(jobCtx, targetPrinter, func(execCtx context.Context) error {
		if s.jobs != nil {
			s.jobs.Start(jobID)
		}
		return s.printer.Print(execCtx, printer.Job{
			JobID:       jobID,
			Printer:     targetPrinter,
			FilePath:    tmpName,
			Format:      requestedFormat,
			ContentType: contentType,
			FileName:    fileName,
			Template:    apitypes.PrintTemplate{Copies: 1},
		})
	})

	if printErr != nil {
		if s.jobs != nil {
			s.jobs.Finish(jobID, printErr, "PRINT_FAILED")
		}
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"jobId":   jobID,
			"error":   printErr.Error(),
		})
		return
	}

	if s.jobs != nil {
		s.jobs.Finish(jobID, nil, "")
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"jobId":   jobID,
	})
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, val interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(val)
}

func (s *Server) handleSwaggerJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(swaggerJSON)
	}
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(swaggerUIHTML))
	}
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>RSCPrinting API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    html { box-sizing: border-box; overflow-y: scroll; }
    *, *:before, *:after { box-sizing: inherit; }
    body { margin: 0; background: #fafafa; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function() {
      SwaggerUIBundle({
        url: "/swagger.json",
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        layout: "StandaloneLayout"
      });
    };
  </script>
</body>
</html>`
