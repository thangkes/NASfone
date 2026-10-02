# Publish a new NASfone version on GitHub in one go. One release, two apps:
#   NASfone-Android-<v>.apk        (server or client role, chosen in the app)
#   NASfone-Windows-Setup-<v>.exe  (server or client role, chosen in the app)
# Steps: bump versions -> build both -> verify APK signing -> SHA256SUMS -> commit -> tag v<v> -> GitHub Release
#
#   .\scripts\release.ps1 0.2.0 -NotesFile notes.md
#   .\scripts\release.ps1 0.2.1-beta.1 -NotesFile notes.md   # a version with "-" is a pre-release
#
# -LegacyNames also attaches copies named like 0.1.x expects (NASfone-Server-*.apk,
# NASfone-Windows-Client-Setup-*.exe) so installs from before the merge can update.
#
# Needs: a clean git tree on main, gh CLI logged in, and the release signing key in
# ~\.nasfone-signing (the installed apps only accept updates signed with that key).
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$NotesFile,
    [switch]$LegacyNames
)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$gh = "C:\Program Files\GitHub CLI\gh.exe"
if (-not (Test-Path $gh)) { $gh = "gh" }
$utf8 = New-Object Text.UTF8Encoding $false

if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.]+)?$') { throw "Version must look like 1.2.3 or 1.2.3-beta.1" }
# Android versionCode grows with every release: (MAJOR*10000+MINOR*100+PATCH)*100 + n,
# n = beta number (beta.3 -> 3) or 99 for a final release.
$base = [int]$Matches[1] * 10000 + [int]$Matches[2] * 100 + [int]$Matches[3]
$pre = [bool]$Matches[4]
$n = 99
if ($pre) { if ($Matches[4] -match '(\d+)$') { $n = [Math]::Min([int]$Matches[1], 98) } else { $n = 1 } }
$code = $base * 100 + $n
$tag = "v$Version"
$notes = (Resolve-Path $NotesFile).Path

Push-Location $root
try {
    # ---- preflight -----------------------------------------------------------
    if (git status --porcelain) { throw "Working tree is not clean - commit or stash first." }
    if ((git branch --show-current) -ne "main") { throw "Release from the main branch." }
    if (git tag -l $tag) { throw "Tag $tag already exists." }
    if (-not (Test-Path "$env:USERPROFILE\.nasfone-signing\keystore.properties")) {
        throw "Release signing key missing (~\.nasfone-signing). Restore it from your backup - a new key breaks updates."
    }
    & $gh auth status *> $null
    if ($LASTEXITCODE) { throw "gh is not logged in (gh auth login)." }

    # ---- bump versions -------------------------------------------------------
    Write-Host "== version $Version (Android code $code) ==" -ForegroundColor Cyan
    [IO.File]::WriteAllText("$root\windows\VERSION", "$Version`n", $utf8)
    $gradle = "$root\android\app\build.gradle.kts"
    $g = [IO.File]::ReadAllText($gradle, $utf8)
    $g = $g -replace 'versionCode = \d+', "versionCode = $code" -replace 'versionName = "[^"]*"', "versionName = ""$Version"""
    [IO.File]::WriteAllText($gradle, $g, $utf8)

    # ---- build ---------------------------------------------------------------
    & "$PSScriptRoot\build-android.ps1"
    if (-not $?) { throw "Android build failed" }
    & "$PSScriptRoot\build-windows.ps1" -Version $Version
    if (-not $?) { throw "Windows build failed" }
    $ErrorActionPreference = "Continue" # apksigner and git print to stderr

    # The APK must carry the release key, never the debug one.
    $apkSrc = "$root\android\app\build\outputs\apk\release\app-release.apk"
    $bt = Get-ChildItem "$env:LOCALAPPDATA\Android\Sdk\build-tools" -Directory | Sort-Object Name | Select-Object -Last 1
    $env:JAVA_HOME = "C:\Program Files\Android\Android Studio\jbr"
    $certs = (& "$($bt.FullName)\apksigner.bat" verify --print-certs $apkSrc 2>$null) -join "`n"
    if ($certs -notmatch 'CN=NASfone') { throw "APK is not signed with the NASfone release key:`n$certs" }

    # ---- assets --------------------------------------------------------------
    $out = "$root\dist\release-$Version"
    if (Test-Path $out) { Remove-Item $out -Recurse -Force }
    New-Item -ItemType Directory -Force $out | Out-Null
    Copy-Item $apkSrc "$out\NASfone-Android-$Version.apk"
    Copy-Item "$root\windows\dist\NASfone-Windows-Setup-$Version.exe" $out
    if ($LegacyNames) {
        Copy-Item $apkSrc "$out\NASfone-Server-$Version.apk"
        Copy-Item "$root\windows\dist\NASfone-Windows-Setup-$Version.exe" "$out\NASfone-Windows-Client-Setup-$Version.exe"
    }
    $sums = Get-ChildItem $out -File | ForEach-Object {
        "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
    }
    [IO.File]::WriteAllText("$out\SHA256SUMS.txt", (($sums -join "`n") + "`n"), $utf8)
    Get-Content "$out\SHA256SUMS.txt"

    # ---- commit, tag, publish ------------------------------------------------
    Write-Host "== publish $tag ==" -ForegroundColor Cyan
    git add windows/VERSION android/app/build.gradle.kts windows/winres/winres.json
    git commit -q -m "Release $tag"
    if ($LASTEXITCODE) { throw "git commit failed" }
    git tag -a $tag -m "NASfone $Version"
    git push -q origin main
    if ($LASTEXITCODE) { throw "git push failed" }
    git push -q origin $tag
    if ($LASTEXITCODE) { throw "git push tag failed" }

    $ghArgs = @("release", "create", $tag, "--title", "NASfone $Version", "--notes-file", $notes)
    if ($pre) { $ghArgs += "--prerelease" }
    $ghArgs += (Get-ChildItem $out -File).FullName
    & $gh @ghArgs
    if ($LASTEXITCODE) { throw "gh release create failed" }
    Write-Host "Released $tag" -ForegroundColor Green
} finally {
    Pop-Location
}
