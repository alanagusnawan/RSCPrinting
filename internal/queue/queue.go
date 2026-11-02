package queue

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrPrinterBusy    = errors.New("PRINTER_BUSY")
	ErrQueueFull      = errors.New("QUEUE_FULL")
	ErrPrinterTimeout = errors.New("PRINTER_TIMEOUT")
)

type Manager struct {
	maxConcurrent int
	maxQueueSize  int
	jobTimeout    time.Duration

	mu       sync.Mutex
	printers map[string]*printerQueue
}

type printerQueue struct {
	sem chan struct{}
}

func NewManager(maxConcurrent, maxQueueSize int, jobTimeout time.Duration) *Manager {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	if maxQueueSize <= 0 {
		maxQueueSize = 10
	}
	if jobTimeout <= 0 {
		jobTimeout = 60 * time.Second
	}

	return &Manager{
		maxConcurrent: maxConcurrent,
		maxQueueSize:  maxQueueSize,
		jobTimeout:    jobTimeout,
		printers:      make(map[string]*printerQueue),
	}
}

func (m *Manager) getQueue(printerName string) *printerQueue {
	m.mu.Lock()
	defer m.mu.Unlock()

	pq, ok := m.printers[printerName]
	if !ok {
		pq = &printerQueue{
			sem: make(chan struct{}, m.maxConcurrent),
		}
		m.printers[printerName] = pq
	}
	return pq
}

// Execute menjalankan job dengan concurrency limit 1 per printer dan timeout
func (m *Manager) Execute(ctx context.Context, printerName string, fn func(ctx context.Context) error) error {
	pq := m.getQueue(printerName)

	acquireTimeout := m.jobTimeout
	timer := time.NewTimer(acquireTimeout)
	defer timer.Stop()

	select {
	case pq.sem <- struct{}{}:
		// Semaphore didapat
		defer func() { <-pq.sem }()
	case <-timer.C:
		return ErrPrinterBusy
	case <-ctx.Done():
		return ctx.Err()
	}

	execCtx, cancel := context.WithTimeout(ctx, m.jobTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- fn(execCtx)
	}()

	select {
	case err := <-done:
		return err
	case <-execCtx.Done():
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return ErrPrinterTimeout
		}
		return execCtx.Err()
	}
}
