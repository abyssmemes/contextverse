# ContextVerse installer for Windows (PowerShell).
# Installs the contextd CLI into a user-writable directory on PATH.
#
# Usage:
#   irm https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.ps1 | iex
#   $env:CONTEXTD_VERSION='v0.0.1'; .\scripts\install.ps1
#   .\scripts\install.ps1 -Version v0.0.1 -Dir "$env:LOCALAPPDATA\contextverse\bin"
#
# Env:
#   CONTEXTD_VERSION / CONTEXTD_INSTALL_DIR / CONTEXTD_REPO
#   GITHUB_TOKEN or GH_TOKEN — required while the repo is private

[CmdletBinding()]
param(
    [string]$Version = $(if ($env:CONTEXTD_VERSION) { $env:CONTEXTD_VERSION } else { "latest" }),
    [string]$Dir = $(if ($env:CONTEXTD_INSTALL_DIR) { $env:CONTEXTD_INSTALL_DIR } else { "" }),
    [string]$Repo = $(if ($env:CONTEXTD_REPO) { $env:CONTEXTD_REPO } else { "abyssmemes/contextverse" })
)

$ErrorActionPreference = "Stop"
$Binary = "contextd.exe"

function Write-Info([string]$Message) { Write-Host "==> $Message" }
function Write-Warn([string]$Message) { Write-Warning $Message }

function Get-GitHubToken {
    if ($env:GITHUB_TOKEN) { return $env:GITHUB_TOKEN }
    if ($env:GH_TOKEN) { return $env:GH_TOKEN }
    if (Get-Command gh -ErrorAction SilentlyContinue) {
        try { return (gh auth token 2>$null) } catch { return $null }
    }
    return $null
}

function Get-Headers {
    $token = Get-GitHubToken
    $headers = @{ "User-Agent" = "contextverse-installer" }
    if ($token) {
        $headers["Authorization"] = "Bearer $token"
    }
    return $headers
}

function Resolve-Arch {
    # Windows PowerShell: AMD64 / ARM64
    $a = $env:PROCESSOR_ARCHITECTURE
    switch ($a) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "unsupported architecture: $a" }
    }
}

function Resolve-InstallDir {
    if ($Dir) { return $Dir }
    $local = Join-Path $env:LOCALAPPDATA "contextverse\bin"
    return $local
}

function Ensure-Dir([string]$Path) {
    if (-not (Test-Path $Path)) {
        New-Item -ItemType Directory -Path $Path -Force | Out-Null
    }
}

function Resolve-Tag([string]$Ver) {
    if ($Ver -ne "latest") { return $Ver }
    $headers = Get-Headers
    $headers["Accept"] = "application/vnd.github+json"
    $uri = "https://api.github.com/repos/$Repo/releases/latest"
    $rel = Invoke-RestMethod -Uri $uri -Headers $headers
    return $rel.tag_name
}

function Install-FromRelease([string]$Tag, [string]$DestDir) {
    $arch = Resolve-Arch
    $ver = $Tag.TrimStart("v")
    $name = "contextd_${ver}_windows_${arch}.zip"
    $url = "https://github.com/$Repo/releases/download/$Tag/$name"
    Write-Info "Downloading $name ($Tag)"

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("contextverse-install-" + [guid]::NewGuid().ToString())
    Ensure-Dir $tmp
    $zip = Join-Path $tmp $name
    $headers = Get-Headers

    try {
        Invoke-WebRequest -Uri $url -Headers $headers -OutFile $zip
    } catch {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        throw
    }

    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $bin = Get-ChildItem -Path $tmp -Recurse -Filter $Binary | Select-Object -First 1
    if (-not $bin) {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        throw "binary $Binary not found in archive"
    }
    Ensure-Dir $DestDir
    Copy-Item -Force $bin.FullName (Join-Path $DestDir $Binary)
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

function Install-FromGo([string]$DestDir) {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "go not found; cannot fall back to go install"
    }
    Write-Info "No usable release binary — building with go install"
    $env:GOPRIVATE = if ($env:GOPRIVATE) { $env:GOPRIVATE } else { "github.com/abyssmemes/*" }
    $spec = if ($Version -eq "latest") { "@latest" } else { "@$Version" }
    & go install "github.com/$Repo/cmd/contextd$spec"
    if ($LASTEXITCODE -ne 0) { throw "go install failed" }

    $gobin = & go env GOBIN
    if (-not $gobin) {
        $gopath = & go env GOPATH
        $gobin = Join-Path $gopath "bin"
    }
    $src = Join-Path $gobin $Binary
    if (-not (Test-Path $src)) {
        # go on Windows may omit .exe in some setups — try without
        $alt = Join-Path $gobin "contextd"
        if (Test-Path $alt) { $src = $alt } else { throw "go install binary not found at $src" }
    }
    Ensure-Dir $DestDir
    Copy-Item -Force $src (Join-Path $DestDir $Binary)
}

function Ensure-UserPath([string]$DestDir) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -split ";" | Where-Object { $_ -eq $DestDir }) {
        return
    }
    Write-Info "Adding $DestDir to user PATH"
    $newPath = if ([string]::IsNullOrEmpty($userPath)) { $DestDir } else { "$userPath;$DestDir" }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $env:Path = "$DestDir;$env:Path"
}

Write-Host "ContextVerse Installer (Windows)"
Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
$dest = Resolve-InstallDir
Write-Host "  Repo:     $Repo"
Write-Host "  Version:  $Version"
Write-Host "  Arch:     $(Resolve-Arch)"
Write-Host "  Install:  $dest"
Write-Host ""

if (-not (Get-GitHubToken)) {
    # public releases don't require a token
}

Ensure-Dir $dest
$installed = $false
try {
    $tag = Resolve-Tag $Version
    Write-Info "Resolved release tag: $tag"
    Install-FromRelease -Tag $tag -DestDir $dest
    $installed = $true
} catch {
    Write-Warn "release install failed: $($_.Exception.Message)"
}

if (-not $installed) {
    Install-FromGo -DestDir $dest
}

$binPath = Join-Path $dest $Binary
if (-not (Test-Path $binPath)) {
    throw "install finished but $binPath is missing"
}

Ensure-UserPath $dest
Write-Info "Installed contextd → $binPath"
try { & $binPath version } catch { }

Write-Host ""
Write-Host "Done. Next (new shell may be needed for PATH):"
Write-Host "  contextd init solo"
Write-Host "  cd <project>; contextd activate"
