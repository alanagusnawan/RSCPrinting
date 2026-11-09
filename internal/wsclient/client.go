package wsclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	"github.com/rscprinting/print-agent/internal/storage"
	"github.com/rscprinting/print-agent/internal/version"
	"github.com/rscprinting/print-agent/pkg/apitypes"
	"golang.org/x/exp/slog"
)

var (
	ErrFatalAuthFailed = errors.New("fatal: invalid token (4001) or agentId (4002)")
	ErrConnectionReset = errors.New("connection reset by peer")
)

type Status string

const (
	StatusDisconnected Status = "Disconnected"
	StatusConnecting   Status = "Connecting"
	StatusConnected    Status = "Connected"
	StatusAuthFailed   Status = "Auth Failed"
)

type Client struct {
	cfg          config.Config
	printer      printer.Printer
	queueManager *queue.Manager
	storage      *storage.Store

	statusMu     sync.RWMutex
	status       Status
	statusDetail string
	statusCh     chan Status

	connMu   sync.Mutex
	conn     net.Conn
	lastPong time.Time
}

func New(cfg config.Config, p printer.Printer, qm *queue.Manager, store *storage.Store) *Client {
	return &Client{
		cfg:          cfg,
		printer:      p,
		queueManager: qm,
		storage:      store,
		status:       StatusDisconnected,
		statusCh:     make(chan Status, 16),
	}
}

func (c *Client) Status() (Status, string) {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status, c.statusDetail
}

func (c *Client) StatusChannel() <-chan Status {
	return c.statusCh
}

func (c *Client) setStatus(s Status, detail string) {
	c.statusMu.Lock()
	c.status = s
	c.statusDetail = detail
	c.statusMu.Unlock()

	select {
	case c.statusCh <- s:
	default:
	}
}

func (c *Client) Run(ctx context.Context) error {
	backoff := 1 * time.Second
	maxBackoff := c.cfg.WSServer.ReconnectBackoffMax
	if maxBackoff <= 0 {
		maxBackoff = 60 * time.Second
	}

	for {
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			c.setStatus(StatusDisconnected, "Shutdown")
			return ctx.Err()
		}

		if errors.Is(err, ErrFatalAuthFailed) {
			c.setStatus(StatusAuthFailed, err.Error())
			slog.Error("fatal websocket auth error, stopping reconnect loop", "err", err)
			return err
		}

		c.setStatus(StatusDisconnected, fmt.Sprintf("Disconnected: %v", err))
		slog.Warn("websocket disconnected, reconnecting...", "err", err, "retry_in", backoff)

		select {
		case <-ctx.Done():
			c.setStatus(StatusDisconnected, "Shutdown")
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context) error {
	c.setStatus(StatusConnecting, fmt.Sprintf("Connecting to %s", c.cfg.WSServer.URL))

	dialer := ws.Dialer{
		Header: ws.HandshakeHeaderHTTP(http.Header{
			"Authorization": []string{"Bearer " + c.cfg.WSServer.Token},
		}),
		Timeout: c.cfg.WSServer.HandshakeTimeout,
	}

	conn, _, _, err := dialer.Dial(ctx, c.cfg.WSServer.URL)
	if err != nil {
		var wsErr ws.StatusError
		if errors.As(err, &wsErr) {
			if wsErr == 4001 || wsErr == 4002 {
				return ErrFatalAuthFailed
			}
		}
		return fmt.Errorf("dial failed: %w", err)
	}

	c.connMu.Lock()
	c.conn = conn
	c.lastPong = time.Now()
	c.connMu.Unlock()

	defer func() {
		c.connMu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
			c.conn = nil
		}
		c.connMu.Unlock()
	}()

	// 1. Send Hello
	printersList, err := c.printer.List(ctx)
	var printerNames []string
	if err == nil {
		for _, p := range printersList {
			printerNames = append(printerNames, p.Name)
		}
	}

	hello := apitypes.HelloMessage{
		Type:        apitypes.TypeHello,
		AgentID:     c.cfg.Agent.ID,
		Hostname:    c.cfg.Agent.Hostname,
		DisplayName: c.cfg.Agent.DisplayName,
		Printers:    printerNames,
		Version:     version.Version,
		OS:          runtime.GOOS,
	}

	helloBytes, _ := json.Marshal(hello)
	if err := wsutil.WriteClientText(conn, helloBytes); err != nil {
		return fmt.Errorf("send hello failed: %w", err)
	}

	// 2. Wait for Welcome with timeout
	welcomeCh := make(chan error, 1)
	go func() {
		frame, err := ws.ReadFrame(conn)
		if err != nil {
			welcomeCh <- err
			return
		}

		if frame.Header.OpCode == ws.OpClose {
			code, _ := ws.ParseCloseFrameData(frame.Payload)
			if code == 4001 || code == 4002 {
				welcomeCh <- ErrFatalAuthFailed
			} else {
				welcomeCh <- fmt.Errorf("server closed during handshake: code=%d", code)
			}
			return
		}

		if frame.Header.OpCode != ws.OpText {
			welcomeCh <- fmt.Errorf("expected text frame for welcome, got %v", frame.Header.OpCode)
			return
		}

		var welcome apitypes.WelcomeMessage
		if err := json.Unmarshal(frame.Payload, &welcome); err != nil {
			welcomeCh <- fmt.Errorf("parse welcome error: %w", err)
			return
		}
		if welcome.Type != apitypes.TypeWelcome {
			welcomeCh <- fmt.Errorf("expected type welcome, got %s", welcome.Type)
			return
		}
		welcomeCh <- nil
	}()

	welcomeTimeout := c.cfg.WSServer.WelcomeTimeout
	if welcomeTimeout <= 0 {
		welcomeTimeout = 10 * time.Second
	}

	select {
	case err := <-welcomeCh:
		if err != nil {
			if errors.Is(err, ErrFatalAuthFailed) {
				return ErrFatalAuthFailed
			}
			return fmt.Errorf("welcome failed: %w", err)
		}
	case <-time.After(welcomeTimeout):
		return errors.New("timeout waiting for welcome message")
	case <-ctx.Done():
		return ctx.Err()
	}

	c.setStatus(StatusConnected, "Connected to server")
	slog.Info("websocket connected and handshake completed")

	// 3. Flush pending results from BoltDB
	c.flushPendingResults(ctx)

	// 4. Start Ping ticker and read loop
	return c.serveLoop(ctx, conn)
}

