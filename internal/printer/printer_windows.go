//go:build windows

package printer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/render"
	"golang.org/x/exp/slog"
)

type WindowsPrinter struct {
	gsPath         string
	officePath     string
	convertTimeout time.Duration
	allowRaw       bool
	thermal        []config.ThermalPrinterConfig

	enumMu    sync.Mutex
	enumCache *windowsEnumeration
	enumAt    time.Time
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

// windowsEnumeration menyimpan hasil enumerasi printer Windows (drivers, ports,
// printers) yang diambil dalam satu proses PowerShell.
type windowsEnumeration struct {
	drivers  []string
	ports    []PrinterPort
	printers []PrinterInfo
}

const (
	// windowsEnumTimeout membatasi lama enumerasi agar agent tetap membalas
	// permintaan server tepat waktu walau WMI/PowerShell lambat di Windows 7.
	windowsEnumTimeout = 8 * time.Second
	// windowsEnumTTL menyegarkan cache enumerasi.
	windowsEnumTTL = 5 * time.Second
)

// windowsEnumScript mengumpulkan drivers, ports, dan printers dalam SATU proses
// PowerShell. Kompatibel dengan PowerShell 2.0 (Windows 7 SP1) hingga PowerShell 5.1+ (Windows 10/11).
const windowsEnumScript = `$ErrorActionPreference='SilentlyContinue'
Import-Module PrintManagement -ErrorAction SilentlyContinue
$wmiPrinters = @(Get-WmiObject Win32_Printer)
$defaultName = ''
foreach ($wp in $wmiPrinters) { if ($wp.Default) { $defaultName = $wp.Name } }
$drivers = @()
$ports = @()
$names = @()
try { $drivers = @(Get-PrinterDriver -ErrorAction Stop | Select-Object -ExpandProperty Name) } catch {}
try { $ports = @(Get-PrinterPort -ErrorAction Stop | Select-Object -ExpandProperty Name) } catch {}
try { $names = @(Get-Printer -ErrorAction Stop | Select-Object -ExpandProperty Name) } catch {}
if ($drivers.Count -eq 0) { $drivers = @(Get-WmiObject Win32_PrinterDriver | ForEach-Object { ($_.Name -split ',')[0] } | Select-Object -Unique) }
if ($ports.Count -eq 0) { $ports = @(Get-WmiObject Win32_TCPIPPrinterPort | Select-Object -ExpandProperty Name) }
if ($names.Count -eq 0) { $names = @($wmiPrinters | Select-Object -ExpandProperty Name) }
$printers = @($names | Where-Object { $_ } | ForEach-Object { [pscustomobject]@{ name = $_; default = ($_ -eq $defaultName) } })
$obj = [pscustomobject]@{ drivers = @($drivers); ports = @($ports); printers = @($printers) }
if (Get-Command ConvertTo-Json -ErrorAction SilentlyContinue) {
    $obj | ConvertTo-Json -Depth 5 -Compress
} else {
    try {
        Add-Type -AssemblyName System.Web.Extensions -ErrorAction SilentlyContinue
        $ser = New-Object System.Web.Script.Serialization.JavaScriptSerializer
        $ser.Serialize($obj)
    } catch {
        # Fallback manual JSON string untuk PowerShell 2.0 tanpa .NET Web Extensions
        $dJson = ($drivers | ForEach-Object { '"' + ($_ -replace '\\','\\\\' -replace '"','\"') + '"' }) -join ','
        $pJson = ($ports | ForEach-Object { '"' + ($_ -replace '\\','\\\\' -replace '"','\"') + '"' }) -join ','
        $prList = @()
        foreach ($p in $printers) {
            $nEsc = $p.name -replace '\\','\\\\' -replace '"','\"'
            $dVal = if ($p.default) { 'true' } else { 'false' }
            $prList += '{"name":"' + $nEsc + '","default":' + $dVal + '}'
        }
        $prJson = $prList -join ','
        '{"drivers":[' + $dJson + '],"ports":[' + $pJson + '],"printers":[' + $prJson + ']}'
    }
}`

type enumJSON struct {
	Drivers  json.RawMessage `json:"drivers"`
	Ports    json.RawMessage `json:"ports"`
	Printers json.RawMessage `json:"printers"`
}

type enumPrinter struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"default"`
}

// decodeStringList menerima array JSON (atau skalar akibat unrolling PowerShell).
func decodeStringList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil && one != "" {
		return []string{one}
	}
	return nil
}

func decodePrinters(raw json.RawMessage) []enumPrinter {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []enumPrinter
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var one enumPrinter
	if err := json.Unmarshal(raw, &one); err == nil && one.Name != "" {
		return []enumPrinter{one}
	}
	return nil
}

// invalidateEnum membuang cache enumerasi setelah perubahan printer.
func (p *WindowsPrinter) invalidateEnum() {
	p.enumMu.Lock()
	p.enumCache = nil
	p.enumAt = time.Time{}
	p.enumMu.Unlock()
}

// enumerateWindows mengembalikan drivers/ports/printers dengan cache TTL pendek.
// Semua data diambil lewat satu panggilan PowerShell.
func (p *WindowsPrinter) enumerateWindows(ctx context.Context) (*windowsEnumeration, error) {
	p.enumMu.Lock()
	if p.enumCache != nil && time.Since(p.enumAt) < windowsEnumTTL {
		cached := p.enumCache
		p.enumMu.Unlock()
		return cached, nil
	}
	p.enumMu.Unlock()

	runCtx, cancel := context.WithTimeout(ctx, windowsEnumTimeout)
	defer cancel()

	out, err := exec.CommandContext(runCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", windowsEnumScript).Output()
	if err != nil {
		// Fallback WMI via wmic jika PowerShell gagal / timeout (terutama di Windows 7 SP1)
		if wmicEnum := p.enumerateWmic(ctx); wmicEnum != nil {
			p.enumMu.Lock()
			p.enumCache = wmicEnum
			p.enumAt = time.Now()
			p.enumMu.Unlock()
			return wmicEnum, nil
		}
		return p.staleEnum(fmt.Errorf("enumerate printers failed: %w", err))
	}

	var raw enumJSON
	if uerr := json.Unmarshal(bytes.TrimSpace(out), &raw); uerr != nil {
		if wmicEnum := p.enumerateWmic(ctx); wmicEnum != nil {
			p.enumMu.Lock()
			p.enumCache = wmicEnum
			p.enumAt = time.Now()
			p.enumMu.Unlock()
			return wmicEnum, nil
		}
		return p.staleEnum(fmt.Errorf("parse printer enumeration failed: %w", uerr))
	}

	enum := &windowsEnumeration{drivers: decodeStringList(raw.Drivers)}
	for _, name := range decodeStringList(raw.Ports) {
		enum.ports = append(enum.ports, PrinterPort{Name: name})
	}
	for _, pr := range decodePrinters(raw.Printers) {
		enum.printers = append(enum.printers, PrinterInfo{
			Name:      pr.Name,
			IsDefault: pr.IsDefault,
			Status:    "ready",
		})
	}

	p.enumMu.Lock()
	p.enumCache = enum
	p.enumAt = time.Now()
	p.enumMu.Unlock()
	return enum, nil
}

// enumerateWmic adalah fallback instan dan ringan via wmic jika PowerShell gagal.
func (p *WindowsPrinter) enumerateWmic(ctx context.Context) *windowsEnumeration {
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(wctx, "wmic", "printer", "get", "name,default", "/format:csv")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(string(out), "\n")
	var printers []PrinterInfo
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "node") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) >= 3 {
			// format csv wmic: Node,Default,Name
			isDef := strings.EqualFold(strings.TrimSpace(parts[1]), "true")
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
	if len(printers) == 0 {
		return nil
	}
	return &windowsEnumeration{printers: printers}
}

// staleEnum mengembalikan cache lama bila ada agar permintaan server tetap
// dijawab meski enumerasi terbaru gagal/timeout.
func (p *WindowsPrinter) staleEnum(err error) (*windowsEnumeration, error) {
	p.enumMu.Lock()
	cached := p.enumCache
	p.enumMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	return nil, err
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
	enum, err := p.enumerateWindows(ctx)
	if err != nil {
		return nil, err
	}
	return enum.printers, nil
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

// escapePS meng-escape tanda kutip tunggal untuk skrip PowerShell.
func escapePS(s string) string { return strings.ReplaceAll(s, "'", "''") }

func runPS(ctx context.Context, script string) (string, error) {
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// looksIPP melaporkan apakah endpoint berupa URL IPP/HTTP.
func looksIPP(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "ipp://") || strings.HasPrefix(l, "ipps://") ||
		strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// parseRawEndpoint mengenali "socket://host:port" atau "host:port".
func parseRawEndpoint(s string) (string, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, false
	}
	if strings.HasPrefix(strings.ToLower(s), "socket://") {
		s = s[len("socket://"):]
	} else if strings.Contains(s, "://") {
		return "", 0, false
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}

func rawPortName(host string, port int) string {
	safe := strings.NewReplacer(".", "_", ":", "_", "\\", "_", "/", "_", " ", "_").Replace(host)
	return fmt.Sprintf("RSC_%s_%d", safe, port)
}

// psRawScript membangun skrip pemasangan queue RAW (TCP/IP) untuk Windows 7 s/d 11.
// Windows 7 tidak punya cmdlet Add-PrinterPort/Add-Printer -> pakai prnport.vbs/prnmngr.vbs.
func psRawScript(name, driver, host string, port int) string {
	portName := rawPortName(host, port)
	return fmt.Sprintf(
		`$OS=[Environment]::OSVersion.Version; `+
			`$win7=($OS.Major -lt 6) -or ($OS.Major -eq 6 -and $OS.Minor -lt 2); `+
			`$portName='%s'; `+
			`$scripts=Join-Path $env:windir 'System32\Printing_Admin_Scripts'; `+
			`$locale=(Get-ChildItem -Path $scripts -Directory -ErrorAction SilentlyContinue | Select-Object -First 1).FullName; `+
			`if ($win7) { `+
			`cscript //nologo (Join-Path $locale 'prnport.vbs') -a -r $portName -h '%s' -o raw -n %d; `+
			`if ($LASTEXITCODE -ne 0) { throw "prnport.vbs gagal (exit $LASTEXITCODE)" }; `+
			`cscript //nologo (Join-Path $locale 'prnmngr.vbs') -a -p '%s' -m '%s' -r $portName; `+
			`if ($LASTEXITCODE -ne 0) { throw "prnmngr.vbs gagal (exit $LASTEXITCODE)" } `+
			`} else { `+
			`if (-not (Get-PrinterPort -Name $portName -ErrorAction SilentlyContinue)) { `+
			`Add-PrinterPort -Name $portName -PrinterHostAddress '%s' -PortNumber %d -ErrorAction Stop }; `+
			`Add-Printer -Name '%s' -DriverName '%s' -PortName $portName -ErrorAction Stop `+
			`}`,
		escapePS(portName),
		escapePS(host), port,
		escapePS(name), escapePS(driver),
		escapePS(host), port,
		escapePS(name), escapePS(driver),
	)
}

