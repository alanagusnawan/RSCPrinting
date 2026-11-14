package relay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/url"
	"strings"
)

// Tag IPP (subset yang dibutuhkan).
const (
	ippTagOperationAttributes = 0x01
	ippTagJobAttributes       = 0x02
	ippTagEndOfAttributes     = 0x03
	ippTagPrinterAttributes   = 0x04
	ippTagUnsupportedAttrs    = 0x05
)

const (
	ippTagInteger  = 0x21
	ippTagBoolean  = 0x22
	ippTagEnum     = 0x23
	ippTagText     = 0x41
	ippTagName     = 0x42
	ippTagKeyword  = 0x44
	ippTagURI      = 0x45
	ippTagCharset  = 0x47
	ippTagLanguage = 0x48
	ippTagMimeType = 0x49
)

// Operation ID IPP.
const (
	ippOpPrintJob             = 0x0002
	ippOpValidateJob          = 0x0004
	ippOpCancelJob            = 0x0008
	ippOpGetJobAttributes     = 0x0009
	ippOpGetPrinterAttributes = 0x000B
)

// Status IPP.
const (
	ippStatusOK                    = 0x0000
	ippStatusClientErrorBadRequest = 0x0400
)

var errShortIPP = errors.New("ipp: body too short")

// ippRequest adalah hasil parsing minimal sebuah request IPP.
type ippRequest struct {
	VersionMajor   byte
	VersionMinor   byte
	Operation      uint16
	RequestID      uint32
	PrinterURI     string
	Charset        string
	DocumentFormat string
	Document       []byte
}

// parseIPP membaca header + attribute group IPP. Bagian setelah
// end-of-attributes dianggap sebagai dokumen (Print-Job).
func parseIPP(body []byte) (ippRequest, error) {
	var req ippRequest
	if len(body) < 8 {
		return req, errShortIPP
	}
	req.VersionMajor = body[0]
	req.VersionMinor = body[1]
	req.Operation = binary.BigEndian.Uint16(body[2:4])
	req.RequestID = binary.BigEndian.Uint32(body[4:8])

	off := 8
	for off < len(body) {
		tag := body[off]
		off++
		if tag == ippTagEndOfAttributes {
			break
		}
		// Delimiter group (operation/job/printer) tidak punya name/value.
		if tag < 0x10 {
			continue
		}
		if off+2 > len(body) {
			return req, errShortIPP
		}
		nameLen := int(binary.BigEndian.Uint16(body[off : off+2]))
		off += 2
		if off+nameLen > len(body) {
			return req, errShortIPP
		}
		name := string(body[off : off+nameLen])
		off += nameLen
		if off+2 > len(body) {
			return req, errShortIPP
		}
		valLen := int(binary.BigEndian.Uint16(body[off : off+2]))
		off += 2
		if off+valLen > len(body) {
			return req, errShortIPP
		}
		val := body[off : off+valLen]
		off += valLen

		switch name {
		case "printer-uri":
			req.PrinterURI = string(val)
		case "attributes-charset":
			req.Charset = string(val)
		case "document-format":
			req.DocumentFormat = string(val)
		}
	}
	req.Document = body[off:]
	return req, nil
}

func writeUint16(buf *bytes.Buffer, v uint16) {
	_ = binary.Write(buf, binary.BigEndian, v)
}

func appendStringAttr(buf *bytes.Buffer, tag byte, name, value string) {
	buf.WriteByte(tag)
	writeUint16(buf, uint16(len(name)))
	buf.WriteString(name)
	writeUint16(buf, uint16(len(value)))
	buf.WriteString(value)
}

func appendIntAttr(buf *bytes.Buffer, tag byte, name string, v int32) {
	buf.WriteByte(tag)
	writeUint16(buf, uint16(len(name)))
	buf.WriteString(name)
	writeUint16(buf, 4)
	_ = binary.Write(buf, binary.BigEndian, v)
}

