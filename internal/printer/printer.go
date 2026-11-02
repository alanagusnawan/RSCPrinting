package printer

import (
	"context"
	"errors"

	"github.com/rscprinting/print-agent/pkg/apitypes"
)

var (
	ErrPrinterNotFound = errors.New("printer not found")
	ErrPrintFailed     = errors.New("print execution failed")
	ErrGSNotFound      = errors.New("ghostscript binary not found")
)

type PrinterInfo struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Status    string `json:"status"`
}

type Job struct {
	JobID    string
	Printer  string
	FilePath string
	Template apitypes.PrintTemplate
}

type Printer interface {
	Print(ctx context.Context, job Job) error
	List(ctx context.Context) ([]PrinterInfo, error)
	Info(ctx context.Context, name string) (PrinterInfo, error)
}