func psIPPScript(name, url string) string {
	return fmt.Sprintf(`Add-Printer -Name '%s' -IppURL '%s' -ErrorAction Stop`, escapePS(name), escapePS(url))
}

func psConnectionScript(url string) string {
	return fmt.Sprintf(`Add-Printer -ConnectionName '%s' -ErrorAction Stop`, escapePS(url))
}

// Register memasang queue printer. Idempotent: bila printer sudah ada,
// konfigurasi diperbarui (repoint/rename) alih-alih gagal.
func (p *WindowsPrinter) Register(ctx context.Context, params RegisterPrinterParams) error {
	if strings.TrimSpace(params.Name) == "" {
		return fmt.Errorf("printer name required")
	}

	// Printer sudah ada -> perbarui (repoint) saja.
	if _, err := p.Info(ctx, params.Name); err == nil {
		isDef := params.IsDefault
		return p.Update(ctx, params.Name, UpdatePrinterParams{
			DriverName: params.DriverName,
			Type:       params.Type,
			PortOrURI:  params.PortOrURI,
			IsDefault:  &isDef,
		})
	}

	endpoint := strings.TrimSpace(params.PortOrURI)
	var psScript string

	switch {
	case strings.HasPrefix(endpoint, `\\`):
		// UNC SMB: \\Server\Share
		psScript = psConnectionScript(endpoint)

	case looksIPP(endpoint):
		// IPP/HTTP (butuh fitur Internet Printing Client).
		psScript = psIPPScript(params.Name, endpoint)

	case params.Type == PrinterTypeUSB || params.Type == PrinterTypeBluetooth:
		driver := params.DriverName
		if driver == "" {
			driver = "Generic / Text Only"
		}
		psScript = fmt.Sprintf("Add-Printer -Name '%s' -DriverName '%s' -PortName '%s' -ErrorAction Stop",
			escapePS(params.Name), escapePS(driver), escapePS(endpoint))

	default:
		host, port, ok := parseRawEndpoint(endpoint)
		if !ok && params.Type == PrinterTypeIP && endpoint != "" && !strings.ContainsAny(endpoint, " /\\") {
			// IP/host tanpa port -> RAW default 9100.
			host, port, ok = endpoint, 9100, true
		}
		if !ok {
			return fmt.Errorf("unsupported endpoint %q (butuh \\\\host\\share, http(s)://, ipp://, atau socket://host:port)", endpoint)
		}
		driver := params.DriverName
		if driver == "" {
			driver = "Generic / Text Only"
		}
		psScript = psRawScript(params.Name, driver, host, port)
	}

	if params.IsDefault {
		psScript += fmt.Sprintf(`; (New-Object -ComObject WScript.Network).SetDefaultPrinter('%s')`, escapePS(params.Name))
	}

	out, err := runPS(ctx, psScript)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = "tanpa detail; periksa hak Administrator & driver printer terpasang"
		}
		return fmt.Errorf("register printer failed: %s (%w)", msg, err)
	}
	p.invalidateEnum()
	return nil
}

