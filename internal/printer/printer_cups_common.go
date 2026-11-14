package printer

import (
	"fmt"
	"net/url"
	"strings"
)

// sanitizeCUPSURI memastikan path URL di-percent-encode agar valid di CUPS/lpadmin.
func sanitizeCUPSURI(rawURI string) string {
	parts := strings.SplitN(rawURI, "://", 2)
	if len(parts) != 2 {
		return rawURI
	}
	scheme, rest := parts[0], parts[1]

	// Pisahkan host[:port] dan path
	hostPath := strings.SplitN(rest, "/", 2)
	host := hostPath[0]
	if len(hostPath) == 1 {
		return scheme + "://" + host
	}

	pathParts := strings.Split(hostPath[1], "/")
	for i, segment := range pathParts {
		// unescape dulu bila sudah ada yang di-escape agar tidak double encode
		if unescaped, err := url.PathUnescape(segment); err == nil {
			segment = unescaped
		}
		pathParts[i] = url.PathEscape(segment)
	}

	return scheme + "://" + host + "/" + strings.Join(pathParts, "/")
}

// unixDeviceURI menormalkan PortOrURI menjadi device URI yang dipahami CUPS.
// Menerima: URL ipp/http(s), UNC Windows (\\host\share), path device, atau IP
// polos untuk printer jaringan (diubah ke socket://<ip>:9100).
func unixDeviceURI(params RegisterPrinterParams) string {
	uri := strings.TrimSpace(params.PortOrURI)
	if uri == "" {
		return uri
	}

	low := strings.ToLower(uri)

	// UNC Windows -> smb://
	if strings.HasPrefix(uri, `\\`) {
		return "smb://" + strings.ReplaceAll(strings.TrimPrefix(uri, `\\`), `\`, "/")
	}

	switch params.Type {
	case PrinterTypeHTTPShared:
		switch {
		case strings.HasPrefix(low, "http://"):
			return sanitizeCUPSURI("ipp://" + uri[len("http://"):])
		case strings.HasPrefix(low, "https://"):
			return sanitizeCUPSURI("ipps://" + uri[len("https://"):])
		default:
			if strings.Contains(uri, "://") {
				return sanitizeCUPSURI(uri)
			}
			// Dianggap host atau host/printers/name -> IPP.
			return sanitizeCUPSURI("ipp://" + strings.TrimPrefix(uri, "/"))
		}
	case PrinterTypeIP:
		if strings.Contains(uri, "://") {
			return sanitizeCUPSURI(uri)
		}
		return fmt.Sprintf("socket://%s:9100", uri)
	default:
		return sanitizeCUPSURI(uri)
	}
}

// cupsModelArgs menentukan argumen `-m` untuk lpadmin berdasarkan PPD/driver
// yang diberikan, atau driverless untuk URI IPP, atau raw untuk socket.
func cupsModelArgs(params RegisterPrinterParams) []string {
	if params.PPD != "" {
		return []string{"-m", params.PPD}
	}
	if params.DriverName != "" && !isGenericDriverName(params.DriverName) {
		return []string{"-m", params.DriverName}
	}
	if params.Driverless {
		return []string{"-m", "everywhere"}
	}
	uri := unixDeviceURI(params)
	if strings.HasPrefix(uri, "ipp://") || strings.HasPrefix(uri, "ipps://") {
		return []string{"-m", "everywhere"}
	}
	return []string{"-m", "raw"}
}

// isGenericDriverName mendeteksi nama driver Windows yang tidak valid sebagai
// model CUPS sehingga harus diabaikan (dibiarkan ke driverless/raw).
func isGenericDriverName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "generic / text only") ||
		strings.Contains(n, "microsoft ipp class") ||
		strings.Contains(n, "microsoft print to pdf")
}

// defaultShareName menghasilkan nama share default RSC-<name>.
func defaultShareName(name string) string {
	if name == "" {
		return "RSC-printer"
	}
	return "RSC-" + name
}

// smbPath membentuk UNC path untuk share.
func smbPath(host, share string) string {
	return fmt.Sprintf(`\\%s\%s`, host, share)
}

// ippURI membentuk URI IPP CUPS standar.
func ippURI(host string, port int, name string) string {
	if port <= 0 {
		port = 631
	}
	return fmt.Sprintf("ipp://%s:%d/printers/%s", host, port, url.PathEscape(name))
}

// socketURI membentuk URI RAW socket.
func socketURI(host string, port int) string {
	return fmt.Sprintf("socket://%s:%d", host, port)
}
