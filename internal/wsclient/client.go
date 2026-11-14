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
	"github.com/rscprinting/print-agent/internal/jobs"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	"github.com/rscprinting/print-agent/internal/relay"
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

// controlTimeout membatasi lama pemrosesan pesan kontrol (get_options,
// get_printers) agar agent selalu membalas sebelum server timeout.
const controlTimeout = 5 * time.Second

// printerOpTimeout membatasi operasi pasang/ubah/hapus/share printer OS.
const printerOpTimeout = 20 * time.Second

type Client struct {
	cfg          config.Config
	printer      printer.Printer
	queueManager *queue.Manager
	storage      *storage.Store
	jobs         *jobs.Registry

	statusMu     sync.RWMutex
	status       Status
	statusDetail string
	statusCh     chan Status

	connMu   sync.Mutex
	conn     net.Conn
	lastPong time.Time

	writeMu sync.Mutex // serialisasi penulisan frame ke koneksi

	shareMu    sync.Mutex
	shareCache map[string]printer.PrinterShare

	relay *relay.Manager
}

// SetRelay menautkan relay manager (opsional) ke client.
func (c *Client) SetRelay(m *relay.Manager) { c.relay = m }

func New(cfg config.Config, p printer.Printer, qm *queue.Manager, store *storage.Store, jobReg *jobs.Registry) *Client {
	return &Client{
		cfg:          cfg,
		printer:      p,
		queueManager: qm,
		storage:      store,
		jobs:         jobReg,
		status:       StatusDisconnected,
		statusCh:     make(chan Status, 16),
		shareCache:   make(map[string]printer.PrinterShare),
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
			raw := frame.Payload
			if len(raw) > 2048 {
				raw = raw[:2048]
			}
			slog.Warn("handshake frame not JSON", "raw", string(raw))
			welcomeCh <- fmt.Errorf("parse welcome error: %w", err)
			return
		}
		if welcome.Type != apitypes.TypeWelcome {
			raw := frame.Payload
			if len(raw) > 2048 {
				raw = raw[:2048]
			}
			slog.Info("handshake frame (unexpected type)", "type", welcome.Type, "raw", string(raw))
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

	// 3b. Laporkan detail printer + endpoint share ke server (best effort).
	if err := c.PublishPrintersDetail(ctx); err != nil {
		slog.Warn("publish printers detail failed", "err", err)
	}

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

			case ws.OpBinary:
				// Bisa jadi pesan kontrol berformat JSON di frame binary.
				payload := append([]byte(nil), frame.Payload...)
				go c.handleControlMessage(payload, conn)

			case ws.OpText:
				var generic struct {
					Type apitypes.MessageType `json:"type"`
				}
				if err := json.Unmarshal(frame.Payload, &generic); err != nil {
					slog.Warn("unmarshal text frame failed", "err", err)
					continue
				}

				switch generic.Type {
				case apitypes.TypeGetOptions:
					var optMsg apitypes.GetOptionsMessage
					_ = json.Unmarshal(frame.Payload, &optMsg)
					go c.handleGetOptions(optMsg)
					continue

				case apitypes.TypeRegisterPrinter:
					var regMsg apitypes.RegisterPrinterMessage
					_ = json.Unmarshal(frame.Payload, &regMsg)
					go c.handleRegisterPrinter(regMsg)
					continue

				case apitypes.TypeSetDefaultPrinter:
					var defMsg apitypes.SetDefaultPrinterMessage
					_ = json.Unmarshal(frame.Payload, &defMsg)
					go c.handleSetDefaultPrinter(defMsg)
					continue

				case apitypes.TypeDeletePrinter:
					var delMsg apitypes.DeletePrinterMessage
					_ = json.Unmarshal(frame.Payload, &delMsg)
					go c.handleDeletePrinter(delMsg)
					continue

				case apitypes.TypeUpdatePrinter:
					var updMsg apitypes.UpdatePrinterMessage
					_ = json.Unmarshal(frame.Payload, &updMsg)
					go c.handleUpdatePrinter(updMsg)
					continue

				case apitypes.TypeSyncPrinters:
					var syncMsg apitypes.SyncPrintersMessage
					_ = json.Unmarshal(frame.Payload, &syncMsg)
					go c.handleSyncPrinters(syncMsg)
					continue

				case apitypes.TypeSharePrinter:
					var shMsg apitypes.SharePrinterMessage
					_ = json.Unmarshal(frame.Payload, &shMsg)
					go c.handleSharePrinter(shMsg)
					continue

				case apitypes.TypeUnsharePrinter:
					var unshMsg apitypes.UnsharePrinterMessage
					_ = json.Unmarshal(frame.Payload, &unshMsg)
					go c.handleUnsharePrinter(unshMsg)
					continue

				case apitypes.TypeJob:
					var jobMeta apitypes.JobMessage
					if err := json.Unmarshal(frame.Payload, &jobMeta); err != nil {
						slog.Warn("unmarshal job meta failed", "err", err)
						continue
					}
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
					continue
				}

				// Pesan kontrol (mis. permintaan options/printer dari server).
				// Jawab agar server tidak timeout menunggu respons.
				payload := append([]byte(nil), frame.Payload...)
				go c.handleControlMessage(payload, conn)
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

	jobCtx, cancelJob := context.WithCancel(context.Background())
	defer cancelJob()
	if c.jobs != nil {
		c.jobs.Create(meta.JobID, meta.Printer, "ws")
		c.jobs.SetCancel(meta.JobID, cancelJob)
	}

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
	err = c.queueManager.Execute(jobCtx, meta.Printer, func(execCtx context.Context) error {
		if c.jobs != nil {
			c.jobs.Start(meta.JobID)
		}
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

		if c.jobs != nil {
			c.jobs.Finish(meta.JobID, err, errCode)
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
		if c.jobs != nil {
			c.jobs.Finish(meta.JobID, nil, "")
		}
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
		if err := c.writeText(conn, data); err == nil {
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

func (c *Client) handleGetOptions(msg apitypes.GetOptionsMessage) {
	slog.Info("received get_options request from server", "requestId", msg.RequestID)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	opts, err := c.printer.Options(ctx)
	res := apitypes.OptionsResultMessage{
		Type:      apitypes.TypeOptionsResult,
		RequestID: msg.RequestID,
		Success:   err == nil,
		Options:   opts,
	}
	if err != nil {
		res.Error = err.Error()
	}
	data, err := json.Marshal(res)
	if err != nil {
		slog.Error("marshal options result failed", "err", err)
		return
	}
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn != nil {
		if writeErr := wsutil.WriteClientText(conn, data); writeErr != nil {
			slog.Error("failed to write options result to server", "err", writeErr)
		} else {
			slog.Info("sent options result to server successfully", "requestId", msg.RequestID)
		}
	}
}

// writeJSON mengirim sebuah pesan JSON ke server via koneksi aktif.
func (c *Client) writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("marshal ws message failed", "err", err)
		return
	}
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn == nil {
		return
	}
	if err := c.writeText(conn, data); err != nil {
		slog.Warn("send ws message failed", "err", err)
	}
}

// notifyPrintersUpdate mengirim daftar nama printer + detail share terbaru.
func (c *Client) notifyPrintersUpdate(ctx context.Context) {
	if list, err := c.printer.List(ctx); err == nil {
		names := make([]string, 0, len(list))
		for _, p := range list {
			names = append(names, p.Name)
		}
		_ = c.NotifyPrintersUpdate(names)
	}
	_ = c.PublishPrintersDetail(ctx)
}

func (c *Client) handleRegisterPrinter(msg apitypes.RegisterPrinterMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), printerOpTimeout)
	defer cancel()
	err := c.printer.Register(ctx, printer.RegisterPrinterParams{
		Name:       msg.Name,
		DriverName: msg.DriverName,
		Type:       printer.PrinterType(msg.PrinterType),
		PortOrURI:  msg.PortOrURI,
		IsDefault:  msg.IsDefault,
		PPD:        msg.PPD,
		Driverless: msg.Driverless,
	})
	res := apitypes.RegisterPrinterResultMessage{
		Type:      apitypes.TypeRegisterPrinterResult,
		RequestID: msg.RequestID,
		Success:   err == nil,
		Printer:   msg.Name,
	}
	if err != nil {
		res.Error = err.Error()
	}
	c.writeJSON(res)

	if err == nil {
		c.notifyPrintersUpdate(ctx)
	}
}

func (c *Client) handleSetDefaultPrinter(msg apitypes.SetDefaultPrinterMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), printerOpTimeout)
	defer cancel()
	err := c.printer.SetDefault(ctx, msg.Printer)
	res := apitypes.SetDefaultResultMessage{
		Type:      apitypes.TypeSetDefaultResult,
		RequestID: msg.RequestID,
		Success:   err == nil,
		Printer:   msg.Printer,
	}
	if err != nil {
		res.Error = err.Error()
	}
	c.writeJSON(res)

	if err == nil {
		c.notifyPrintersUpdate(ctx)
	}
}

