package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/exp/slog"
)

// Manager menjalankan relay untuk printer yang dibagikan agent ini:
//   - IPP pada satu port (routing via /printers/<name>).
//   - RAW pada satu port per printer (base + index), untuk Windows 7-11.
type Manager struct {
	printer DocumentPrinter
	host    string
	dataDir string

	ippEnabled bool
	ippPort    int
	rawEnabled bool
	rawBase    int

	mu     sync.Mutex
	ports  map[string]int
	lns    map[string]net.Listener
	ippSrv *http.Server
	ctx    context.Context
	wg     sync.WaitGroup
}

// NewManager membuat relay manager.
func NewManager(p DocumentPrinter, host string, ippEnabled bool, ippPort int, rawEnabled bool, rawBase int, dataDir string) *Manager {
	if ippPort <= 0 {
		ippPort = 19100
	}
	if rawBase <= 0 {
		rawBase = 19101
	}
	m := &Manager{
		printer:    p,
		host:       host,
		dataDir:    dataDir,
		ippEnabled: ippEnabled,
		ippPort:    ippPort,
		rawEnabled: rawEnabled,
		rawBase:    rawBase,
		ports:      map[string]int{},
		lns:        map[string]net.Listener{},
	}
	m.loadPorts()
	return m
}

// Start menjalankan listener IPP (bila diaktifkan).
func (m *Manager) Start(ctx context.Context) error {
	m.ctx = ctx
	if !m.ippEnabled {
		return nil
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", m.ippPort),
		Handler:           NewIPPServer(m.printer),
		ReadHeaderTimeout: 10 * time.Second,
	}
	m.ippSrv = srv

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		slog.Info("relay IPP listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("relay IPP stopped", "err", err)
		}
	}()
	return nil
}

// Reconcile memastikan listener RAW ada untuk tiap nama printer yang dibagikan
// dan menutup listener untuk printer yang tak lagi dibagikan.
func (m *Manager) Reconcile(names []string) {
	if !m.rawEnabled {
		return
	}
	names = append([]string(nil), names...)
	sort.Strings(names)

	m.mu.Lock()
	defer m.mu.Unlock()

	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	for n, ln := range m.lns {
		if !want[n] {
			_ = ln.Close()
			delete(m.lns, n)
		}
	}

	for _, n := range names {
		if _, ok := m.lns[n]; ok {
			continue
		}
		port := m.portFor(n)
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			slog.Warn("relay RAW listen failed", "printer", n, "port", port, "err", err)
			continue
		}
		m.lns[n] = ln
		slog.Info("relay RAW listening", "printer", n, "port", port)
		go m.serveRaw(n, ln)
	}
}

func (m *Manager) serveRaw(name string, ln net.Listener) {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	NewRawServer(m.printer).Serve(ctx, ln, name)
}

// IPPURI mengembalikan endpoint IPP relay (kosong bila IPP nonaktif).
func (m *Manager) IPPURI(name string) string {
	if !m.ippEnabled || name == "" {
		return ""
	}
	return fmt.Sprintf("ipp://%s:%d/printers/%s", m.host, m.ippPort, name)
}

// SocketURI mengembalikan endpoint RAW relay untuk printer (kosong bila belum ada).
func (m *Manager) SocketURI(name string) string {
	if !m.rawEnabled || name == "" {
		return ""
	}
	m.mu.Lock()
	port, ok := m.ports[name]
	m.mu.Unlock()
	if !ok {
		return ""
	}
	return fmt.Sprintf("socket://%s:%d", m.host, port)
}

// Close menutup semua listener.
func (m *Manager) Close() {
	m.mu.Lock()
	for _, ln := range m.lns {
		_ = ln.Close()
	}
	m.lns = map[string]net.Listener{}
	m.mu.Unlock()
}

// portFor memberi port RAW stabil untuk printer (base + slot bebas).
func (m *Manager) portFor(name string) int {
	if p, ok := m.ports[name]; ok {
		return p
	}
	used := make(map[int]bool, len(m.ports))
	for _, p := range m.ports {
		used[p] = true
	}
	p := m.rawBase
	for used[p] {
		p++
	}
	m.ports[name] = p
	m.savePorts()
	return p
}

func (m *Manager) portsFile() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "relay-ports.json")
}

func (m *Manager) loadPorts() {
	f := m.portsFile()
	if f == "" {
		return
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &m.ports)
	if m.ports == nil {
		m.ports = map[string]int{}
	}
}

func (m *Manager) savePorts() {
	f := m.portsFile()
	if f == "" {
		return
	}
	b, err := json.MarshalIndent(m.ports, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(f, b, 0600)
}
