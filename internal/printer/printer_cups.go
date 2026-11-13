//go:build linux || darwin

package printer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/render"
)

// UnixPrinter mengimplementasikan Printer lewat CUPS (lp/lpadmin/lpstat).
// Berlaku untuk Linux dan macOS; perbedaan kecil ditangani di file per-OS.
type UnixPrinter struct {
	officePath     string
	convertTimeout time.Duration
	allowRaw       bool
}

func New(cfg config.Config) (*UnixPrinter, error) {
	office := ""
	if cfg.Office.Enabled {
		office = render.FindLibreOffice(cfg.Office.Path)
	}

	allowRaw := true
	if cfg.Document.AllowRaw != nil {
		allowRaw = *cfg.Document.AllowRaw
	}

	return &UnixPrinter{
		officePath:     office,
		convertTimeout: cfg.Document.ConvertTimeout,
		allowRaw:       allowRaw,
	}, nil
}

// cupBin mencari binary CUPS di PATH lalu di lokasi umum (/usr/sbin dsb).
func cupBin(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	for _, dir := range []string{"/usr/sbin", "/usr/bin", "/usr/local/sbin", "/usr/local/bin"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return name
}

func cupCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, cupBin(name), args...)
}

func cupRun(ctx context.Context, name string, args ...string) (string, error) {
	out, err := cupCmd(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (p *UnixPrinter) Print(ctx context.Context, job Job) error {
	doc, err := render.Prepare(ctx, render.Input{
		FilePath:    job.FilePath,
		Format:      job.Format,
		ContentType: job.ContentType,
		FileName:    job.FileName,
	}, render.Options{
		OfficePath:     p.officePath,
		ConvertTimeout: p.convertTimeout,
		Paper:          job.Template.Paper,
	})
	if err != nil {
		return err
	}
	defer doc.Cleanup()

	if doc.IsRaw() {
		if !p.allowRaw {
			return fmt.Errorf("%w: raw printing is disabled in config", ErrRawSendFailed)
		}
		return p.printRaw(ctx, job.Printer, doc.Raw)
	}

	return p.printRendered(ctx, job, doc.Path)
}

func (p *UnixPrinter) printRaw(ctx context.Context, printerName string, data []byte) error {
	cmd := cupCmd(ctx, "lp", "-d", printerName, "-o", "raw")
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s (%v)", ErrRawSendFailed, strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

func (p *UnixPrinter) printRendered(ctx context.Context, job Job, filePath string) error {
	args := []string{"-d", job.Printer}

	if job.Template.Paper != "" {
		args = append(args, "-o", fmt.Sprintf("media=%s", job.Template.Paper))
	}
	if strings.EqualFold(job.Template.Orientation, "landscape") {
		args = append(args, "-o", "landscape")
	}
	if job.Template.Copies > 1 {
		args = append(args, "-n", strconv.Itoa(job.Template.Copies))
	}
	if job.Template.Duplex {
		args = append(args, "-o", "sides=two-sided-long-edge")
	}
	if strings.EqualFold(job.Template.Scale, "fit") {
		args = append(args, "-o", "fit-to-page")
	}

	args = append(args, filePath)

	cmd := cupCmd(ctx, "lp", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s (%v)", ErrPrintFailed, strings.TrimSpace(stderr.String()), err)
	}

	return nil
}

func (p *UnixPrinter) List(ctx context.Context) ([]PrinterInfo, error) {
	cmd := cupCmd(ctx, "lpstat", "-p", "-d")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lpstat failed: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	var defaultPrinter string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "system default destination: ") {
			defaultPrinter = strings.TrimPrefix(l, "system default destination: ")
			break
		}
	}

	var printers []PrinterInfo
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "printer ") {
			fields := strings.Fields(l)
			if len(fields) >= 2 {
				name := fields[1]
				isDefault := (name == defaultPrinter)
				status := "idle"
				if strings.Contains(l, "disabled") {
					status = "disabled"
				}
				printers = append(printers, PrinterInfo{
					Name:      name,
					IsDefault: isDefault,
					Status:    status,
				})
			}
		}
	}

	return printers, nil
}

func (p *UnixPrinter) Info(ctx context.Context, name string) (PrinterInfo, error) {
	list, err := p.List(ctx)
	if err != nil {
		return PrinterInfo{}, err
	}
	for _, pr := range list {
		if pr.Name == name {
			return pr, nil
		}
	}
	return PrinterInfo{}, ErrPrinterNotFound
}

