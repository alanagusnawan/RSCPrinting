//go:build windows

package instance

import (
	"strings"

	"golang.org/x/sys/windows"
)

// acquire memakai named mutex agar hanya satu instance berjalan lintas sesi
// (service di Session 0 vs user di sesi interaktif). Coba namespace Global
// lebih dulu; bila gagal (privilege), fallback ke Local.
func acquire(name string) (func(), bool, error) {
	base := strings.TrimPrefix(strings.TrimPrefix(name, `Global\`), `Local\`)

	var lastErr error
	for _, full := range []string{`Global\` + base, `Local\` + base} {
		n, err := windows.UTF16PtrFromString(full)
		if err != nil {
			lastErr = err
			continue
		}
		h, err := windows.CreateMutex(nil, false, n)
		if err != nil {
			if err == windows.ERROR_ALREADY_EXISTS {
				if h != 0 {
					_ = windows.CloseHandle(h)
				}
				return func() {}, true, nil
			}
			lastErr = err
			continue
		}
		handle := h
		return func() { _ = windows.CloseHandle(handle) }, false, nil
	}
	return nil, false, lastErr
}

// acquireFile mengunci file secara exclusive (share mode 0). Proses kedua yang
// mencoba membuka file yang sama akan gagal (ERROR_SHARING_VIOLATION).
func acquireFile(path string) (func(), bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // no sharing -> exclusive
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if err == windows.ERROR_SHARING_VIOLATION || err == windows.ERROR_LOCK_VIOLATION {
			return func() {}, true, nil
		}
		return nil, false, err
	}
	handle := h
	return func() { _ = windows.CloseHandle(handle) }, false, nil
}
