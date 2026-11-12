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
	Type       MessageType `json:"type"`
	RequestID  string      `json:"requestId"`
	Name       string      `json:"name"`
	DriverName string      `json:"driverName,omitempty"`
	PrinterType string     `json:"printerType"` // "HTTP_SHARED" | "IP" | "USB" | "BLUETOOTH"
	PortOrURI  string      `json:"portOrUri"`
	IsDefault  bool        `json:"isDefault,omitempty"`
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