func (p *WindowsPrinter) SetDefault(ctx context.Context, name string) error {
	cleanName := escapePS(name)
	psScript := fmt.Sprintf(`(New-Object -ComObject WScript.Network).SetDefaultPrinter('%s')`, cleanName)
	if out, err := runPS(ctx, psScript); err != nil {
		return fmt.Errorf("set default printer failed: %s (%w)", out, err)
	}
	return nil
}

func (p *WindowsPrinter) Remove(ctx context.Context, name string) error {
	cleanName := escapePS(name)
	psScript := fmt.Sprintf(`Remove-Printer -Name '%s' -ErrorAction Stop`, cleanName)
	out, err := runPS(ctx, psScript)
	if err != nil {
		// Fallback wmic
		wmic := exec.CommandContext(ctx, "wmic", "printer", "where", fmt.Sprintf(`name="%s"`, cleanName), "delete")
		if wout, werr := wmic.CombinedOutput(); werr != nil {
			return fmt.Errorf("remove printer failed: %s / %s (%w)", out, strings.TrimSpace(string(wout)), err)
		}
	}
	p.invalidateEnum()
	return nil
}

// Share membagikan printer lokal via SMB (Windows tidak punya IPP server native).
func (p *WindowsPrinter) Share(ctx context.Context, name string, opts ShareOptions) (PrinterShare, error) {
	if _, err := p.Info(ctx, name); err != nil {
		return PrinterShare{}, err
	}
	share := strings.TrimSpace(opts.ShareName)
	if share == "" {
		share = defaultShareName(name)
	}
	ps := fmt.Sprintf("Set-Printer -Name '%s' -Shared $true -ShareName '%s'", escapePS(name), escapePS(share))
	if out, err := runPS(ctx, ps); err != nil {
		return PrinterShare{}, fmt.Errorf("share printer failed: %s (%w)", out, err)
	}
	host := LocalHostname()
	return PrinterShare{
		Name:      name,
		Protocol:  "smb",
		ShareName: share,
		Host:      host,
		SMBPath:   smbPath(host, share),
	}, nil
}

