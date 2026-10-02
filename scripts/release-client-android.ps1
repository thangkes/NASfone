# Publish NASfone Client (Android) as its own GitHub Release:
#   set the version -> build (arm64, release key) -> verify the signature -> SHA256SUMS -> tag android-client-v<version> -> release
#
#   .\scripts\release-client-android.ps1 0.1.0-beta.1 -NotesFile notes.md   # a version with "-" is a pre-release (beta)
#
# versionCode grows with every release: (MAJOR*10000 + MINOR*100 + PATCH) * 100 + n,
# where n is the beta number (beta.3 -> 3) and 99 for the final release.
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$NotesFile
)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$gh = "C:\Program Files\GitHub CLI\gh.exe"
if (-not (Test-Path $gh)) { $gh = "gh" }
$utf8 = New-Object Text.UTF8Encoding $false

if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.]+)?$') { throw "Version must look like 1.2.3 or 1.2.3-beta.1" }
$base = [int]$Matches[1] * 10000 + [int]$Matches[2] * 100 + [int]$Matches[3]
$beta = [bool]$Matches[4]
$n = 99
if ($beta) {
    if ($Matches[4] -match '(\d+)$') { $n = [Math]::Min([int]$Matches[1], 98) } else { $n = 1 }
}
$code = $base * 100 + $n
$tag = "android-client-v$Version"
$notes = (Resolve-Path $NotesFile).Path

Push-Location $root
try {
    if (git status --porcelain) { throw "Working tree is not clean - commit or stash first." }
    if ((git branch --show-current) -ne "main") { throw "Release from the main branch." }
    if (git tag -l $tag) { throw "Tag $tag already exists." }
    if (-not (Test-Path "$env:USERPROFILE\.nasfone-signing\keystore.properties")) { throw "Release signing key missing (~\.nasfone-signing)." }
    & $gh auth status *> $null
    if ($LASTEXITCODE) { throw "gh is not logged in (gh auth login)." }

    Write-Host "== version $Version (code $code) ==" -ForegroundColor Cyan
    $gradle = "$root\client-android\app\build.gradle.kts"
    $g = [IO.File]::ReadAllText($gradle, $utf8)
    $g = $g -replace 'versionCode = \d+', "versionCode = $code" -replace 'versionName = "[^"]*"', "versionName = ""$Version"""
    [IO.File]::WriteAllText($gradle, $g, $utf8)

    & "$PSScriptRoot\build-client-android.ps1"
    if (-not $?) { throw "build failed" }

    $ErrorActionPreference = "Continue" # apksigner prints warnings on stderr
    $apkSrc = "$root\client-android\app\build\outputs\apk\release\app-release.apk"
    $bt = Get-ChildItem "$env:LOCALAPPDATA\Android\Sdk\build-tools" -Directory | Sort-Object Name | Select-Object -Last 1
    $env:JAVA_HOME = "C:\Program Files\Android\Android Studio\jbr"
    $certs = (& "$($bt.FullName)\apksigner.bat" verify --print-certs $apkSrc 2>$null) -join "`n"
    if ($certs -notmatch 'CN=NASfone') { throw "APK is not signed with the NASfone release key:`n$certs" }

    $out = "$root\dist\android-client-$Version"
    if (Test-Path $out) { Remove-Item $out -Recurse -Force }
    New-Item -ItemType Directory -Force $out | Out-Null
    Copy-Item $apkSrc "$out\NASfone-Client-$Version.apk"
    $sums = Get-ChildItem $out -File | ForEach-Object {
        "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
    }
    [IO.File]::WriteAllText("$out\SHA256SUMS.txt", (($sums -join "`n") + "`n"), $utf8)
    Get-Content "$out\SHA256SUMS.txt"

    git add client-android/app/build.gradle.kts
    git commit -q -m "Release Android client $Version"
    if ($LASTEXITCODE) { throw "git commit failed" }
    git tag -a $tag -m "NASfone Client (Android) $Version"
    git push -q origin main
    if ($LASTEXITCODE) { throw "git push failed" }
    git push -q origin $tag
    if ($LASTEXITCODE) { throw "git push tag failed" }

    $ghArgs = @("release", "create", $tag, "--title", "NASfone Client (Android) $Version", "--notes-file", $notes)
    if ($beta) { $ghArgs += "--prerelease" } else { $ghArgs += "--latest=false" }
    $ghArgs += (Get-ChildItem $out -File).FullName
    & $gh @ghArgs
    if ($LASTEXITCODE) { throw "gh release create failed" }
    Write-Host "Released $tag" -ForegroundColor Green
} finally {
    Pop-Location
}
