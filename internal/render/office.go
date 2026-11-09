package render

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// FindLibreOffice mencari executable LibreOffice/soffice.
// overridePath dipakai lebih dulu bila diisi dan file-nya ada.
func FindLibreOffice(overridePath string) string {
	if overridePath != "" {
		if _, err := os.Stat(overridePath); err == nil {
			return overridePath
		}
	}

	for _, name := range []string{"soffice", "soffice.exe", "soffice.com", "libreoffice"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}

	candidates := []string{
		`C:\Program Files\LibreOffice\program\soffice.exe`,
		`C:\Program Files (x86)\LibreOffice\program\soffice.exe`,
		`/usr/bin/soffice`,
		`/usr/local/bin/soffice`,
		`/Applications/LibreOffice.app/Contents/MacOS/soffice`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// ConvertOfficeToPDF mengonversi dokumen Office ke PDF memakai LibreOffice headless.
// Mengembalikan path PDF hasil konversi dan fungsi cleanup untuk membuang direktori temp.
func ConvertOfficeToPDF(ctx context.Context, sofficePath, inputPath string, timeout time.Duration) (string, func(), error) {
	if sofficePath == "" {
		return "", nil, ErrOfficeConverterNotFound
	}

	outDir, err := os.MkdirTemp("", "rsc-office-out-*")
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrOfficeConvertFailed, err)
	}
	cleanup := func() { _ = os.RemoveAll(outDir) }

	profileDir, err := os.MkdirTemp("", "rsc-office-profile-*")
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("%w: %v", ErrOfficeConvertFailed, err)
	}

	profileURI := "file:///" + filepath.ToSlash(profileDir)
	if runtime.GOOS != "windows" {
		profileURI = "file://" + filepath.ToSlash(profileDir)
	}

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{
		"-env:UserInstallation=" + profileURI,
		"--headless",
		"--norestore",
		"--invisible",
		"--nologo",
		"--nolockcheck",
		"--nodefault",
		"--convert-to",
		"pdf",
		"--outdir",
		outDir,
		inputPath,
	}

	cmd := exec.CommandContext(runCtx, sofficePath, args...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		cleanup()
		_ = os.RemoveAll(profileDir)
		return "", nil, fmt.Errorf("%w: %s (%v)", ErrOfficeConvertFailed, strings.TrimSpace(out.String()), err)
	}
	_ = os.RemoveAll(profileDir)

	base := filepath.Base(inputPath)
	expected := strings.TrimSuffix(base, filepath.Ext(base)) + ".pdf"
	pdfPath := filepath.Join(outDir, expected)
	if _, err := os.Stat(pdfPath); err != nil {
		// Cari PDF apa saja di outDir (antisipasi nama berbeda).
		matches, _ := filepath.Glob(filepath.Join(outDir, "*.pdf"))
		if len(matches) == 0 {
			cleanup()
			return "", nil, fmt.Errorf("%w: output pdf not found", ErrOfficeConvertFailed)
		}
		pdfPath = matches[0]
	}

	return pdfPath, cleanup, nil
}
