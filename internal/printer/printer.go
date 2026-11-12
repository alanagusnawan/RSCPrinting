package printer

import (
	"context"
	"errors"

	"github.com/rscprinting/print-agent/internal/render"
	"github.com/rscprinting/print-agent/pkg/apitypes"
)

var (
	ErrPrinterNotFound         = errors.New("printer not found")
	ErrPrintFailed             = errors.New("print execution failed")
	ErrGSNotFound              = errors.New("ghostscript binary not found")
	ErrRawSendFailed           = errors.New("raw print send failed")
	ErrDriverIncompatible      = errors.New("printer driver incompatible")
	ErrUnsupportedFormat       = render.ErrUnsupportedFormat
	ErrPrepareFailed           = render.ErrPrepareFailed
	ErrOfficeConverterNotFound = render.ErrOfficeConverterNotFound
	ErrOfficeConvertFailed     = render.ErrOfficeConvertFailed
	ErrThermalRenderFailed     = render.ErrThermalRenderFailed
)

// Format merepresentasikan cara sebuah dokumen harus diproses sebelum dicetak.
type Format = render.Format

const (
	FormatAuto   = render.FormatAuto
	FormatPDF    = render.FormatPDF
	FormatPS     = render.FormatPS
	FormatImage  = render.FormatImage
	FormatText   = render.FormatText
	FormatOffice = render.FormatOffice
	FormatRaw    = render.FormatRaw
	FormatZPL    = render.FormatZPL
)

// ParseFormat memetakan string (mis. dari CLI/HTTP) ke Format yang dikenal.
func ParseFormat(s string) Format { return render.NormalizeFormat(s) }

type PrinterInfo struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Status    string `json:"status"`
}

type Job struct {
	JobID       string
	Printer     string
	FilePath    string
	Format      Format
	ContentType string
	FileName    string
	Template    apitypes.PrintTemplate
}

type PrinterType string

const (
	PrinterTypeHTTPShared PrinterType = "HTTP_SHARED"
	PrinterTypeIP         PrinterType = "IP"
	PrinterTypeUSB        PrinterType = "USB"
	PrinterTypeBluetooth  PrinterType = "BLUETOOTH"
)

type RegisterPrinterParams struct {
	Name       string      `json:"name"`
	DriverName string      `json:"driverName,omitempty"`
	Type       PrinterType `json:"type"`
	PortOrURI  string      `json:"portOrUri"`
	IsDefault  bool        `json:"isDefault,omitempty"`
}

// UpdatePrinterParams mendeskripsikan perubahan pada printer yang sudah ada.
// Semua field opsional; hanya yang terisi yang diubah.
type UpdatePrinterParams struct {
	NewName    string      `json:"newName,omitempty"`
	DriverName string      `json:"driverName,omitempty"`
	Type       PrinterType `json:"type,omitempty"`
	PortOrURI  string      `json:"portOrUri,omitempty"`
	IsDefault  *bool       `json:"isDefault,omitempty"`
}

type PrinterTypeOption struct {
	Type        PrinterType `json:"type"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	NeedDriver  bool        `json:"needDriver"`
	PortHint    string      `json:"portHint"`
}

type PrinterPort struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PrinterOptions struct {
	Types   []PrinterTypeOption `json:"types"`
	Drivers []string            `json:"drivers"`
	Ports   []PrinterPort       `json:"ports"`
}

var SupportedPrinterTypes = []PrinterTypeOption{
	{
		Type:        PrinterTypeHTTPShared,
		Label:       "Shared Printer (HTTP/IPP)",
		Description: "Printer jaringan yang dibagikan; dipetakan via URL/guna IPP.",
		NeedDriver:  false,
		PortHint:    "http://server:631/printers/NamaPrinter",
	},
	{
		Type:        PrinterTypeIP,
		Label:       "Network Printer (IP)",
		Description: "Printer Wi-Fi/LAN dengan IP statis; port TCP 9100 dibuat otomatis.",
		NeedDriver:  true,
		PortHint:    "192.168.1.200",
	},
	{
		Type:        PrinterTypeUSB,
		Label:       "USB Printer",
		Description: "Printer lokal via USB; pastikan printer tercolok agar port virtual terbentuk.",
		NeedDriver:  true,
		PortHint:    "USB001",
	},
	{
		Type:        PrinterTypeBluetooth,
		Label:       "Bluetooth / Wireless",
		Description: "Printer Bluetooth; lakukan pairing dulu lalu gunakan COM port virtual.",
		NeedDriver:  true,
		PortHint:    "COM3",
	},
}

type Printer interface {
	Print(ctx context.Context, job Job) error
	List(ctx context.Context) ([]PrinterInfo, error)
	Info(ctx context.Context, name string) (PrinterInfo, error)
	Register(ctx context.Context, params RegisterPrinterParams) error
	Update(ctx context.Context, name string, params UpdatePrinterParams) error
	Remove(ctx context.Context, name string) error
	SetDefault(ctx context.Context, name string) error
	Options(ctx context.Context) (PrinterOptions, error)
	SetDefault(ctx context.Context, name string) error
	Delete(ctx context.Context, name string) error
}
