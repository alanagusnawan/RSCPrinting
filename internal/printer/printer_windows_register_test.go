//go:build windows

package printer

import (
	"strings"
	"testing"
)

func TestParseRawEndpoint(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
		wantOK   bool
	}{
		{"socket://172.16.0.161:19101", "172.16.0.161", 19101, true},
		{"172.16.0.161:19101", "172.16.0.161", 19101, true},
		{"host:9100", "host", 9100, true},
		{"172.16.0.161", "", 0, false},
		{"http://172.16.0.161:19100/printers/x", "", 0, false},
		{`\\srv\share`, "", 0, false},
		{"", "", 0, false},
	}
	for _, c := range cases {
		host, port, ok := parseRawEndpoint(c.in)
		if ok != c.wantOK || (ok && (host != c.wantHost || port != c.wantPort)) {
			t.Errorf("parseRawEndpoint(%q) = (%q,%d,%v), want (%q,%d,%v)", c.in, host, port, ok, c.wantHost, c.wantPort, c.wantOK)
		}
	}
}

func TestLooksIPP(t *testing.T) {
	yes := []string{"http://a:631/printers/x", "ipp://a/printers/x", "https://a/ipp/print", "IPPS://a"}
	for _, s := range yes {
		if !looksIPP(s) {
			t.Errorf("looksIPP(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"socket://a:9100", `\\a\b`, "a:9100", ""} {
		if looksIPP(s) {
			t.Errorf("looksIPP(%q) = true, want false", s)
		}
	}
}

func TestRawPortName(t *testing.T) {
	if got := rawPortName("172.16.0.161", 19101); got != "RSC_172_16_0_161_19101" {
		t.Errorf("rawPortName = %q", got)
	}
}

func TestPSRawScript(t *testing.T) {
	script := psRawScript("EPSON_L3210", "EPSON L3210 Series", "172.16.0.161", 19101)
	for _, want := range []string{
		"$win7=",
		"prnport.vbs",
		"prnmngr.vbs",
		"Add-PrinterPort",
		"-PrinterHostAddress '172.16.0.161'",
		"-PortNumber 19101",
		"-Name 'EPSON_L3210'",
		"-DriverName 'EPSON L3210 Series'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("psRawScript missing %q\nscript:\n%s", want, script)
		}
	}
}

func TestPSConnectionScript(t *testing.T) {
	script := psConnectionScript(`\\SERVER\Share`)
	for _, want := range []string{
		"$win7=",
		"AddWindowsPrinterConnection",
		"Add-Printer",
		"-ConnectionName '\\\\SERVER\\Share'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("psConnectionScript missing %q\nscript:\n%s", want, script)
		}
	}
}
