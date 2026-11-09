#Requires -Version 2.0
<#
  install.ps1 - Instalasi RSCPrinting satu perintah (Windows, Administrator).

  Menggabungkan provisioning driver (APD) + pembuatan queue + sinkron config +
  install & start service agent, dengan default diambil dari blok `provision:`
  di config.yaml (via `print-agent config get-provision`). Param CLI menimpa default.

  Contoh (tanpa argumen, pakai config):
    powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1

  Contoh override:
    powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1 `
      -InstallerPath C:\installer\APD.exe -TestPrint
#>
[CmdletBinding()]
param(
    [string]$InstallerPath = '',
    [string]$InstallerArgs = '',
    [string]$DriverName = '',
    [string]$DriverSource = '',
    [string]$GithubRepo = '',
    [string]$GithubTag = '',
    [string]$ApdAssetPattern = '',
    [string]$ApdUrl = '',
    [string]$ApdSha256 = '',
    [string]$ApdDir = '',
    [string]$ApdCacheDir = '',
    [string]$GhToken = '',
    [string]$ThermalDriver = '',
    [string]$ThermalDriverPattern = '',
    [string]$PrinterSpec = '',
    [string]$ConfigPath = '',
    [string]$AgentExe = '',
    [ValidateSet('service', 'user', 'both')]
    [string]$Autorun = 'both',
    [switch]$TestPrint,
    [switch]$Force,
    [switch]$ForceDriverUpdate,
    [string]$LogPath = ''
)

$ErrorActionPreference = 'Stop'

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
$RepoRoot = Split-Path -Parent $ScriptDir

if (-not $ConfigPath) { $ConfigPath = Join-Path $RepoRoot 'configs\config.yaml' }
if (-not $PrinterSpec) { $PrinterSpec = Join-Path $ScriptDir 'printers.csv' }

if (-not $AgentExe) {
    $candidates = @(
        (Join-Path $RepoRoot 'dist\print-agent-windows-amd64.exe'),
        (Join-Path $RepoRoot 'dist\print-agent-windows-386.exe'),
        (Join-Path $RepoRoot 'print-agent.exe')
    )
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c) { $AgentExe = $c; break }
    }
}

function Write-Info {
    param([string]$Message)
    $ts = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    Write-Host "[$ts][install] $Message"
}

# Baca blok provision dari config via helper Go (tanpa parsing YAML di PowerShell).
$provision = @{}
if ($AgentExe -and (Test-Path -LiteralPath $AgentExe)) {
    try {
        $out = & $AgentExe config get-provision --config "$ConfigPath" 2>$null
        foreach ($line in @($out)) {
            $idx = $line.IndexOf('=')
            if ($idx -gt 0) {
                $k = $line.Substring(0, $idx).Trim()
                $v = $line.Substring($idx + 1).Trim()
                if ($k) { $provision[$k] = $v }
            }
        }
        Write-Info "Default provision dibaca dari '$ConfigPath'."
    }
    catch {
        Write-Info "Gagal membaca provision dari config: $($_.Exception.Message)"
    }
}
else {
    Write-Info "Binary agent tidak ditemukan; lanjut dengan parameter CLI saja."
}

# Terapkan default dari config bila param CLI kosong.
if (-not $DriverSource) { $DriverSource = $provision['driverSource'] }
if (-not $DriverSource) { $DriverSource = 'local' }
if (-not $GithubRepo) { $GithubRepo = $provision['githubRepo'] }
if (-not $GithubTag) { $GithubTag = $provision['githubTag'] }
if (-not $ApdAssetPattern) { $ApdAssetPattern = $provision['assetPattern'] }
if (-not $ApdUrl) { $ApdUrl = $provision['url'] }
if (-not $ApdSha256) { $ApdSha256 = $provision['sha256'] }
if (-not $ApdDir) { $ApdDir = $provision['apdDir'] }
if (-not $ThermalDriverPattern) { $ThermalDriverPattern = $provision['thermalDriverPattern'] }
if (-not $GhToken) { $GhToken = $env:PRINT_AGENT_GH_TOKEN }

# Untuk sumber lokal, default folder ke <repo>\dist dan jadikan absolut.
if ($DriverSource -eq 'local') {
    if (-not $ApdDir) { $ApdDir = 'dist' }
}
if ($ApdDir -and (-not [System.IO.Path]::IsPathRooted($ApdDir))) {
    $ApdDir = Join-Path $RepoRoot $ApdDir
}
# Cache unduhan default ke folder yang sama agar hasil unduhan langsung terpasang dari sana.
if (-not $ApdCacheDir) { $ApdCacheDir = $ApdDir }
if ($ApdCacheDir -and (-not [System.IO.Path]::IsPathRooted($ApdCacheDir))) {
    $ApdCacheDir = Join-Path $RepoRoot $ApdCacheDir
}

$deployPath = Join-Path $ScriptDir 'deploy-printers.ps1'
if (-not (Test-Path -LiteralPath $deployPath)) { throw "Tidak menemukan engine: $deployPath" }

$deployArgs = @{
    InstallerPath        = $InstallerPath
    InstallerArgs        = $InstallerArgs
    DriverName           = $DriverName
    DriverSource         = $DriverSource
    GithubRepo           = $GithubRepo
    GithubTag            = $GithubTag
    ApdAssetPattern      = $ApdAssetPattern
    ApdUrl               = $ApdUrl
    ApdSha256            = $ApdSha256
    ApdDir               = $ApdDir
    ApdCacheDir          = $ApdCacheDir
    GhToken              = $GhToken
    ThermalDriver        = $ThermalDriver
    ThermalDriverPattern = $ThermalDriverPattern
    PrinterSpec          = $PrinterSpec
    ConfigPath           = $ConfigPath
    AgentExe             = $AgentExe
    Autorun              = $Autorun
    LogPath              = $LogPath
}
if ($TestPrint) { $deployArgs['TestPrint'] = $true }
if ($Force) { $deployArgs['Force'] = $true }
if ($ForceDriverUpdate) { $deployArgs['ForceDriverUpdate'] = $true }

Write-Info "Menjalankan provisioning (source=$DriverSource, config=$ConfigPath)."
& $deployPath @deployArgs