func (p *WindowsPrinter) Unshare(ctx context.Context, name string) error {
	ps := fmt.Sprintf("Set-Printer -Name '%s' -Shared $false", escapePS(name))
	if out, err := runPS(ctx, ps); err != nil {
		return fmt.Errorf("unshare printer failed: %s (%w)", out, err)
	}
	return nil
}

func (p *WindowsPrinter) Options(ctx context.Context) (PrinterOptions, error) {
	enum, err := p.enumerateWindows(ctx)
	if err != nil {
		return PrinterOptions{Types: SupportedPrinterTypes}, err
	}
	return PrinterOptions{
		Types:   SupportedPrinterTypes,
		Drivers: enum.drivers,
		Ports:   enum.ports,
	}, nil
}

func (p *WindowsPrinter) Update(ctx context.Context, name string, params UpdatePrinterParams) error {
	cleanName := strings.ReplaceAll(name, "'", "''")

	setParts := []string{fmt.Sprintf("Set-Printer -Name '%s'", cleanName)}
	var preParts []string

	if params.NewName != "" {
		setParts = append(setParts, fmt.Sprintf("-NewName '%s'", strings.ReplaceAll(params.NewName, "'", "''")))
	}
	if params.DriverName != "" {
		setParts = append(setParts, fmt.Sprintf("-DriverName '%s'", strings.ReplaceAll(params.DriverName, "'", "''")))
	}
	if params.PortOrURI != "" {
		port := params.PortOrURI
		if params.Type == PrinterTypeIP {
			cleanPort := strings.ReplaceAll(params.PortOrURI, "'", "''")
			port = fmt.Sprintf("IP_%s", params.PortOrURI)
			preParts = append(preParts, fmt.Sprintf(
				"$port = Get-PrinterPort -Name 'IP_%s' -ErrorAction SilentlyContinue; "+
					"if (-not $port) { Add-PrinterPort -Name 'IP_%s' -PrinterHostAddress '%s' -PortNumber 9100 }",
				cleanPort, cleanPort, cleanPort,
			))
		}
		setParts = append(setParts, fmt.Sprintf("-PortName '%s'", strings.ReplaceAll(port, "'", "''")))
	}

	ps := strings.Join(append(preParts, strings.Join(setParts, " ")), "; ")
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		return fmt.Errorf("update printer failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	if params.IsDefault != nil && *params.IsDefault {
		target := name
		if params.NewName != "" {
			target = params.NewName
		}
		return p.SetDefault(ctx, target)
	}
	p.invalidateEnum()
	return nil
}
