# Go Print Agent (Client-Only WebSocket Print Listener)

Print agent berbasis Go tanpa runtime eksternal (Go 1.20, `CGO_ENABLED=0`), kompatibel Windows 7 SP1+ hingga Windows 11, Linux, dan macOS.

## Fitur Utama
- **Client-only**: Membuka koneksi persistent WebSocket keluar ke server Node.js.
- **Push job 2-frame**: Menerima frame teks metadata diikuti binary frame PDF.
- **Zero IP / Zero mDNS**: Tidak ada heartbeat kustom, deteksi IP, atau database eksternal.
- **Offline Resilience**: Job result yang gagal kirim saat disconnect otomatis tersimpan di BoltDB lokal dan dikirim saat reconnect.
- **Printer Concurrency**: Limit concurrency 1 job per printer untuk mencegah tabrakan spooler.
- **Multiplatform Printer Engine**:
  - Windows: Ghostscript (`gswin64c.exe` / `gswin32c.exe`) dengan `-sDEVICE=mswinpr2`.
  - Linux/macOS: CUPS via `lp` dan `lpstat`.
- **System Tray & Service**: Menu tray dan instalasi background service / autorun.
- **Auto-Update**: Release GitHub private dengan verifikasi SHA256 dan rollback watchdog.

---

## Persyaratan Sistem

### Windows 7 SP1+ / 10 / 11
1. **Ghostscript** terpasang:
   - 64-bit: `gswin64c.exe`
   - 32-bit: `gswin32c.exe`
   - Download: https://www.ghostscript.com/download/gsdnld.html
   - Agent otomatis mendeteksi Ghostscript di PATH, `C:\Program Files\gs\*`, dan `C:\Program Files (x86)\gs\*`.
2. Driver printer sudah terpasang dan dapat mencetak halaman uji Windows.

### Linux / macOS
1. **CUPS** aktif dengan perintah `lp` dan `lpstat` tersedia di PATH.

---

## Instalasi & Menjalankan

### 1. Konfigurasi
Salin `configs/config.example.yaml` ke `configs/config.yaml`:
```bash
cp configs/config.example.yaml configs/config.yaml
```

Atur URL WebSocket dan token server:
```yaml
ws_server:
  url: "wss://app-server.example.com/agent/ws"
  token: "your-jwt-or-bearer-token"
```

Environment variable yang didukung:
- `PRINT_AGENT_WS_URL`: URL WebSocket server.
- `PRINT_AGENT_WS_TOKEN`: Bearer token auth WebSocket.
- `PRINT_AGENT_DISPLAY_NAME`: Nama tampilan agent di dashboard server.
- `PRINT_AGENT_GH_TOKEN`: GitHub Personal Access Token (PAT) untuk private release.

### 2. Jalankan Agent
```bash
# Jalankan agent listener
./print-agent serve --config configs/config.yaml

# Cek daftar printer yang terdeteksi
./print-agent printers

# Test cetak halaman uji lokal
./print-agent test "HP-LaserJet"
```

### 3. Background Service / Autorun
```bash
# Install sebagai system service (Windows Service / Linux systemd / macOS launchd)
./print-agent service install

# Jalankan service
./print-agent service start

# Cek status
./print-agent service status

# Install autorun level user (HKCU Windows Run / Linux desktop autostart)
./print-agent service install --autorun=user
```

---

## Build Binary Statis

Gunakan script build yang tersedia:
```bash
# Windows 7 SP1+ (32-bit & 64-bit)
./scripts/build-win7.sh

# Linux (amd64 & arm64)
./scripts/build-linux.sh

# macOS (amd64 & arm64)
./scripts/build-macos.sh
```
Binary hasil kompilasi akan berada di folder `dist/`.

---

## Local Debug HTTP API
Agent membuka loopback listener di `127.0.0.1:8080`:
- `GET /health`: Status WebSocket, versi, daftar printer lokal.
- `GET /version`: Informasi versi dan commit build.
- `GET /printers`: Daftar printer yang terpasang di OS.
- `GET /jobs/{id}`: Status pemrosesan job lokal.
- `POST /print`: Endpoint cetak lokal (nonaktif secara default di production, aktif bila `server.enable_local_print_endpoint: true` atau mode `standalone`).
