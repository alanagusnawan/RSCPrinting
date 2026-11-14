package relay

import (
	"context"
	"testing"

	"github.com/rscprinting/print-agent/internal/printer"
)

type fakePrinter struct{}

func (fakePrinter) Print(ctx context.Context, job printer.Job) error { return nil }

func TestManagerPortAssignmentAndURIs(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(fakePrinter{}, "host-a", true, 19100, true, 19101, dir)

	p1 := m.portFor("Alpha")
	p2 := m.portFor("Beta")
	if p1 != 19101 || p2 != 19102 {
		t.Fatalf("ports = %d,%d, want 19101,19102", p1, p2)
	}
	// Printer yang sama harus dapat port yang sama.
	if again := m.portFor("Alpha"); again != 19101 {
		t.Errorf("portFor Alpha stable = %d, want 19101", again)
	}

	if got := m.IPPURI("Alpha"); got != "ipp://host-a:19100/printers/Alpha" {
		t.Errorf("IPPURI = %q", got)
	}
	if got := m.SocketURI("Alpha"); got != "socket://host-a:19101" {
		t.Errorf("SocketURI = %q", got)
	}

	// Persistensi: manager baru pada dataDir yang sama mengingat port.
	m2 := NewManager(fakePrinter{}, "host-a", false, 19100, true, 19101, dir)
	if got := m2.portFor("Beta"); got != 19102 {
		t.Errorf("persisted portFor Beta = %d, want 19102", got)
	}

	// IPP nonaktif -> URI kosong.
	m3 := NewManager(fakePrinter{}, "host-a", false, 19100, true, 19101, dir)
	if got := m3.IPPURI("Alpha"); got != "" {
		t.Errorf("IPPURI when disabled = %q, want empty", got)
	}
}
