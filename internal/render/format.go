package render

import (
	"bytes"
	"path/filepath"
	"strings"
)

// Format merepresentasikan cara sebuah dokumen diproses sebelum dicetak.
type Format string

const (
	FormatAuto   Format = ""       // deteksi otomatis
	FormatPDF    Format = "pdf"    // PDF
	FormatPS     Format = "ps"     // PostScript / EPS
	FormatImage  Format = "image"  // JPG/PNG/BMP/GIF/TIFF
	FormatText   Format = "text"   // teks biasa
	FormatOffice Format = "office" // docx/xlsx/pptx/doc/xls/ppt/odt
	FormatRaw    Format = "raw"    // byte mentah (ESC/POS, PCL, dll)
	FormatZPL    Format = "zpl"    // ZPL label printer
)

// IsRawFormat mengembalikan true bila format harus dikirim mentah (tanpa rendering).
func IsRawFormat(f Format) bool {
	return f == FormatRaw || f == FormatZPL
}

// IsRenderFormat mengembalikan true bila format didukung jalur rendering (Ghostscript/lp).
func IsRenderFormat(f Format) bool {
	switch f {
	case FormatPDF, FormatPS, FormatImage, FormatText, FormatOffice:
		return true
	}
	return false
}

// NormalizeFormat memetakan string bebas (dari client) ke Format yang dikenal.
func NormalizeFormat(s string) Format {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		if f := formatFromContentType(s); f != FormatAuto {
			return f
		}
	}
	switch strings.ToLower(s) {
	case "", "auto":
		return FormatAuto
	case "pdf", "application/pdf":
		return FormatPDF
	case "ps", "eps", "postscript", "application/postscript", "application/eps":
		return FormatPS
	case "image", "img", "photo", "picture":
		return FormatImage
	case "text", "txt", "plain", "text/plain":
		return FormatText
	case "office", "doc", "docx", "xlsx", "pptx", "spreadsheet", "document":
		return FormatOffice
	case "raw", "escpos", "esc/pos", "pcl", "binary":
		return FormatRaw
	case "zpl", "zplii", "label":
		return FormatZPL
	}
	return FormatAuto
}

// Detect menentukan format dari nama file, Content-Type, dan isi (magic bytes).
func Detect(fileName, contentType string, data []byte) Format {
	if f := formatFromContentType(contentType); f != FormatAuto {
		return f
	}
	if f := formatFromMagic(data); f != FormatAuto {
		return f
	}
	if f := formatFromExtension(fileName); f != FormatAuto {
		return f
	}
	if looksLikeText(data) {
		return FormatText
	}
	return FormatAuto
}

func formatFromContentType(ct string) Format {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "":
		return FormatAuto
	case "application/pdf":
		return FormatPDF
	case "application/postscript", "application/eps", "application/x-eps":
		return FormatPS
	case "text/plain", "text/csv", "text/tab-separated-values":
		return FormatText
	case "text/zpl", "application/zpl":
		return FormatZPL
	case "application/octet-stream":
		return FormatAuto
	}
	if strings.HasPrefix(ct, "image/") {
		return FormatImage
	}
	if strings.Contains(ct, "officedocument") ||
		ct == "application/msword" ||
		ct == "application/vnd.ms-excel" ||
		ct == "application/vnd.ms-powerpoint" ||
		ct == "application/rtf" ||
		ct == "application/vnd.oasis.opendocument.text" ||
		ct == "application/vnd.oasis.opendocument.spreadsheet" ||
		ct == "application/vnd.oasis.opendocument.presentation" {
		return FormatOffice
	}
	return FormatAuto
}

func formatFromMagic(data []byte) Format {
	if len(data) == 0 {
		return FormatAuto
	}
	has := func(prefix string) bool { return bytes.HasPrefix(data, []byte(prefix)) }

	switch {
	case has("%PDF"):
		return FormatPDF
	case has("%!PS"):
		return FormatPS
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return FormatImage
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return FormatImage
	case has("GIF87a"), has("GIF89a"):
		return FormatImage
	case has("BM"):
		return FormatImage
	case bytes.HasPrefix(data, []byte{'I', 'I', 0x2A, 0x00}), bytes.HasPrefix(data, []byte{'M', 'M', 0x00, 0x2A}):
		return FormatImage
	case bytes.HasPrefix(data, []byte{'P', 'K', 0x03, 0x04}):
		return FormatOffice
	case bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}):
		return FormatOffice
	case bytes.HasPrefix(data, []byte("^XA")), bytes.HasPrefix(data, []byte("^xa")):
		return FormatZPL
	}
	if isEscPOS(data) {
		return FormatRaw
	}
	return FormatAuto
}

func formatFromExtension(fileName string) Format {
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".pdf":
		return FormatPDF
	case ".ps", ".eps":
		return FormatPS
	case ".jpg", ".jpeg", ".png", ".bmp", ".gif", ".tif", ".tiff", ".webp":
		return FormatImage
	case ".txt", ".csv", ".log", ".md", ".tsv":
		return FormatText
	case ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp", ".rtf":
		return FormatOffice
	case ".zpl", ".zplii":
		return FormatZPL
	case ".prn", ".escpos", ".esc", ".raw", ".bin", ".pcl":
		return FormatRaw
	}
	return FormatAuto
}

// isEscPOS mendeteksi byte awal perintah ESC/POS umum.
func isEscPOS(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	// ESC @ (initialize), GS ( , ESC a , ESC ! , ESC d , ESC p (kick)
	if data[0] == 0x1B {
		switch data[1] {
		case '@', 'a', '!', 'd', 'p', 'E', 'G', 'e':
			return true
		}
	}
	if data[0] == 0x1D && data[1] == 0x56 { // GS V (cut)
		return true
	}
	return false
}

// looksLikeText mengecek heuristik apakah data berupa teks yang dapat dicetak.
func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sample := data
	if len(sample) > 1024 {
		sample = sample[:1024]
	}
	nonPrintable := 0
	for _, b := range sample {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 || b > 0x7E {
			// izinkan UTF-8 (byte >= 0x80)
			if b >= 0x80 {
				continue
			}
			nonPrintable++
		}
	}
	return nonPrintable*20 < len(sample) // <5% byte kontrol aneh
}
