package printer

import (
	"net"
	"os"
)

// LocalHostname mengembalikan nama host mesin (tanpa domain).
func LocalHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	// Buang domain bila ada (mis. host.domain.local -> host).
	if i := indexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}

// LocalIPs mengembalikan daftar alamat IPv4 privat/host yang aktif (non-loopback).
func LocalIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			ips = append(ips, ip4.String())
		}
	}
	return ips
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
