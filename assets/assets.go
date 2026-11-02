package assets

import (
	_ "embed"
)

// Minimal valid PNG/ICO 16x16 transparent bytes
var (
	//go:embed icon.png
	IconPNG []byte

	//go:embed icon.ico
	IconICO []byte
)