func (c *Client) handleDeletePrinter(msg apitypes.DeletePrinterMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), printerOpTimeout)
	defer cancel()
	err := c.printer.Remove(ctx, msg.Printer)
	res := apitypes.DeletePrinterResultMessage{
		Type:      apitypes.TypeDeletePrinterResult,
		RequestID: msg.RequestID,
		Success:   err == nil,
		Printer:   msg.Printer,
	}
	if err != nil {
		res.Error = err.Error()
	}
	c.writeJSON(res)

	if err == nil {
		c.shareMu.Lock()
		delete(c.shareCache, msg.Printer)
		c.shareMu.Unlock()
		c.notifyPrintersUpdate(ctx)
	}
}

func (c *Client) handleUpdatePrinter(msg apitypes.UpdatePrinterMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), printerOpTimeout)
	defer cancel()
	err := c.printer.Update(ctx, msg.Printer, printer.UpdatePrinterParams{
		NewName:    msg.NewName,
		DriverName: msg.DriverName,
		Type:       printer.PrinterType(msg.PrinterType),
		PortOrURI:  msg.PortOrURI,
		IsDefault:  msg.IsDefault,
	})
	res := apitypes.UpdatePrinterResultMessage{
		Type:      apitypes.TypeUpdatePrinterResult,
		RequestID: msg.RequestID,
		Success:   err == nil,
		Printer:   msg.Printer,
	}
	if msg.NewName != "" {
		res.Printer = msg.NewName
	}
	if err != nil {
		res.Error = err.Error()
	}
	c.writeJSON(res)

	if err == nil {
		c.notifyPrintersUpdate(ctx)
	}
}

