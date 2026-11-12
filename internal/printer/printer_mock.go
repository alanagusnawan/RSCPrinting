package printer

import (
	"context"
	"sync"
)

type MockPrinter struct {
	mu           sync.Mutex
	Printers     []PrinterInfo
	PrintedJobs  []Job
	PrintErrFunc func(job Job) error
}

func NewMockPrinter(printers []PrinterInfo) *MockPrinter {
	if printers == nil {
		printers = []PrinterInfo{
			{Name: "HP-LaserJet", IsDefault: true, Status: "idle"},
			{Name: "Epson-TM-T82", IsDefault: false, Status: "idle"},
		}
	}
	return &MockPrinter{
		Printers:    printers,
		PrintedJobs: make([]Job, 0),
	}
}

func (m *MockPrinter) Print(ctx context.Context, job Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.PrintErrFunc != nil {
		if err := m.PrintErrFunc(job); err != nil {
			return err
		}
	}

	m.PrintedJobs = append(m.PrintedJobs, job)
	return nil
}

func (m *MockPrinter) List(ctx context.Context) ([]PrinterInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]PrinterInfo, len(m.Printers))
	copy(res, m.Printers)
	return res, nil
}

func (m *MockPrinter) Info(ctx context.Context, name string) (PrinterInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.Printers {
		if p.Name == name {
			return p, nil
		}
	}
	return PrinterInfo{}, ErrPrinterNotFound
}

func (m *MockPrinter) Register(ctx context.Context, params RegisterPrinterParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Printers = append(m.Printers, PrinterInfo{
		Name:      params.Name,
		IsDefault: params.IsDefault,
		Status:    "ready",
	})
	return nil
}

func (m *MockPrinter) Update(ctx context.Context, name string, params UpdatePrinterParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for i := range m.Printers {
		if m.Printers[i].Name != name {
			continue
		}
		found = true
		if params.NewName != "" {
			m.Printers[i].Name = params.NewName
		}
		if params.IsDefault != nil {
			m.Printers[i].IsDefault = *params.IsDefault
		}
	}
	if !found {
		return ErrPrinterNotFound
	}
	return nil
}

func (m *MockPrinter) Remove(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Printers {
		if m.Printers[i].Name == name {
			m.Printers = append(m.Printers[:i], m.Printers[i+1:]...)
			return nil
		}
	}
	return ErrPrinterNotFound
}

func (m *MockPrinter) SetDefault(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for i := range m.Printers {
		if m.Printers[i].Name == name {
			found = true
		}
		m.Printers[i].IsDefault = (m.Printers[i].Name == name)
	}
	if !found {
		return ErrPrinterNotFound
	}
	return nil
}

func (m *MockPrinter) Options(ctx context.Context) (PrinterOptions, error) {
	return PrinterOptions{
		Types:   SupportedPrinterTypes,
		Drivers: []string{"Generic / Text Only", "Microsoft Print To PDF"},
		Ports:   []PrinterPort{{Name: "USB001"}, {Name: "IP_192.168.1.200"}},
	}, nil
}
