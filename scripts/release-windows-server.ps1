# Publish NASfone for Windows - Server as its own GitHub Release:
#   set the version -> commit -> build the installer -> SHA256SUMS -> tag windows-server-v<version> -> release
#
#   .\scripts\release-windows-server.ps1 0.2.0-beta.1 -NotesFile notes.md   # a version with "-" is a pre-release (beta)
#   .\scripts\release-windows-server.ps1 0.2.0 -NotesFile notes.md
#
# The Android server and the Windows client keep their own vX.Y.Z releases
# (scripts\release.ps1). Each app's updater only looks at releases that
# carry its own installer, so the lines never mix.
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$NotesFile
)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$gh = "C:\Program Files\GitHub CLI\gh.exe"
if (-not (Test-Path $gh)) { $gh = "gh" }
$utf8 = New-Object Text.UTF8Encoding $false

if ($Version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$') { throw "Version must look like 1.2.3 or 1.2.3-beta.1" }
$beta = $Version.Contains("-")
$tag = "windows-server-v$Version"
$notes = (Resolve-Path $NotesFile).Path

Push-Location $root
try {
    if (git status --porcelain) { throw "Working tree is not clean - commit or stash first." }
    if ((git branch --show-current) -ne "main") { throw "Release from the main branch." }
    if (git tag -l $tag) { throw "Tag $tag already exists." }
    & $gh auth status *> $null
    if ($LASTEXITCODE) { throw "gh is not logged in (gh auth login)." }

    [IO.File]::WriteAllText("$root\server-windows\VERSION", "$Version`n", $utf8)
    & "$PSScriptRoot\build-windows-server.ps1" -Version $Version
    if (-not $?) { throw "build failed" }

    $out = "$root\dist\windows-server-$Version"
    if (Test-Path $out) { Remove-Item $out -Recurse -Force }
    New-Item -ItemType Directory -Force $out | Out-Null
    Copy-Item "$root\server-windows\dist\NASfone-Windows-Server-Setup-$Version.exe" $out
    $sums = Get-ChildItem $out -File | ForEach-Object {
        "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
    }
    [IO.File]::WriteAllText("$out\SHA256SUMS.txt", (($sums -join "`n") + "`n"), $utf8)
    Get-Content "$out\SHA256SUMS.txt"

    $ErrorActionPreference = "Continue" # git writes progress to stderr
    git add server-windows/VERSION server-windows/winres/winres.json
    git commit -q -m "Release Windows server $Version"
    if ($LASTEXITCODE) { throw "git commit failed" }
    git tag -a $tag -m "NASfone for Windows - Server $Version"
    git push -q origin main
    if ($LASTEXITCODE) { throw "git push failed" }
    git push -q origin $tag
    if ($LASTEXITCODE) { throw "git push tag failed" }

    $title = "NASfone for Windows - Server $Version"
    $ghArgs = @("release", "create", $tag, "--title", $title, "--notes-file", $notes)
    # A beta never becomes the repository's "Latest" release.
    if ($beta) { $ghArgs += "--prerelease" } else { $ghArgs += "--latest=false" }
    $ghArgs += (Get-ChildItem $out -File).FullName
    & $gh @ghArgs
    if ($LASTEXITCODE) { throw "gh release create failed" }
    Write-Host "Released $tag" -ForegroundColor Green
} finally {
    Pop-Location
}