func (c *Client) handleSyncPrinters(msg apitypes.SyncPrintersMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), printerOpTimeout)
	defer cancel()

	current, _ := c.printer.List(ctx)
	have := make(map[string]bool, len(current))
	for _, p := range current {
		have[p.Name] = true
	}

	res := apitypes.SyncPrintersResultMessage{
		Type:      apitypes.TypeSyncPrintersResult,
		RequestID: msg.RequestID,
		Failed:    map[string]string{},
	}

	for _, spec := range msg.Printers {
		params := printer.RegisterPrinterParams{
			Name:       spec.Name,
			DriverName: spec.DriverName,
			Type:       printer.PrinterType(spec.PrinterType),
			PortOrURI:  spec.PortOrURI,
			IsDefault:  spec.IsDefault,
			PPD:        spec.PPD,
			Driverless: spec.Driverless,
		}
		if err := c.printer.Register(ctx, params); err != nil {
			res.Failed[spec.Name] = err.Error()
			continue
		}
		if have[spec.Name] {
			res.Updated = append(res.Updated, spec.Name)
		} else {
			res.Installed = append(res.Installed, spec.Name)
		}
	}

	if msg.Prune {
		want := make(map[string]bool, len(msg.Printers))
		for _, spec := range msg.Printers {
			want[spec.Name] = true
		}
		for _, p := range current {
			if want[p.Name] {
				continue
			}
			if err := c.printer.Remove(ctx, p.Name); err != nil {
				res.Failed[p.Name] = err.Error()
			} else {
				res.Removed = append(res.Removed, p.Name)
			}
		}
	}

	res.Success = len(res.Failed) == 0
	c.writeJSON(res)
	c.notifyPrintersUpdate(ctx)
}

func (c *Client) handleSharePrinter(msg apitypes.SharePrinterMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), printerOpTimeout)
	defer cancel()
	share, err := c.printer.Share(ctx, msg.Printer, printer.ShareOptions{
		Protocol:  printer.ShareProtocol(msg.Protocol),
		ShareName: msg.ShareName,
	})
	res := apitypes.SharePrinterResultMessage{
		Type:      apitypes.TypeSharePrinterResult,
		RequestID: msg.RequestID,
		Success:   err == nil,
		Printer:   msg.Printer,
	}
	if err != nil {
		res.Error = err.Error()
	} else {
		res.Protocol = share.Protocol
		res.ShareName = share.ShareName
		res.SMBPath = share.SMBPath
		res.IPPURI = share.IPPURI
		res.SocketURI = share.SocketURI
		c.shareMu.Lock()
		c.shareCache[msg.Printer] = share
		c.shareMu.Unlock()
	}
	c.writeJSON(res)
	_ = c.PublishPrintersDetail(ctx)
}

