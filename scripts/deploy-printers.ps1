#Requires -Version 2.0
<#
  deploy-printers.ps1 - Engine deploy sekali-jalan untuk RSCPrinting.

  Melakukan:
    1. Provisioning driver Epson APD (install silent + auto-update) - idempotent.
       Sumber: -InstallerPath (lokal), atau -DriverSource github|url|local.
    2. Membuat/merepoint N queue printer dari file CSV - idempotent.
    3. Opsional cetak halaman uji tiap queue.
    4. Opsional sinkronkan config.yaml (allowlist/default/thermal).
    5. Opsional install & start service agent (mode system service).

  Kompatibel Windows 7 SP1+ (PowerShell 2.0) hingga Windows 11.
  WAJIB dijalankan sebagai Administrator. Untuk pengalaman satu-perintah, pakai
  scripts/install.ps1 (membaca default dari blok `provision:` di config).

  Contoh (auto-unduh APD dari GitHub Releases repo yang sama):
    powershell -ExecutionPolicy Bypass -File .\scripts\deploy-printers.ps1 `
      -DriverSource github -GithubRepo owner/RSCPrinting -GithubTag drivers `
      -PrinterSpec .\scripts\printers.csv `
      -ConfigPath .\configs\config.yaml `
      -AgentExe .\dist\print-agent-windows-amd64.exe -TestPrint

  Contoh (installer lokal):
    powershell -ExecutionPolicy Bypass -File .\scripts\deploy-printers.ps1 `
      -InstallerPath C:\installer\APD.exe -InstallerArgs /s -TestPrint
#>
[CmdletBinding()]
param(
    [string]$InstallerPath = '',

    [string]$InstallerArgs = '',

    [string]$DriverName = '',

    [string]$ThermalDriver = '',

    [string]$ThermalDriverPattern = '*TM-T82*',

    [ValidateSet('github', 'url', 'local')]
    [string]$DriverSource = 'github',

    [string]$GithubRepo = '',

    [string]$GithubTag = '',

    [string]$ApdAssetPattern = '',

    [string]$ApdUrl = '',

    [string]$ApdSha256 = '',

    [string]$ApdDir = '',

    [string]$GhToken = '',

    [string]$ApdCacheDir = '',

    [switch]$ForceDriverUpdate,

    [string]$PrinterSpec = '',

    [string]$ConfigPath = '',

    [string]$AgentExe = '',

    [ValidateSet('service', 'user', 'both')]
    [string]$Autorun = 'service',

    [switch]$TestPrint,

    [switch]$Force,

    [string]$LogPath = ''
)

$ErrorActionPreference = 'Stop'

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
if (-not $PrinterSpec) { $PrinterSpec = Join-Path $ScriptDir 'printers.csv' }
if (-not $LogPath) { $LogPath = Join-Path (Split-Path -Parent $ScriptDir) 'logs\deploy-printers.log' }

$script:LogFile = $LogPath
$logDir = Split-Path -Parent $LogPath
if ($logDir -and -not (Test-Path -LiteralPath $logDir)) {
    New-Item -ItemType Directory -Path $logDir -Force | Out-Null
}

function Write-Log {
    param(
        [Parameter(Mandatory = $true)][string]$Message,
        [ValidateSet('INFO', 'WARN', 'ERROR')][string]$Level = 'INFO'
    )
    $ts = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $line = "[$ts][$Level] $Message"
    switch ($Level) {
        'WARN'  { Write-Host $line -ForegroundColor Yellow }
        'ERROR' { Write-Host $line -ForegroundColor Red }
        default { Write-Host $line }
    }
    try { Add-Content -LiteralPath $script:LogFile -Value $line -Encoding UTF8 } catch { }
}

function Assert-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Skrip harus dijalankan sebagai Administrator.'
    }
}

function Get-Capabilities {
    return @{
        AddPrinter       = $null -ne (Get-Command Add-Printer -ErrorAction SilentlyContinue)
        AddPrinterDriver = $null -ne (Get-Command Add-PrinterDriver -ErrorAction SilentlyContinue)
        SetPrinter       = $null -ne (Get-Command Set-Printer -ErrorAction SilentlyContinue)
        RemovePrinter    = $null -ne (Get-Command Remove-Printer -ErrorAction SilentlyContinue)
        GetPrinter       = $null -ne (Get-Command Get-Printer -ErrorAction SilentlyContinue)
        GetPrinterDriver = $null -ne (Get-Command Get-PrinterDriver -ErrorAction SilentlyContinue)
        GetPrinterPort   = $null -ne (Get-Command Get-PrinterPort -ErrorAction SilentlyContinue)
    }
}

function Get-InstalledDriverNames {
    if ($script:Caps.GetPrinterDriver) {
        return @(Get-PrinterDriver -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Name)
    }
    return @(Get-WmiObject Win32_PrinterDriver -ErrorAction SilentlyContinue | ForEach-Object { ($_.Name -split ',')[0] })
}

function Enable-Tls12 {
    try {
        [Net.ServicePointManager]::SecurityProtocol =
            [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }
    catch {
        try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch { }
    }
}

function Get-CryptoSha256 {
    param([string]$Path)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $fs = [System.IO.File]::OpenRead($Path)
    try { $hash = $sha.ComputeHash($fs) }
    finally { $fs.Close(); $sha.Dispose() }
    return ([System.BitConverter]::ToString($hash) -replace '-', '').ToLower()
}

# Download-File memakai WebClient agar kompatibel PowerShell 2.0 (Win7).
function Download-File {
    param([string]$Url, [string]$OutPath, [string]$Token)
    Enable-Tls12
    $wc = New-Object System.Net.WebClient
    $wc.Headers.Add('User-Agent', 'RSCPrinting-Setup')
    if ($Token) { $wc.Headers.Add('Authorization', "token $Token") }
    try {
        Write-Log "Mengunduh: $Url"
        $wc.DownloadFile($Url, $OutPath)
    }
    finally { $wc.Dispose() }
}

function Invoke-WebRequestText {
    param([string]$Url, [string]$Token)
    Enable-Tls12
    $wc = New-Object System.Net.WebClient
    $wc.Headers.Add('User-Agent', 'RSCPrinting-Setup')
    $wc.Headers.Add('Accept', 'application/vnd.github+json')
    if ($Token) { $wc.Headers.Add('Authorization', "token $Token") }
    try { return $wc.DownloadString($Url) }
    finally { $wc.Dispose() }
}

function Get-VersionFromName {
    param([string]$Name)
    $m = [regex]::Match($Name, '(\d+(?:\.\d+)+)')
    if ($m.Success) { return $m.Groups[1].Value }
    $m2 = [regex]::Match($Name, 'APD[_\-\s]*(\d+)', 'IgnoreCase')
    if ($m2.Success) { return $m2.Groups[1].Value }
    return ''
}

function Get-ApdStateFile {
    if (-not $script:ApdCacheDir) {
        $script:ApdCacheDir = Join-Path (Split-Path -Parent $ScriptDir) 'data\drivers'
    }
    return (Join-Path $script:ApdCacheDir 'apd.state')
}

function Read-ApdState {
    $f = Get-ApdStateFile
    if (Test-Path -LiteralPath $f) {
        $lines = @(Get-Content -LiteralPath $f)
        if ($lines.Count -gt 0) { return ([string]$lines[0]).Trim() }
    }
    return ''
}

function Write-ApdState {
    param([string]$Version)
    if (-not $script:ApdCacheDir) { $script:ApdCacheDir = Split-Path -Parent (Get-ApdStateFile) }
    if (-not (Test-Path -LiteralPath $script:ApdCacheDir)) {
        New-Item -ItemType Directory -Path $script:ApdCacheDir -Force | Out-Null
    }
    Set-Content -LiteralPath (Get-ApdStateFile) -Value $Version -Encoding ASCII
}

function Test-ApdDriverInstalled {
    $installed = @(Get-InstalledDriverNames)
    if ($script:DriverName -and ($installed -contains $script:DriverName)) { return $true }
    if ($script:ThermalDriverPattern) {
        foreach ($d in $installed) {
            if ($d -like $script:ThermalDriverPattern) { return $true }
        }
    }
    return $false
}

# Get-ApdInstaller menyelesaikan sumber installer APD (local/url/github) dan
# mengembalikan hashtable Path, Version, Source.
function Get-ApdInstaller {
    if ($InstallerPath) {
        if (-not (Test-Path -LiteralPath $InstallerPath)) { throw "Installer tidak ditemukan: $InstallerPath" }
        return @{ Path = $InstallerPath; Version = (Get-VersionFromName (Split-Path -Leaf $InstallerPath)); Source = 'path' }
    }

    if (-not $script:ApdCacheDir) { $script:ApdCacheDir = Join-Path (Split-Path -Parent $ScriptDir) 'data\drivers' }
    if (-not (Test-Path -LiteralPath $script:ApdCacheDir)) {
        New-Item -ItemType Directory -Path $script:ApdCacheDir -Force | Out-Null
    }

    if ($DriverSource -eq 'local') {
        $dir = $ApdDir
        if (-not $dir) { $dir = $script:ApdCacheDir }
        if (-not (Test-Path -LiteralPath $dir)) { throw "Folder driver lokal tidak ditemukan: $dir" }
        $pattern = $script:ApdAssetPattern
        if (-not $pattern) { $pattern = '*.exe' }
        $cand = @(Get-ChildItem -LiteralPath $dir -Filter $pattern -Recurse -ErrorAction SilentlyContinue | Where-Object { -not $_.PSIsContainer })
        if ($cand.Count -eq 0) { throw "Tidak ada installer '$pattern' di $dir (termasuk subfolder)." }
        $file = $cand | Sort-Object LastWriteTime -Descending | Select-Object -First 1
        Write-Log "Installer lokal terpilih: $($file.FullName)"
        return @{ Path = $file.FullName; Version = (Get-VersionFromName $file.Name); Source = 'local' }
    }

    if ($DriverSource -eq 'url') {
        if (-not $ApdUrl) { throw 'driverSource=url tetapi -ApdUrl kosong.' }
        $leaf = Split-Path -Leaf ($ApdUrl.Split('?')[0])
        $file = Join-Path $script:ApdCacheDir $leaf
        Download-File -Url $ApdUrl -OutPath $file -Token $script:GhToken
        if ($ApdSha256) {
            $h = Get-CryptoSha256 -Path $file
            if ($h -ne $ApdSha256.ToLower()) { throw "SHA256 mismatch untuk $leaf" }
        }
        Write-Log "Installer siap (url): $file"
        return @{ Path = $file; Version = (Get-VersionFromName $leaf); Source = 'url' }
    }

    if (-not $GithubRepo) { throw 'driverSource=github tetapi -GithubRepo kosong.' }

    $pattern = $script:ApdAssetPattern
    if (-not $pattern) { $pattern = '*.exe' }

    $json = Get-GitHubReleaseJson -Repo $GithubRepo -Tag $GithubTag -Token $script:GhToken
    if (-not $json) {
        $hint = ''
        if (-not $script:GhToken) { $hint = ' Jika repo privat, set -GhToken atau env PRINT_AGENT_GH_TOKEN.' }
        throw ("Tidak dapat membaca rilis GitHub '$GithubRepo' (tag '$GithubTag'/latest). " +
            "Pastikan rilis berisi aset APD sudah dibuat, atau pakai -InstallerPath / -DriverSource url / -DriverSource local." + $hint)
    }

    $assetsMatch = [regex]::Match($json, '"assets"\s*:\s*\[(.*?)\]\s*[,}]', 'Singleline')
    if (-not $assetsMatch.Success) { throw 'Tidak menemukan daftar aset pada rilis.' }
    $assets = $assetsMatch.Groups[1].Value
    $names = [regex]::Matches($assets, '"name"\s*:\s*"([^"]+)"')
    $urls = [regex]::Matches($assets, '"browser_download_url"\s*:\s*"([^"]+)"')

    for ($i = 0; $i -lt $names.Count -and $i -lt $urls.Count; $i++) {
        $name = $names[$i].Groups[1].Value
        if ($name -like $pattern) {
            $dl = $urls[$i].Groups[1].Value
            $file = Join-Path $script:ApdCacheDir $name
            Download-File -Url $dl -OutPath $file -Token $script:GhToken
            if ($ApdSha256) {
                $h = Get-CryptoSha256 -Path $file
                if ($h -ne $ApdSha256.ToLower()) { throw "SHA256 mismatch untuk $name" }
            }
            Write-Log "Installer siap (github): $file"
            return @{ Path = $file; Version = (Get-VersionFromName $name); Source = 'github' }
        }
    }
    throw "Aset driver cocok '$pattern' tidak ditemukan pada rilis '$GithubRepo'."
}

# Get-GitHubReleaseJson mengambil JSON rilis: coba tag (bila ada) lalu latest.
# Mengembalikan $null bila keduanya 404 agar bisa ditangani dengan pesan jelas.
function Get-GitHubReleaseJson {
    param([string]$Repo, [string]$Tag, [string]$Token)

    $urls = @()
    if ($Tag) { $urls += ('https://api.github.com/repos/' + $Repo + '/releases/tags/' + $Tag) }
    $urls += ('https://api.github.com/repos/' + $Repo + '/releases/latest')

    foreach ($u in $urls) {
        try {
            $json = Invoke-WebRequestText -Url $u -Token $Token
            Write-Log "Rilis GitHub dibaca: $u"
            return $json
        }
        catch [System.Net.WebException] {
            $code = 0
            if ($_.Exception.Response) { $code = [int]$_.Exception.Response.StatusCode }
            Write-Log "GitHub '$u' gagal (HTTP $code)." 'WARN'
        }
    }
    return $null
}

function Get-DriverInfName {
    param([string]$Model)
    $drv = Get-WmiObject Win32_PrinterDriver -ErrorAction SilentlyContinue |
        Where-Object { ($_.Name -split ',')[0] -eq $Model } |
        Select-Object -First 1
    if ($drv -and $drv.InfName) { return $drv.InfName }
    return ''
}

function Wait-DriverInstalled {
    param([string]$Model, [int]$TimeoutSeconds = 90)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (@(Get-InstalledDriverNames) -contains $Model) { return $true }
        Start-Sleep -Seconds 3
    }
    return $false
}

# Resolve-ThermalDriver menentukan driver RAW-capable untuk queue thermal.
# Prioritas: -ThermalDriver eksplisit > driver APD (pola) > "Generic / Text Only".
function Resolve-ThermalDriver {
    if ($script:ThermalDriver) { return $script:ThermalDriver }

    $installed = @(Get-InstalledDriverNames)
    if ($script:ThermalDriverPattern) {
        $apd = $installed | Where-Object { $_ -like $script:ThermalDriverPattern } | Select-Object -First 1
        if ($apd) {
            $script:ThermalDriver = $apd
            Write-Log "Driver thermal terdeteksi (APD): '$apd'."
            return $script:ThermalDriver
        }
    }

    $generic = 'Generic / Text Only'
    if (($installed -contains $generic) -or ($script:Caps.AddPrinterDriver)) {
        if (-not ($installed -contains $generic) -and $script:Caps.AddPrinterDriver) {
            try { Add-PrinterDriver -Name $generic -ErrorAction Stop; Write-Log "Driver '$generic' didaftarkan." }
            catch { Write-Log "Gagal mendaftarkan '$generic': $($_.Exception.Message)" 'WARN' }
        }
        $script:ThermalDriver = $generic
    }
    else {
        $script:ThermalDriver = $generic
        Write-Log "Driver APD tidak ditemukan; fallback '$generic' (mungkin belum terpasang)." 'WARN'
    }
    Write-Log "Driver thermal dipakai: '$($script:ThermalDriver)'."
    return $script:ThermalDriver
}

function Install-Apd {
    param([string]$Path, [string]$ArgLine, [string]$Model)

    if ($Model -and (@(Get-InstalledDriverNames) -contains $Model)) {
        Write-Log "Driver '$Model' sudah terpasang. Lewati instalasi APD."
        return
    }

    Write-Log "Menjalankan installer APD: $Path $ArgLine"
    if (($Path -match '(?i)APD[_\-\s]*\d') -and (-not $ArgLine)) {
        Write-Log 'Paket dasar APD6: wizard instalasi (GUI) akan muncul. Ikuti sampai selesai; skrip menunggu.' 'WARN'
    }
    $argList = @()
    if ($ArgLine) { $argList = $ArgLine -split '\s+' | Where-Object { $_ -ne '' } }

    $proc = Start-Process -FilePath $Path -ArgumentList $argList -Wait -PassThru
    if ($proc.ExitCode -ne 0) {
        throw ("Installer APD exit code $($proc.ExitCode). Catatan: paket dasar APD6 tidak punya install-silent " +
            "untuk instalasi baru (opsi /s hanya untuk /a upgrade & /uninstall). Untuk unattended gunakan " +
            "'copy installation package', atau jalankan APD_...exe (GUI) sekali lalu jalankan ulang skrip ini.")
    }

    if ($Model) {
        if (-not (Wait-DriverInstalled -Model $Model -TimeoutSeconds 90)) {
            throw ("Driver '$Model' tidak terdeteksi setelah instalasi. Paket dasar APD6 mungkin hanya membuka GUI " +
                "(tidak silent). Jalankan APD_...exe sekali via GUI untuk memasang driver, atau gunakan copy-install package.")
        }
        Write-Log "Driver '$Model' terverifikasi terpasang."
    }
    elseif ($script:ThermalDriverPattern) {
        $deadline = (Get-Date).AddSeconds(90)
        while ((Get-Date) -lt $deadline) {
            if (Test-ApdDriverInstalled) {
                Write-Log "Driver APD terverifikasi terpasang (pola '$($script:ThermalDriverPattern)')."
                return
            }
            Start-Sleep -Seconds 3
        }
        throw ("Driver APD pola '$($script:ThermalDriverPattern)' tidak terdeteksi. Paket dasar APD6 tidak mendukung " +
            "install-silent instalasi baru; jalankan APD_...exe via GUI sekali, atau gunakan copy-install package.")
    }
    else {
        Write-Log 'DriverName/pattern tidak diisi; verifikasi driver dilewati.' 'WARN'
    }
}

# Provision-Driver: ambil installer (sesuai sumber), auto-update bila perlu,
# jalankan install, dan simpan state versi.
function Provision-Driver {
    $info = Get-ApdInstaller
    $state = Read-ApdState
    $driverPresent = Test-ApdDriverInstalled

    $need = $false
    if ($ForceDriverUpdate) { $need = $true }
    elseif (-not $driverPresent) { $need = $true }
    elseif ($info.Version -and ($info.Version -ne $state)) { $need = $true }

    if (-not $need) {
        Write-Log "Driver APD sudah terpasang versi '$state'; tidak ada update."
        return $info
    }

    Write-Log "Memasang driver APD versi '$($info.Version)' (sumber: $($info.Source), state lama: '$state')."
    Install-Apd -Path $info.Path -ArgLine $InstallerArgs -Model $DriverName
    if ($info.Version) { Write-ApdState -Version $info.Version }
    return $info
}

function Get-InstalledPrinterNames {
    if ($script:Caps.GetPrinter) {
        return @(Get-Printer -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Name)
    }
    return @(Get-WmiObject Win32_Printer -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Name)
}

function Test-PortExists {
    param([string]$Port)
    if (-not $Port) { return $true }
    if ($script:Caps.GetPrinterPort) {
        return @(Get-PrinterPort -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Name) -contains $Port
    }
    # Win7: port USB tidak dapat di-enumerasi lewat WMI; anggap ada, biarkan spooler memvalidasi.
    return $true
}

function Remove-Queue {
    param([string]$Name)
    if ($script:Caps.RemovePrinter) {
        Remove-Printer -Name $Name -ErrorAction SilentlyContinue | Out-Null
    }
    else {
        & rundll32 printui.dll,PrintUIEntry /dl /n "$Name" /q | Out-Null
    }
}

function Get-CurrentDriver {
    param([string]$Name)
    if ($script:Caps.GetPrinter) {
        $d = (Get-Printer -Name $Name -ErrorAction SilentlyContinue).DriverName
        if ($d) { return $d }
    }
    $clean = $Name.Replace("'", "''")
    $w = Get-WmiObject Win32_Printer -Filter "Name='$clean'" -ErrorAction SilentlyContinue
    if ($w) { return $w.DriverName }
    return ''
}

# Repoint-Driver mengganti driver sebuah queue. Coba Set-Printer lebih dulu;
# bila gagal, hapus & buat ulang di port yang sama.
function Repoint-Driver {
    param([string]$Name, [string]$Model, [string]$Port)

    if ($script:Caps.SetPrinter) {
        try {
            Set-Printer -Name $Name -DriverName $Model -ErrorAction Stop
            Write-Log "Driver queue '$Name' diubah ke '$Model' (Set-Printer)."
            return $true
        }
        catch {
            Write-Log "Set-Printer gagal: $($_.Exception.Message). Hapus & buat ulang." 'WARN'
        }
    }

    Remove-Queue -Name $Name
    if ($script:Caps.AddPrinter) {
        Add-Printer -Name $Name -DriverName $Model -PortName $Port | Out-Null
    }
    else {
        $inf = Get-DriverInfName -Model $Model
        if (-not $inf) {
            throw "Tidak menemukan INF driver untuk model '$Model' (dibutuhkan jalur fallback Windows 7)."
        }
        & rundll32 printui.dll,PrintUIEntry /if /b "$Name" /f "$inf" /r "$Port" /m "$Model" | Out-Null
    }
    Write-Log "Queue '$Name' dibuat ulang dengan driver '$Model' (port='$Port')."
    return $true
}

function New-Queue {
    param(
        [string]$Name,
        [string]$Model,
        [string]$Port,
        [bool]$Default
    )

    $exists = @(Get-InstalledPrinterNames) -contains $Name

    if ($exists -and -not $Force) {
        $current = Get-CurrentDriver -Name $Name
        if ($Model -and $current -and ($current -ine $Model)) {
            if (-not $Port) {
                Write-Log "Queue '$Name' ada dengan driver '$current' (ingin '$Model') tapi kolom Port kosong; isi kolom Port untuk repoint." 'WARN'
            }
            else {
                Write-Log "Queue '$Name' ada dengan driver '$current'; repoint ke '$Model'."
                Repoint-Driver -Name $Name -Model $Model -Port $Port
            }
        }
        else {
            Write-Log "Queue '$Name' sudah ada (driver '$current'). Lewati pembuatan."
        }
    }
    elseif (-not $Port) {
        # Tanpa port: andalkan queue Plug & Play ketika printer dicolok.
        if ($exists) {
            Write-Log "Queue '$Name' tersedia dari Plug & Play. Lewati pembuatan."
        }
        else {
            Write-Log "Queue '$Name' belum ada dan kolom Port kosong. Colok printer agar PnP membuat queue, atau isi kolom Port." 'WARN'
            return $false
        }
    }
    else {
        if ($exists -and $Force) {
            Write-Log "Menghapus queue lama '$Name' (-Force)."
            Remove-Queue -Name $Name
        }

        if (-not (Test-PortExists -Port $Port)) {
            Write-Log "Port '$Port' belum terdeteksi; pembuatan queue tetap dicoba." 'WARN'
        }

        if ($script:Caps.AddPrinter) {
            $params = @{ Name = $Name; DriverName = $Model; PortName = $Port }
            Add-Printer @params | Out-Null
        }
        else {
            # Fallback Windows 7 via printui.dll
            $inf = Get-DriverInfName -Model $Model
            if (-not $inf) {
                throw "Tidak menemukan INF driver untuk model '$Model' (dibutuhkan jalur fallback Windows 7)."
            }
            & rundll32 printui.dll,PrintUIEntry /if /b "$Name" /f "$inf" /r "$Port" /m "$Model" | Out-Null
        }
        Write-Log "Queue '$Name' dibuat (driver='$Model', port='$Port')."
    }

    if ($Default) {
        (New-Object -ComObject WScript.Network).SetDefaultPrinter($Name) | Out-Null
        Write-Log "Queue '$Name' dijadikan printer default."
    }
    return $true
}

function Invoke-TestPrint {
    param([string]$Name)
    & $AgentExe test "$Name"
    if ($LASTEXITCODE -ne 0) {
        Write-Log "Test print ke '$Name' gagal (exit $LASTEXITCODE)." 'WARN'
    }
    else {
        Write-Log "Test print terkirim ke '$Name'."
    }
}

function Update-AgentConfig {
    param([string[]]$Names, [string]$DefaultName, [string[]]$Thermal)
    if (-not $ConfigPath) { return }
    $allow = $Names -join ','
    Write-Log "Sinkronisasi config '$ConfigPath' (default='$DefaultName', thermal=$($Thermal.Count))."
    $cliArgs = @('config', 'set-printers', '--config', "$ConfigPath", '--allowlist', "$allow", '--default', "$DefaultName")
    if ($Thermal -and $Thermal.Count -gt 0) {
        $cliArgs += @('--thermal', ($Thermal -join ','))
    }
    & $AgentExe @cliArgs
    if ($LASTEXITCODE -ne 0) {
        throw "Gagal memperbarui config (exit $LASTEXITCODE)."
    }
}

function Install-AgentService {
    Write-Log "Install agent autorun (mode=$Autorun)."

    $svcConfig = $ConfigPath
    if (-not $svcConfig) { $svcConfig = Join-Path (Split-Path -Parent $ScriptDir) 'configs\config.yaml' }
    if ($svcConfig -and (-not [System.IO.Path]::IsPathRooted($svcConfig))) {
        $svcConfig = Join-Path (Get-Location) $svcConfig
    }

    if ($Autorun -eq 'service' -or $Autorun -eq 'both') {
        # Refresh: hentikan & hapus service lama agar argumen/config terbaru dipakai.
        & $AgentExe service stop 2>$null | Out-Null
        & $AgentExe service uninstall 2>$null | Out-Null

        & $AgentExe service install --autorun service --config "$svcConfig"
        if ($LASTEXITCODE -ne 0) {
            Write-Log 'Install service gagal.' 'WARN'
        }
        else {
            Write-Log "Service terpasang (config: $svcConfig)."
            & $AgentExe service start
            if ($LASTEXITCODE -ne 0) {
                Write-Log 'Start service gagal; periksa dengan "print-agent service status".' 'WARN'
            }
            else {
                Start-Sleep -Seconds 2
                & $AgentExe service status
                Write-Log 'Service agent berjalan.'
            }
        }
    }

    if ($Autorun -eq 'user' -or $Autorun -eq 'both') {
        & $AgentExe service install --autorun user --config "$svcConfig"
        if ($LASTEXITCODE -ne 0) {
            Write-Log 'Install registry autorun (HKCU) gagal.' 'WARN'
        }
        else {
            Write-Log "Registry autorun (HKCU) terpasang (config: $svcConfig)."
        }
    }
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

Write-Log "=== Deploy RSCPrinting mulai ==="
Assert-Administrator

$script:Caps = Get-Capabilities
$script:ThermalDriver = $ThermalDriver
$script:ThermalDriverPattern = $ThermalDriverPattern
$script:DriverName = $DriverName
$script:ApdCacheDir = $ApdCacheDir
$script:GhToken = $GhToken
$psMajor = $PSVersionTable.PSVersion.Major
$osCaption = (Get-WmiObject Win32_OperatingSystem).Caption
Write-Log "OS: $osCaption | PowerShell: $psMajor | PrinterSpec: $PrinterSpec"
if ($script:GhToken) { Write-Log 'GitHub token: tersedia.' } else { Write-Log 'GitHub token: tidak ada (repo privat akan gagal unduh; set -GhToken / env PRINT_AGENT_GH_TOKEN).' 'WARN' }

if (-not (Test-Path -LiteralPath $PrinterSpec)) { throw "File spec printer tidak ditemukan: $PrinterSpec" }

if ((-not $script:Caps.AddPrinter) -and (-not $DriverName)) {
    throw 'Jalur fallback Windows 7 membutuhkan -DriverName agar INF driver bisa ditemukan.'
}

if (($ConfigPath -or $TestPrint) -and (-not $AgentExe)) {
    Write-Log 'ConfigPath/TestPrint diberikan tanpa -AgentExe; langkah tersebut dilewati.' 'WARN'
}
if ($AgentExe -and -not (Test-Path -LiteralPath $AgentExe)) {
    throw "Binary agent tidak ditemukan: $AgentExe"
}

$rows = @(Import-Csv -LiteralPath $PrinterSpec)
if ($rows.Count -eq 0) { throw "Tidak ada baris printer di $PrinterSpec." }
Write-Log "Ditemukan $($rows.Count) entri printer."

# Provisioning driver APD (opsional; dilewati bila tidak ada sumber).
$script:DriverProvisionFailed = $false
$hasDriverSource = $InstallerPath -or $ApdUrl -or ($DriverSource -eq 'local') -or ($DriverSource -eq 'github' -and $GithubRepo)
if ($hasDriverSource) {
    try {
        Provision-Driver | Out-Null
    }
    catch {
        $script:DriverProvisionFailed = $true
        Write-Log "Provisioning driver gagal: $($_.Exception.Message)" 'ERROR'
        Write-Log "Melanjutkan dengan fallback driver 'Generic / Text Only' untuk queue thermal." 'WARN'
    }
}
else {
    Write-Log 'Tidak ada sumber driver APD dikonfigurasi; lewati instalasi driver.' 'WARN'
}

$createdNames = @()
$thermalNames = @()
$defaultName = ''
$summary = @()

foreach ($row in $rows) {
    $name = ''
    if ($row.PSObject.Properties['Name'] -and $row.Name) { $name = $row.Name.Trim() }
    if (-not $name) { Write-Log 'Baris tanpa kolom Name dilewati.' 'WARN'; continue }

    $isThermalRow = $false
    if ($row.PSObject.Properties['Thermal'] -and $row.Thermal) {
        $tval = $row.Thermal.Trim().ToLower()
        if ($tval -eq 'true' -or $tval -eq '1' -or $tval -eq 'yes') { $isThermalRow = $true }
    }

    $model = $DriverName
    if ($isThermalRow) {
        $model = Resolve-ThermalDriver
    }
    elseif ($row.PSObject.Properties['Model'] -and $row.Model) {
        $model = $row.Model.Trim()
    }

    $port = ''
    if ($row.PSObject.Properties['Port'] -and $row.Port) { $port = $row.Port.Trim() }

    $isDefault = $false
    if ($row.PSObject.Properties['Default'] -and $row.Default) {
        $val = $row.Default.Trim().ToLower()
        if ($val -eq 'true' -or $val -eq '1' -or $val -eq 'yes') { $isDefault = $true }
    }

    $ok = New-Queue -Name $name -Model $model -Port $port -Default $isDefault

    $summary += New-Object PSObject -Property @{
        Queue   = $name
        Driver  = $model
        Port    = $port
        Default = $isDefault
        Status  = $(if ($ok) { 'OK' } else { 'SKIP' })
    }

    if ($ok) {
        $createdNames += $name
        if ($isThermalRow) { $thermalNames += $name }
        if ($isDefault -and -not $defaultName) { $defaultName = $name }
    }
}

if ($createdNames.Count -gt 0 -and -not $defaultName) { $defaultName = $createdNames[0] }

if ($TestPrint -and $AgentExe) {
    foreach ($n in $createdNames) { Invoke-TestPrint -Name $n }
}

if ($ConfigPath -and $AgentExe) {
    Update-AgentConfig -Names $createdNames -DefaultName $defaultName -Thermal $thermalNames
}

if ($AgentExe) { Install-AgentService }

Write-Log "=== Ringkasan ==="
$summary | Format-Table Queue, Driver, Port, Default, Status -AutoSize | Out-String | Write-Host

if (($summary | Where-Object { $_.Status -eq 'SKIP' }).Count -gt 0) {
    Write-Log 'Sebagian queue dilewati (lihat status SKIP).' 'WARN'
}

Write-Log 'Deploy selesai.'
if ($script:DriverProvisionFailed) {
    Write-Log 'Selesai dengan peringatan: driver APD tidak terpasang (queue thermal memakai Generic / Text Only).' 'WARN'
    exit 2
}
exit 0
