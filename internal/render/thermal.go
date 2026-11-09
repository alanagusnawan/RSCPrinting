package render

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ThermalOptions mengatur render dokumen ke perintah ESC/POS raster (GS v 0).
// Jalur ini melewati driver printer OS sepenuhnya sehingga tidak terpengaruh
// driver class/v4 yang mengeluarkan EJL/ESC-P-R (penyebab output "kata aneh").
type ThermalOptions struct {
	GhostscriptPath string
	WidthMm         int           // lebar kertas fisik (58 atau 80); default 80
	DPI             int           // resolusi render; default 203
	Cut             bool          // auto-cut di akhir
	Trim            bool          // pangkas margin putih agar cetakan penuh lebar
	Feed            int           // feed (dot) sebelum cut; default 150
	Timeout         time.Duration // timeout render Ghostscript
}

const (
	thermalThreshold  = 160
	thermalDefaultDPI = 203
	// Feed default sebelum cut: cukup melewati posisi autocutter (~13-18mm).
	thermalDefaultFeed = 150
	// Batas baris per perintah GS v 0 agar tidak melebihi batas buffer/tinggi printer.
	thermalRasterChunkLines = 255
)

// PDFToESCPOS merender dokumen (PDF/PS/dll via Ghostscript) menjadi byte ESC/POS
// yang siap dikirim mentah ke printer thermal.
func PDFToESCPOS(ctx context.Context, src string, opts ThermalOptions) ([]byte, error) {
	if opts.GhostscriptPath == "" {
		return nil, fmt.Errorf("%w: ghostscript path not set", ErrThermalRenderFailed)
	}
	if src == "" {
		return nil, fmt.Errorf("%w: empty source", ErrThermalRenderFailed)
	}

	dpi := opts.DPI
	if dpi <= 0 {
		dpi = thermalDefaultDPI
	}
	widthDots := thermalWidthDots(opts.WidthMm, dpi)

	outDir, err := os.MkdirTemp("", "rsc-thermal-*")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrThermalRenderFailed, err)
	}
	defer func() { _ = os.RemoveAll(outDir) }()

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	args := []string{
		"-dBATCH",
		"-dNOPAUSE",
		"-dQUIET",
		"-sDEVICE=pnggray",
		fmt.Sprintf("-r%d", dpi),
		"-dTextAlphaBits=4",
		"-dGraphicsAlphaBits=4",
		"-o", filepath.Join(outDir, "page-%03d.png"),
		"-f", src,
	}

	cmd := exec.CommandContext(runCtx, opts.GhostscriptPath, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: ghostscript failed: %s (%v)", ErrThermalRenderFailed, strings.TrimSpace(out.String()), err)
	}

	pages, err := filepath.Glob(filepath.Join(outDir, "page-*.png"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrThermalRenderFailed, err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: no rendered pages", ErrThermalRenderFailed)
	}
	sort.Strings(pages)

	var escpos []byte
	escpos = append(escpos, 0x1B, 0x40) // ESC @ initialize
	for _, page := range pages {
		gray, err := loadGrayPNG(page)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrThermalRenderFailed, err)
		}
		if opts.Trim {
			gray = cropGray(gray, thermalThreshold)
		}
		scaled := fitWidthGray(gray, widthDots)
		// Pecah raster jadi band vertikal agar tidak melebihi batas printer.
		escpos = append(escpos, packRasterChunks(scaled, thermalThreshold, thermalRasterChunkLines)...)
		escpos = append(escpos, 0x1B, 0x64, 0x02) // ESC d 2 (feed 2 lines)
	}
	if opts.Cut {
		feed := opts.Feed
		if feed <= 0 {
			feed = thermalDefaultFeed
		}
		if feed > 255 {
			feed = 255
		}
		// GS V 'A' n: feed n dot (n × 0.125mm) ke posisi cutter, lalu full cut.
		// Ini mencegah pisau memotong konten yang belum melewati cutter.
		escpos = append(escpos, 0x1D, 0x56, 0x41, byte(feed))
	}
	return escpos, nil
}

// thermalWidthDots menghitung lebar cetak (dot) berdasarkan lebar kertas & DPI,
// dibulatkan ke bawah ke kelipatan 8 (syarat GS v 0).
func thermalWidthDots(widthMm, dpi int) int {
	if dpi <= 0 {
		dpi = thermalDefaultDPI
	}
	// Lebar cetak standar ESC/POS: 58mm = 384 dot, 80mm = 576 dot pada 203dpi.
	base := 576
	if widthMm > 0 && widthMm <= 58 {
		base = 384
	}
	dots := int(float64(dpi) / float64(thermalDefaultDPI) * float64(base))
	dots = dots / 8 * 8
	if dots < 8 {
		dots = 8
	}
	return dots
}

func loadGrayPNG(path string) (*image.Gray, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(g, g.Bounds(), img, b.Min, draw.Src)
	return g, nil
}