func (c *Client) handleUnsharePrinter(msg apitypes.UnsharePrinterMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), printerOpTimeout)
	defer cancel()
	err := c.printer.Unshare(ctx, msg.Printer)
	res := apitypes.UnsharePrinterResultMessage{
		Type:      apitypes.TypeUnsharePrinterResult,
		RequestID: msg.RequestID,
		Success:   err == nil,
		Printer:   msg.Printer,
	}
	if err != nil {
		res.Error = err.Error()
	} else {
		c.shareMu.Lock()
		delete(c.shareCache, msg.Printer)
		c.shareMu.Unlock()
	}
	c.writeJSON(res)
	_ = c.PublishPrintersDetail(ctx)
}

// shareAllowed memeriksa apakah printer termasuk allowlist (kosong = semua).
func (c *Client) shareAllowed(name string) bool {
	al := c.cfg.Printers.Allowlist
	if len(al) == 0 {
		return true
	}
	for _, n := range al {
		if n == name {
			return true
		}
	}
	return false
}

// shareNameFor membentuk nama share dari pola config (mis. "RSC-%s").
func (c *Client) shareNameFor(name string) string {
	pattern := c.cfg.Printers.Share.NamePattern
	if pattern == "" {
		return ""
	}
	if strings.Contains(pattern, "%s") {
		return fmt.Sprintf(pattern, name)
	}
	return pattern
}

// shareFor mengembalikan endpoint share sebuah printer (dengan cache).
func (c *Client) shareFor(ctx context.Context, name string) (printer.PrinterShare, error) {
	if !c.cfg.Printers.Share.Enabled {
		return printer.PrinterShare{}, nil
	}
	if !c.shareAllowed(name) {
		return printer.PrinterShare{}, nil
	}
	c.shareMu.Lock()
	if s, ok := c.shareCache[name]; ok {
		c.shareMu.Unlock()
		return s, nil
	}
	c.shareMu.Unlock()

	share, err := c.printer.Share(ctx, name, printer.ShareOptions{
		Protocol:  printer.ShareProtocol(c.cfg.Printers.Share.Protocol),
		ShareName: c.shareNameFor(name),
	})
	if err != nil {
		return printer.PrinterShare{}, err
	}
	c.shareMu.Lock()
	c.shareCache[name] = share
	c.shareMu.Unlock()
	return share, nil
}

