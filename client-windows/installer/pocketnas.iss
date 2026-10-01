; PocketNAS for Windows — Inno Setup script.
; Built by scripts\build-windows.ps1 (which passes /DAppVersion=x.y.z).

#ifndef AppVersion
  #define AppVersion "0.1.0"
#endif

[Setup]
AppId={{8C1B7A52-5E2F-4C8E-9A51-0D6C2F5B9E71}
AppName=PocketNAS
AppVersion={#AppVersion}
AppVerName=PocketNAS {#AppVersion}
AppPublisher=PocketNAS
AppPublisherURL=https://github.com/
VersionInfoVersion={#AppVersion}
DefaultDirName={autopf}\PocketNAS
DefaultGroupName=PocketNAS
DisableProgramGroupPage=yes
DisableDirPage=auto
OutputDir=..\dist
OutputBaseFilename=PocketNAS-Setup-{#AppVersion}
SetupIconFile=app.ico
UninstallDisplayIcon={app}\PocketNAS.exe
UninstallDisplayName=PocketNAS
Compression=lzma2/ultra64
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
; WinFsp is a kernel driver, so installing it needs administrator rights.
PrivilegesRequired=admin
WizardStyle=modern
ShowLanguageDialog=no
; We stop the running app ourselves (PocketNAS.exe --quit), which also unmounts the drive.
CloseApplications=no
RestartApplications=no

[Languages]
Name: "vi"; MessagesFile: "Vietnamese.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
vi.DesktopIcon=Tạo lối tắt trên màn hình Desktop
en.DesktopIcon=Create a desktop shortcut
vi.Autostart=Tự khởi động PocketNAS cùng Windows (chạy ẩn ở khay hệ thống)
en.Autostart=Start PocketNAS with Windows (in the system tray)
vi.InstallingWinFsp=Đang cài WinFsp (trình điều khiển để gắn ổ đĩa)…
en.InstallingWinFsp=Installing WinFsp (drive driver)…
vi.Launch=Mở PocketNAS
en.Launch=Open PocketNAS
vi.WinFspFailed=Không cài được WinFsp (mã lỗi %1). PocketNAS vẫn được cài, nhưng chưa gắn được ổ đĩa cho tới khi cài WinFsp từ https://winfsp.dev.
en.WinFspFailed=WinFsp could not be installed (error %1). PocketNAS is installed, but it cannot mount the drive until WinFsp is installed from https://winfsp.dev.

[Tasks]
Name: "autostart"; Description: "{cm:Autostart}"
Name: "desktopicon"; Description: "{cm:DesktopIcon}"; Flags: unchecked

[Files]
Source: "..\build\PocketNAS.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "deps\rclone.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "THIRD-PARTY-NOTICES.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "deps\winfsp.msi"; DestDir: "{tmp}"; Flags: deleteafterinstall; Check: not WinFspInstalled

[Icons]
Name: "{autoprograms}\PocketNAS"; Filename: "{app}\PocketNAS.exe"
Name: "{autodesktop}\PocketNAS"; Filename: "{app}\PocketNAS.exe"; Tasks: desktopicon

[Registry]
; Per-user autostart for the user running setup (the app manages this value afterwards).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "PocketNAS"; ValueData: """{app}\PocketNAS.exe"" --minimized"; Tasks: autostart; Flags: uninsdeletevalue

[Run]
Filename: "{app}\PocketNAS.exe"; Description: "{cm:Launch}"; Flags: nowait postinstall skipifsilent runasoriginaluser

[UninstallRun]
; Stop the app (unmounting the drive) and remove the per-user pocketnas:// handler.
; Pairing data in %APPDATA%\PocketNAS is kept so a reinstall stays paired.
Filename: "{app}\PocketNAS.exe"; Parameters: "--cleanup"; RunOnceId: "PocketNASCleanup"; Flags: runhidden waituntilterminated

[Code]
function WinFspInstalled: Boolean;
begin
  Result := FileExists(ExpandConstant('{commonpf32}\WinFsp\bin\winfsp-x64.dll'))
    or FileExists(ExpandConstant('{commonpf64}\WinFsp\bin\winfsp-x64.dll'));
end;

// Ask a running PocketNAS (any version with --quit) to exit and unmount
// before files are replaced.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  rc: Integer;
begin
  ExtractTemporaryFile('PocketNAS.exe');
  Exec(ExpandConstant('{tmp}\PocketNAS.exe'), '--quit', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Result := '';
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  rc: Integer;
begin
  if (CurStep = ssPostInstall) and not WinFspInstalled then
  begin
    WizardForm.StatusLabel.Caption := CustomMessage('InstallingWinFsp');
    if not Exec('msiexec.exe', '/i "' + ExpandConstant('{tmp}\winfsp.msi') + '" /qn /norestart',
                '', SW_HIDE, ewWaitUntilTerminated, rc) or ((rc <> 0) and (rc <> 3010)) then
      MsgBox(FmtMessage(CustomMessage('WinFspFailed'), [IntToStr(rc)]), mbError, MB_OK);
  end;
end;
