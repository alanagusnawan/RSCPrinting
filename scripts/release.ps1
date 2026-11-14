#Requires -Version 5.1
<#
  release.ps1 - Rilis RSCPrinting print-agent dari komputer lokal (tanpa GitHub Actions).

  Alur: hitung versi (conventional commits) -> build 6 binary -> checksums.txt ->
  git tag -> push tag -> buat GitHub Release + upload asset via REST API.

  Butuh:
    - Go (go build untuk cross-compile)
    - Git (dengan akses push ke origin)
    - PAT GitHub dengan akses Contents: Read & Write (fine-grained) atau scope "repo" (classic),
      diset di environment GH_TOKEN (atau GITHUB_TOKEN), atau dilewatkan via -Token.

  Contoh:
    $env:GH_TOKEN = "ghp_xxx"
    powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1
    powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1 -Bump minor
    powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1 -Version v1.0.0
    powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1 -DryRun
#>
[CmdletBinding()]
param(
    [string]$Version = '',
    [ValidateSet('', 'patch', 'minor', 'major')]
    [string]$Bump = '',
    [string]$Token = '',
    [string]$Repo = '',
    [switch]$RunTests,
    [switch]$SkipBuild,
    [switch]$SkipPush,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
$RepoRoot = Split-Path -Parent $ScriptDir
$DistDir = Join-Path $RepoRoot 'dist'
$Pkg = 'github.com/rscprinting/print-agent/internal/version'

$Assets = @(
    'print-agent-windows-amd64.exe',
    'print-agent-windows-386.exe',
    'print-agent-linux-amd64',
    'print-agent-linux-arm64',
    'print-agent-darwin-amd64',
    'print-agent-darwin-arm64'
)

function Info([string]$msg) { Write-Host "[release] $msg" }
function Fail([string]$msg) { Write-Error "[release] $msg"; exit 1 }

# Ambil PAT yang tersimpan untuk github.com via Git Credential Manager,
# agar tidak perlu set GH_TOKEN manual bila sudah pernah `git push`.
function Get-StoredGitHubToken {
    $eap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = "protocol=https`nhost=github.com`n`n" | git credential fill 2>$null
    }
    finally {
        $ErrorActionPreference = $eap
    }
    $line = ($out | Where-Object { $_ -like 'password=*' } | Select-Object -First 1)
    if ($line) { return ($line -replace '^password=', '') }
    return ''
}

function Invoke-Git {
    param([string[]]$GitArgs)
    # Native command (git) menulis progres ke stderr; dengan EAP=Stop itu bisa
    # dianggap error fatal. Turunkan EAP sementara agar hanya exit code yang dinilai.
    $eap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & git @GitArgs 2>&1
        $code = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $eap
    }
    if ($code -ne 0) { throw "git $($GitArgs -join ' ') gagal (exit $code)" }
    return $out
}

# --- 0. Cek prasyarat -------------------------------------------------------
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail "Go tidak ditemukan di PATH." }
if (-not (Get-Command git -ErrorAction SilentlyContinue)) { Fail "Git tidak ditemukan di PATH." }

