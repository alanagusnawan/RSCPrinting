package jobs

import (
	"errors"
	"testing"
)

func TestRegistryLifecycle(t *testing.T) {
	r := NewRegistry(2)

	r.Create("a", "Printer-1", "http")
	r.Start("a")
	r.Finish("a", nil, "")

	j, ok := r.Get("a")
	if !ok {
		t.Fatal("job a not found")
	}
	if j.Status != StatusSuccess {
		t.Fatalf("status = %s, want success", j.Status)
	}
	if j.FinishedAt == nil {
		t.Fatal("FinishedAt should be set")
	}

	// eviction: max=2, tambah b & c -> a terbuang
	r.Create("b", "Printer-1", "http")
	r.Create("c", "Printer-1", "ws")
	if _, ok := r.Get("a"); ok {
		t.Fatal("job a should be evicted")
	}
	if len(r.List()) != 2 {
		t.Fatalf("list len = %d, want 2", len(r.List()))
	}

	if !r.Delete("b") {
		t.Fatal("delete b should succeed")
	}
	if r.Delete("zzz") {
		t.Fatal("delete unknown should fail")
	}
}

func TestRegistryFinishError(t *testing.T) {
	r := NewRegistry(10)
	r.Create("x", "P", "http")
	r.Start("x")
	r.Finish("x", errors.New("boom"), "PRINT_FAILED")

	j, _ := r.Get("x")
	if j.Status != StatusError || j.ErrorCode != "PRINT_FAILED" || j.Error != "boom" {
		t.Fatalf("unexpected job: %+v", j)
	}
}
