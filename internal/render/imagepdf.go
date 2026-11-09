package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// ImageToPDF mengubah data gambar menjadi sebuah PDF satu halaman.
// Gambar akan diskalakan agar muat pada ukuran kertas yang diminta tanpa memotong.
func ImageToPDF(imgData []byte, paperW, paperH float64) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("decode image failed: %w", err)
	}

	// Ratakan ke latar putih (buang alpha).
	flattened := flattenToWhite(img)

	bounds := flattened.Bounds()
	iw := bounds.Dx()
	ih := bounds.Dy()
	if iw <= 0 || ih <= 0 {
		return nil, fmt.Errorf("image has zero dimensions")
	}

	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, flattened, &jpeg.Options{Quality: 92}); err != nil {
		return nil, fmt.Errorf("encode jpeg failed: %w", err)
	}

	// Hitung skala fit-to-page dengan margin kecil.
	const margin = 18.0 // 0.25 inci
	availW := paperW - 2*margin
	availH := paperH - 2*margin
	scale := availW / float64(iw)
	if s := availH / float64(ih); s < scale {
		scale = s
	}
	drawW := float64(iw) * scale
	drawH := float64(ih) * scale
	x := (paperW - drawW) / 2
	y := (paperH - drawH) / 2

	return buildImagePDF(jpegBuf.Bytes(), iw, ih, paperW, paperH, drawW, drawH, x, y), nil
}

func flattenToWhite(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}

// buildImagePDF menyusun byte PDF minimal dengan satu XObject JPEG (DCTDecode).
func buildImagePDF(jpegData []byte, iw, ih int, pageW, pageH, drawW, drawH, x, y float64) []byte {
	var buf bytes.Buffer
	offset := func() int { return buf.Len() }

	writeObj := func(id int, body []byte) int {
		off := offset()
		fmt.Fprintf(&buf, "%d 0 obj\n", id)
		buf.Write(body)
		buf.WriteString("\nendobj\n")
		return off
	}

	writeStream := func(id int, dict string, stream []byte) int {
		off := offset()
		fmt.Fprintf(&buf, "%d 0 obj\n<< %s /Length %d >>\nstream\n", id, dict, len(stream))
		buf.Write(stream)
		buf.WriteString("\nendstream\nendobj\n")
		return off
	}

	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	offsets := make([]int, 6)
	offsets[1] = writeObj(1, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	offsets[2] = writeObj(2, []byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"))

	pageBody := fmt.Sprintf(
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] "+
			"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>",
		pageW, pageH,
	)
	offsets[3] = writeObj(3, []byte(pageBody))

	content := fmt.Sprintf("q\n%.4f 0 0 %.4f %.4f %.4f cm\n/Im0 Do\nQ\n", drawW, drawH, x, y)
	offsets[4] = writeStream(4, "", []byte(content))

	imgDict := fmt.Sprintf(
		"/Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
		iw, ih,
	)
	offsets[5] = writeStream(5, imgDict, jpegData)

	xrefOff := offset()
	fmt.Fprintf(&buf, "xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)

	return buf.Bytes()
}
