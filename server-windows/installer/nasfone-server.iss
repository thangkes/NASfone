; NASfone for Windows - Server: Inno Setup script.
; Built by scripts\build-windows-server.ps1 (which passes /DAppVersion=x.y.z).

#ifndef AppVersion
  #define AppVersion "0.1.0"
#endif

[Setup]
AppId={{4DA80CE8-09F4-4666-8874-4E57A15C9284}
AppName=NASfone for Windows - Server
AppVersion={#AppVersion}
AppVerName=NASfone for Windows - Server {#AppVersion}
AppPublisher=NASfone
AppPublisherURL=https://github.com/thangkes/NASfone
VersionInfoVersion={#AppVersion}
DefaultDirName={autopf}\NASfone Server
DefaultGroupName=NASfone
DisableProgramGroupPage=yes
DisableDirPage=auto
OutputDir=..\dist
OutputBaseFilename=NASfone-Windows-Server-Setup-{#AppVersion}
SetupIconFile=app.ico
UninstallDisplayIcon={app}\NASfoneServer.exe
UninstallDisplayName=NASfone for Windows - Server
Compression=lzma2/ultra64
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
; Program Files and the firewall rule for LAN access need administrator rights.
PrivilegesRequired=admin
WizardStyle=modern
ShowLanguageDialog=no
; We stop the running app ourselves (NASfoneServer.exe --quit).
CloseApplications=no
RestartApplications=no

[Languages]
Name: "vi"; MessagesFile: "Vietnamese.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
vi.DesktopIcon=Tạo lối tắt trên màn hình Desktop
en.DesktopIcon=Create a desktop shortcut
vi.Autostart=Tự khởi động NASfone Server cùng Windows (chạy ẩn ở khay hệ thống)
en.Autostart=Start NASfone Server with Windows (in the system tray)
vi.Launch=Mở NASfone Server
en.Launch=Open NASfone Server

[Tasks]
Name: "autostart"; Description: "{cm:Autostart}"
Name: "desktopicon"; Description: "{cm:DesktopIcon}"; Flags: unchecked

[Files]
Source: "..\build\NASfoneServer.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\NASfone for Windows - Server"; Filename: "{app}\NASfoneServer.exe"
Name: "{autodesktop}\NASfone for Windows - Server"; Filename: "{app}\NASfoneServer.exe"; Tasks: desktopicon

[Registry]
; Per-user autostart for the user running setup (the app manages this value afterwards).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "NASfone Server"; ValueData: """{app}\NASfoneServer.exe"" --minimized"; Tasks: autostart; Flags: uninsdeletevalue

[Run]
; LAN access: allow inbound connections to the app on private networks only,
; so Windows does not ask and public Wi-Fi stays closed. Tailscale needs no rule.
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""NASfone Server"""; Flags: runhidden waituntilterminated
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""NASfone Server"" dir=in action=allow program=""{app}\NASfoneServer.exe"" enable=yes profile=private"; Flags: runhidden waituntilterminated
Filename: "{app}\NASfoneServer.exe"; Description: "{cm:Launch}"; Flags: nowait postinstall skipifsilent runasoriginaluser
; In-app updates run the installer with /SILENT: reopen the app afterwards (quietly in the tray).
Filename: "{app}\NASfoneServer.exe"; Parameters: "--minimized"; Flags: nowait runasoriginaluser; Check: WizardSilent

[UninstallRun]
; Stop the app and remove autostart. Server data in %APPDATA%\NASfone-Server
; (Tailscale sign-in, paired apps) is kept so a reinstall is the same server.
Filename: "{app}\NASfoneServer.exe"; Parameters: "--cleanup"; RunOnceId: "NASfoneServerCleanup"; Flags: runhidden waituntilterminated
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""NASfone Server"""; RunOnceId: "NASfoneServerFirewall"; Flags: runhidden waituntilterminated

[Code]
// Ask a running NASfone Server to stop and exit before files are replaced.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  rc: Integer;
begin
  if FileExists(ExpandConstant('{app}\NASfoneServer.exe')) then
    Exec(ExpandConstant('{app}\NASfoneServer.exe'), '--quit', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Result := '';
end;
