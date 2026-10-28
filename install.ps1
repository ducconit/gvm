# ==============================================================================
# g (gvm) — Cross-Platform Golang Version Manager One-Line Installer (Windows)
# Repository: https://github.com/ducconit/gvm
# Author:     DNT (@ducconit)
#
# Usage (PowerShell):
#   irm https://raw.githubusercontent.com/ducconit/gvm/main/install.ps1 | iex
# ==============================================================================

$ErrorActionPreference = "Stop"

$Repo = "ducconit/gvm"
$GvmHome = if ($env:GVM_HOME) { $env:GVM_HOME } else { Join-Path $HOME ".gvm" }
$BinDir  = Join-Path $GvmHome "bin"
$GoRoot  = Join-Path $GvmHome "current"
$GoBin   = Join-Path $GoRoot "bin"
$Version = if ($env:GVM_VERSION) { $env:GVM_VERSION } else { "latest" }

$ArchRaw = $env:PROCESSOR_ARCHITECTURE
$GoArch = switch -Wildcard ($ArchRaw) {
    "*ARM64*" { "arm64" }
    default   { "amd64" }
}

$AssetName = "g-windows-$GoArch.exe"
if ($Version -eq "latest") {
    $DownloadUrl = "https://github.com/$Repo/releases/latest/download/$AssetName"
} else {
    $Tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
    $DownloadUrl = "https://github.com/$Repo/releases/download/$Tag/$AssetName"
}

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $GvmHome "versions") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $GvmHome "tmp") | Out-Null

$TargetExe = Join-Path $BinDir "g.exe"
Write-Host "==> Downloading $AssetName ($Version) from $DownloadUrl..." -ForegroundColor Cyan
Invoke-WebRequest -Uri $DownloadUrl -OutFile $TargetExe -UseBasicParsing

# Add $GvmHome\bin and $GvmHome\current\bin to User PATH if not already present
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $UserPath) { $UserPath = "" }
$PathsToAdd = @($BinDir, $GoBin)
$Updated = $false
foreach ($p in $PathsToAdd) {
    if ($UserPath -notlike "*$p*") {
        $UserPath = if ($UserPath) { "$p;$UserPath" } else { $p }
        $Updated = $true
    }
}
if ($Updated) {
    [Environment]::SetEnvironmentVariable("Path", $UserPath, "User")
    $env:Path = "$BinDir;$GoBin;$env:Path"
}
[Environment]::SetEnvironmentVariable("GOROOT", $GoRoot, "User")
$env:GOROOT = $GoRoot

Write-Host "✓ Installed g to $TargetExe" -ForegroundColor Green
Write-Host "✓ Try running: g -v  or  g i latest" -ForegroundColor Green
