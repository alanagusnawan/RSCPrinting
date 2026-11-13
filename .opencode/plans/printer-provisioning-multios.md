# Rencana: Auto-provision printer OS lintas-OS dari dashboard server

## Tujuan
Server (dashboard eksternal, `ws://172.16.0.34:5488/agent/ws`) mengirim daftar printer
per device. Agent membuat **queue printer OS sungguhan** (Windows/Linux/macOS) sehingga
printer muncul di dialog print Word/aplikasi apa pun, dan cetak dari aplikasi langsung
lewat spooler OS. Alur utamanya: **otomatis dari server saat connect**.

## Keputusan yang sudah disepakati
- Sumber perintah: dashboard server eksternal via WebSocket (sisi server di luar repo).
- Jenis printer: network/IP, USB lokal, dan shared/queue server.
- Prioritas OS: Windows + Linux + macOS sekaligus.
- Strategi driver Unix: driverless (`-m everywhere`) default, fallback PPD.
- Model: otomatis dari server (bulk `sync_printers`), plus `register_printer` per-item.
- Method hapus dikonsolidasi menjadi `Remove`.
- Mulai eksekusi disetujui.

## Prasyarat privilege (didokumentasikan)
- Windows: service LocalSystem (sudah di `install.ps1`).
- Linux: systemd root / user di grup `lpadmin`.
- macOS: LaunchDaemon root (bukan LaunchAgent) agar `lpadmin` berhasil.

## Kontrak pesan
Pesan baru di `pkg/apitypes/protocol.go`:
- `sync_printers` (server -> agent), `sync_printers_result` (agent -> server).
- `PrinterSpec`: `name, driverName, printerType, portOrUri, ppd, driverless, isDefault, enabled, accept`.
- `SyncPrintersMessage`: `type, requestId, printers[], prune`.
- `SyncPrintersResultMessage`: `type, requestId, success, installed[], updated[], removed[], failed{name:err}, error`.
- `RegisterPrinterMessage` ditambah field `ppd`, `driverless` (opsional, backward compatible).

## Perubahan per file

### 1. `internal/printer/printer.go`
- Hapus `Delete` dari interface `Printer`; sisakan `Remove`.
- Tambah `PPD string` dan `Driverless bool` pada `RegisterPrinterParams`.

### 2. `internal/printer/printer_mock.go`
- Hapus method `Delete` (sisakan `Remove`).

### 3. `internal/printer/printer_windows.go`
- Hapus method `Delete`; pindahkan logikanya ke `Remove` (Remove-Printer + fallback wmic
  + `invalidateEnum`).
- `Register` idempotent: jika printer sudah ada -> `Update`; jika belum -> `Add-Printer`.
- Auto-pilih driver bila kosong: IPP shared pakai `Add-Printer -ConnectionName`;
  IP raw/USB pakai `Generic / Text Only` sebagai default.
- `SetDefault`/`Update` konsisten + error jelas (sertakan daftar driver tersedia).

### 4. `internal/printer/printer_unix.go` -> pecah
- `printer_cups.go` (`//go:build linux || darwin`): struct `UnixPrinter`, `New`, `Print`,
  `printRaw`, `printRendered`, `List`, `Info`, `Options`, `Update`, `Remove`, dan helper
  (cari binary di PATH **dan** `/usr/sbin`; `listUnixDrivers`, `listUnixPorts`).
- `printer_linux.go` (`//go:build linux`): `Register` + `SetDefault`
  (`lpadmin -d`; `cupsenable` + `cupsaccept`).
- `printer_darwin.go` (`//go:build darwin`): `Register` + `SetDefault`
  (`lpoptions -d` per-user; kuirk macOS).
- Hapus duplikasi `SetDefault` (baris 200 & 253) dan `Remove`/`Delete`.
- Logika URI & driver di helper murni (tanpa build tag) agar bisa diuji di Windows:
  - `IP` -> `socket://host:9100`
  - `HTTP_SHARED` -> `ipp://host/...`
  - driver: `-m <ppd>` bila `PPD`/`DriverName` diisi; jika tidak dan URI IPP -> `-m everywhere`;
    jika raw -> `-m raw`.

### 5. `pkg/apitypes/protocol.go`
- Tambah `TypeSyncPrinters`, `TypeSyncPrintersResult`, `PrinterSpec`,
  `SyncPrintersMessage`, `SyncPrintersResultMessage`.
- Tambah `PPD`, `Driverless` pada `RegisterPrinterMessage`.

### 6. `internal/wsclient/client.go`
- `handleRegisterPrinter`: context berbatas waktu, idempotent, balas dengan `requestId`,
  lalu push `printers_update`.
- `handleSyncPrinters`: install yang belum ada, update yang berubah, opsional hapus
  (`prune`), balas ringkasan.
- Tambah `case apitypes.TypeSyncPrinters` di read loop.

### 7. `internal/httpserver/server.go` (opsional)
- `POST /printers/sync` untuk uji lokal/bulk.

### 8. Docs `README.md`
- Protokol `sync_printers` + tabel mapping install per OS + prasyarat privilege.

## Tabel mapping install
| Tipe | Windows | Linux (CUPS) | macOS (CUPS) |
|---|---|---|---|
| HTTP_SHARED (IPP) | `Add-Printer -ConnectionName http://...` | `lpadmin -p NAME -v ipp://... -m everywhere -E` | sama; default `lpoptions -d` |
| IP (raw 9100) | port `IP_<ip>` + driver | `lpadmin -p NAME -v socket://ip:9100 -E` + enable/accept | `lpadmin -p NAME -v socket://ip:9100 -E` |
| USB lokal | pakai port yang ada | `lpadmin -p NAME -v <device-uri> -E` | sama |
| Shared/queue | `-ConnectionName` | `ipp://`/`smb://` | sama |

## Verifikasi
- Cross-compile: `GOOS=windows/linux/darwin go build ./...` (wajib lolos ketiganya).
- `go test ./...` + unit test helper murni (URI/driver args) yang jalan di Windows.
- Manual: kirim `sync_printers`/`register_printer` -> cek muncul di Word / `lpstat -p` -> cetak.

## Risiko
- Nama/format `sync_printers` harus disepakati dengan tim server.
- Driver USB/thermal Windows tetap butuh driver terpasang (APN/PnP); auto-install di luar scope.
- Default printer GUI macOS tidak sepenuhnya diatur CLI (set default CUPS + `lpoptions` per-user).
