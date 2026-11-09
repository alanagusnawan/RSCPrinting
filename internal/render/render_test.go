package render

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name        string
		fileName    string
		contentType string
		data        []byte
		want        Format
	}{
		{"pdf magic", "", "", []byte("%PDF-1.7\n..."), FormatPDF},
		{"ps magic", "", "", []byte("%!PS-Adobe-3.0\n"), FormatPS},
		{"png magic", "", "", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, FormatImage},
		{"jpeg magic", "", "", []byte{0xFF, 0xD8, 0xFF, 0xE0}, FormatImage},
		{"zpl magic", "", "", []byte("^XA^FO50,50^FDHi^FS^XZ"), FormatZPL},
		{"escpos magic", "", "", []byte{0x1B, 0x40, 0x1B, 0x61, 0x01}, FormatRaw},
		{"office zip", "laporan.docx", "", []byte{'P', 'K', 0x03, 0x04}, FormatOffice},
		{"office content-type", "", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", []byte("x"), FormatOffice},
		{"image content-type", "", "image/jpeg", []byte("x"), FormatImage},
		{"office ext only", "data.xlsx", "", []byte("plain-ish data"), FormatOffice},
		{"text heuristic", "", "", []byte("hello world\nsecond line\n"), FormatText},
		{"unknown", "", "", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}, FormatAuto},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Detect(c.fileName, c.contentType, c.data)
			if got != c.want {
				t.Fatalf("Detect(%q,%q,...) = %q, want %q", c.fileName, c.contentType, got, c.want)
			}
		})
	}
}

func TestTextToPS(t *testing.T) {
	ps := TextToPS([]byte("Line one\nLine (two) \\ three"), 595.28, 841.89)
	if !bytes.HasPrefix(ps, []byte("%!PS-Adobe")) {
		t.Fatalf("output is not PostScript: %q", ps[:20])
	}
	if !bytes.Contains(ps, []byte("showpage")) {
		t.Fatalf("missing showpage")
	}
	if !bytes.Contains(ps, []byte(`\(two\)`)) {
		t.Fatalf("parentheses not escaped: %s", ps)
	}
}

func TestImageToPDF(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	pdf, err := ImageToPDF(buf.Bytes(), 595.28, 841.89)
	if err != nil {
		t.Fatalf("ImageToPDF error: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("output is not PDF")
	}
	if !bytes.Contains(pdf, []byte("/DCTDecode")) {
		t.Fatalf("expected embedded JPEG image object")
	}
	if !bytes.Contains(pdf, []byte("%%EOF")) {
		t.Fatalf("missing EOF marker")
	}
}

func TestNormalizeFormat(t *testing.T) {
	cases := map[string]Format{
		"":                FormatAuto,
		"auto":            FormatAuto,
		"application/pdf": FormatPDF,
		"image/png":       FormatImage,
		"text/plain":      FormatText,
		"zpl":             FormatZPL,
		"escpos":          FormatRaw,
		"docx":            FormatOffice,
	}
	for in, want := range cases {
		if got := NormalizeFormat(in); got != want {
			t.Fatalf("NormalizeFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrepareRaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "struk.escpos")
	data := []byte{0x1B, 0x40, 0x1B, 0x61, 0x01, 'H', 'i'}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := Prepare(context.Background(), Input{FilePath: path}, Options{})
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer doc.Cleanup()
	if !doc.IsRaw() {
		t.Fatalf("expected raw document")
	}
	if !bytes.Equal(doc.Raw, data) {
		t.Fatalf("raw bytes mismatch")
	}
}

func TestPrepareText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("hello world\nline two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := Prepare(context.Background(), Input{FilePath: path}, Options{})
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer doc.Cleanup()
	if doc.IsRaw() {
		t.Fatalf("expected rendered document")
	}
	if filepath.Ext(doc.Path) != ".ps" {
		t.Fatalf("expected .ps output, got %s", doc.Path)
	}
}
