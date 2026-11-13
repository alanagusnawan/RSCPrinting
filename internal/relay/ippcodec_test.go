package relay

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func buildPrintJobRequest(printerURI, format string, doc []byte) []byte {
	buf := buildIPPHeader(0x02, 0x00, ippOpPrintJob, 0x00000001)
	buf.WriteByte(ippTagOperationAttributes)
	appendStringAttr(buf, ippTagCharset, "attributes-charset", "utf-8")
	appendStringAttr(buf, ippTagLanguage, "attributes-natural-language", "en-us")
	appendStringAttr(buf, ippTagURI, "printer-uri", printerURI)
	if format != "" {
		appendStringAttr(buf, ippTagMimeType, "document-format", format)
	}
	buf.WriteByte(ippTagEndOfAttributes)
	buf.Write(doc)
	return buf.Bytes()
}

func TestParseIPPPrintJob(t *testing.T) {
	doc := []byte("%PDF-1.4 hello")
	body := buildPrintJobRequest("ipp://host:631/printers/Thermal", "application/pdf", doc)

	req, err := parseIPP(body)
	if err != nil {
		t.Fatalf("parseIPP error: %v", err)
	}
	if req.Operation != ippOpPrintJob {
		t.Errorf("operation = %#x, want %#x", req.Operation, ippOpPrintJob)
	}
	if req.RequestID != 1 {
		t.Errorf("requestID = %d, want 1", req.RequestID)
	}
	if req.PrinterURI != "ipp://host:631/printers/Thermal" {
		t.Errorf("printerURI = %q", req.PrinterURI)
	}
	if req.DocumentFormat != "application/pdf" {
		t.Errorf("documentFormat = %q", req.DocumentFormat)
	}
	if !bytes.Equal(req.Document, doc) {
		t.Errorf("document = %q, want %q", req.Document, doc)
	}
	if got := printerNameFromURI(req.PrinterURI); got != "Thermal" {
		t.Errorf("printerNameFromURI = %q", got)
	}
}

func TestParseIPPTooShort(t *testing.T) {
	if _, err := parseIPP([]byte{0x02, 0x00}); err == nil {
		t.Fatal("expected error for short body")
	}
}

func TestPrinterAttributesResponse(t *testing.T) {
	req := ippRequest{VersionMajor: 2, VersionMinor: 0, RequestID: 7, Charset: "utf-8"}
	resp := buildPrinterAttributesResponse(req, "Thermal", "RSC Relay", []string{"application/pdf"})
	if len(resp) < 8 {
		t.Fatalf("response too short: %d", len(resp))
	}
	if resp[0] != 2 || resp[1] != 0 {
		t.Errorf("version = %d.%d", resp[0], resp[1])
	}
	if status := binary.BigEndian.Uint16(resp[2:4]); status != ippStatusOK {
		t.Errorf("status = %#x", status)
	}
	if rid := binary.BigEndian.Uint32(resp[4:8]); rid != 7 {
		t.Errorf("requestID = %d", rid)
	}
	if resp[len(resp)-1] != ippTagEndOfAttributes {
		t.Errorf("response must end with end-of-attributes tag, got %#x", resp[len(resp)-1])
	}
}

func TestExtForFormat(t *testing.T) {
	cases := []struct {
		format string
		data   []byte
		want   string
	}{
		{"application/pdf", nil, ".pdf"},
		{"image/jpeg", nil, ".jpg"},
		{"image/png", nil, ".png"},
		{"", []byte("%PDF-1.7"), ".pdf"},
		{"", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, ".png"},
		{"", []byte{0xFF, 0xD8, 0xFF}, ".jpg"},
		{"", []byte("random"), ".bin"},
	}
	for _, c := range cases {
		if got := extForFormat(c.format, c.data); got != c.want {
			t.Errorf("extForFormat(%q, %v) = %q, want %q", c.format, c.data, got, c.want)
		}
	}
}