// cropGray memangkas border putih (margin/padding) di sekeliling konten.
// Piksel dianggap konten bila nilainya < threshold. Bila halaman kosong,
// gambar asli dikembalikan.
func cropGray(src *image.Gray, threshold uint8) *image.Gray {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}

	minX, minY := w, h
	maxX, maxY := -1, -1
	for y := 0; y < h; y++ {
		row := y * src.Stride
		for x := 0; x < w; x++ {
			if src.Pix[row+x] < threshold {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 || maxY < 0 {
		return src // halaman kosong
	}

	cw := maxX - minX + 1
	ch := maxY - minY + 1
	if cw == w && ch == h && minX == 0 && minY == 0 {
		return src
	}

	dst := image.NewGray(image.Rect(0, 0, cw, ch))
	for y := 0; y < ch; y++ {
		srcRow := (minY+y)*src.Stride + minX
		dstRow := y * dst.Stride
		copy(dst.Pix[dstRow:dstRow+cw], src.Pix[srcRow:srcRow+cw])
	}
	return dst
}

// fitWidthGray menskalakan gambar agar lebarnya tepat dstW (menjaga rasio).
// Pengecilan memakai area-average; pembesaran memakai nearest-neighbor.
func fitWidthGray(src *image.Gray, dstW int) *image.Gray {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return image.NewGray(image.Rect(0, 0, 0, 0))
	}
	if dstW <= 0 || dstW == sw {
		return src
	}

	scale := float64(sw) / float64(dstW)
	dstH := int(float64(sh) / scale)
	if dstH < 1 {
		dstH = 1
	}
	dst := image.NewGray(image.Rect(0, 0, dstW, dstH))

	if dstW < sw {
		// Area-average (pengecilan).
		for dy := 0; dy < dstH; dy++ {
			sy0 := int(float64(dy) * scale)
			sy1 := int(float64(dy+1) * scale)
			if sy1 <= sy0 {
				sy1 = sy0 + 1
			}
			if sy1 > sh {
				sy1 = sh
			}
			for dx := 0; dx < dstW; dx++ {
				sx0 := int(float64(dx) * scale)
				sx1 := int(float64(dx+1) * scale)
				if sx1 <= sx0 {
					sx1 = sx0 + 1
				}
				if sx1 > sw {
					sx1 = sw
				}
				sum, n := 0, 0
				for yy := sy0; yy < sy1; yy++ {
					row := yy * src.Stride
					for xx := sx0; xx < sx1; xx++ {
						sum += int(src.Pix[row+xx])
						n++
					}
				}
				if n == 0 {
					n = 1
				}
				dst.SetGray(dx, dy, color.Gray{Y: uint8(sum / n)})
			}
		}
		return dst
	}

	// Nearest-neighbor (pembesaran).
	for dy := 0; dy < dstH; dy++ {
		sy := int(float64(dy) * scale)
		if sy >= sh {
			sy = sh - 1
		}
		for dx := 0; dx < dstW; dx++ {
			sx := int(float64(dx) * scale)
			if sx >= sw {
				sx = sw - 1
			}
			dst.SetGray(dx, dy, src.GrayAt(sb.Min.X+sx, sb.Min.Y+sy))
		}
	}
	return dst
}

// packGSv0 memaketkan seluruh gambar menjadi perintah ESC/POS "GS v 0"
// (raster bit image), bit 1 = titik hitam, MSB lebih dulu.
func packGSv0(g *image.Gray, threshold uint8) []byte {
	return packGSv0Band(g, 0, g.Bounds().Dy(), threshold)
}

// packGSv0Band memaketkan baris [y0,y1) (relatif ke Bounds()) sebagai satu
// perintah GS v 0.
func packGSv0Band(g *image.Gray, y0, y1 int, threshold uint8) []byte {
	b := g.Bounds()
	w := b.Dx()
	h := y1 - y0
	if w <= 0 || h <= 0 {
		return nil
	}
	bytesPerRow := (w + 7) / 8

	out := make([]byte, 0, 8+bytesPerRow*h)
	out = append(out,
		0x1D, 0x76, 0x30, 0x00,
		byte(bytesPerRow&0xFF), byte((bytesPerRow>>8)&0xFF),
		byte(h&0xFF), byte((h>>8)&0xFF),
	)

	row := make([]byte, bytesPerRow)
	for y := y0; y < y1; y++ {
		for i := range row {
			row[i] = 0
		}
		for x := 0; x < w; x++ {
			if g.GrayAt(b.Min.X+x, b.Min.Y+y).Y < threshold {
				row[x/8] |= 0x80 >> uint(x%8)
			}
		}
		out = append(out, row...)
	}
	return out
}

// packRasterChunks memecah gambar menjadi band vertikal (masing-masing maksimum
// maxLines baris) lalu memaketkan setiap band sebagai perintah GS v 0 terpisah.
// Band-band menyambung tanpa celah.
func packRasterChunks(g *image.Gray, threshold uint8, maxLines int) []byte {
	h := g.Bounds().Dy()
	if h <= 0 {
		return nil
	}
	if maxLines <= 0 || maxLines > h {
		maxLines = h
	}
	var out []byte
	for y := 0; y < h; y += maxLines {
		end := y + maxLines
		if end > h {
			end = h
		}
		out = append(out, packGSv0Band(g, y, end, threshold)...)
	}
	return out
}