// PublishPrintersDetail mengirim detail printer + endpoint share ke server.
func (c *Client) PublishPrintersDetail(ctx context.Context) error {
	list, err := c.printer.List(ctx)
	if err != nil {
		return err
	}

	msg := apitypes.PrintersDetailMessage{
		Type:     apitypes.TypePrintersDetail,
		AgentID:  c.cfg.Agent.ID,
		Hostname: printer.LocalHostname(),
		OS:       runtime.GOOS,
		IPs:      printer.LocalIPs(),
	}
	// Tentukan printer yang dibagikan + sinkronkan listener RAW relay.
	var sharedNames []string
	if c.relay != nil {
		for _, p := range list {
			if c.shareAllowed(p.Name) {
				sharedNames = append(sharedNames, p.Name)
			}
		}
		c.relay.Reconcile(sharedNames)
	}
	shared := make(map[string]bool, len(sharedNames))
	for _, n := range sharedNames {
		shared[n] = true
	}

	for _, p := range list {
		d := apitypes.PrinterDetail{
			Name:      p.Name,
			IsDefault: p.IsDefault,
			Status:    p.Status,
		}
		if share, serr := c.shareFor(ctx, p.Name); serr == nil && share.Name != "" {
			d.Shared = true
			d.ShareName = share.ShareName
			d.Protocol = share.Protocol
			d.SMBPath = share.SMBPath
			d.IPPURI = share.IPPURI
			d.SocketURI = share.SocketURI
		}
		if c.relay != nil && shared[p.Name] {
			if uri := c.relay.IPPURI(p.Name); uri != "" {
				d.IPPURI = uri
				d.Shared = true
				if d.Protocol == "" {
					d.Protocol = "relay"
				}
			}
			if uri := c.relay.SocketURI(p.Name); uri != "" {
				d.SocketURI = uri
				d.Shared = true
			}
		}
		msg.Printers = append(msg.Printers, d)
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
	return c.writeText(conn, data)
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

	return c.writeText(conn, data)
}

// handleControlMessage merespons pesan kontrol dari server (mis. permintaan
// daftar opsi/port printer) agar server tidak timeout menunggu respons.
func (c *Client) handleControlMessage(payload []byte, conn net.Conn) {
	{
		raw := payload
		if len(raw) > 4096 {
			raw = raw[:4096]
		}
		slog.Info("ws control message received", "raw", string(raw))
	}
	var msg map[string]interface{}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}
	msgType, _ := msg["type"].(string)

	// Pertimbangkan juga field alternatif (action/command/request) untuk
	// toleransi terhadap variasi protokol server.
	candidates := []string{msgType}
	for _, k := range []string{"action", "command", "request", "event"} {
		if s, ok := msg[k].(string); ok && s != "" {
			candidates = append(candidates, s)
		}
	}
	lt := strings.ToLower(strings.Join(candidates, " "))

	if strings.TrimSpace(lt) == "" {
		return
	}

	hasID := false
	for k := range msg {
		if strings.Contains(strings.ToLower(k), "id") {
			hasID = true
			break
		}
	}
	looksOptions := strings.Contains(lt, "option")
	looksPrinters := strings.Contains(lt, "printer") && !strings.Contains(lt, "update")
	looksRPC := strings.EqualFold(msgType, "request") || strings.EqualFold(msgType, "rpc")

	if !looksOptions && !looksPrinters && !hasID && !looksRPC {
		slog.Info("received unhandled control message", "type", msgType)
		return
	}

	// Tentukan tipe respons: turunkan dari tipe request bila memungkinkan.
	respType := "response"
	switch {
	case strings.HasSuffix(strings.ToLower(msgType), "_request"):
		respType = msgType[:len(msgType)-len("_request")]
	case looksPrinters:
		respType = "printers"
	case looksOptions:
		respType = "options"
	}

	resp := map[string]interface{}{"type": respType, "agentId": c.cfg.Agent.ID}
	echoCorrelation(resp, msg)

	// Batasi waktu agar handler tak pernah menggantung dan selalu membalas.
	opCtx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()

	if respType == "printers" {
		list, err := c.printer.List(opCtx)
		if err != nil {
			resp["success"] = false
			resp["error"] = err.Error()
		} else {
			resp["success"] = true
			resp["printers"] = list
			resp["data"] = list
			resp["result"] = list
		}
	} else {
		opts, err := c.printer.Options(opCtx)
		if err != nil {
			resp["success"] = false
			resp["error"] = err.Error()
		} else {
			resp["success"] = true
			resp["types"] = opts.Types
			resp["drivers"] = opts.Drivers
			resp["ports"] = opts.Ports
			resp["options"] = opts
			resp["data"] = opts
			resp["result"] = opts
		}
	}

	c.writeControlJSON(conn, resp)
	slog.Info("answered control request from server", "requestType", msgType, "responseType", respType)
}

var correlationKeys = []string{"requestId", "request_id", "reqId", "req_id", "id", "correlationId", "correlation_id"}

// echoCorrelation menyalin SEMUA field korelasi (nama apa pun yang mengandung
// "id") dari request ke response, agar server dapat mencocokkan respons.
func echoCorrelation(resp, req map[string]interface{}) {
	for k, v := range req {
		if v == nil {
			continue
		}
		if strings.Contains(strings.ToLower(k), "id") {
			resp[k] = v
		}
	}
	if _, ok := resp["id"]; !ok {
		for _, k := range correlationKeys {
			if v, ok := req[k]; ok && v != nil {
				resp["id"] = v
				resp["requestId"] = v
				break
			}
		}
	}
}

func (c *Client) writeText(conn net.Conn, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return wsutil.WriteClientText(conn, data)
}

func (c *Client) writeControlJSON(conn net.Conn, v interface{}) {
	if conn == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := c.writeText(conn, data); err != nil {
		slog.Warn("send control response failed", "err", err)
	}
}
