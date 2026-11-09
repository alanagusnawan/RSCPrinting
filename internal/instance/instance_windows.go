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
