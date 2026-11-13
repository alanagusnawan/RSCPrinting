package relay

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/rscprinting/print-agent/internal/printer"
	"golang.org/x/exp/slog"
)

// RawServer adalah proxy TCP sederhana: setiap koneksi berisi byte mentah yang
// langsung diteruskan ke printer lokal (dipakai untuk socket://host:port).
type RawServer struct {
	printer DocumentPrinter
}

// NewRawServer membuat proxy RAW untuk backend printer.
func NewRawServer(p DocumentPrinter) *RawServer {
	return &RawServer{printer: p}
}

// Serve menerima koneksi pada ln dan mencetak setiap payload ke printerName.
func (s *RawServer) Serve(ctx context.Context, ln net.Listener, printerName string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
			default:
				slog.Warn("relay raw listener stopped", "err", err)
			}
			return
		}
		go s.handle(ctx, conn, printerName)
	}
}

func (s *RawServer) handle(ctx context.Context, conn net.Conn, printerName string) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	data, err := io.ReadAll(io.LimitReader(conn, 64<<20))
	if err != nil || len(data) == 0 {
		return
	}

	tmp, err := os.CreateTemp("", "relay-raw-*")
	if err != nil {
		slog.Error("relay raw temp file failed", "err", err)
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	_ = tmp.Close()

	pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.printer.Print(pctx, printer.Job{
		JobID:    fmt.Sprintf("relay-raw-%d", time.Now().UnixNano()),
		Printer:  printerName,
		FilePath: tmp.Name(),
		Format:   printer.FormatRaw,
	}); err != nil {
		slog.Error("relay raw print failed", "printer", printerName, "err", err)
		return
	}
	slog.Info("relay raw print accepted", "printer", printerName, "bytes", len(data))
}
