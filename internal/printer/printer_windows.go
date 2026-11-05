//go:build windows

package printer

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type WindowsPrinter struct {
	gsPath string
}

func New(gsOverride string) (*WindowsPrinter, error) {
	gs := findGhostscript(gsOverride)
	return &WindowsPrinter{gsPath: gs}, nil
}

func findGhostscript(override string) string {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override
		}
	}

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

func purgeStuckJobs(ctx context.Context, printerName string) {
	cleanName := strings.ReplaceAll(printerName, "'", "''")
	psCmd := fmt.Sprintf("Get-PrintJob -PrinterName '%s' -ErrorAction SilentlyContinue | Where-Object { $_.JobStatus -match 'Error|Deleting' } | Remove-PrintJob -ErrorAction SilentlyContinue", cleanName)
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	_ = cmd.Run()
}

func (p *WindowsPrinter) Print(ctx context.Context, job Job) error {
	gs := p.gsPath
	if gs == "" {
		gs = findGhostscript("")
		p.gsPath = gs
	}
	if gs == "" {
		return ErrGSNotFound
	}

	purgeStuckJobs(ctx, job.Printer)

	args := []string{
		"-dBATCH",
		"-dNOPAUSE",
		"-dQUIET",
		"-sDEVICE=mswinpr2",
		fmt.Sprintf("-sOutputFile=%%printer%%%s", job.Printer),
		"-dFitPage",
		"-dPDFFitPage",
		"-dNoCancel",
	}

	paper := "a4"
	if job.Template.Paper != "" {
		paper = strings.ToLower(job.Template.Paper)
	}
	args = append(args, fmt.Sprintf("-sPAPERSIZE=%s", paper), "-dFIXEDMEDIA")

	if job.Template.Copies > 1 {
		args = append(args, fmt.Sprintf("-dNumCopies=%d", job.Template.Copies))
	}

	args = append(args,
		"-c",
		"<< /UserSettings << /MaxResolution 300 >> >> setpagedevice",
		"-f",
		job.FilePath,
	)

	fi, _ := os.Stat(job.FilePath)
	var fileSize int64
	if fi != nil {
		fileSize = fi.Size()
	}
	slog.Info("executing windows print job", "printer", job.Printer, "file", job.FilePath, "bytes", fileSize)

	cmd := exec.CommandContext(ctx, gs, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		purgeStuckJobs(context.Background(), job.Printer)
		return fmt.Errorf("%w: %s (%v)", ErrPrintFailed, strings.TrimSpace(output.String()), err)
	}

	return nil
}

func (p *WindowsPrinter) List(ctx context.Context) ([]PrinterInfo, error) {
	// Gunakan powershell / wmic untuk Win7 SP1+ tanpa CGO
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-WmiObject Win32_Printer | Select-Object Name, Default | ConvertTo-Csv -NoTypeInformation")
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		var printers []PrinterInfo
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, `"Name"`) {
				continue
			}
			parts := strings.Split(line, ",")
			if len(parts) >= 2 {
				name := strings.Trim(parts[0], `" `)
				isDef := strings.EqualFold(strings.Trim(parts[1], `" `), "True")
				if name != "" {
					printers = append(printers, PrinterInfo{
						Name:      name,
						IsDefault: isDef,
						Status:    "ready",
					})
				}
			}
		}
		if len(printers) > 0 {
			return printers, nil
		}
	}

	// Fallback wmic untuk Win7
	wmic := exec.CommandContext(ctx, "wmic", "printer", "get", "name,default", "/format:csv")
	if wmicOut, werr := wmic.Output(); werr == nil {
		lines := strings.Split(string(wmicOut), "\n")
		var printers []PrinterInfo
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(strings.ToLower(line), "node") {
				continue
			}
			parts := strings.Split(line, ",")
			if len(parts) >= 3 {
				isDef := strings.EqualFold(strings.TrimSpace(parts[1]), "TRUE")
				name := strings.TrimSpace(parts[2])
				if name != "" {
					printers = append(printers, PrinterInfo{
						Name:      name,
						IsDefault: isDef,
						Status:    "ready",
					})
				}
			}
		}
		return printers, nil
	}

	return nil, fmt.Errorf("unable to enumerate printers: %v", err)
}

func (p *WindowsPrinter) Info(ctx context.Context, name string) (PrinterInfo, error) {
	list, err := p.List(ctx)
	if err != nil {
		return PrinterInfo{}, err
	}
	for _, pr := range list {
		if strings.EqualFold(pr.Name, name) {
			return pr, nil
		}
	}
	return PrinterInfo{}, ErrPrinterNotFound
}
