package printer

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// resolveResolvableHost mencoba mengecek apakah host bisa di-resolve.
// Bila host tanpa domain gagal (khas Windows NetBIOS di macOS/Linux),
// dicoba fallback ke host + ".local" (mDNS).
func resolveResolvableHost(hostPort string) string {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		port = ""
	}

	// Jangan ubah jika sudah berupa IP atau sudah berakhiran .local
	if net.ParseIP(host) != nil || strings.HasSuffix(strings.ToLower(host), ".local") {
		return hostPort
	}

	// Jangan lookup bila host adalah dummy/test (misal single letter)
	if len(host) <= 1 {
		return hostPort
	}

	checkHost := func(h string) bool {
		addrs, lerr := net.LookupHost(h)
		return lerr == nil && len(addrs) > 0
	}

	if checkHost(host) {
		return hostPort
	}

	// Coba mDNS (.local)
	localCandidate := host + ".local"
	if checkHost(localCandidate) {
		if port != "" {
			return net.JoinHostPort(localCandidate, port)
		}
		return localCandidate
	}

	return hostPort
}

// sanitizeCUPSURI memastikan host bisa di-resolve dan path URL di-percent-encode agar valid di CUPS/lpadmin.
func sanitizeCUPSURI(rawURI string) string {
	parts := strings.SplitN(rawURI, "://", 2)
	if len(parts) != 2 {
		return rawURI
	}
	scheme, rest := parts[0], parts[1]

	// Pisahkan host[:port] dan path
	hostPath := strings.SplitN(rest, "/", 2)
	host := resolveResolvableHost(hostPath[0])
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
		// host:port eksplisit (mis. relay) -> socket://host:port
		if strings.Contains(uri, ":") {
			return "socket://" + uri
		}
		return fmt.Sprintf("socket://%s:9100", uri)
	default:
		return sanitizeCUPSURI(uri)
	}
}

// isHostPortConnectable menguji apakah endpoint remote IPP aktif (timeout cepat 1.5s).
func isHostPortConnectable(rawURI string) bool {
	parts := strings.SplitN(rawURI, "://", 2)
	if len(parts) != 2 {
		return false
	}
	hostPort := strings.SplitN(parts[1], "/", 2)[0]
	if !strings.Contains(hostPort, ":") {
		hostPort = net.JoinHostPort(hostPort, "631")
	}
	conn, err := net.DialTimeout("tcp", hostPort, 1500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// cupsFallbackPPD mencari model PPD CUPS yang sesuai dengan nama printer atau fallback ke generic.ppd.
func cupsFallbackPPD(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "tm-t82") || strings.Contains(n, "t82"):
		return "Library/Printers/PPDs/Contents/Resources/EPSON TM-T82-SA.gz"
	case strings.Contains(n, "epson"):
		return "drv:///sample.drv/epson24.ppd"
	default:
		return "drv:///sample.drv/generic.ppd"
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
	uri := unixDeviceURI(params)
	if strings.HasPrefix(uri, "ipp://") || strings.HasPrefix(uri, "ipps://") {
		// Jika remote host responsif (probe cepat 1.5s), pakai driverless everywhere
		if isHostPortConnectable(uri) {
			return []string{"-m", "everywhere"}
		}
		// Di macOS, lpadmin tanpa flag -m atau dengan -m raw akan ditolak/tidak terdaftar di Pengaturan.
		// Gunakan PPD fallback agar queue resmi terpasang di CUPS & System Settings.
		if ppd := cupsFallbackPPD(params.Name); ppd != "" {
			return []string{"-m", ppd}
		}
		return []string{"-m", "drv:///sample.drv/generic.ppd"}
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
