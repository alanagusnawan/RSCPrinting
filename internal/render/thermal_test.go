package render

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestThermalWidthDots(t *testing.T) {
	cases := []struct {
		widthMm, dpi, want int
	}{
		{80, 203, 576},
		{58, 203, 384},
		{0, 203, 576},
		{80, 0, 576},
		{58, 0, 384},
		{80, 406, 1152},
	}
	for _, c := range cases {
		if got := thermalWidthDots(c.widthMm, c.dpi); got != c.want {
			t.Errorf("thermalWidthDots(%d,%d) = %d, want %d", c.widthMm, c.dpi, got, c.want)
		}
	}
}

func TestPackGSv0(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 8, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	img.SetGray(0, 0, color.Gray{Y: 0})
	img.SetGray(1, 0, color.Gray{Y: 0})

	got := packGSv0(img, 160)
	want := []byte{
		0x1D, 0x76, 0x30, 0x00,
		0x01, 0x00, 0x02, 0x00,
		0xC0, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("packGSv0 = % x, want % x", got, want)
	}
}

func TestFitWidthGray(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 16, 8))
	for i := range src.Pix {
		src.Pix[i] = 200
	}
	dst := fitWidthGray(src, 8)
	b := dst.Bounds()
	if b.Dx() != 8 {
		t.Fatalf("width = %d, want 8", b.Dx())
	}
	if b.Dy() != 4 {
		t.Fatalf("height = %d, want 4", b.Dy())
	}

	up := fitWidthGray(src, 32)
	ub := up.Bounds()
	if ub.Dx() != 32 {
		t.Fatalf("upscale width = %d, want 32", ub.Dx())
	}
	if ub.Dy() != 16 {
		t.Fatalf("upscale height = %d, want 16", ub.Dy())
	}
}

func TestPackRasterChunks(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 8, 600))
	for i := range img.Pix {
		img.Pix[i] = 255
	}

	out := packRasterChunks(img, 160, 255)

	var heights []int
	for i := 0; i+7 < len(out); i++ {
		if out[i] == 0x1D && out[i+1] == 0x76 && out[i+2] == 0x30 && out[i+3] == 0x00 {
			h := int(out[i+6]) + int(out[i+7])*256
			heights = append(heights, h)
		}
	}
	if len(heights) != 3 {
		t.Fatalf("expected 3 chunks, got %d (%v)", len(heights), heights)
	}
	if heights[0] != 255 || heights[1] != 255 || heights[2] != 90 {
		t.Fatalf("chunk heights = %v, want [255 255 90]", heights)
	}
}

func TestCropGray(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 20, 20))
	for i := range src.Pix {
		src.Pix[i] = 255 // putih
	}
	// blok hitam 4x3 di (5,6)-(8,8)
	for y := 6; y <= 8; y++ {
		for x := 5; x <= 8; x++ {
			src.SetGray(x, y, color.Gray{Y: 0})
		}
	}

	cropped := cropGray(src, 160)
	b := cropped.Bounds()
	if b.Dx() != 4 || b.Dy() != 3 {
		t.Fatalf("cropped size = %dx%d, want 4x3", b.Dx(), b.Dy())
	}
}

func TestCropGrayBlankPageUnchanged(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 10, 10))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	cropped := cropGray(src, 160)
	if cropped.Bounds().Dx() != 10 || cropped.Bounds().Dy() != 10 {
		t.Fatalf("blank page should be unchanged, got %dx%d", cropped.Bounds().Dx(), cropped.Bounds().Dy())
	}
}

func TestPrepareThermalRequiresGhostscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := Prepare(context.Background(), Input{FilePath: path}, Options{
		Thermal: &ThermalOptions{},
	})
	if err == nil {
		t.Fatal("expected error without ghostscript path")
	}
	if !errors.Is(err, ErrThermalRenderFailed) {
		t.Fatalf("error = %v, want ErrThermalRenderFailed", err)
	}
}
