# RSCPrinting — Go Print Agent

Print Agent berbasis **Go** (ringan, static binary, `CGO_ENABLED=0`) yang menghubungkan aplikasi cloud/POS ke printer lokal via **koneksi WebSocket keluar** (client-only).

Mendukung **Windows (Windows 7 SP1 s/d Windows 11)**, **macOS**, dan **Linux**.

---

## Daftar Isi
1. [Cara Pasang Cepat](#cara-pasang-cepat)
   - [Windows (7 SP1 / 8 / 10 / 11)](#1-windows-7-sp1--8--10--11)
   - [macOS (Apple Silicon & Intel)](#2-macos-apple-silicon--intel)
   - [Linux (Ubuntu / Debian / CentOS)](#3-linux-ubuntu--debian--centos)
2. [Konfigurasi (`config.yaml`)](#konfigurasi-configyaml)
3. [Cetak Thermal / Struk Kasir (ESC/POS)](#cetak-thermal--struk-kasir-escpos)
4. [Format Dokumen yang Didukung](#format-dokumen-yang-didukung)
5. [Manajemen Service & Autorun](#manajemen-service--autorun)
6. [Perintah CLI](#perintah-cli)
7. [Local REST API](#local-rest-api)
8. [Troubleshooting](#troubleshooting)
9. [Build Binary dari Source](#build-binary-dari-source)

---

## Cara Pasang Cepat

### 1. Windows (7 SP1 / 8 / 10 / 11)

#### Syarat Awal:
- Pasang **Ghostscript** (untuk cetak dokumen/thermal): [Download Ghostscript Windows](https://www.ghostscript.com/download/gsdnld.html) (pilih 64-bit atau 32-bit).

#### Langkah Pemasangan:
Buka **PowerShell** sebagai **Administrator**, lalu jalankan:

```powershell
# 1. Download / clone dan masuk ke folder RSCPrinting
cd RSCPrinting

# 2. Pasang driver, buat antrian printer, dan aktifkan service otomatis
powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1 -TestPrint
```

> **Selesai!** Agent otomatis berjalan di background sebagai Windows Service dan siap menerima job cetak.

---

### 2. macOS (Apple Silicon & Intel)

#### Syarat Awal:
- Pasang **Ghostscript** via Terminal:
  ```bash
  brew install ghostscript
  ```

#### Langkah Pemasangan:
Buka **Terminal**, lalu jalankan:

```bash
# 1. Masuk ke folder RSCPrinting
cd RSCPrinting

# 2. Beri izin eksekusi pada binary sesuai arsitektur mesin
chmod +x dist/print-agent-darwin-*

# 3. Lepas blokir Gatekeeper macOS
xattr -cr dist/print-agent-darwin-*

# 4. Pasang dan jalankan agent sebagai service latar belakang
./dist/print-agent-darwin-$(uname -m | sed 's/x86_64/amd64/') service install --config configs/config.yaml
./dist/print-agent-darwin-$(uname -m | sed 's/x86_64/amd64/') service start
```

> **Selesai!** Agent berjalan otomatis di background via macOS `launchd`.

---

### 3. Linux (Ubuntu / Debian / CentOS)

#### Syarat Awal:
```bash
# Pasang CUPS dan Ghostscript
sudo apt update && sudo apt install -y cups ghostscript
```

#### Langkah Pemasangan:
Buka **Terminal**, lalu jalankan:

```bash
# 1. Masuk ke folder RSCPrinting
cd RSCPrinting

# 2. Beri izin eksekusi binary
chmod +x dist/print-agent-linux-*

# 3. Pasang dan aktifkan service (systemd)
sudo ./dist/print-agent-linux-amd64 service install --config configs/config.yaml
sudo ./dist/print-agent-linux-amd64 service start
```

> **Selesai!** Agent berjalan otomatis sebagai systemd service.

---

## Konfigurasi (`config.yaml`)

Salin file contoh template:
```bash
cp configs/config.example.yaml configs/config.yaml
```

Pengaturan utama di `configs/config.yaml`:
```yaml
mode: agent

server:
  host: 127.0.0.1
  port: 8080               # Port REST API lokal
  enable_local_print_endpoint: false

ws_server:
  url: "wss://server-pos.com/agent/ws"   # URL WebSocket server aplikasi
  token: "secret-token-agent"            # Token otentikasi agent
  reconnectBackoffMax: 60s

agent:
  id: ""                   # Kosongkan untuk auto-generate UUID
  displayName: "Kasir 1"

printers:
  defaultName: ""
  allowlist: []            # Kosongkan untuk melayani semua printer yang ada
  thermal:
    - name: "Thermal"      # Nama printer struk di sistem operasi
      widthMm: 80          # Ukuran kertas: 80 atau 58
      dpi: 203
      cut: true            # Auto-potong kertas di akhir cetak
      trim: true           # Pangkas margin putih berlebih
      feed: 150            # Feed dot sebelum pisau memotong

ghostscript:
  path: ""                 # Kosong = auto-detect di sistem

office:
  enabled: true            # Konversi dokumen Office ke PDF via LibreOffice
  path: ""

document:
  allowRaw: true           # Izinkan cetak byte mentah (ESC/POS, ZPL)
```

---

## Cetak Thermal / Struk Kasir (ESC/POS)

### Masalah Driver Bawaan OS (Class Driver v4):
Printer struk seperti **Epson TM-T82X** sering bermasalah jika memakai driver otomatis Windows `Class Driver`:
- Hasil cetak keluar teks acak seperti `@EJL 1284.4`.
- Kertas keluar kosong.

### Solusi Otomatis:
Daftarkan nama printer di blok `printers.thermal` pada `config.yaml`.
Agent otomatis mengonversi dokumen menjadi bitmap monokrom dan mengirim perintah mentah **`GS v 0`** langsung ke hardware printer tanpa melalui driver OS. Hasil cetak dijamin tajam, rapi, dan cutter memotong otomatis.

---

## Format Dokumen yang Didukung

| Format | Ekstensi | Penanganan |
|---|---|---|
| **PDF / PS** | `.pdf`, `.ps` | Ghostscript (Windows) / CUPS (Unix) |
| **Gambar** | `.png`, `.jpg`, `.bmp`, `.webp` | Dikonversi ke PDF di internal Go lalu dicetak |
| **Teks Struk** | `.txt`, `.csv`, `.log` | Dikonversi ke PostScript atau dikirim RAW |
| **Office** | `.docx`, `.xlsx`, `.pptx` | Dikonversi via LibreOffice headless |
| **RAW / ESC-POS** | Byte mentah | Dikirim langsung ke port printer (`winspool.drv` / `lp -o raw`) |
| **Label Barcode** | `.zpl` | Dikirim langsung ke printer label Zebra / kompatibel |

---

## Manajemen Service & Autorun

Agent dapat dikelola dengan perintah CLI bawaan:

```bash
# Cek status service
./print-agent service status

# Restart service (misal setelah ubah config.yaml)
./print-agent service restart

# Hentikan service
./print-agent service stop

# Hapus instalasi service
./print-agent service uninstall
```

---

## Perintah CLI

```text
print-agent <command> [opsi]

Commands:
  serve       Jalankan agent secara langsung di terminal (foreground)
  printers    Daftar printer yang terdeteksi di sistem
  test        Cetak halaman tes ke printer tertentu
  service     Kelola background service (install, start, stop, status)
  config      Ubah konfigurasi (set-printers, get-provision)
  update      Cek dan unduh update agent dari GitHub
  version     Cek versi agent saat ini
```

**Contoh:**
```bash
# Cetak halaman tes
./print-agent test "Thermal"

# Cetak file tertentu
./print-agent test "EPSON TM-T82X" --file ./invoice.pdf

# Tampilkan daftar printer
./print-agent printers
```

---

## Local REST API

Agent membuka web server lokal di `http://127.0.0.1:8080`:
- **Dokumentasi Swagger**: `http://127.0.0.1:8080/docs`
- **Cek Status**: `GET /health`
- **Daftar Printer**: `GET /printers`
- **Cetak File Lokal**: `POST /print` (form-data: `printer`, `file`)

---

## Troubleshooting

| Kendala | Penyebab | Solusi |
|---|---|---|
| Struk keluar simbol acak `@EJL 1284.4` | Windows memakai driver v4 Class | Daftarkan nama printer di bagian `printers.thermal` pada `config.yaml`. |
| Job sukses tapi printer diam | Port RAW diblokir driver | Ubah driver printer ke `Generic / Text Only` atau `Epson APD`. |
| `GS_NOT_FOUND` | Ghostscript belum terpasang | Pasang Ghostscript (`gswin64c.exe` di Windows / `brew install ghostscript` di Mac). |
| macOS Gatekeeper memblokir file | File didownload dari browser | Jalankan `xattr -cr dist/print-agent-darwin-*`. |
| Windows 7 crash saat dijalankan | Compiler Go terlalu baru (Go 1.21+) | Gunakan binary dari `scripts/build-win7.sh` (Go 1.20). |

---

## Build Binary dari Source

```bash
# Windows 7 s/d 11 (Static 32-bit & 64-bit)
./scripts/build-win7.sh

# macOS (Apple Silicon & Intel)
./scripts/build-macos.sh

# Linux (amd64 & arm64)
./scripts/build-linux.sh
```

Hasil kompilasi akan berada di folder `dist/`.
