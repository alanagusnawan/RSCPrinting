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
