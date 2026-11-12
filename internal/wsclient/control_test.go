package wsclient

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/printer"
	"github.com/rscprinting/print-agent/internal/queue"
)

func newControlTestClient() *Client {
	cfg := config.DefaultConfig()
	mockP := printer.NewMockPrinter(nil)
	qm := queue.NewManager(1, 10, 5*time.Second)
	return New(cfg, mockP, qm, nil, nil)
}

func readTextPayload(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	fr, err := ws.ReadFrame(conn)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if fr.Header.OpCode != ws.OpText {
		t.Fatalf("opcode = %v, want text", fr.Header.OpCode)
	}
	if fr.Header.Masked {
		ws.Cipher(fr.Payload, fr.Header.Mask, 0)
	}
	return fr.Payload
}

func TestHandleControlOptions(t *testing.T) {
	c := newControlTestClient()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go c.handleControlMessage([]byte(`{"type":"get_options","requestId":"abc-123"}`), c1)

	var resp map[string]interface{}
	if err := json.Unmarshal(readTextPayload(t, c2), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["type"] != "options" {
		t.Fatalf("type = %v, want options", resp["type"])
	}
	if resp["requestId"] != "abc-123" {
		t.Fatalf("requestId not echoed: %v", resp["requestId"])
	}
	if _, ok := resp["options"]; !ok {
		t.Fatalf("missing options field: %v", resp)
	}
	if _, ok := resp["drivers"]; !ok {
		t.Fatalf("missing drivers field: %v", resp)
	}
}

func TestHandleControlPrinters(t *testing.T) {
	c := newControlTestClient()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go c.handleControlMessage([]byte(`{"type":"get_printers"}`), c1)

	var resp map[string]interface{}
	if err := json.Unmarshal(readTextPayload(t, c2), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["type"] != "printers" {
		t.Fatalf("type = %v, want printers", resp["type"])
	}
	if _, ok := resp["printers"]; !ok {
		t.Fatalf("missing printers field: %v", resp)
	}
}

func TestHandleControlIgnoresUnknown(t *testing.T) {
	c := newControlTestClient()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	// Tidak boleh ada respons untuk tipe yang tidak dikenal.
	go c.handleControlMessage([]byte(`{"type":"something_else"}`), c1)

	_ = c2.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := ws.ReadFrame(c2); err == nil {
		t.Fatalf("expected no response for unknown message type")
	}
}
