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
	"time"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/render"
)

type WindowsPrinter struct {
	gsPath         string
	officePath     string
	convertTimeout time.Duration
	allowRaw       bool
	thermal        []config.ThermalPrinterConfig
}

func New(cfg config.Config) (*WindowsPrinter, error) {
	gs := findGhostscript(cfg.Ghostscript.Path)

	office := ""
	if cfg.Office.Enabled {
		office = render.FindLibreOffice(cfg.Office.Path)
	}

	allowRaw := true
	if cfg.Document.AllowRaw != nil {
		allowRaw = *cfg.Document.AllowRaw
	}

	return &WindowsPrinter{
		gsPath:         gs,
		officePath:     office,
		convertTimeout: cfg.Document.ConvertTimeout,
		allowRaw:       allowRaw,
		thermal:        cfg.Printers.Thermal,
	}, nil
}

func (p *WindowsPrinter) resolveGhostscript() string {
	if p.gsPath != "" {
		return p.gsPath
	}
	p.gsPath = findGhostscript("")
	return p.gsPath
}

// thermalOptions mengembalikan konfigurasi render thermal bila printer terdaftar
// sebagai thermal di config.
func (p *WindowsPrinter) thermalOptions(printerName string) (*render.ThermalOptions, bool) {
	cfg, ok := config.PrintersConfig{Thermal: p.thermal}.ThermalFor(printerName)
	if !ok {
		return nil, false
	}
	return &render.ThermalOptions{
		GhostscriptPath: p.resolveGhostscript(),
		WidthMm:         cfg.ResolvedWidthMm(),
		DPI:             cfg.ResolvedDPI(),
		Cut:             cfg.ResolvedCut(),
		Trim:            cfg.ResolvedTrim(),
		Feed:            cfg.ResolvedFeed(),
		Timeout:         p.convertTimeout,
	}, true
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
	thermal, isThermal := p.thermalOptions(job.Printer)

	doc, err := render.Prepare(ctx, render.Input{
		FilePath:    job.FilePath,
		Format:      job.Format,
		ContentType: job.ContentType,
		FileName:    job.FileName,
	}, render.Options{
		OfficePath:     p.officePath,
		ConvertTimeout: p.convertTimeout,
		Paper:          job.Template.Paper,
		Thermal:        thermal,
	})
	if err != nil {
		return err
	}
	defer doc.Cleanup()

	slog.Info("prepared print job",
		"printer", job.Printer,
		"requestedFormat", string(job.Format),
		"resolvedFormat", string(doc.Format),
		"raw", doc.IsRaw(),
		"thermal", isThermal,
	)

	purgeStuckJobs(ctx, job.Printer)

	// Deteksi driver class/v4: driver model v4 tidak meneruskan datatype RAW ke
	// port (job "sukses" tapi kertas kosong), dan jalur driver-nya mengeluarkan
	// EJL/ESC-P-R yang dicetak sebagai sampah oleh printer ESC/POS.
	drv, drvErr := p.printerDriverName(ctx, job.Printer)
	classDriver := drvErr == nil && isClassDriver(drv)

	if doc.IsRaw() {
		if !p.allowRaw {
			return fmt.Errorf("%w: raw printing is disabled in config", ErrRawSendFailed)
		}
		if classDriver {
			return fmt.Errorf("%w: printer %q memakai driver class/v4 %q; RAW/ESC-POS tidak diteruskan ke perangkat. Ganti driver queue ke \"Epson APD\" atau \"Generic / Text Only\"",
				ErrDriverIncompatible, job.Printer, drv)
		}
		return p.sendRaw(ctx, job.Printer, doc.Raw)
	}

	if classDriver {
		return fmt.Errorf("%w: printer %q memakai driver %q; aktifkan jalur thermal (printers.thermal) untuk printer ESC/POS atau pasang driver Epson APD",
			ErrDriverIncompatible, job.Printer, drv)
	}

	return p.printRendered(ctx, job, doc.Path)
}

func (p *WindowsPrinter) printerDriverName(ctx context.Context, name string) (string, error) {
	clean := strings.ReplaceAll(name, "'", "''")
	ps := fmt.Sprintf("(Get-Printer -Name '%s' -ErrorAction SilentlyContinue).DriverName", clean)
	if out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s, nil
		}
	}

	wmic := exec.CommandContext(ctx, "wmic", "printer", "where", fmt.Sprintf("name='%s'", clean), "get", "drivername", "/format:list")
	if out, werr := wmic.Output(); werr == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if len(line) > len("drivername=") && strings.EqualFold(line[:len("drivername=")], "drivername=") {
				return strings.TrimSpace(line[len("drivername="):]), nil
			}
		}
	}
	return "", fmt.Errorf("cannot determine driver for %q", name)
}

func isClassDriver(driver string) bool {
	d := strings.ToLower(driver)
	return strings.Contains(d, "class driver") ||
		strings.Contains(d, "universal") ||
		strings.Contains(d, "ipp class")
}

func (p *WindowsPrinter) printRendered(ctx context.Context, job Job, filePath string) error {
	gs := p.gsPath
	if gs == "" {
		gs = findGhostscript("")
		p.gsPath = gs
	}
	if gs == "" {
		return ErrGSNotFound
	}

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
		filePath,
	)

	fi, _ := os.Stat(filePath)
	var fileSize int64
	if fi != nil {
		fileSize = fi.Size()
	}
	slog.Info("executing windows print job", "printer", job.Printer, "file", filePath, "bytes", fileSize)

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
