package apitypes

import "time"

type MessageType string

const (
	TypeHello                 MessageType = "hello"
	TypeWelcome               MessageType = "welcome"
	TypeJob                   MessageType = "job"
	TypeJobResult             MessageType = "job_result"
	TypePrintersUpdate        MessageType = "printers_update"
	TypeGetOptions            MessageType = "get_options"
	TypeOptionsResult         MessageType = "options_result"
	TypeRegisterPrinter       MessageType = "register_printer"
	TypeRegisterPrinterResult MessageType = "register_printer_result"
	TypeSetDefaultPrinter     MessageType = "set_default_printer"
	TypeSetDefaultResult      MessageType = "set_default_result"
	TypeDeletePrinter         MessageType = "delete_printer"
	TypeDeletePrinterResult   MessageType = "delete_printer_result"
	TypeUpdatePrinter         MessageType = "update_printer"
	TypeUpdatePrinterResult   MessageType = "update_printer_result"
	TypeSyncPrinters          MessageType = "sync_printers"
	TypeSyncPrintersResult    MessageType = "sync_printers_result"
	TypePrintersDetail        MessageType = "printers_detail"
	TypeSharePrinter          MessageType = "share_printer"
	TypeSharePrinterResult    MessageType = "share_printer_result"
	TypeUnsharePrinter        MessageType = "unshare_printer"
	TypeUnsharePrinterResult  MessageType = "unshare_printer_result"
)

type HelloMessage struct {
	Type        MessageType `json:"type"`
	AgentID     string      `json:"agentId"`
	Hostname    string      `json:"hostname"`
	DisplayName string      `json:"displayName"`
	Printers    []string    `json:"printers"`
	Version     string      `json:"version"`
	OS          string      `json:"os"`
}

type WelcomeMessage struct {
	Type         MessageType `json:"type"`
	ServerTime   time.Time   `json:"serverTime"`
	ConnectionID string      `json:"connectionId"`
}

type PrintTemplate struct {
	Paper       string `json:"paper"`
	Orientation string `json:"orientation"`
	Copies      int    `json:"copies"`
	Duplex      bool   `json:"duplex"`
	Color       bool   `json:"color"`
	Scale       string `json:"scale"`
}

type JobMessage struct {
	Type        MessageType   `json:"type"`
	JobID       string        `json:"jobId"`
	Printer     string        `json:"printer"`
	Template    PrintTemplate `json:"template"`
	FileName    string        `json:"fileName"`
	FileSize    int64         `json:"fileSize"`
	PDFEncoding string        `json:"pdfEncoding"` // "binary"
	Format      string        `json:"format,omitempty"`
	ContentType string        `json:"contentType,omitempty"`
}

type JobResultMessage struct {
	Type       MessageType `json:"type"`
	JobID      string      `json:"jobId"`
	Success    bool        `json:"success"`
	Error      string      `json:"error,omitempty"`
	ErrorCode  string      `json:"errorCode,omitempty"`
	DurationMs int64       `json:"durationMs"`
}

type PrintersUpdateMessage struct {
	Type     MessageType `json:"type"`
	Printers []string    `json:"printers"`
}

type GetOptionsMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
}

type OptionsResultMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Success   bool        `json:"success"`
	Options   interface{} `json:"options,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type RegisterPrinterMessage struct {
	Type        MessageType `json:"type"`
	RequestID   string      `json:"requestId"`
	Name        string      `json:"name"`
	DriverName  string      `json:"driverName,omitempty"`
	PrinterType string      `json:"printerType"` // "HTTP_SHARED" | "IP" | "USB" | "BLUETOOTH"
	PortOrURI   string      `json:"portOrUri"`
	IsDefault   bool        `json:"isDefault,omitempty"`
	PPD         string      `json:"ppd,omitempty"`
	Driverless  bool        `json:"driverless,omitempty"`
}

type RegisterPrinterResultMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Success   bool        `json:"success"`
	Printer   string      `json:"printer,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type SetDefaultPrinterMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Printer   string      `json:"printer"`
}

type SetDefaultResultMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Success   bool        `json:"success"`
	Printer   string      `json:"printer,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type DeletePrinterMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Printer   string      `json:"printer"`
}

type DeletePrinterResultMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Success   bool        `json:"success"`
	Printer   string      `json:"printer,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// UpdatePrinterMessage menyuruh agent merepoint/me-rename queue yang sudah ada
// (dipakai saat IP/host sumber berubah, tanpa install ulang).
type UpdatePrinterMessage struct {
	Type        MessageType `json:"type"`
	RequestID   string      `json:"requestId"`
	Printer     string      `json:"printer"`
	NewName     string      `json:"newName,omitempty"`
	DriverName  string      `json:"driverName,omitempty"`
	PrinterType string      `json:"printerType,omitempty"`
	PortOrURI   string      `json:"portOrUri,omitempty"`
	PPD         string      `json:"ppd,omitempty"`
	Driverless  bool        `json:"driverless,omitempty"`
	IsDefault   *bool       `json:"isDefault,omitempty"`
}

type UpdatePrinterResultMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Success   bool        `json:"success"`
	Printer   string      `json:"printer,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// PrinterSpec adalah satu entri printer yang diinginkan server untuk dipasang
// pada sebuah device (dipakai pada pesan sync_printers).
type PrinterSpec struct {
	Name        string `json:"name"`
	DriverName  string `json:"driverName,omitempty"`
	PrinterType string `json:"printerType,omitempty"`
	PortOrURI   string `json:"portOrUri,omitempty"`
	PPD         string `json:"ppd,omitempty"`
	Driverless  bool   `json:"driverless,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Accept      *bool  `json:"accept,omitempty"`
}

// SyncPrintersMessage memasang beberapa printer sekaligus (bulk) pada device.
// Prune=true menghapus queue yang tidak ada lagi di daftar.
type SyncPrintersMessage struct {
	Type      MessageType   `json:"type"`
	RequestID string        `json:"requestId"`
	Printers  []PrinterSpec `json:"printers"`
	Prune     bool          `json:"prune,omitempty"`
}

type SyncPrintersResultMessage struct {
	Type      MessageType       `json:"type"`
	RequestID string            `json:"requestId"`
	Success   bool              `json:"success"`
	Installed []string          `json:"installed,omitempty"`
	Updated   []string          `json:"updated,omitempty"`
	Removed   []string          `json:"removed,omitempty"`
	Failed    map[string]string `json:"failed,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// PrinterDetail merinci satu printer lokal beserta endpoint share-nya.
type PrinterDetail struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Status    string `json:"status"`
	Shared    bool   `json:"shared,omitempty"`
	ShareName string `json:"shareName,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	SMBPath   string `json:"smbPath,omitempty"`
	IPPURI    string `json:"ippUri,omitempty"`
	SocketURI string `json:"socketUri,omitempty"`
	RelayURI  string `json:"relayUri,omitempty"`
}

// PrintersDetailMessage dilaporkan agent ke server (hostname + IP + endpoint).
type PrintersDetailMessage struct {
	Type     MessageType     `json:"type"`
	AgentID  string          `json:"agentId,omitempty"`
	Hostname string          `json:"hostname,omitempty"`
	OS       string          `json:"os,omitempty"`
	IPs      []string        `json:"ips,omitempty"`
	Printers []PrinterDetail `json:"printers"`
}

// SharePrinterMessage menyuruh agent membagikan printer lokalnya.
type SharePrinterMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Printer   string      `json:"printer"`
	Protocol  string      `json:"protocol,omitempty"`
	ShareName string      `json:"shareName,omitempty"`
}

type SharePrinterResultMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Success   bool        `json:"success"`
	Printer   string      `json:"printer,omitempty"`
	Protocol  string      `json:"protocol,omitempty"`
	ShareName string      `json:"shareName,omitempty"`
	SMBPath   string      `json:"smbPath,omitempty"`
	IPPURI    string      `json:"ippUri,omitempty"`
	SocketURI string      `json:"socketUri,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type UnsharePrinterMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Printer   string      `json:"printer"`
}

type UnsharePrinterResultMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"requestId"`
	Success   bool        `json:"success"`
	Printer   string      `json:"printer,omitempty"`
	Error     string      `json:"error,omitempty"`
}
