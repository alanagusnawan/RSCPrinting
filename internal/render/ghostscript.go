package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// FindGhostscript mencari binary Ghostscript pada Windows, macOS, atau Linux.
// Jika overridePath diisi dan valid, path tersebut diprioritaskan.
func FindGhostscript(overridePath string) string {
	if overridePath != "" {
		if _, err := os.Stat(overridePath); err == nil {
			return overridePath
		}
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"gswin64c.exe", "gswin32c.exe", "gswin64c", "gswin32c"} {
			if path, err := exec.LookPath(name); err == nil {
				return path
			}
		}

		searchDirs := []string{
			`C:\Program Files\gs`,
			`C:\Program Files (x86)\gs`,
		}
		for _, root := range searchDirs {
			matches, err := filepath.Glob(filepath.Join(root, "gs*", "bin", "gswin*.exe"))
			if err == nil && len(matches) > 0 {
				for _, m := range matches {
					base := strings.ToLower(filepath.Base(m))
					if base == "gswin64c.exe" || base == "gswin32c.exe" {
						return m
					}
				}
				return matches[0]
			}
		}
		return ""
	}

	// Linux & macOS (Darwin)
	for _, name := range []string{"gs"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}

	unixPaths := []string{
		"/usr/bin/gs",
		"/usr/local/bin/gs",
		"/opt/homebrew/bin/gs",
		"/usr/pkg/bin/gs",
	}
	for _, p := range unixPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}
