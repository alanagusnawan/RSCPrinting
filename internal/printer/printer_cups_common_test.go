package printer

import "testing"

func TestUnixDeviceURI(t *testing.T) {
	cases := []struct {
		name string
		p    RegisterPrinterParams
		want string
	}{
		{"http->ipp", RegisterPrinterParams{Type: PrinterTypeHTTPShared, PortOrURI: "http://a:631/printers/x"}, "ipp://a:631/printers/x"},
		{"http->ipp with space", RegisterPrinterParams{Type: PrinterTypeHTTPShared, PortOrURI: "http://DESKTOP-T0CGKSE:19100/printers/EPSON TM-T82X Receipt6"}, "ipp://DESKTOP-T0CGKSE:19100/printers/EPSON%20TM-T82X%20Receipt6"},
		{"https->ipps", RegisterPrinterParams{Type: PrinterTypeHTTPShared, PortOrURI: "https://a/printers/x"}, "ipps://a/printers/x"},
		{"unc->smb", RegisterPrinterParams{Type: PrinterTypeHTTPShared, PortOrURI: `\\host\RSC-x`}, "smb://host/RSC-x"},
		{"ipp stays", RegisterPrinterParams{Type: PrinterTypeHTTPShared, PortOrURI: "ipp://a/printers/x"}, "ipp://a/printers/x"},
		{"ip->socket", RegisterPrinterParams{Type: PrinterTypeIP, PortOrURI: "192.168.1.5"}, "socket://192.168.1.5:9100"},
		{"ip host:port", RegisterPrinterParams{Type: PrinterTypeIP, PortOrURI: "172.16.0.161:19101"}, "socket://172.16.0.161:19101"},
		{"socket passthrough", RegisterPrinterParams{Type: PrinterTypeIP, PortOrURI: "socket://h:1"}, "socket://h:1"},
		{"usb dev", RegisterPrinterParams{Type: PrinterTypeUSB, PortOrURI: "/dev/usb/lp0"}, "/dev/usb/lp0"},
	}
	for _, c := range cases {
		if got := unixDeviceURI(c.p); got != c.want {
			t.Errorf("%s: unixDeviceURI = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCupsModelArgs(t *testing.T) {
	cases := []struct {
		name string
		p    RegisterPrinterParams
		want string
	}{
		{"ppd", RegisterPrinterParams{PPD: "drv.ppd", Type: PrinterTypeIP, PortOrURI: "1.2.3.4"}, "drv.ppd"},
		{"driverless flag", RegisterPrinterParams{Driverless: true}, "everywhere"},
		{"ipp auto", RegisterPrinterParams{Type: PrinterTypeHTTPShared, PortOrURI: "http://a/printers/x"}, "everywhere"},
		{"generic ignored -> raw", RegisterPrinterParams{DriverName: "Generic / Text Only", Type: PrinterTypeIP, PortOrURI: "1.2.3.4"}, "raw"},
		{"custom driver", RegisterPrinterParams{DriverName: "my.ppd", Type: PrinterTypeIP, PortOrURI: "1.2.3.4"}, "my.ppd"},
	}
	for _, c := range cases {
		args := cupsModelArgs(c.p)
		if len(args) != 2 || args[0] != "-m" || args[1] != c.want {
			t.Errorf("%s: cupsModelArgs = %v, want [-m %s]", c.name, args, c.want)
		}
	}
}

func TestShareHelpers(t *testing.T) {
	if got := defaultShareName("Thermal"); got != "RSC-Thermal" {
		t.Errorf("defaultShareName = %q", got)
	}
	if got := smbPath("host", "RSC-x"); got != `\\host\RSC-x` {
		t.Errorf("smbPath = %q", got)
	}
	if got := ippURI("host", 631, "Thermal Receipt"); got != "ipp://host:631/printers/Thermal%20Receipt" {
		t.Errorf("ippURI = %q", got)
	}
	if got := ippURI("host", 0, "Thermal"); got != "ipp://host:631/printers/Thermal" {
		t.Errorf("ippURI default port = %q", got)
	}
	if got := socketURI("host", 19100); got != "socket://host:19100" {
		t.Errorf("socketURI = %q", got)
	}
}
