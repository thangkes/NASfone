# Publish a new NASfone version on GitHub in one go:
#   bump versions -> commit -> build APK + Windows installer -> SHA256SUMS -> tag -> GitHub Release
#
#   .\scripts\release.ps1 0.2.0                          # notes generated from commits
#   .\scripts\release.ps1 0.2.0 -NotesFile notes.md      # your own release notes
#   .\scripts\release.ps1 0.2.0 -Prerelease
#
# Needs: a clean git tree on main, gh CLI logged in, and the release signing key in
# ~\.nasfone-signing (the installed apps only accept updates signed with that key).
# Installed apps (Windows and Android) find the new release on their own.
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$NotesFile,
    [switch]$Prerelease
)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$gh = "C:\Program Files\GitHub CLI\gh.exe"
if (-not (Test-Path $gh)) { $gh = "gh" }
$utf8 = New-Object Text.UTF8Encoding $false

if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)$') { throw "Version must look like 1.2.3" }
$code = [int]$Matches[1] * 10000 + [int]$Matches[2] * 100 + [int]$Matches[3]
$tag = "v$Version"

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
    Write-Host "== version $Version (code $code) ==" -ForegroundColor Cyan
    [IO.File]::WriteAllText("$root\client-windows\VERSION", "$Version`n", $utf8)
    $gradle = "$root\server-android\app\build.gradle.kts"
    $g = [IO.File]::ReadAllText($gradle, $utf8)
    $g = $g -replace 'versionCode = \d+', "versionCode = $code" -replace 'versionName = "[^"]*"', "versionName = ""$Version"""
    [IO.File]::WriteAllText($gradle, $g, $utf8)

    # ---- build ---------------------------------------------------------------
    & "$PSScriptRoot\build-server.ps1"
    if (-not $?) { throw "server build failed" }
    & "$PSScriptRoot\build-windows.ps1" -Version $Version
    if (-not $?) { throw "windows build failed" }
    $ErrorActionPreference = "Continue" # apksigner prints warnings on stderr

    # The APK must carry the release key, never the debug one.
    $apkSrc = "$root\server-android\app\build\outputs\apk\release\app-release.apk"
    $bt = Get-ChildItem "$env:LOCALAPPDATA\Android\Sdk\build-tools" -Directory | Sort-Object Name | Select-Object -Last 1
    $env:JAVA_HOME = "C:\Program Files\Android\Android Studio\jbr"
    $certs = (& "$($bt.FullName)\apksigner.bat" verify --print-certs $apkSrc 2>$null) -join "`n"
    if ($certs -notmatch 'CN=NASfone') { throw "APK is not signed with the NASfone release key:`n$certs" }

    # ---- assets --------------------------------------------------------------
    $out = "$root\dist\release-$Version"
    if (Test-Path $out) { Remove-Item $out -Recurse -Force }
    New-Item -ItemType Directory -Force $out | Out-Null
    Copy-Item $apkSrc "$out\NASfone-Server-$Version.apk"
    Copy-Item "$root\client-windows\dist\NASfone-Setup-$Version.exe" $out
    $sums = Get-ChildItem $out -File | ForEach-Object {
        "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
    }
    [IO.File]::WriteAllText("$out\SHA256SUMS.txt", (($sums -join "`n") + "`n"), $utf8)
    Get-Content "$out\SHA256SUMS.txt"

    # ---- commit, tag, publish ------------------------------------------------
    Write-Host "== publish $tag ==" -ForegroundColor Cyan
    git add client-windows/VERSION server-android/app/build.gradle.kts client-windows/winres/winres.json
    git commit -q -m "Release $tag"
    if ($LASTEXITCODE) { throw "git commit failed" }
    git tag -a $tag -m "NASfone $Version"
    git push -q origin main
    if ($LASTEXITCODE) { throw "git push failed" }
    git push -q origin $tag
    if ($LASTEXITCODE) { throw "git push tag failed" }

    $ghArgs = @("release", "create", $tag, "--title", "NASfone $Version")
    if ($NotesFile) { $ghArgs += @("--notes-file", (Resolve-Path $NotesFile).Path) } else { $ghArgs += "--generate-notes" }
    if ($Prerelease) { $ghArgs += "--prerelease" }
    $ghArgs += (Get-ChildItem $out -File).FullName
    & $gh @ghArgs
    if ($LASTEXITCODE) { throw "gh release create failed" }
    Write-Host "Released $tag" -ForegroundColor Green
} finally {
    Pop-Location
}
