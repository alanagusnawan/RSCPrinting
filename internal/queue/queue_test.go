package queue_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rscprinting/print-agent/internal/queue"
)

func TestQueueConcurrencyLimit(t *testing.T) {
	qm := queue.NewManager(1, 10, 2*time.Second)

	var active int32
	var maxActive int32
	var wg sync.WaitGroup

	numJobs := 5
	for i := 0; i < numJobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := qm.Execute(context.Background(), "Printer-A", func(ctx context.Context) error {
				curr := atomic.AddInt32(&active, 1)
				for {
					oldMax := atomic.LoadInt32(&maxActive)
					if curr <= oldMax || atomic.CompareAndSwapInt32(&maxActive, oldMax, curr) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return nil
			})
			if err != nil {
				t.Errorf("execute job error: %v", err)
			}
		}()
	}

	wg.Wait()

	if maxActive > 1 {
		t.Fatalf("expected concurrency 1, got max active %d", maxActive)
	}
}

func TestQueueTimeoutBusy(t *testing.T) {
	qm := queue.NewManager(1, 10, 100*time.Millisecond)

	started := make(chan struct{})
	blockerDone := make(chan struct{})
	go func() {
		_ = qm.Execute(context.Background(), "Printer-B", func(ctx context.Context) error {
			close(started)
			<-blockerDone
			return nil
		})
	}()

	<-started

	// Second job should time out with PRINTER_BUSY because the first is still holding the semaphore
	err := qm.Execute(context.Background(), "Printer-B", func(ctx context.Context) error {
		return nil
	})

	close(blockerDone)

	if err != queue.ErrPrinterBusy {
		t.Fatalf("expected ErrPrinterBusy, got %v", err)
	}
}
