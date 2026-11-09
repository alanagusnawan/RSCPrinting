package render

import "strings"

// PaperSize mengembalikan dimensi kertas (dalam point, 1/72 inci) untuk nama kertas umum.
// Nilai fallback adalah A4 bila nama tidak dikenal.
func PaperSize(name string) (width, height float64) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "letter", "letterpaper", "na_letter":
		return 612, 792
	case "legal", "legalpaper":
		return 612, 1008
	case "a3":
		return 841.89, 1190.55
	case "a5":
		return 419.53, 595.28
	case "a6":
		return 297.64, 419.53
	case "b5":
		return 498.90, 708.66
	case "tabloid", "ledger", "11x17":
		return 792, 1224
	case "a4", "":
		return 595.28, 841.89
	}
	return 595.28, 841.89
}
