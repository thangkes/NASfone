# Build lõi Go -> AAR, build APK server, (tùy chọn) cài lên điện thoại qua adb.
#   .\scripts\build-server.ps1            # build core + APK
#   .\scripts\build-server.ps1 -Install   # build + cài + mở app
#   .\scripts\build-server.ps1 -SkipCore  # chỉ build APK (khi chỉ sửa Kotlin)
param([switch]$Install, [switch]$SkipCore)
$ErrorActionPreference = "Stop"

$root = Split-Path $PSScriptRoot -Parent
$sdk = "$env:LOCALAPPDATA\Android\Sdk"
$env:ANDROID_HOME = $sdk
$env:ANDROID_NDK_HOME = (Get-ChildItem "$sdk\ndk" -Directory | Sort-Object Name | Select-Object -Last 1).FullName
$env:JAVA_HOME = "C:\Program Files\Android\Android Studio\jbr"
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User") +
    ";$env:USERPROFILE\go\bin;$env:JAVA_HOME\bin"

if (-not $SkipCore) {
    Write-Host "== gomobile bind (core) ==" -ForegroundColor Cyan
    Push-Location "$root\core"
    try {
        go test ./server/
        if ($LASTEXITCODE) { throw "go test failed" }
        gomobile bind -target=android/arm64 -androidapi 29 -javapkg com.nasfone.core -ldflags="-s -w" `
            -o "$root\server-android\app\libs\nasfonecore.aar" ./mobile
        if ($LASTEXITCODE) { throw "gomobile bind failed" }
    } finally { Pop-Location }
}

Write-Host "== gradle assembleRelease ==" -ForegroundColor Cyan
$app = "$root\server-android"
# Call gradlew directly: Start-Process -Wait would also wait for the Gradle
# daemon it spawns, which keeps running, and hang forever.
Push-Location $app
$ErrorActionPreference = "Continue" # gradle warnings on stderr are not failures; check the exit code
try {
    & "$app\gradlew.bat" assembleRelease --offline -q --console=plain
    if ($LASTEXITCODE) { throw "gradle failed ($LASTEXITCODE)" }
} finally { Pop-Location; $ErrorActionPreference = "Stop" }
$apk = "$app\app\build\outputs\apk\release\app-release.apk"
Write-Host ("APK: {0} ({1:N1} MB)" -f $apk, ((Get-Item $apk).Length / 1MB)) -ForegroundColor Green

if ($Install) {
    # adb prints harmless warnings on stderr (e.g. "intent delivered to top-most instance").
    $ErrorActionPreference = "Continue"
    $adb = "$sdk\platform-tools\adb.exe"
    & $adb install -r $apk
    & $adb shell "appops set com.nasfone.server MANAGE_EXTERNAL_STORAGE allow; dumpsys deviceidle whitelist +com.nasfone.server >/dev/null; pm grant com.nasfone.server android.permission.POST_NOTIFICATIONS; am start -n com.nasfone.server/.MainActivity"
}