// Register memasang queue CUPS. Idempotent: bila queue sudah ada, konfigurasi
// diperbarui (lpadmin -p ... -v ... -E).
func (p *UnixPrinter) Register(ctx context.Context, params RegisterPrinterParams) error {
	if params.Name == "" {
		return fmt.Errorf("printer name required")
	}
	uri := unixDeviceURI(params)
	if uri == "" {
		return fmt.Errorf("portOrUri required")
	}

	args := []string{"-p", params.Name, "-v", uri, "-E"}
	args = append(args, cupsModelArgs(params)...)

	if out, err := cupRun(ctx, "lpadmin", args...); err != nil {
		return fmt.Errorf("lpadmin failed: %s (%w)", out, err)
	}

	// Aktifkan & terima job agar printer siap dipakai.
	_, _ = cupRun(ctx, "cupsenable", params.Name)
	_, _ = cupRun(ctx, "cupsaccept", params.Name)

	if params.IsDefault {
		_ = p.SetDefault(ctx, params.Name)
	}
	return nil
}

func (p *UnixPrinter) Update(ctx context.Context, name string, params UpdatePrinterParams) error {
	if params.PortOrURI != "" {
		uri := unixDeviceURI(RegisterPrinterParams{Type: params.Type, PortOrURI: params.PortOrURI})
		args := []string{"-p", name, "-v", uri, "-E"}
		if out, err := cupRun(ctx, "lpadmin", args...); err != nil {
			return fmt.Errorf("lpadmin -v failed: %s (%w)", out, err)
		}
		_, _ = cupRun(ctx, "cupsenable", name)
		_, _ = cupRun(ctx, "cupsaccept", name)
	}

	if params.NewName != "" {
		if err := p.Remove(ctx, name); err != nil {
			return err
		}
		regType := params.Type
		if regType == "" {
			regType = PrinterTypeHTTPShared
		}
		if err := p.Register(ctx, RegisterPrinterParams{
			Name:       params.NewName,
			DriverName: params.DriverName,
			Type:       regType,
			PortOrURI:  params.PortOrURI,
		}); err != nil {
			return err
		}
		name = params.NewName
	}

	if params.IsDefault != nil && *params.IsDefault {
		return p.SetDefault(ctx, name)
	}
	return nil
}

func (p *UnixPrinter) Remove(ctx context.Context, name string) error {
	if out, err := cupRun(ctx, "lpadmin", "-x", name); err != nil {
		return fmt.Errorf("delete printer failed: %s (%w)", out, err)
	}
	return nil
}

// Share mengaktifkan berbagi CUPS untuk printer dan mengembalikan endpoint
// (IPP + SMB) yang bisa dipasang di komputer lain.
func (p *UnixPrinter) Share(ctx context.Context, name string, opts ShareOptions) (PrinterShare, error) {
	if _, err := p.Info(ctx, name); err != nil {
		return PrinterShare{}, err
	}

	_, _ = cupRun(ctx, "cupsctl", "--share-printers")
	if out, err := cupRun(ctx, "lpadmin", "-p", name, "-o", "printer-is-shared=true", "-E"); err != nil {
		return PrinterShare{}, fmt.Errorf("share printer failed: %s (%w)", out, err)
	}

	share := strings.TrimSpace(opts.ShareName)
	if share == "" {
		share = defaultShareName(name)
	}
	host := platformShareHost()

	return PrinterShare{
		Name:      name,
		Protocol:  "ipp",
		ShareName: share,
		Host:      host,
		SMBPath:   smbPath(host, share),
		IPPURI:    ippURI(host, 631, name),
	}, nil
}

func (p *UnixPrinter) Unshare(ctx context.Context, name string) error {
	if out, err := cupRun(ctx, "lpadmin", "-p", name, "-o", "printer-is-shared=false"); err != nil {
		return fmt.Errorf("unshare printer failed: %s (%w)", out, err)
	}
	return nil
}

func (p *UnixPrinter) Options(ctx context.Context) (PrinterOptions, error) {
	return PrinterOptions{
		Types:   SupportedPrinterTypes,
		Drivers: listUnixDrivers(ctx),
		Ports:   listUnixPorts(ctx),
	}, nil
}

func listUnixDrivers(ctx context.Context) []string {
	out, err := cupCmd(ctx, "lpinfo", "-m").Output()
	if err != nil {
		return nil
	}
	var drivers []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		model := fields[0]
		if model != "" && !seen[model] {
			seen[model] = true
			drivers = append(drivers, model)
		}
	}
	return drivers
}

func listUnixPorts(ctx context.Context) []PrinterPort {
	out, err := cupCmd(ctx, "lpinfo", "-v").Output()
	if err != nil {
		return nil
	}
	var ports []PrinterPort
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		uri := fields[1]
		desc := strings.Join(fields[2:], " ")
		ports = append(ports, PrinterPort{Name: uri, Description: desc})
	}
	return ports
}
