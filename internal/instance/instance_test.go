package instance

import (
	"path/filepath"
	"testing"
)

func TestAcquireFileSingleInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".agent.lock")

	rel1, already1, err1 := AcquireFile(path)
	if err1 != nil {
		t.Fatalf("first acquire error: %v", err1)
	}
	if already1 {
		t.Fatalf("first acquire should not report already")
	}

	_, already2, err2 := AcquireFile(path)
	if err2 != nil {
		t.Fatalf("second acquire error: %v", err2)
	}
	if !already2 {
		t.Fatalf("second acquire should report already")
	}

	rel1()

	rel3, already3, err3 := AcquireFile(path)
	if err3 != nil {
		t.Fatalf("third acquire error: %v", err3)
	}
	if already3 {
		t.Fatalf("after release, acquire should not report already")
	}
	rel3()
}
