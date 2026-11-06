//go:build linux || darwin

package printer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type UnixPrinter struct{}

func New(_ string) (*UnixPrinter, error) {
	return &UnixPrinter{}, nil
}

func (p *UnixPrinter) Print(ctx context.Context, job Job) error {
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

	args = append(args, job.FilePath)

	cmd := exec.CommandContext(ctx, "lp", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s (%v)", ErrPrintFailed, strings.TrimSpace(stderr.String()), err)
	}

	return nil
}

func (p *UnixPrinter) List(ctx context.Context) ([]PrinterInfo, error) {
	cmd := exec.CommandContext(ctx, "lpstat", "-p", "-d")
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

func (p *UnixPrinter) Register(ctx context.Context, params RegisterPrinterParams) error {
	var uri string
	switch params.Type {
	case PrinterTypeHTTPShared:
		uri = params.PortOrURI
		if strings.HasPrefix(strings.ToLower(uri), "http://") {
			uri = "ipp://" + uri[7:]
		}
	case PrinterTypeIP:
		uri = fmt.Sprintf("socket://%s:9100", params.PortOrURI)
	default:
		uri = params.PortOrURI
	}

	args := []string{"-p", params.Name, "-v", uri, "-E"}
	cmd := exec.CommandContext(ctx, "lpadmin", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("lpadmin failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	if params.IsDefault {
		defCmd := exec.CommandContext(ctx, "lpadmin", "-d", params.Name)
		_ = defCmd.Run()
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
	out, err := exec.CommandContext(ctx, "lpinfo", "-m").Output()
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
	out, err := exec.CommandContext(ctx, "lpinfo", "-v").Output()
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
