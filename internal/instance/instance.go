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
