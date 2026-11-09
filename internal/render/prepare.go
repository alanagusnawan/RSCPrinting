package render

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Options mengatur perilaku penyiapan dokumen.
type Options struct {
	OfficePath     string          // path LibreOffice (opsional)
	ConvertTimeout time.Duration   // timeout konversi Office
	Paper          string          // nama kertas untuk rendering gambar/teks
	Thermal        *ThermalOptions // bila diisi, hasil dirender ke ESC/POS raster
}

// Input mendeskripsikan dokumen yang akan disiapkan.
type Input struct {
	FilePath    string
	Format      Format
	ContentType string
	FileName    string
}

// Document adalah hasil penyiapan: berupa byte mentah (raw) atau path file yang
// siap dirender oleh Ghostscript/CUPS.
type Document struct {
	Format  Format
	Raw     []byte
	Path    string
	cleanup func()
}

// Cleanup membuang sumber daya sementara yang dibuat saat penyiapan.
func (d *Document) Cleanup() {
	if d != nil && d.cleanup != nil {
		d.cleanup()
	}
}

// IsRaw true bila dokumen harus dikirim mentah ke printer.
func (d *Document) IsRaw() bool { return d.Raw != nil }

// Prepare mendeteksi format (bila perlu) dan menyiapkan file untuk dicetak.
func Prepare(ctx context.Context, in Input, opts Options) (*Document, error) {
	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		return nil, fmt.Errorf("%w: read file failed: %v", ErrPrepareFailed, err)
	}

	f := in.Format
	if f == FormatAuto {
		name := in.FileName
		if name == "" {
			name = in.FilePath
		}
		f = Detect(name, in.ContentType, data)
	}
	if f == FormatAuto {
		return nil, fmt.Errorf("%w: cannot detect document format", ErrUnsupportedFormat)
	}

	paperW, paperH := PaperSize(opts.Paper)

	if IsRawFormat(f) {
		return &Document{Format: f, Raw: data}, nil
	}

	renderPath, renderFormat, cleanup, err := prepareRenderSource(ctx, in, f, data, paperW, paperH, opts)
	if err != nil {
		return nil, err
	}

	if opts.Thermal != nil {
		raw, terr := PDFToESCPOS(ctx, renderPath, *opts.Thermal)
		if cleanup != nil {
			cleanup()
		}
		if terr != nil {
			return nil, terr
		}
		return &Document{Format: FormatRaw, Raw: raw}, nil
	}

	return &Document{Format: renderFormat, Path: renderPath, cleanup: cleanup}, nil
}

// prepareRenderSource menyiapkan file sumber (PDF/PS) yang siap dirender oleh
// Ghostscript/CUPS. Mengembalikan path, format hasil, dan fungsi cleanup.
func prepareRenderSource(ctx context.Context, in Input, f Format, data []byte, paperW, paperH float64, opts Options) (string, Format, func(), error) {
	switch f {
	case FormatPDF, FormatPS:
		return in.FilePath, f, nil, nil

	case FormatImage:
		pdf, err := ImageToPDF(data, paperW, paperH)
		if err != nil {
			return "", "", nil, fmt.Errorf("%w: %v", ErrPrepareFailed, err)
		}
		path, cleanup, err := writeTemp(".pdf", pdf)
		if err != nil {
			return "", "", nil, err
		}
		return path, FormatPDF, cleanup, nil

	case FormatText:
		ps := TextToPS(data, paperW, paperH)
		path, cleanup, err := writeTemp(".ps", ps)
		if err != nil {
			return "", "", nil, err
		}
		return path, FormatPS, cleanup, nil

	case FormatOffice:
		soffice := FindLibreOffice(opts.OfficePath)
		pdfPath, cleanup, err := ConvertOfficeToPDF(ctx, soffice, in.FilePath, opts.ConvertTimeout)
		if err != nil {
			return "", "", nil, err
		}
		return pdfPath, FormatPDF, cleanup, nil
	}

	return "", "", nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, f)
}

func writeTemp(ext string, data []byte) (string, func(), error) {
	tmp, err := os.CreateTemp("", "rsc-render-*"+ext)
	if err != nil {
		return "", nil, fmt.Errorf("%w: create temp failed: %v", ErrPrepareFailed, err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return "", nil, fmt.Errorf("%w: write temp failed: %v", ErrPrepareFailed, err)
	}
	_ = tmp.Close()
	return name, func() { _ = os.Remove(name) }, nil
}
