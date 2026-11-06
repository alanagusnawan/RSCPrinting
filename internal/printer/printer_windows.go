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

func (p *WindowsPrinter) Register(ctx context.Context, params RegisterPrinterParams) error {
	driver := params.DriverName
	if driver == "" {
		driver = "Generic / Text Only"
	}

	cleanName := strings.ReplaceAll(params.Name, "'", "''")
	cleanDriver := strings.ReplaceAll(driver, "'", "''")
	cleanPort := strings.ReplaceAll(params.PortOrURI, "'", "''")

	var psScript string

	switch params.Type {
	case PrinterTypeHTTPShared:
		psScript = fmt.Sprintf("Add-Printer -ConnectionName '%s'", cleanPort)
	case PrinterTypeIP:
		portName := fmt.Sprintf("IP_%s", cleanPort)
		psScript = fmt.Sprintf(
			`$port = Get-PrinterPort -Name '%s' -ErrorAction SilentlyContinue; `+
				`if (-not $port) { Add-PrinterPort -Name '%s' -PrinterHostAddress '%s' -PortNumber 9100 }; `+
				`Add-Printer -Name '%s' -DriverName '%s' -PortName '%s'`,
			portName, portName, cleanPort, cleanName, cleanDriver, portName,
		)
	case PrinterTypeUSB, PrinterTypeBluetooth:
		psScript = fmt.Sprintf("Add-Printer -Name '%s' -DriverName '%s' -PortName '%s'", cleanName, cleanDriver, cleanPort)
	default:
		return fmt.Errorf("unsupported printer type: %s", params.Type)
	}

	if params.IsDefault {
		psScript += fmt.Sprintf(`; (New-Object -ComObject WScript.Network).SetDefaultPrinter('%s')`, cleanName)
	}

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("register printer failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func (p *WindowsPrinter) Options(ctx context.Context) (PrinterOptions, error) {
	return PrinterOptions{
		Types:   SupportedPrinterTypes,
		Drivers: listWindowsDrivers(ctx),
		Ports:   listWindowsPorts(ctx),
	}, nil
}

func runPowerShellLines(ctx context.Context, script string) []string {
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func listWindowsDrivers(ctx context.Context) []string {
	script := "Get-PrinterDriver -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Name"
	if drivers := runPowerShellLines(ctx, script); len(drivers) > 0 {
		return drivers
	}
	// Fallback untuk Windows 7: WMI (format Name = "Driver,Version,Environment")
	wmiScript := "Get-WmiObject Win32_PrinterDriver | ForEach-Object { ($_.Name -split ',')[0] } | Select-Object -Unique"
	return runPowerShellLines(ctx, wmiScript)
}

func listWindowsPorts(ctx context.Context) []PrinterPort {
	script := "Get-PrinterPort -ErrorAction SilentlyContinue | Select-Object Name,Description | ConvertTo-Csv -NoTypeInformation"
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		var ports []PrinterPort
		for _, line := range lines {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if line == "" || strings.HasPrefix(line, `"Name"`) {
				continue
			}
			parts := strings.Split(line, ",")
			if len(parts) >= 1 {
				name := strings.Trim(parts[0], `" `)
				desc := ""
				if len(parts) >= 2 {
					desc = strings.Trim(parts[1], `" `)
				}
				if name != "" {
					ports = append(ports, PrinterPort{Name: name, Description: desc})
				}
			}
		}
		if len(ports) > 0 {
			return ports
		}
	}

	// Fallback untuk Windows 7: WMI TCP/IP ports
	var ports []PrinterPort
	for _, name := range runPowerShellLines(ctx, "Get-WmiObject Win32_TCPIPPrinterPort | Select-Object -ExpandProperty Name") {
		ports = append(ports, PrinterPort{Name: name})
	}
	return ports
}
