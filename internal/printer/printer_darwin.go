//go:build darwin

package printer

import (
	"context"
	"fmt"
)

// SetDefault menetapkan default printer. macOS: coba default sistem (lpadmin -d),
// lalu fallback ke default per-user (lpoptions -d).
func (p *UnixPrinter) SetDefault(ctx context.Context, name string) error {
	if out, err := cupRun(ctx, "lpadmin", "-d", name); err == nil {
		_ = out
		return nil
	}
	if out, err := cupRun(ctx, "lpoptions", "-d", name); err != nil {
		return fmt.Errorf("set default printer failed: %s (%w)", out, err)
	}
	return nil
}

// platformShareHost mengembalikan nama host mDNS (.local) untuk endpoint share.
func platformShareHost() string {
	host := LocalHostname()
	if host == "" || host == "localhost" {
		if ips := LocalIPs(); len(ips) > 0 {
			return ips[0]
		}
		return host
	}
	return host + ".local"
}
