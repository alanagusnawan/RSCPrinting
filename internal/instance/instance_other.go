//go:build !windows

package instance

import (
	"errors"
	"os"
	"syscall"
)

// acquire: non-Windows tidak menerapkan single-instance via named mutex.
func acquire(_ string) (func(), bool, error) {
	return func() {}, false, nil
}

// acquireFile mengunci file via flock (exclusive, non-blocking).
func acquireFile(path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return func() {}, true, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, false, nil
}
