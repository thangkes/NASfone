; NASfone for Windows — Inno Setup script.
; Built by scripts\build-windows.ps1 (which passes /DAppVersion=x.y.z).

#ifndef AppVersion
  #define AppVersion "0.1.0"
#endif

[Setup]
AppId={{89CEEE73-3F07-4F84-B203-C7FEAB44D07A}
AppName=NASfone
AppVersion={#AppVersion}
AppVerName=NASfone {#AppVersion}
AppPublisher=NASfone
AppPublisherURL=https://github.com/
VersionInfoVersion={#AppVersion}
DefaultDirName={autopf}\NASfone
DefaultGroupName=NASfone
DisableProgramGroupPage=yes
DisableDirPage=auto
OutputDir=..\dist
OutputBaseFilename=NASfone-Setup-{#AppVersion}
SetupIconFile=app.ico
UninstallDisplayIcon={app}\NASfone.exe
UninstallDisplayName=NASfone
Compression=lzma2/ultra64
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
; WinFsp is a kernel driver, so installing it needs administrator rights.
PrivilegesRequired=admin
WizardStyle=modern
ShowLanguageDialog=no
; We stop the running app ourselves (NASfone.exe --quit), which also unmounts the drive.
CloseApplications=no
RestartApplications=no

[Languages]
Name: "vi"; MessagesFile: "Vietnamese.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
vi.DesktopIcon=Tạo lối tắt trên màn hình Desktop
en.DesktopIcon=Create a desktop shortcut
vi.Autostart=Tự khởi động NASfone cùng Windows (chạy ẩn ở khay hệ thống)
en.Autostart=Start NASfone with Windows (in the system tray)
vi.InstallingWinFsp=Đang cài WinFsp (trình điều khiển để gắn ổ đĩa)…
en.InstallingWinFsp=Installing WinFsp (drive driver)…
vi.Launch=Mở NASfone
en.Launch=Open NASfone
vi.WinFspFailed=Không cài được WinFsp (mã lỗi %1). NASfone vẫn được cài, nhưng chưa gắn được ổ đĩa cho tới khi cài WinFsp từ https://winfsp.dev.
en.WinFspFailed=WinFsp could not be installed (error %1). NASfone is installed, but it cannot mount the drive until WinFsp is installed from https://winfsp.dev.

[Tasks]
Name: "autostart"; Description: "{cm:Autostart}"
Name: "desktopicon"; Description: "{cm:DesktopIcon}"; Flags: unchecked

[Files]
Source: "..\build\NASfone.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "deps\rclone.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "THIRD-PARTY-NOTICES.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "deps\winfsp.msi"; DestDir: "{tmp}"; Flags: deleteafterinstall; Check: not WinFspInstalled

[Icons]
Name: "{autoprograms}\NASfone"; Filename: "{app}\NASfone.exe"
Name: "{autodesktop}\NASfone"; Filename: "{app}\NASfone.exe"; Tasks: desktopicon

[Registry]
; Per-user autostart for the user running setup (the app manages this value afterwards).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "NASfone"; ValueData: """{app}\NASfone.exe"" --minimized"; Tasks: autostart; Flags: uninsdeletevalue

[Run]
Filename: "{app}\NASfone.exe"; Description: "{cm:Launch}"; Flags: nowait postinstall skipifsilent runasoriginaluser

[UninstallRun]
; Stop the app (unmounting the drive) and remove the per-user nasfone:// handler.
; Pairing data in %APPDATA%\NASfone is kept so a reinstall stays paired.
Filename: "{app}\NASfone.exe"; Parameters: "--cleanup"; RunOnceId: "NASfoneCleanup"; Flags: runhidden waituntilterminated

[Code]
function WinFspInstalled: Boolean;
begin
  Result := FileExists(ExpandConstant('{commonpf32}\WinFsp\bin\winfsp-x64.dll'))
    or FileExists(ExpandConstant('{commonpf64}\WinFsp\bin\winfsp-x64.dll'));
end;

// Ask a running NASfone (any version with --quit) to exit and unmount
// before files are replaced.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  rc: Integer;
begin
  ExtractTemporaryFile('NASfone.exe');
  Exec(ExpandConstant('{tmp}\NASfone.exe'), '--quit', '', SW_HIDE, ewWaitUntilTerminated, rc);
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
