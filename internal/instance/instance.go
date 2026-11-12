package instance

// Acquire mencoba mengambil lock single-instance bernama name.
//
// Mengembalikan:
//   - release: fungsi untuk melepas lock (non-nil saat err == nil)
//   - already: true bila instance lain sudah memegang lock
//   - err: error teknis (mis. lock tak tersedia); pemanggil dapat lanjut tanpa lock
func Acquire(name string) (release func(), already bool, err error) {
	return acquire(name)
}

// AcquireFile mengambil lock exclusive pada file path (dibuat bila belum ada).
// Cocok untuk single-instance per-data-dir: lintas sesi & tidak bergantung ACL
// named mutex. Mengembalikan already=true bila file sedang dikunci proses lain.
func AcquireFile(path string) (release func(), already bool, err error) {
	return acquireFile(path)
}
