//go:build windows

package printer

import (
	"context"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winspool             = windows.NewLazySystemDLL("winspool.drv")
	procOpenPrinter      = winspool.NewProc("OpenPrinterW")
	procClosePrinter     = winspool.NewProc("ClosePrinter")
	procStartDocPrinter  = winspool.NewProc("StartDocPrinterW")
	procEndDocPrinter    = winspool.NewProc("EndDocPrinter")
	procStartPagePrinter = winspool.NewProc("StartPagePrinter")
	procEndPagePrinter   = winspool.NewProc("EndPagePrinter")
	procWritePrinter     = winspool.NewProc("WritePrinter")
)

type docInfo1 struct {
	pDocName    *uint16
	pOutputFile *uint16
	pDatatype   *uint16
}

// sendRaw mengirim byte mentah ke printer melalui spooler Windows (datatype RAW),
// melewati rendering driver. Cocok untuk printer thermal (ESC/POS) dan label (ZPL).
func (p *WindowsPrinter) sendRaw(ctx context.Context, printerName string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	// Pastikan loop Win32 call terkunci ke satu OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	namePtr, err := windows.UTF16PtrFromString(printerName)
	if err != nil {
		return fmt.Errorf("%w: invalid printer name: %v", ErrRawSendFailed, err)
	}
	datatypePtr, _ := windows.UTF16PtrFromString("RAW")
	docNamePtr, _ := windows.UTF16PtrFromString("RSCPrinting Raw Job")

	var hPrinter windows.Handle
	r1, _, e := procOpenPrinter.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(&hPrinter)),
		0,
	)
	if r1 == 0 {
		return fmt.Errorf("%w: OpenPrinter failed: %v", ErrRawSendFailed, e)
	}
	defer procClosePrinter.Call(uintptr(hPrinter))

	di := docInfo1{
		pDocName:  docNamePtr,
		pDatatype: datatypePtr,
	}
	jobID, _, e := procStartDocPrinter.Call(
		uintptr(hPrinter),
		1, // level 1 => DOC_INFO_1
		uintptr(unsafe.Pointer(&di)),
	)
	if jobID == 0 {
		return fmt.Errorf("%w: StartDocPrinter failed: %v", ErrRawSendFailed, e)
	}

	if r1, _, e = procStartPagePrinter.Call(uintptr(hPrinter)); r1 == 0 {
		procEndDocPrinter.Call(uintptr(hPrinter))
		return fmt.Errorf("%w: StartPagePrinter failed: %v", ErrRawSendFailed, e)
	}

	var written uint32
	r1, _, e = procWritePrinter.Call(
		uintptr(hPrinter),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&written)),
	)
	procEndPagePrinter.Call(uintptr(hPrinter))
	procEndDocPrinter.Call(uintptr(hPrinter))

	if r1 == 0 {
		return fmt.Errorf("%w: WritePrinter failed: %v", ErrRawSendFailed, e)
	}
	if int(written) != len(data) {
		return fmt.Errorf("%w: partial write %d/%d bytes", ErrRawSendFailed, written, len(data))
	}

	return nil
}
