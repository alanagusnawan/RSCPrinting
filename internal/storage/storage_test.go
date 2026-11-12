package storage_test

import (
	"errors"
	"os"
	"testing"

	"github.com/rscprinting/print-agent/internal/storage"
	"github.com/rscprinting/print-agent/pkg/apitypes"
)

func TestStoragePendingResults(t *testing.T) {
	tempDir := t.TempDir()

	store, err := storage.Open(tempDir)
	if err != nil {
		t.Fatalf("open storage failed: %v", err)
	}
	defer store.Close()

	res1 := apitypes.JobResultMessage{
		Type:       apitypes.TypeJobResult,
		JobID:      "job-101",
		Success:    true,
		DurationMs: 150,
	}

	res2 := apitypes.JobResultMessage{
		Type:       apitypes.TypeJobResult,
		JobID:      "job-102",
		Success:    false,
		Error:      "Ghostscript not found",
		ErrorCode:  "GS_NOT_FOUND",
		DurationMs: 20,
	}

	if err := store.SavePendingResult(res1); err != nil {
		t.Fatalf("save res1 failed: %v", err)
	}
	if err := store.SavePendingResult(res2); err != nil {
		t.Fatalf("save res2 failed: %v", err)
	}

	list, err := store.GetPendingResults()
	if err != nil {
		t.Fatalf("get pending results failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 items, got %d", len(list))
	}

	if err := store.DeletePendingResult("job-101"); err != nil {
		t.Fatalf("delete res1 failed: %v", err)
	}

	listAfter, err := store.GetPendingResults()
	if err != nil {
		t.Fatalf("get after delete failed: %v", err)
	}
	if len(listAfter) != 1 || listAfter[0].JobID != "job-102" {
		t.Fatalf("expected 1 item with job-102, got %v", listAfter)
	}
}

func TestOpenLockedReturnsErrLocked(t *testing.T) {
	// Pakai MkdirTemp (bukan t.TempDir) karena bbolt dapat meninggalkan handle
	// file saat timeout lock; cleanup terbaik-usaha agar tak menggagalkan tes.
	tempDir, err := os.MkdirTemp("", "rsc-storage-lock-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	store, err := storage.Open(tempDir)
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}

	_, err2 := storage.Open(tempDir)
	if err2 == nil {
		_ = store.Close()
		t.Fatalf("second open should fail while DB is locked")
	}
	if !errors.Is(err2, storage.ErrLocked) {
		_ = store.Close()
		t.Fatalf("expected ErrLocked, got %v", err2)
	}

	_ = store.Close()
}
