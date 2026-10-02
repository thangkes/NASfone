# Build the Android client: Go core (mobileclient) -> AAR -> APK, optionally install via adb.
#   .\scripts\build-client-android.ps1                              # build core + APK
#   .\scripts\build-client-android.ps1 -SkipCore                    # only the APK (Kotlin changes)
#   .\scripts\build-client-android.ps1 -Install -Device <serial>    # never the server phone
#   .\scripts\build-client-android.ps1 -Emulator -Install -Device emulator-5554   # also x86_64, for the emulator
param([switch]$Install, [switch]$SkipCore, [switch]$Emulator, [string]$Device)
$ErrorActionPreference = "Stop"

$root = Split-Path $PSScriptRoot -Parent
$sdk = "$env:LOCALAPPDATA\Android\Sdk"
$env:ANDROID_HOME = $sdk
$env:ANDROID_NDK_HOME = (Get-ChildItem "$sdk\ndk" -Directory | Sort-Object Name | Select-Object -Last 1).FullName
$env:JAVA_HOME = "C:\Program Files\Android\Android Studio\jbr"
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User") +
    ";$env:USERPROFILE\go\bin;$env:JAVA_HOME\bin"

if (-not $SkipCore) {
    Write-Host "== gomobile bind (mobileclient) ==" -ForegroundColor Cyan
    Push-Location "$root\core"
    try {
        go test ./mobileclient/
        if ($LASTEXITCODE) { throw "go test failed" }
        $targets = if ($Emulator) { "android/arm64,android/amd64" } else { "android/arm64" }
        gomobile bind "-target=$targets" -androidapi 29 -javapkg com.nasfone.core -trimpath -ldflags="-s -w" `
            -o "$root\client-android\app\libs\nasfoneclient.aar" ./mobileclient
        if ($LASTEXITCODE) { throw "gomobile bind failed" }
    } finally { Pop-Location }
}

Write-Host "== gradle assembleRelease ==" -ForegroundColor Cyan
$app = "$root\client-android"
# Call gradlew directly: Start-Process -Wait would also wait for the Gradle daemon.
Push-Location $app
$ErrorActionPreference = "Continue" # gradle warnings on stderr are not failures; check the exit code
try {
    # The value keeps PowerShell from splitting "-PwithX86" when calling the .bat.
    if ($Emulator) { & "$app\gradlew.bat" assembleRelease --offline -q --console=plain "-PwithX86=true" }
    else { & "$app\gradlew.bat" assembleRelease --offline -q --console=plain }
    if ($LASTEXITCODE) { throw "gradle failed ($LASTEXITCODE)" }
} finally { Pop-Location; $ErrorActionPreference = "Stop" }
$apk = "$app\app\build\outputs\apk\release\app-release.apk"
Write-Host ("APK: {0} ({1:N1} MB)" -f $apk, ((Get-Item $apk).Length / 1MB)) -ForegroundColor Green

if ($Install) {
    if (-not $Device) { throw "Pass -Device <adb serial>: the client must not go on the server phone." }
    $ErrorActionPreference = "Continue"
    $adb = "$sdk\platform-tools\adb.exe"
    & $adb -s $Device install -r $apk
    & $adb -s $Device shell am start -n com.nasfone.client/.MainActivity
}
