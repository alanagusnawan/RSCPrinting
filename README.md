# RSCPrinting — Go Print Agent

Print agent berbasis **Go** (Go 1.20, `CGO_ENABLED=0`, tanpa runtime eksternal) yang
menerima job cetak dari server via **WebSocket keluar** dan mencetaknya ke printer
lokal. Kompatibel **Windows 7 SP1+ s/d Windows 11**, Linux, dan macOS.

---

## Daftar Isi
- [Fitur Utama](#fitur-utama)
- [Persyaratan Sistem](#persyaratan-sistem)
- [Instalasi Cepat (Windows, satu perintah)](#instalasi-cepat-windows-satu-perintah)
- [Konfigurasi](#konfigurasi)
- [Format Cetak yang Didukung](#format-cetak-yang-didukung)
- [Cetak Thermal / ESC/POS](#cetak-thermal--escpos)
- [Authorun: Service & Registry](#autorun-service--registry)
- [Deploy Multi-Printer](#deploy-multi-printer)
- [Referensi CLI](#referensi-cli)
- [Local HTTP API](#local-http-api)
- [Kode Error Job](#kode-error-job)
- [Troubleshooting](#troubleshooting)
- [Build Binary Statis](#build-binary-statis)

---

## Fitur Utama
- **Client-only**: membuka koneksi WebSocket **keluar** (persistent) ke server.
- **Push job 2-frame**: frame teks metadata lalu binary frame PDF. Mendukung multi-format.
- **Zero IP / Zero mDNS**: tanpa heartbeat kustom, deteksi IP, atau DB eksternal.
- **Offline resilience**: `job_result` yang gagal terkirim disimpan di BoltDB lokal dan dikirim saat reconnect.
- **Single-instance**: hanya satu agent berjalan (named mutex global di Windows) — mencegah bentrok service + autorun registry.
- **Printer concurrency**: 1 job per printer untuk menghindari tabrakan spooler.
- **Printer engine multiplatform**:
  - Windows: Ghostscript (`gswin64c.exe`/`gswin32c.exe`) dengan `-sDEVICE=mswinpr2`.
  - Linux/macOS: CUPS via `lp`/`lpstat`.
- **Mode Thermal / ESC/POS**: render PDF → bitmap → kirim perintah `GS v 0` mentah, **melewati driver OS** (solusi output rusak seperti `@EJL 1284.4`).
- **System tray & service**: tray di sesi user; Windows Service untuk start saat boot.
- **Auto-update**: rilis GitHub (privat/opsional) dengan verifikasi SHA256 + rollback watchdog.

---

## Persyaratan Sistem

### Windows 7 SP1+ / 10 / 11
1. **Ghostscript** terpasang (untuk jalur render PDF/PS/gambar):
   - 64-bit: `gswin64c.exe`, 32-bit: `gswin32c.exe`
   - Download: https://www.ghostscript.com/download/gsdnld.html
   - Agent auto-detect di PATH, `C:\Program Files\gs\*`, `C:\Program Files (x86)\gs\*`.
2. Driver printer terpasang & bisa mencetak halaman uji Windows.
   - **Printer termal** (mis. Epson TM-T82X) disarankan memakai driver **RAW-capable (v3)**: **Epson APD** atau **`Generic / Text Only`** (lihat [Cetak Thermal](#cetak-thermal--escpos)).

### Linux / macOS
1. **CUPS** aktif dengan `lp` dan `lpstat` tersedia di PATH.

#### Menjalankan Binary di macOS (Apple Silicon / Intel)
File binary yang diunduh dari browser/GitHub Release akan diblokir Gatekeeper dan belum memiliki izin eksekusi. Gunakan perintah dinamis berikut:

```bash
# Deteksi arsitektur otomatis (arm64 atau amd64)
ARCH=$(uname -m)
[ "$ARCH" = "x86_64" ] && ARCH="amd64"
BINARY="print-agent-darwin-${ARCH}"

# Beri permission execute
chmod +x "$BINARY"

# Lepas atribut karantina Gatekeeper macOS
xattr -d com.apple.quarantine "$BINARY" 2>/dev/null || xattr -cr "$BINARY"

# Jalankan agent
./"$BINARY" serve --config configs/config.yaml
```

---

## Instalasi Cepat (Windows, satu perintah)

Jalankan sebagai **Administrator**. Skrip ini membaca default dari blok `provision:`
di `config.yaml`, lalu: provisioning driver → buat/repoint queue → sinkron config →
install & start service + registry autorun.

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1
```

Opsi umum:
```powershell
# Uji cetak di akhir
... -TestPrint

# Pakai installer APD lokal
... -InstallerPath "C:\installer\APD_612_T82X.exe"

# Pilih mode autorun (default: both)
... -Autorun service|user|both
```

Skrip terkait:
- `scripts/install.ps1` — entry point satu perintah (memanggil engine `deploy-printers.ps1`).
- `scripts/deploy-printers.ps1` — engine provisioning + queue + config + service.
- `scripts/printers.csv` — daftar printer (lihat `printers.example.csv`).

---

## Konfigurasi

Salin contoh lalu sesuaikan:
```bash
cp configs/config.example.yaml configs/config.yaml
```

```yaml
mode: agent                     # agent | standalone

server:
  host: 127.0.0.1               # 0.0.0.0 hanya untuk debugging LAN
  port: 8080
  enable_local_print_endpoint: false   # aktifkan untuk standalone/testing

ws_server:
  url: "wss://app-server.example.com/agent/ws"
  token: "your-bearer-token"
  reconnectBackoffMax: 60s
  handshakeTimeout: 15s
  pingInterval: 30s
  welcomeTimeout: 10s

agent:
  id: ""                        # UUID; generate otomatis & disimpan di ./data/config
  displayName: ""               # "Kasir Depan" dsb.

printers:
  defaultName: ""
  allowlist: []                 # bila diisi, hanya printer ini yang dilayani
  pollInterval: 5m
  thermal:                      # daftar printer ESC/POS (mode thermal)
    - name: "Thermal"
      widthMm: 80               # 58 | 80
      dpi: 203
      cut: true                 # auto-cut di akhir
      trim: true                # pangkas margin putih (default true)
      feed: 150                 # feed (dot) sebelum cut (default 150 ≈ 18.75mm)

ghostscript:
  path: ""                      # kosong = auto-detect

office:
  enabled: true                 # konversi Office via LibreOffice headless
  path: ""                      # kosong = auto-detect soffice

document:
  allowRaw: true                # izinkan RAW (ESC/POS, PCL, ZPL)
  convertTimeout: 60s

logging:
  level: info                   # debug|info|warn|error
  format: json                  # json|text
  file: ""                      # kosong = stdout

queue:
  maxSizePerPrinter: 10
  jobTimeout: 60s
  maxConcurrentPerPrinter: 1

updater:
  enabled: true
  owner: "alanagusnawan"
  repo: "RSCPrinting"
  token: ""                     # PAT bila repo privat
  checkInterval: 6h
  prerelease: false

tray:
  enabled: true
  startMinimized: true

provision:                      # dipakai skrip install/deploy
  driverSource: "local"         # github | url | local
  apdDir: "dist"                 # folder installer APD lokal (relatif root repo)
  assetPattern: "*APD*.exe"
  githubRepo: "alanagusnawan/RSCPrinting"
  githubTag: "drivers"
  url: ""                        # bila driverSource=url
  sha256: ""                     # opsional
  thermalDriverPattern: "*TM-T82*"

dataDir: "./data"                # direktori data (BoltDB, state)
```

### Environment variable
| Env | Fungsi |
|---|---|
| `PRINT_AGENT_WS_URL` | URL WebSocket server |
| `PRINT_AGENT_WS_TOKEN` | Bearer token WebSocket |
| `PRINT_AGENT_DISPLAY_NAME` | Nama tampilan agent |
| `PRINT_AGENT_ID` | Agent ID (UUID) |
| `PRINT_AGENT_MODE` | `agent` / `standalone` |
| `PRINT_AGENT_DATA_DIR` | Direktori data (dipakai service/registry agar absolut) |
| `PRINT_AGENT_GH_TOKEN` | GitHub PAT (release/updater/privat) |
| `PRINT_AGENT_GH_OWNER` / `PRINT_AGENT_GH_REPO` | Override repo updater |

---

## Format Cetak yang Didukung

| Format | Contoh | Cara Proses |
|---|---|---|
| PDF / PostScript / EPS | `.pdf`, `.ps`, `.eps` | Ghostscript (Windows) / CUPS (Linux/macOS) |
| Gambar | `.jpg`, `.png`, `.bmp`, `.gif`, `.tiff`, `.webp` | Dikonversi ke PDF (murni Go) lalu dirender |
| Teks | `.txt`, `.csv`, `.log`, `.md` | Dikonversi ke PostScript lalu dirender |
| Office | `.docx`, `.xlsx`, `.pptx`, `.doc`, `.xls`, `.ppt`, `.odt`, `.rtf` | LibreOffice headless → PDF (opsional) |
| Raw printer | ESC/POS, PCL | Byte dikirim langsung ke port printer |
| Label (ZPL) | `.zpl` | Byte dikirim langsung ke port printer |

Agent mendeteksi format otomatis dari **Content-Type → magic bytes → ekstensi**. Kirim
`format` eksplisit bila deteksi kurang akurat (`pdf|ps|image|text|office|raw|zpl|auto`).

> Office butuh **LibreOffice** (`soffice`); bila tidak ada → `OFFICE_CONVERTER_NOT_FOUND`
> (format lain tetap jalan). RAW/ZPL dikirim tanpa melalui driver printer.

---

## Cetak Thermal / ESC/POS

Printer struk (mis. **Epson TM-T82X**) berbicara **ESC/POS**, bukan bahasa driver Epson
(ESC/P-R/EJL). Jika queue memakai *class/universal driver* (mis. `Epson ESC/P-R V4
Class Driver`), output bisa berupa sampah seperti `@EJL 1284.4` **atau** job "sukses"
tapi kertas kosong (driver v4 tidak meneruskan datatype `RAW`).

**Solusi**: daftarkan printer di `printers.thermal`. Pada mode ini agent merender
dokumen ke bitmap monokrom lalu mengirim raster **`GS v 0`** mentah (melewati driver OS).

```yaml
printers:
  thermal:
    - name: "Thermal"   # harus sama persis dengan nama queue di OS
      widthMm: 80        # 58 | 80
      dpi: 203
      cut: true          # auto-cut di akhir
      trim: true         # pangkas margin/padding putih (default true)
      feed: 150          # feed (dot) sebelum cut (default 150 ≈ 18.75mm)
```

Detail:
- `trim: true` — pangkas border putih tiap halaman, lalu skala isi ke lebar penuh (576 dot @80mm / 384 dot @58mm) → **tanpa margin/padding**.
- `cut: true` — job diakhiri `GS V 'A' n` (feed `feed` dot ke posisi cutter lalu **full cut**) agar ekor tidak terpotong.
- Raster dipecah per **≤255 baris** per perintah `GS v 0` (aman untuk struk panjang).
- Mendukung PDF, PostScript, gambar, teks, Office — semua dirender ke raster.
- **Driver wajib RAW-capable (v3)**: Epson APD atau `Generic / Text Only`. Bila queue thermal memakai driver class/v4 → job gagal `DRIVER_INCOMPATIBLE`.
- Printer yang **tidak** terdaftar di `printers.thermal` memakai jalur driver biasa.
- **Ghostscript wajib** tersedia untuk mode thermal.

---

## Autorun: Service & Registry

```bash
# Windows Service (start saat boot, tanpa login — tanpa tray)
./print-agent service install --autorun service --config configs/config.yaml

# Registry HKCU Run (start saat user login — dengan tray, konsol disembunyikan)
./print-agent service install --autorun user --config configs/config.yaml

# Keduanya
./print-agent service install --autorun both --config configs/config.yaml

./print-agent service start | stop | status | uninstall
```

Mode:
| Mode | Kapan jalan | Tray | Butuh admin |
|---|---|---|---|
| `service` | Boot (sebelum login) | Tidak | Ya |
| `user` | Login user | Ya | Tidak |
| `both` | Boot + login | Tidak saat service aktif | Ya |

Catatan:
- Nilai registry HKCU (`...\Run` → `PrintAgent`) ditulis dengan **path absolut** +
  `--data-dir` + `--hidden`, sehingga tidak bergantung working directory dan tanpa jendela konsol.
- **Single-instance**: hanya **satu** agent berjalan. Service (boot) biasanya lebih dulu → instance registry otomatis keluar, sehingga **tray tidak tampil** saat service aktif. Tray hanya muncul bila agent dijalankan di sesi user (mode `user`).
- `install.ps1` default memakai `-Autorun both`.

---

## Deploy Multi-Printer

`scripts/deploy-printers.ps1` mengotomasi (Windows 7 SP1+ s/d 11, **Administrator**):
provisioning driver APD (install + auto-update) → buat/repoint N queue dari CSV → uji cetak →
sinkron `config.yaml` → install/start service + registry.

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\deploy-printers.ps1 `
  -DriverSource github -GithubRepo alanagusnawan/RSCPrinting -GithubTag drivers `
  -PrinterSpec .\scripts\printers.csv `
  -ConfigPath .\configs\config.yaml `
  -AgentExe .\dist\print-agent-windows-amd64.exe `
  -TestPrint
```

Sumber driver APD (`-DriverSource`): `github` (auto-unduh dari GitHub Releases), `url` (unduh langsung),
atau `local`/`-InstallerPath` (file lokal). Verifikasi `-ApdSha256` opsional.

`scripts/printers.csv` (lihat `printers.example.csv`):
```
Name,Model,Port,Default,Thermal
EPSON TM-T82X Kasir 1,EPSON TM-T82X,USB001,true,false
Thermal,EPSON TM-T82X,USB002,false,true
```

- `Model` = nama driver persis seperti di `Get-PrinterDriver`.
- `Thermal=true` → queue memakai driver RAW-capable (otomatis: driver APD `*TM-T82*`, fallback `Generic / Text Only`) dan didaftarkan ke `printers.thermal` (kolom `Model` diabaikan).
- Baris thermal dengan driver berbeda dari queue yang ada akan di-**repoint** otomatis (`Set-Printer`, fallback hapus & buat ulang).
- Override: `-ThermalDriver "EPSON TM-T82X"`, pola `-ThermalDriverPattern '*TM-T82*'`.
- `Port` kosong = andalkan queue Plug & Play (printer USB sudah dicolok).
- Flag: `-Force` (recreate queue), `-Autorun service|user|both`, `-InstallerArgs`, `-ApdDir`, `-ApdCacheDir`, `-GhToken`, `-ForceDriverUpdate`, `-LogPath`.

### Catatan APD6
Paket dasar `APD_6xx_xxx.exe` **tidak punya install-silent untuk instalasi baru** (opsi `/s`
hanya untuk `/s /a` upgrade dan `/s /uninstall`). Pilihan:
- Jalankan `APD_6xx_xxx.exe` sekali via **GUI** (memasang driver + queue), lalu jalankan skrip
  (skrip mendeteksi driver sudah ada → hanya repoint queue + config + service).
- Atau buat **copy installation package** via GUI APD6 (`EPSON APD6 - TM Printer Settings` →
  *Copy Installation* → *Create*), taruh di `dist/`, lalu skrip menjalankannya tanpa argumen (unattended penuh).
- Versi driver diturunkan dari nama file/aset (mis. `...-6.00.exe` → `6.00`) dan dibandingkan dengan
  state `./data/drivers/apd.state`; install ulang hanya jika belum ada/versi berubah (atau `-ForceDriverUpdate`).
- Bila provisioning gagal, instalasi tetap lanjut memakai `Generic / Text Only` + exit code `2`.

---

## Referensi CLI

```
print-agent <command> [arguments]

  serve       Jalankan agent print listener
  printers    Daftar printer lokal
  test        Cetak halaman test ke printer tertentu
  service     Kelola background service / autorun (install, uninstall, start, stop, status)
  config      Kelola konfigurasi (set-printers, get-provision)
  update      Cek / terapkan update binary dari GitHub
  version     Tampilkan versi dan build info
  status      Ringkasan status agent
```

Contoh:
```bash
./print-agent serve --config configs/config.yaml
./print-agent printers
./print-agent test "HP-LaserJet"
./print-agent test "HP-LaserJet" --file ./invoice.xlsx
./print-agent test "Thermal" --file ./struk.escpos --format raw

# Kelola daftar printer di config (lintas-OS)
./print-agent config set-printers --config configs/config.yaml \
  --allowlist "EPSON TM-T82X Kasir 1,Thermal" --default "Kasir 1" --thermal "Thermal"

# Baca pengaturan provisioning (dipakai skrip install)
./print-agent config get-provision --config configs/config.yaml
```

Flag tambahan `serve`/`run`: `--data-dir <abs>`, `--hidden` (sembunyikan konsol).

---

## Protokol WebSocket

Agent membuka koneksi keluar ke `ws_server.url`. Pesan teks JSON (`type`):

- **hello** (agent → server): `agentId`, `hostname`, `displayName`, `printers`, `version`, `os`.
- **welcome** (server → agent): `serverTime`, `connectionId`.
- **job** (server → agent): metadata job di frame teks, lalu **binary frame** berisi dokumen.
- **job_result** (agent → server): `jobId`, `success`, `error`, `errorCode`, `durationMs`.
- **printers_update** (agent → server): daftar nama printer terbaru.
- **printers_detail** (agent → server): hostname, `os`, `ips[]`, dan per printer endpoint share (`shared`, `shareName`, `protocol`, `smbPath`, `ippUri`, `socketUri`, `relayUri`). Dikirim saat connect & setelah perubahan.

### Control request/response (opsional)
Agar server tidak timeout menunggu, agent menjawab pesan kontrol:
- Permintaan opsi printer (tipe mengandung `option`, mis. `get_options`) → dibalas `{"type":"options", success, options:{types,drivers,ports}, types, drivers, ports}`.
- Permintaan daftar printer (tipe mengandung `printer`, mis. `get_printers`) → dibalas `{"type":"printers", success, printers:[...]}`.
- Bila ada `requestId`/`id`/`correlationId`, nilainya di-echo di respons.

### Perintah printer (server → agent)
Semua membalas pesan `*_result` ber-`requestId` dan diikuti `printers_update` + `printers_detail`:

| Perintah | Fungsi |
|---|---|
| `register_printer` | Pasang queue OS (`name`, `printerType`, `portOrUri`, `driverName`, `ppd`, `driverless`, `isDefault`). Idempotent. |
| `update_printer` | Repoint/rename queue yang sudah ada (mis. saat IP sumber berubah). |
| `delete_printer` | Copot queue. |
| `set_default_printer` | Set default printer OS. |
| `sync_printers` | Pasang beberapa printer sekaligus (bulk) + opsional `prune`. |
| `share_printer` / `unshare_printer` | Bagikan / hentikan pembagian printer lokal. |

---

## Cetak Lintas-Komputer (B cetak lewat printer A)

Skenario: **A** punya printer terpasang; dari dashboard server, admin meng-klik "Install" untuk **B**,
sehingga di B muncul queue printer A. Saat user mencetak dari Word di B, job dikirim ke A dan A mencetak.

Dua model:

- **Model 1 — Berbagi lewat OS** (sesama Windows / sesama Unix):
  - A Windows → share SMB `\\HOST_A\RSC-<printer>`; A Unix → CUPS IPP `ipp://HOST_A.local:631/printers/<printer>`.
  - B memasang queue ke endpoint itu (`register_printer` dengan `printerType: HTTP_SHARED`).
- **Model 2 — Relay IPP** (`printers.share.relayEnabled: true`, cross-OS & driverless):
  - A membuka listener IPP (`relayPort`, default 19100). B memasang `ipp://HOST_A:<relayPort>/printers/<printer>`.
  - Dokumen yang diterima A dirender/dicetak lewat pipeline yang sudah ada.

**IP dinamis (DHCP):** endpoint memakai **hostname** (bukan IP) agar spooler me-resolve ulang tiap job;
selain itu agent A melaporkan `printers_detail` (hostname + IP terbaru) tiap connect / saat berubah,
dan server dapat mengirim `update_printer` ke B untuk repoint. IP hanya fallback.

**Prasyarat:** A menyala saat jam kerja; firewall membuka `631` (IPP), `445/139` (SMB), dan `relayPort`;
hak admin/root saat share/install; Linux A: `avahi-daemon` untuk mDNS.

---

## Local HTTP API

Agent membuka loopback listener di `127.0.0.1:8080` (Swagger UI di `/docs`).
Endpoint **mutasi** (POST/PUT/DELETE) hanya aktif bila `mode: standalone` atau
`server.enable_local_print_endpoint: true`; endpoint GET selalu aktif.

### System
| Metode | Path | Fungsi |
|---|---|---|
| GET | `/health` | Status WebSocket, versi, daftar printer |
| GET | `/version` | Versi & commit build |
| GET | `/docs`, `/swagger.json` | Swagger UI & spesifikasi |

### Printers (CRUD)
| Metode | Path | Fungsi |
|---|---|---|
| GET | `/printers` | Daftar printer OS |
| POST | `/printers` | Buat/daftarkan printer (`name`, `type`, `portOrUri`, `driverName`, `isDefault`) |
| GET | `/printers/options` | Tipe printer, nama driver, port tersedia |
| GET | `/printers/{name}` | Detail satu printer |
| PUT | `/printers/{name}` | Update (rename `newName`, `driverName`, `type`, `portOrUri`, `isDefault`) |
| DELETE | `/printers/{name}` | Hapus queue printer |
| POST | `/printers/{name}/default` | Jadikan printer default |
| POST | `/printers/{name}/share` | Bagikan printer lokal (opsional body `{protocol,shareName}`) |
| POST | `/printers/{name}/unshare` | Hentikan pembagian printer |
| POST | `/printers/sync` | Pasang beberapa printer sekaligus (`{printers:[...],prune}`) |

Contoh create/update:
```bash
curl -X POST http://127.0.0.1:8080/printers -H "Content-Type: application/json" \
  -d '{"name":"Kasir-Network","type":"IP","portOrUri":"192.168.1.200","driverName":"Generic / Text Only"}'

curl -X PUT http://127.0.0.1:8080/printers/Kasir-Network -H "Content-Type: application/json" \
  -d '{"newName":"Kasir-Utama","isDefault":true}'
```

### Config
| Metode | Path | Fungsi |
|---|---|---|
| GET | `/config/printers` | Baca bagian `printers` config |
| PUT | `/config/printers` | Ganti `defaultName`/`allowlist`/`thermal` (ditulis ke `config.yaml`) |

### Jobs
| Metode | Path | Fungsi |
|---|---|---|
| GET | `/jobs` | Daftar job terakhir (terbaru dulu) |
| GET | `/jobs/{id}` | Detail job |
| DELETE | `/jobs/{id}` | Batalkan (bila berjalan) & hapus record |

### Print
| Metode | Path | Fungsi |
|---|---|---|
| POST | `/print` | Cetak lokal multi-format (`multipart/form-data`: `printer`, `format`, `file`) |

---

## Kode Error Job

| Kode | Arti |
|---|---|
| `PRINT_FAILED` | Gagal eksekusi cetak umum |
| `PRINTER_BUSY` / `PRINTER_TIMEOUT` | Antrian printer penuh / timeout |
| `GS_NOT_FOUND` | Ghostscript tidak ditemukan |
| `UNSUPPORTED_FORMAT` | Format tidak dikenal |
| `PREPARE_FAILED` | Gagal menyiapkan dokumen |
| `RAW_SEND_FAILED` | Gagal kirim RAW ke spooler |
| `DRIVER_INCOMPATIBLE` | Driver class/v4 (tidak mendukung RAW) |
| `THERMAL_RENDER_FAILED` | Gagal render raster thermal |
| `OFFICE_CONVERTER_NOT_FOUND` / `OFFICE_CONVERT_FAILED` | LibreOffice tidak ada / gagal konversi |
| `TEMP_FILE_ERROR` / `WRITE_ERROR` | Gagal temp file |

---

## Troubleshooting

| Gejala | Penyebab | Solusi |
|---|---|---|
| Cetak PDF keluar `@EJL 1284.4` / simbol acak | Driver Epson mengeluarkan EJL/ESC-P-R | Aktifkan `printers.thermal` untuk printer itu |
| Job sukses tapi kertas kosong | Driver v4/class tidak meneruskan RAW | Ganti driver queue ke **Epson APD** atau **`Generic / Text Only`** |
| `DRIVER_INCOMPATIBLE` | Driver queue class/v4 | Ganti driver queue ke RAW-capable (v3) |
| Ada margin/padding putih | PDF punya area putih tepi | Pastikan `trim: true` |
| Ekor terpotong saat cut | Feed sebelum cut kurang | Naikkan `feed` (mis. 200) |
| Terlalu banyak kertas kosong sebelum cut | Feed berlebih | Turunkan `feed` |
| Service gagal start | (sudah diperbaiki) path relatif / SCM | Pastikan binary & config terbaru; `services.msc` → restart |
| Dua agent jalan | service + registry | Otomatis dicegah single-instance |

---

## Build Binary Statis

```bash
# Windows 7 SP1+ (32-bit & 64-bit)
./scripts/build-win7.sh

# Linux (amd64 & arm64)
./scripts/build-linux.sh

# macOS (amd64 & arm64)
./scripts/build-macos.sh
```

Hasil kompilasi ada di `dist/`. Contoh build Windows manual dengan version info:
```powershell
go build -ldflags "-s -w -X github.com/rscprinting/print-agent/internal/version.Version=1.0.0" `
  -o dist/print-agent-windows-amd64.exe ./cmd/print-agent
```

> Binary `dist/print-agent-*` **tidak lagi dilacak git**; ia dihasilkan dari rilis GitHub
> (lihat bagian Rilis & Auto-update).

---

## Rilis & Auto-update

Rilis dilakukan **dari komputer lokal** (tanpa GitHub Actions) memakai script. Script akan:

1. Menghitung **versi** dari tag terakhir (semver `vX.Y.Z`) memakai **conventional commits**:
   - `feat:` → minor, `fix:`/lainnya → patch, `BREAKING CHANGE`/`!:` → major.
   - Belum ada tag → `v0.1.0`.
2. Build 6 binary (Windows amd64/386, Linux amd64/arm64, macOS amd64/arm64).
3. Membuat `dist/checksums.txt` (SHA256, format dua spasi) — **wajib** agar auto-update lolos verifikasi.
4. Membuat git tag, push tag, lalu **membuat GitHub Release + upload asset** via REST API.

**Penting:** gunakan awalan conventional commit (`feat:`, `fix:`, `feat!:`, …) agar bump versi benar.

### Rilis di Windows (PowerShell)
Butuh **Go**, **Git**, dan **PAT GitHub** (fine-grained: *Contents: Read & Write*; klasik: scope `repo`).
Set token di environment, lalu jalankan:
```powershell
$env:GH_TOKEN = "ghp_xxx"
powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1

# override bump / versi
powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1 -Bump minor
powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1 -Version v1.0.0

# uji tanpa tag/push/release (build + checksums saja)
powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1 -DryRun
```
Opsi lain: `-Repo owner/name`, `-Token`, `-RunTests`, `-SkipBuild`, `-SkipPush`.

### Rilis di Git Bash / macOS / Linux
```bash
export GH_TOKEN=ghp_xxx
scripts/release.sh --dry-run     # build + checksums saja
scripts/release.sh               # versi otomatis
scripts/release.sh v1.2.3        # versi eksplisit
scripts/release.sh --bump minor  # paksa minor
```

### Auto-update di agent
Agent membandingkan versinya dengan rilis terbaru (semver) dan mengunduh binary yang cocok
via `go-selfupdate`, dengan verifikasi `checksums.txt`.

- Repo **privat** → set token agar agent bisa mengunduh asset:
  - config `updater.token`, atau environment `PRINT_AGENT_GH_TOKEN`, atau OS keyring
    (`print-agent` / `github-token`).
- Repo publik → token opsional (hanya untuk menghindari rate limit).
- Cek/terapkan manual: `print-agent update` (cek) dan `print-agent update --apply`.
