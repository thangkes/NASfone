# NASfone

**English** · [Tiếng Việt](README.vi.md)

Turn a spare Android phone into a pocket NAS you can reach from anywhere.

NASfone runs a small file server on the phone and puts it on your own
[Tailscale](https://tailscale.com) network (embedded, no separate Tailscale app needed).
Open it in any browser, or map it as a drive letter on Windows.

> Status: early (0.1.x). Works day to day on the author's phone; expect rough edges.
> **Download:** [latest release](https://github.com/thangkes/NASfone/releases/latest)
> (server APK for the phone, client installer for Windows). The **Windows server** has its
> own releases tagged `windows-server-v…` on the [releases page](https://github.com/thangkes/NASfone/releases) (beta).

## Features

- **Server app for Android** — runs as a foreground service, survives "clear all" in
  recents, keeps running offline and reconnects as soon as Wi-Fi or mobile data returns.
  A notification shows live status and traffic.
- **Bring your own Tailscale account** — sign in, sign out or switch accounts inside the
  app. A custom control server (e.g. [Headscale](https://github.com/juanfont/headscale))
  is supported. Nothing is hardcoded.
- **Public HTTPS link via Tailscale Funnel** (optional) — open your files from a browser
  that is not on your tailnet.
- **Web file browser** — upload, download, create folders, delete; asks what to do on
  duplicate uploads (overwrite, keep both, skip). Also a standard WebDAV endpoint.
- **No passwords** — browser login uses rolling 6-digit codes shown in the phone app:
  - an **Admin** code (full access) and a **User** code (view and download only),
    guaranteed never to be equal;
  - sessions live only until the browser closes (24 h server-side cap).
- **Paired devices with their own keys** — each client app gets its own key pair and a
  role (Admin or User). Revoke or change roles from the phone at any time.
- **Windows server (NASfone for Windows - Server, beta)** — share a folder of a PC the same
  way: embedded Tailscale, Funnel, sign-in codes, paired apps, LAN access (LAN QR requests
  are approved in its window), tray icon and start with Windows.
- **Android client (NASfone Client, beta)** — use your servers from another phone: QR
  pairing with a key held by the Android Keystore, browse/upload/download, the NAS as a
  location in the Files app, automatic photo & video backup, fastest path (LAN, tailnet,
  Funnel) chosen automatically. Releases tagged `android-client-v…`.
- **Windows app (NASfone for Windows - Client)** — tray icon, mounts the phone as a drive (`P:` by default) with the
  real free space, read-only for the User role, detects revocation. Pair in one click
  from the web page ("Connect the app on this computer").
- **LAN access, no internet needed** (optional switch) — browsers on the same Wi-Fi or on
  the phone's hotspot open `http://<phone-ip>:8080` and sign in with a 6-digit code or by
  **scanning the QR code** on the page with the phone app. QR sessions are view-only,
  never saved, and end after 1 hour without traffic or when the phone's local IP changes.
  (Plain HTTP: use it on networks you trust.)
- **Automatic configuration backup** — the phone app keeps
  `Download/NASfone-config-backup.zip` up to date (Tailscale sign-in, paired devices,
  settings) and offers to restore it after a reinstall.
- **Updates from GitHub Releases** — both apps check for new versions and update in one
  tap (checksum-verified; Android asks you to confirm the install). Updates keep your setup.
- **English and Vietnamese** everywhere (phone app, web, Windows app).

## How it works

```
                 ┌──────────────── core (Go) ─────────────────┐
                 │ auth codes · pairing · WebDAV · web UI     │
                 │ embedded Tailscale (tsnet) · Funnel        │
                 └───────┬────────────────┬──────────────┬────┘
                gomobile │       gomobile │     go build │
          ┌──────────────▼───┐  ┌─────────▼────────┐  ┌──▼───────────────┐
          │ server-android   │  │ client-android   │  │ client-windows   │
          │ (APK, the NAS)   │  │ (APK, beta)      │  │ (EXE + installer)│
          └──────────────────┘  └──────────────────┘  └──────────────────┘
```

- [docs/DESIGN.md](docs/DESIGN.md) — architecture and decisions
- [docs/PAIRING.md](docs/PAIRING.md) — pairing and authentication protocol

## Getting started

1. Download `NASfone-Server-<version>.apk` from the
   [latest release](https://github.com/thangkes/NASfone/releases/latest), install it on the
   phone that will hold the files and open it.
2. Grant the requested permissions (all-files access, notifications, ignore battery
   optimisation).
3. Under **Tailscale account**, tap **Sign in** and sign in with your own account.
4. Open the address shown in the app from a device on the same tailnet (or turn on
   Funnel for a public HTTPS link) and log in with the current Admin or User code.
5. On Windows: install `NASfone-Windows-Client-Setup-<version>.exe`, then on the web page click
   **Connect the app on this computer** and confirm in the NASfone dialog.

6. Optional: under **LAN access**, switch it on to reach the phone directly from the same
   network, even without internet.

> Some Android brands kill background apps aggressively. Allow NASfone to run in the
> background / auto-launch in the phone's battery settings.

## Building from source

Requirements: Go 1.27+, [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile),
Android SDK + NDK, the JDK bundled with Android Studio. For the Windows installer:
[go-winres](https://github.com/tc-hib/go-winres), [Inno Setup 6](https://jrsoftware.org/isinfo.php),
[rclone](https://rclone.org) (`winget install Rclone.Rclone`) and the signed
[WinFsp](https://github.com/winfsp/winfsp/releases) MSI in `client-windows/installer/deps/`.

```powershell
.\scripts\build-server.ps1             # Go core -> AAR -> server APK
.\scripts\build-server.ps1 -Install    # ...and install on a phone via adb
.\scripts\build-windows.ps1            # NASfone.exe + client-windows\dist\NASfone-Windows-Client-Setup-<version>.exe
.\scripts\release.ps1 0.2.0            # bump, build both, tag and publish a GitHub Release
.\scripts\build-windows-server.ps1     # NASfoneServer.exe + server-windows\dist\NASfone-Windows-Server-Setup-<version>.exe
.\scripts\release-windows-server.ps1 0.2.0-beta.1 -NotesFile notes.md   # Windows server release
```

For quick web UI work there is a local dev server: `cd core; go run ./cmd/devserver`
(add `-lan` to try the QR sign-in page).

> Release APKs are signed with the key in `~/.nasfone-signing/keystore.properties`
> (kept outside the repo). Without it, builds fall back to the debug key. Android only
> accepts updates signed with the same key, so keep a backup of it.

## Project layout

| Folder | What |
|---|---|
| [`core/`](core/) | Shared Go code: auth codes, pairing, LAN QR sign-in, WebDAV + web UI, updates, gomobile bindings |
| [`server-android/`](server-android/) | Android server app (Kotlin shell around the Go core) |
| [`client-windows/`](client-windows/) | Windows tray app, drive mount, installer |
| [`server-windows/`](server-windows/) | Windows server app (beta) and its installer |
| [`client-android/`](client-android/) | Android client (beta): pairing, browsing, Files app integration, photo backup |
| [`scripts/`](scripts/) | Build scripts |
| [`docs/`](docs/) | Design notes |

## Roadmap

- WebDAV access keys for third-party apps
- Code-signed Windows installer

## License

[MIT](LICENSE). The Windows installer bundles rclone (MIT) and WinFsp (GPLv3 with FLOSS
exception); see [THIRD-PARTY-NOTICES](client-windows/installer/THIRD-PARTY-NOTICES.txt).
NASfone is not affiliated with Tailscale Inc.
