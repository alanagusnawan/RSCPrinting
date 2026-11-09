//go:build windows

package instance

import "testing"

func TestAcquireSingleInstance(t *testing.T) {
	name := "RSCPrintingTest-Instance-1234"

	rel1, already1, err1 := acquire(name)
	if err1 != nil {
		t.Fatalf("first acquire error: %v", err1)
	}
	if already1 {
		t.Fatalf("first acquire should not report already")
	}

	_, already2, err2 := acquire(name)
	if err2 != nil {
		t.Fatalf("second acquire error: %v", err2)
	}
	if !already2 {
		t.Fatalf("second acquire should report already")
	}

	rel1()

	rel3, already3, err3 := acquire(name)
	if err3 != nil {
		t.Fatalf("third acquire error: %v", err3)
	}
	if already3 {
		t.Fatalf("after release, acquire should not report already")
	}
	rel3()
}