func (c *Client) flushPendingResults(ctx context.Context) {
	if c.storage == nil {
		return
	}
	pending, err := c.storage.GetPendingResults()
	if err != nil {
		slog.Error("failed to get pending results", "err", err)
		return
	}

	for _, res := range pending {
		if ctx.Err() != nil {
			return
		}
		data, err := json.Marshal(res)
		if err != nil {
			continue
		}
		c.connMu.Lock()
		conn := c.conn
		c.connMu.Unlock()
		if conn == nil {
			return
		}

		if err := wsutil.WriteClientText(conn, data); err == nil {
			_ = c.storage.DeletePendingResult(res.JobID)
			slog.Info("flushed pending job result", "jobId", res.JobID, "success", res.Success)
		} else {
			slog.Warn("failed to flush pending result, will retry later", "jobId", res.JobID, "err", err)
			return
		}
	}
}

func (c *Client) serveLoop(ctx context.Context, conn net.Conn) error {
	pingInterval := c.cfg.WSServer.PingInterval
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	errCh := make(chan error, 2)

	// Goroutine Ping / Pong monitor
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.connMu.Lock()
				conn := c.conn
				c.connMu.Unlock()
				if conn == nil {
					return
				}

				if err := wsutil.WriteClientMessage(conn, ws.OpPing, []byte("ping")); err != nil {
					errCh <- fmt.Errorf("send ping error: %w", err)
					return
				}
			}
		}
	}()

	// Read loop
	go func() {
		for {
			frame, err := ws.ReadFrame(conn)
			if err != nil {
				errCh <- err
				return
			}

			switch frame.Header.OpCode {
			case ws.OpClose:
				code, reason := ws.ParseCloseFrameData(frame.Payload)
				if code == 4001 || code == 4002 {
					errCh <- ErrFatalAuthFailed
				} else {
					errCh <- fmt.Errorf("server closed connection: code=%d reason=%s", code, reason)
				}
				return

			case ws.OpPing:
				_ = wsutil.WriteClientMessage(conn, ws.OpPong, frame.Payload)

			case ws.OpPong:
				c.connMu.Lock()
				c.lastPong = time.Now()
				c.connMu.Unlock()

			case ws.OpText:
				var jobMeta apitypes.JobMessage
				if err := json.Unmarshal(frame.Payload, &jobMeta); err != nil {
					slog.Warn("unmarshal text frame failed", "err", err)
					continue
				}

				if jobMeta.Type == apitypes.TypeJob {
					// Tunggu frame binary berikutnya untuk byte PDF
					binFrame, err := ws.ReadFrame(conn)
					if err != nil {
						errCh <- fmt.Errorf("read binary frame error: %w", err)
						return
					}
					if binFrame.Header.OpCode != ws.OpBinary {
						slog.Error("expected binary frame after job metadata, got", "opcode", binFrame.Header.OpCode)
						continue
					}

					pdfData := make([]byte, len(binFrame.Payload))
					copy(pdfData, binFrame.Payload)

					// Handle job secara async dengan concurrency kontrol
					go c.handleJob(jobMeta, pdfData)
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (c *Client) handleJob(meta apitypes.JobMessage, pdfBytes []byte) {
	start := time.Now()

	ext := jobFileExt(meta)

	// 1. Simpan payload ke temp file dengan ekstensi sesuai format.
	tmp, err := os.CreateTemp("", "print-job-*"+ext)
	if err != nil {
		c.sendJobResult(apitypes.JobResultMessage{
			Type:       apitypes.TypeJobResult,
			JobID:      meta.JobID,
			Success:    false,
			Error:      fmt.Sprintf("create temp file failed: %v", err),
			ErrorCode:  "TEMP_FILE_ERROR",
			DurationMs: time.Since(start).Milliseconds(),
		})
		return
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(pdfBytes); err != nil {
		_ = tmp.Close()
		c.sendJobResult(apitypes.JobResultMessage{
			Type:       apitypes.TypeJobResult,
			JobID:      meta.JobID,
			Success:    false,
			Error:      fmt.Sprintf("write temp file failed: %v", err),
			ErrorCode:  "WRITE_ERROR",
			DurationMs: time.Since(start).Milliseconds(),
		})
		return
	}
	_ = tmp.Close()

	// 2. Eksekusi print via Queue Manager
	err = c.queueManager.Execute(context.Background(), meta.Printer, func(execCtx context.Context) error {
		return c.printer.Print(execCtx, printer.Job{
			JobID:       meta.JobID,
			Printer:     meta.Printer,
			FilePath:    tmpName,
			Format:      printer.ParseFormat(meta.Format),
			ContentType: meta.ContentType,
			FileName:    meta.FileName,
			Template:    meta.Template,
		})
	})

	duration := time.Since(start).Milliseconds()

	if err != nil {
		errCode := "PRINT_FAILED"
		if errors.Is(err, queue.ErrPrinterBusy) {
			errCode = "PRINTER_BUSY"
		} else if errors.Is(err, queue.ErrPrinterTimeout) {
			errCode = "PRINTER_TIMEOUT"
		} else if errors.Is(err, printer.ErrGSNotFound) {
			errCode = "GS_NOT_FOUND"
		} else if errors.Is(err, printer.ErrUnsupportedFormat) {
			errCode = "UNSUPPORTED_FORMAT"
		} else if errors.Is(err, printer.ErrOfficeConverterNotFound) {
			errCode = "OFFICE_CONVERTER_NOT_FOUND"
		} else if errors.Is(err, printer.ErrOfficeConvertFailed) {
			errCode = "OFFICE_CONVERT_FAILED"
		} else if errors.Is(err, printer.ErrRawSendFailed) {
			errCode = "RAW_SEND_FAILED"
		} else if errors.Is(err, printer.ErrDriverIncompatible) {
			errCode = "DRIVER_INCOMPATIBLE"
		} else if errors.Is(err, printer.ErrThermalRenderFailed) {
			errCode = "THERMAL_RENDER_FAILED"
		} else if errors.Is(err, printer.ErrPrepareFailed) {
			errCode = "PREPARE_FAILED"
		}

		c.sendJobResult(apitypes.JobResultMessage{
			Type:       apitypes.TypeJobResult,
			JobID:      meta.JobID,
			Success:    false,
			Error:      err.Error(),
			ErrorCode:  errCode,
			DurationMs: duration,
		})
	} else {
		c.sendJobResult(apitypes.JobResultMessage{
			Type:       apitypes.TypeJobResult,
			JobID:      meta.JobID,
			Success:    true,
			DurationMs: duration,
		})
	}
}

func jobFileExt(meta apitypes.JobMessage) string {
	if ext := strings.ToLower(filepath.Ext(meta.FileName)); ext != "" {
		return ext
	}
	switch printer.ParseFormat(meta.Format) {
	case printer.FormatPDF:
		return ".pdf"
	case printer.FormatPS:
		return ".ps"
	case printer.FormatText:
		return ".txt"
	case printer.FormatZPL:
		return ".zpl"
	case printer.FormatOffice:
		return ".docx"
	default:
		return ".bin"
	}
}

func (c *Client) sendJobResult(res apitypes.JobResultMessage) {
	data, err := json.Marshal(res)
	if err != nil {
		slog.Error("marshal job result failed", "err", err)
		return
	}

	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()

	var sent bool
	if conn != nil {
		if err := wsutil.WriteClientText(conn, data); err == nil {
			sent = true
			slog.Info("sent job result to server", "jobId", res.JobID, "success", res.Success, "durationMs", res.DurationMs)
		} else {
			slog.Warn("failed to send job result, saving to pending store", "jobId", res.JobID, "err", err)
		}
	}

	if !sent && c.storage != nil {
		if err := c.storage.SavePendingResult(res); err != nil {
			slog.Error("failed to save pending result to boltdb", "jobId", res.JobID, "err", err)
		} else {
			slog.Info("saved pending job result to boltdb", "jobId", res.JobID)
		}
	}
}

func (c *Client) NotifyPrintersUpdate(printers []string) error {
	msg := apitypes.PrintersUpdateMessage{
		Type:     apitypes.TypePrintersUpdate,
		Printers: printers,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()

	if conn == nil {
		return errors.New("websocket not connected")
	}

	return wsutil.WriteClientText(conn, data)
}
