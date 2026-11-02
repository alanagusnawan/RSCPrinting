package storage_test

import (
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
