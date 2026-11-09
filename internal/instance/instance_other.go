//go:build !windows

package instance

// acquire: non-Windows tidak menerapkan single-instance (target utama Windows
// service + registry). Mengembalikan lock no-op.
func acquire(_ string) (func(), bool, error) {
	return func() {}, false, nil
}
