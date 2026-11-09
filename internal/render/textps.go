package render

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	textFontSize   = 10.0
	textLineHeight = 12.0
	textMargin     = 40.0
	courierCharW   = 0.6 // lebar karakter Courier = 0.6 * font size
)

// TextToPS mengubah teks biasa menjadi dokumen PostScript satu/lebih halaman.
func TextToPS(textData []byte, paperW, paperH float64) []byte {
	text := strings.ReplaceAll(string(textData), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	rawLines := strings.Split(text, "\n")

	maxChars := int((paperW - 2*textMargin) / (courierCharW * textFontSize))
	if maxChars < 10 {
		maxChars = 10
	}
	linesPerPage := int((paperH - 2*textMargin) / textLineHeight)
	if linesPerPage < 1 {
		linesPerPage = 1
	}

	var wrapped []string
	for _, line := range rawLines {
		wrapped = append(wrapped, wrapLine(line, maxChars)...)
	}
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}

	var buf bytes.Buffer
	buf.WriteString("%!PS-Adobe-3.0\n")
	fmt.Fprintf(&buf, "%%%%BoundingBox: 0 0 %.0f %.0f\n", paperW, paperH)
	fmt.Fprintf(&buf, "/FS %g def\n", textFontSize)
	buf.WriteString("/Courier findfont FS scalefont setfont\n")

	pageStart := -1
	startTop := paperH - textMargin
	for i, line := range wrapped {
		if i%linesPerPage == 0 {
			if pageStart >= 0 {
				buf.WriteString("showpage\n")
			}
			pageStart = i
			pageNum := i/linesPerPage + 1
			fmt.Fprintf(&buf, "%%%%Page: %d %d\n", pageNum, pageNum)
		}
		y := startTop - float64(i%linesPerPage)*textLineHeight
		fmt.Fprintf(&buf, "%.1f %.1f moveto (%s) show\n", textMargin, y, escapePS(line))
	}
	buf.WriteString("showpage\n")
	buf.WriteString("%%EOF\n")

	return buf.Bytes()
}

func wrapLine(line string, maxChars int) []string {
	if len(line) <= maxChars {
		return []string{line}
	}
	var out []string
	for len(line) > maxChars {
		out = append(out, line[:maxChars])
		line = line[maxChars:]
	}
	out = append(out, line)
	return out
}

func escapePS(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '(', ')':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			if c < 0x20 || c > 0x7E {
				b.WriteByte(' ')
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}