func appendBoolAttr(buf *bytes.Buffer, name string, v bool) {
	val := byte(0x00)
	if v {
		val = 0x01
	}
	buf.WriteByte(ippTagBoolean)
	writeUint16(buf, uint16(len(name)))
	buf.WriteString(name)
	writeUint16(buf, 1)
	buf.WriteByte(val)
}

func buildIPPHeader(versionMajor, versionMinor byte, status uint16, requestID uint32) *bytes.Buffer {
	buf := &bytes.Buffer{}
	buf.WriteByte(versionMajor)
	buf.WriteByte(versionMinor)
	_ = binary.Write(buf, binary.BigEndian, status)
	_ = binary.Write(buf, binary.BigEndian, requestID)
	return buf
}

func appendOperationAttributes(buf *bytes.Buffer, charset string) {
	if charset == "" {
		charset = "utf-8"
	}
	buf.WriteByte(ippTagOperationAttributes)
	appendStringAttr(buf, ippTagCharset, "attributes-charset", charset)
	appendStringAttr(buf, ippTagLanguage, "attributes-natural-language", "en-us")
}

// buildSimpleResponse menghasilkan respons sukses tanpa attribute tambahan.
func buildSimpleResponse(req ippRequest, status uint16) []byte {
	buf := buildIPPHeader(req.VersionMajor, req.VersionMinor, status, req.RequestID)
	appendOperationAttributes(buf, req.Charset)
	buf.WriteByte(ippTagEndOfAttributes)
	return buf.Bytes()
}

// buildPrinterAttributesResponse menjawab Get-Printer-Attributes secukupnya
// agar client (CUPS/macOS/Windows IPP) mau lanjut mencetak.
func buildPrinterAttributesResponse(req ippRequest, printerName, makeModel string, formats []string) []byte {
	buf := buildIPPHeader(req.VersionMajor, req.VersionMinor, ippStatusOK, req.RequestID)
	appendOperationAttributes(buf, req.Charset)

	buf.WriteByte(ippTagPrinterAttributes)
	appendStringAttr(buf, ippTagURI, "printer-uri-supported", "ipp://localhost/printers/"+url.PathEscape(printerName))
	appendStringAttr(buf, ippTagName, "printer-name", printerName)
	appendIntAttr(buf, ippTagEnum, "printer-state", 3) // idle
	appendBoolAttr(buf, "printer-is-accepting-jobs", true)
	appendStringAttr(buf, ippTagKeyword, "printer-state-reasons", "none")
	appendStringAttr(buf, ippTagText, "printer-make-and-model", makeModel)
	appendStringAttr(buf, ippTagKeyword, "ipp-versions-supported", "1.1")
	appendStringAttr(buf, ippTagKeyword, "ipp-versions-supported", "2.0")
	appendStringAttr(buf, ippTagCharset, "charset-configured", "utf-8")
	appendStringAttr(buf, ippTagCharset, "charset-supported", "utf-8")
	appendStringAttr(buf, ippTagLanguage, "natural-language-configured", "en-us")
	appendStringAttr(buf, ippTagLanguage, "generated-natural-language-supported", "en-us")
	appendStringAttr(buf, ippTagKeyword, "pdl-override-supported", "not-attempted")
	if len(formats) > 0 {
		appendStringAttr(buf, ippTagMimeType, "document-format-default", formats[0])
	}
	for _, f := range formats {
		appendStringAttr(buf, ippTagMimeType, "document-format-supported", f)
	}
	for _, op := range []int32{ippOpPrintJob, ippOpValidateJob, ippOpGetPrinterAttributes, ippOpCancelJob, ippOpGetJobAttributes} {
		appendIntAttr(buf, ippTagEnum, "operations-supported", op)
	}
	buf.WriteByte(ippTagEndOfAttributes)
	return buf.Bytes()
}

// printerNameFromURI mengambil segmen terakhir printer-uri dan melakukan unescape.
func printerNameFromURI(uri string) string {
	uri = strings.TrimRight(strings.TrimSpace(uri), "/")
	name := uri
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		name = uri[i+1:]
	}
	if unescaped, err := url.PathUnescape(name); err == nil {
		return unescaped
	}
	return name
}
