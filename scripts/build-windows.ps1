# Build NASfone.exe (client + server roles) and the installer windows\dist\NASfone-Windows-Setup-<version>.exe
#   .\scripts\build-windows.ps1                 # version from windows\VERSION
#   .\scripts\build-windows.ps1 -Version 0.2.0
param([string]$Version)
$ErrorActionPreference = "Stop"

$root = Split-Path $PSScriptRoot -Parent
$win = "$root\windows"
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User") + ";$env:USERPROFILE\go\bin"
if (-not $Version) { $Version = (Get-Content "$win\VERSION" -Raw).Trim() }
$v4 = "$Version.0"

Push-Location $win
$ErrorActionPreference = "Continue" # native tools write progress to stderr; check exit codes instead
try {
    Write-Host "== icons & resources ($Version) ==" -ForegroundColor Cyan
    go run ./tools/genicon
    if ($LASTEXITCODE) { throw "genicon failed" }
    # Stamp the version into the exe resources.
    $j = Get-Content winres\winres.json -Raw -Encoding UTF8 | ConvertFrom-Json
    $j.RT_MANIFEST.'#1'.'0409'.identity.version = $v4
    $vi = $j.RT_VERSION.'#1'.'0000'
    $vi.fixed.file_version = $v4; $vi.fixed.product_version = $v4
    $vi.info.'0409'.FileVersion = $Version; $vi.info.'0409'.ProductVersion = $Version
    # UTF-8 without BOM: go-winres rejects a byte-order mark.
    [IO.File]::WriteAllText("$win\winres\winres.json", ($j | ConvertTo-Json -Depth 10), (New-Object Text.UTF8Encoding $false))
    go-winres make --in winres\winres.json --arch amd64
    if ($LASTEXITCODE) { throw "go-winres failed" }

    Write-Host "== go build ==" -ForegroundColor Cyan
    go build -trimpath -ldflags="-s -w -H=windowsgui -X main.appVersion=$Version" -o build\NASfone.exe .
    if ($LASTEXITCODE) { throw "go build failed" }

    Write-Host "== dependencies ==" -ForegroundColor Cyan
    New-Item -ItemType Directory -Force installer\deps | Out-Null
    $rc = Get-ChildItem "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\Rclone.Rclone_*\rclone-*\rclone.exe" -ErrorAction SilentlyContinue | Select-Object -Last 1
    if (-not $rc) { throw "rclone.exe not found - winget install Rclone.Rclone" }
    Copy-Item $rc.FullName installer\deps\rclone.exe -Force
    if (-not (Test-Path installer\deps\winfsp.msi)) { throw "installer\deps\winfsp.msi missing (download the signed MSI from https://github.com/winfsp/winfsp/releases)" }
    if ((Get-AuthenticodeSignature installer\deps\winfsp.msi).Status -ne "Valid") { throw "winfsp.msi signature is not valid" }

    Write-Host "== Inno Setup ==" -ForegroundColor Cyan
    $iscc = @("$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe", "C:\Program Files (x86)\Inno Setup 6\ISCC.exe") | Where-Object { Test-Path $_ } | Select-Object -First 1
    if (-not $iscc) { throw "Inno Setup not found - winget install JRSoftware.InnoSetup" }
    & $iscc /Q "/DAppVersion=$Version" installer\nasfone.iss
    if ($LASTEXITCODE) { throw "ISCC failed ($LASTEXITCODE)" }
    $out = Get-Item "dist\NASfone-Windows-Setup-$Version.exe"
    Write-Host ("Installer: {0} ({1:N1} MB)" -f $out.FullName, ($out.Length / 1MB)) -ForegroundColor Green
} finally {
    Pop-Location
}
