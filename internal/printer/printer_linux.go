//go:build linux

package printer

import (
	"context"
	"fmt"
)

// SetDefault menetapkan default printer sistem via lpadmin (butuh root/kapabilitas).
func (p *UnixPrinter) SetDefault(ctx context.Context, name string) error {
	if out, err := cupRun(ctx, "lpadmin", "-d", name); err != nil {
		return fmt.Errorf("set default printer failed: %s (%w)", out, err)
	}
	return nil
}

// platformShareHost mengembalikan nama host yang dipakai di endpoint share.
// Linux memakai mDNS (.local) melalui Avahi.
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
