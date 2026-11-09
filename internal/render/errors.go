package render

import "errors"

var (
	// ErrUnsupportedFormat menandakan format dokumen tidak dikenal/didukung.
	ErrUnsupportedFormat = errors.New("unsupported document format")
	// ErrPrepareFailed menandakan kegagalan menyiapkan dokumen sebelum cetak.
	ErrPrepareFailed = errors.New("document preparation failed")
	// ErrOfficeConverterNotFound menandakan LibreOffice tidak ditemukan.
	ErrOfficeConverterNotFound = errors.New("office converter (libreoffice) not found")
	// ErrOfficeConvertFailed menandakan kegagalan konversi dokumen Office.
	ErrOfficeConvertFailed = errors.New("office document conversion failed")
	// ErrThermalRenderFailed menandakan kegagalan render ke ESC/POS raster thermal.
	ErrThermalRenderFailed = errors.New("thermal raster render failed")
)
