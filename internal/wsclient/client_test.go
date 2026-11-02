package wsclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
	"github.com/rscprinting/print-agent/internal/storage"
	"github.com/rscprinting/print-agent/internal/wsclient"
	"github.com/rscprinting/print-agent/pkg/apitypes"
)

func TestWSClientHandshakeAndTwoFrameJob(t *testing.T) {
	var (
		serverReceivedHello   bool
		serverReceivedResult  apitypes.JobResultMessage
		receivedResultSignal  = make(chan struct{})
		mockPDF               = []byte("%PDF-1.4 TEST PDF STREAM")
		testJobID             = "job-test-777"
	)

	// Mock server
	upgrader := ws.HTTPUpgrader{
		Header: http.Header{},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer valid-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			t.Errorf("upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// 1. Read hello
		msg, op, err := wsutil.ReadClientData(conn)
		if err != nil || op != ws.OpText {
			t.Errorf("read hello failed: %v", err)
			return
		}

		var hello apitypes.HelloMessage
		if err := json.Unmarshal(msg, &hello); err != nil {
			t.Errorf("unmarshal hello failed: %v", err)
			return
		}
		serverReceivedHello = true

		// 2. Send welcome
		welcome := apitypes.WelcomeMessage{
			Type:         apitypes.TypeWelcome,
			ServerTime:   time.Now(),
			ConnectionID: "conn-1",
		}
		wBytes, _ := json.Marshal(welcome)
		if err := wsutil.WriteServerText(conn, wBytes); err != nil {
			t.Errorf("write welcome error: %v", err)
			return
		}

		// 3. Send 2-frame job: text frame then binary frame
		jobMeta := apitypes.JobMessage{
			Type:        apitypes.TypeJob,
			JobID:       testJobID,
			Printer:     "HP-LaserJet",
			Template:    apitypes.PrintTemplate{Copies: 1},
			FileName:    "test.pdf",
			FileSize:    int64(len(mockPDF)),
			PDFEncoding: "binary",
		}
		jBytes, _ := json.Marshal(jobMeta)

		// Text frame
		if err := wsutil.WriteServerText(conn, jBytes); err != nil {
			t.Errorf("write job meta error: %v", err)
			return
		}
		// Binary frame
		if err := wsutil.WriteServerBinary(conn, mockPDF); err != nil {
			t.Errorf("write job pdf error: %v", err)
			return
		}

		// 4. Wait for job_result
		resBytes, resOp, err := wsutil.ReadClientData(conn)
		if err != nil || resOp != ws.OpText {
			t.Errorf("read job result error: %v", err)
			return
		}
		_ = json.Unmarshal(resBytes, &serverReceivedResult)
		close(receivedResultSignal)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	cfg := config.DefaultConfig()
	cfg.WSServer.URL = wsURL
	cfg.WSServer.Token = "valid-token"
	cfg.Agent.ID = "test-agent-id"
	cfg.Agent.DisplayName = "Test Agent"
	cfg.DataDir = t.TempDir()

	store, err := storage.Open(cfg.DataDir)
	if err != nil {
		t.Fatalf("open storage error: %v", err)
	}
	defer store.Close()

	mockP := printer.NewMockPrinter(nil)
	qm := queue.NewManager(1, 10, 5*time.Second)

	client := wsclient.New(cfg, mockP, qm, store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = client.Run(ctx)
	}()

	select {
	case <-receivedResultSignal:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job_result from agent")
	}

	if !serverReceivedHello {
		t.Error("server never received hello frame")
	}
	if !serverReceivedResult.Success || serverReceivedResult.JobID != testJobID {
		t.Errorf("unexpected job result: %+v", serverReceivedResult)
	}
	if len(mockP.PrintedJobs) != 1 || mockP.PrintedJobs[0].JobID != testJobID {
		t.Errorf("printer mock did not execute job: %+v", mockP.PrintedJobs)
	}
}

func TestWSClientFatalAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		defer conn.Close()

		// Wait for hello message from client before sending close frame
		_, _, _ = wsutil.ReadClientData(conn)

		// Send close 4001
		_ = ws.WriteFrame(conn, ws.NewCloseFrame(ws.NewCloseFrameBody(4001, "invalid token")))
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	cfg := config.DefaultConfig()
	cfg.WSServer.URL = wsURL
	cfg.WSServer.Token = "bad-token"
	cfg.DataDir = t.TempDir()

	store, _ := storage.Open(cfg.DataDir)
	defer store.Close()

	mockP := printer.NewMockPrinter(nil)
	qm := queue.NewManager(1, 10, 5*time.Second)

	client := wsclient.New(cfg, mockP, qm, store)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "4001") {
		t.Errorf("expected fatal auth error 4001, got: %v", err)
	}

	status, _ := client.Status()
	if status != wsclient.StatusAuthFailed {
		t.Errorf("expected StatusAuthFailed, got: %s", status)
	}
}
