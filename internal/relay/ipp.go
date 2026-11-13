package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rscprinting/print-agent/internal/printer"
	"golang.org/x/exp/slog"
)

// DocumentPrinter adalah ketergantungan relay ke backend printer lokal.
type DocumentPrinter interface {
	Print(ctx context.Context, job printer.Job) error
}

// IPPServer adalah HTTP handler yang memahami protokol IPP (subset) dan
// mencetak dokumen yang diterima lewat printer lokal (Model 2 / relay).
type IPPServer struct {
	printer DocumentPrinter
}

// NewIPPServer membuat relay IPP untuk backend printer tertentu.
func NewIPPServer(p DocumentPrinter) *IPPServer {
	return &IPPServer{printer: p}
}

// SupportedDocumentFormats adalah format dokumen yang diiklankan ke client.
var SupportedDocumentFormats = []string{
	"application/pdf",
	"application/octet-stream",
	"image/jpeg",
	"image/png",
	"application/postscript",
}

func (s *IPPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Beberapa client mem-GET root untuk cek konektivitas.
		w.WriteHeader(http.StatusOK)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	req, err := parseIPP(body)
	if err != nil {
		http.Error(w, "invalid ipp request", http.StatusBadRequest)
		return
	}

	name := printerNameFromURI(req.PrinterURI)
	if name == "" {
		name = strings.Trim(strings.TrimPrefix(r.URL.Path, "/printers/"), "/")
	}

	switch req.Operation {
	case ippOpGetPrinterAttributes:
		writeIPP(w, buildPrinterAttributesResponse(req, name, "RSCPrinting Relay", SupportedDocumentFormats))

	case ippOpValidateJob:
		writeIPP(w, buildSimpleResponse(req, ippStatusOK))

	case ippOpPrintJob:
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if perr := s.printDocument(ctx, name, req); perr != nil {
			slog.Error("relay print failed", "printer", name, "err", perr)
			writeIPP(w, buildSimpleResponse(req, ippStatusClientErrorBadRequest))
			return
		}
		slog.Info("relay print accepted", "printer", name, "bytes", len(req.Document), "format", req.DocumentFormat)
		writeIPP(w, buildSimpleResponse(req, ippStatusOK))

	default:
		// Cancel-Job / Get-Job-Attributes dan lain-lain: balas sukses minimal.
		writeIPP(w, buildSimpleResponse(req, ippStatusOK))
	}
}

func (s *IPPServer) printDocument(ctx context.Context, name string, req ippRequest) error {
	if len(req.Document) == 0 {
		return fmt.Errorf("empty document")
	}
	ext := extForFormat(req.DocumentFormat, req.Document)
	tmp, err := os.CreateTemp("", "relay-ipp-*"+ext)
	if err != nil {
		return err
	}
	if _, err := tmp.Write(req.Document); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()

	return s.printer.Print(ctx, printer.Job{
		JobID:       fmt.Sprintf("relay-%d", time.Now().UnixNano()),
		Printer:     name,
		FilePath:    tmp.Name(),
		FileName:    "relay" + ext,
		Format:      printer.FormatAuto,
		ContentType: req.DocumentFormat,
	})
}

func writeIPP(w http.ResponseWriter, resp []byte) {
	w.Header().Set("Content-Type", "application/ipp")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

// extForFormat menentukan ekstensi file dari document-format IPP, dengan
// fallback deteksi magic bytes.
func extForFormat(format string, data []byte) string {
	f := strings.ToLower(strings.TrimSpace(format))
	switch {
	case strings.Contains(f, "pdf"):
		return ".pdf"
	case strings.Contains(f, "jpeg"), strings.Contains(f, "jpg"):
		return ".jpg"
	case strings.Contains(f, "png"):
		return ".png"
	case strings.Contains(f, "postscript"):
		return ".ps"
	}
	// Magic bytes.
	if len(data) >= 4 && data[0] == '%' && data[1] == 'P' && data[2] == 'D' && data[3] == 'F' {
		return ".pdf"
	}
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return ".png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 {
		return ".jpg"
	}
	if len(data) >= 2 && data[0] == 0x1B && data[1] == '@' {
		return ".escpos"
	}
	return ".bin"
}