Push-Location $RepoRoot
try {
    # --- 1. Repo bersih -----------------------------------------------------
    $dirty = & git status --porcelain
    if ($dirty) {
        Info "PERINGATAN: ada perubahan belum di-commit:"
        $dirty | ForEach-Object { Write-Host "  $_" }
        Info "Rilis akan memakai commit terakhir (HEAD)."
    }

    Invoke-Git @('fetch', '--tags') | Out-Null

    # --- 2. Owner/repo ------------------------------------------------------
    if (-not $Repo) {
        $url = ((Invoke-Git @('remote', 'get-url', 'origin')) -join "`n").Trim()
        if ($url -match 'github\.com[/:]([^/]+)/([^/]+?)(\.git)?$') {
            $Repo = "$($Matches[1])/$($Matches[2])"
        }
        else {
            Fail "Tidak bisa membaca owner/repo dari remote '$url'. Gunakan -Repo owner/name."
        }
    }
    Info "Repo: $Repo"

    # --- 3. Hitung versi ----------------------------------------------------
    if ($Version) {
        $tag = if ($Version.StartsWith('v')) { $Version } else { "v$Version" }
    }
    else {
        $latest = (& git tag --list 'v*' --sort=-v:refname | Select-Object -First 1)
        if (-not $latest) {
            $tag = 'v0.1.0'
        }
        else {
            $parts = $latest.TrimStart('v').Split('.')
            $ma = [int]$parts[0]; $mi = [int]$parts[1]; $pa = [int]$parts[2]
            $range = "$latest..HEAD"
            $full = (& git log --format=%B $range) -join "`n"
            $subj = (& git log --format=%s $range) -join "`n"

            if ($Bump) {
                switch ($Bump) {
                    'major' { $ma++; $mi = 0; $pa = 0 }
                    'minor' { $mi++; $pa = 0 }
                    default { $pa++ }
                }
            }
            elseif ($full -match 'BREAKING CHANGE' -or $full -match '(^|\n)[a-zA-Z]+(\([^)]+\))?!:') {
                $ma++; $mi = 0; $pa = 0
            }
            elseif ($subj -match '(?m)^feat(\([^)]+\))?:') {
                $mi++; $pa = 0
            }
            else {
                $pa++
            }
            $tag = "v$ma.$mi.$pa"
        }
    }
    $semver = $tag.TrimStart('v')
    Info "Versi rilis: $tag (semver $semver)"

    $tagExists = $false
    if (& git rev-parse -q --verify "refs/tags/$tag" 2>$null) {
        $tagExists = $true
        $tagCommit = ((& git rev-parse "$tag^{}") -join '').Trim()
        $headCommit = ((& git rev-parse 'HEAD') -join '').Trim()
        if ($tagCommit -ne $headCommit) {
            Fail "Tag $tag sudah ada tetapi menunjuk commit lain ($tagCommit). Naikkan versi."
        }
        Info "Tag $tag sudah ada di commit HEAD; lanjut ke pembuatan release."
    }

    $commit = ((& git rev-parse --short HEAD) -join '').Trim()
    $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    $ldflags = "-s -w -X ${Pkg}.Version=${semver} -X ${Pkg}.GitCommit=${commit} -X ${Pkg}.BuildDate=${date}"

    # --- 4. Build binary ----------------------------------------------------
    function Build-Target {
        param([string]$Goos, [string]$Goarch, [string]$Name, [string[]]$Tags)
        Info "build $Name ($Goos/$Goarch)"
        $env:CGO_ENABLED = '0'; $env:GOOS = $Goos; $env:GOARCH = $Goarch
        $bargs = @('build')
        if ($Tags) { $bargs += @('-tags', ($Tags -join ',')) }
        $bargs += @('-ldflags', $ldflags, '-o', (Join-Path $DistDir $Name), './cmd/print-agent')
        & go @bargs
        if ($LASTEXITCODE -ne 0) { throw "build gagal: $Name" }
    }

    if (-not $SkipBuild) {
        if (-not (Test-Path -LiteralPath $DistDir)) { New-Item -ItemType Directory -Path $DistDir | Out-Null }
        Build-Target windows amd64 'print-agent-windows-amd64.exe' @()
        Build-Target windows 386   'print-agent-windows-386.exe'   @()
        Build-Target linux   amd64 'print-agent-linux-amd64'        @('headless')
        Build-Target linux   arm64 'print-agent-linux-arm64'        @('headless')
        Build-Target darwin  amd64 'print-agent-darwin-amd64'       @('headless')
        Build-Target darwin  arm64 'print-agent-darwin-arm64'       @('headless')

        if ($RunTests) {
            Info "go test ./..."
            & go test ./...
            if ($LASTEXITCODE -ne 0) { throw "go test gagal" }
        }
    }
    else {
        Info "SkipBuild: memakai binary yang ada di dist/."
    }

    # --- 5. checksums.txt ---------------------------------------------------
    $lines = foreach ($n in $Assets) {
        $p = Join-Path $DistDir $n
        if (-not (Test-Path -LiteralPath $p)) { Fail "Binary tidak ditemukan: $p" }
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $p).Hash.ToLower()
        "{0}  {1}" -f $hash, $n
    }
    Set-Content -LiteralPath (Join-Path $DistDir 'checksums.txt') -Value $lines -Encoding ASCII
    Info "checksums.txt dibuat."

    if ($DryRun) {
        Info "DRYRUN: selesai (tidak tag/push/release)."
        Write-Host ($lines -join "`n")
        return
    }

    # --- 6. Tag + push ------------------------------------------------------
    if (-not $tagExists) {
        Info "membuat tag $tag"
        Invoke-Git @('tag', '-a', $tag, '-m', "release $tag") | Out-Null
        if (-not $SkipPush) {
            Info "push tag ke origin"
            Invoke-Git @('push', 'origin', $tag) | Out-Null
        }
        else {
            Info "SkipPush: tag tidak di-push."
        }
    }
    else {
        Info "Lewati pembuatan/push tag (sudah ada)."
    }

    # --- 7. GitHub Release via REST API ------------------------------------
    $tok = if ($Token) { $Token }
    elseif ($env:GH_TOKEN) { $env:GH_TOKEN }
    elseif ($env:GITHUB_TOKEN) { $env:GITHUB_TOKEN }
    else { Get-StoredGitHubToken }
    if (-not $tok) { Fail "Token tidak ada. Set environment GH_TOKEN (atau GITHUB_TOKEN), pakai -Token, atau login git untuk github.com." }
    Info "token: $([bool]$tok) (tidak ditampilkan)"

    $headers = @{
        Authorization          = "Bearer $tok"
        Accept                 = 'application/vnd.github+json'
        'X-GitHub-Api-Version' = '2022-11-28'
        'User-Agent'           = 'rsc-release-script'
    }

    Info "membuat GitHub Release $tag"
    $body = @{
        tag_name               = $tag
        name                   = $tag
        generate_release_notes = $true
        draft                  = $false
        prerelease             = $false
    } | ConvertTo-Json

    $release = Invoke-RestMethod -Method Post `
        -Uri "https://api.github.com/repos/$Repo/releases" `
        -Headers $headers -Body $body -ContentType 'application/json'

    $uploadBase = ($release.upload_url -replace '\{.*\}', '')
    foreach ($n in ($Assets + 'checksums.txt')) {
        Info "upload $n"
        $url = '{0}?name={1}' -f $uploadBase, $n
        Invoke-RestMethod -Method Post -Uri $url -Headers $headers `
            -InFile (Join-Path $DistDir $n) -ContentType 'application/octet-stream' | Out-Null
    }

    Info "Selesai: $($release.html_url)"
}
finally {
    Pop-Location
}
