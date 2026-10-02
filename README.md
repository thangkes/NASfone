# NASfone

**English** · [Tiếng Việt](README.vi.md)

Turn a spare Android phone into a pocket NAS you can reach from anywhere.

NASfone runs a small file server on the phone and puts it on your own
[Tailscale](https://tailscale.com) network (embedded, no separate Tailscale app needed).
Open it in any browser, or map it as a drive letter on Windows.

> Status: early (0.1). Works day to day on the author's phone; expect rough edges.

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
- **Windows app** — tray icon, mounts the phone as a drive (`P:` by default) with the
  real free space, read-only for the User role, detects revocation. Pair in one click
  from the web page ("Connect the app on this computer").
- **Updates from GitHub Releases** — both apps check for new versions and update in one
  tap (checksum-verified; Android asks you to confirm the install).
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
          │ (APK, the NAS)   │  │ (planned)        │  │ (EXE + installer)│
          └──────────────────┘  └──────────────────┘  └──────────────────┘
```

- [docs/DESIGN.md](docs/DESIGN.md) — architecture and decisions
- [docs/PAIRING.md](docs/PAIRING.md) — pairing and authentication protocol

## Getting started

1. Install the **server APK** on the phone that will hold the files and open it.
2. Grant the requested permissions (all-files access, notifications, ignore battery
   optimisation).
3. Under **Tailscale account**, tap **Sign in** and sign in with your own account.
4. Open the address shown in the app from a device on the same tailnet (or turn on
   Funnel for a public HTTPS link) and log in with the current Admin or User code.
5. On Windows: install `NASfone-Setup-<version>.exe`, then on the web page click
   **Connect the app on this computer** and confirm in the NASfone dialog.

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
.\scripts\build-windows.ps1            # NASfone.exe + client-windows\dist\NASfone-Setup-<version>.exe
.\scripts\release.ps1 0.2.0            # bump, build both, tag and publish a GitHub Release
```

For quick web UI work there is a local dev server: `cd core; go run ./cmd/devserver`.

> Release APKs are signed with the key in `~/.nasfone-signing/keystore.properties`
> (kept outside the repo). Without it, builds fall back to the debug key. Android only
> accepts updates signed with the same key, so keep a backup of it.

## Project layout

| Folder | What |
|---|---|
| [`core/`](core/) | Shared Go code: auth codes, pairing, WebDAV + web UI, gomobile bindings |
| [`server-android/`](server-android/) | Android server app (Kotlin shell around the Go core) |
| [`client-windows/`](client-windows/) | Windows tray app, drive mount, installer |
| [`client-android/`](client-android/) | Android client (planned) |
| [`scripts/`](scripts/) | Build scripts |
| [`docs/`](docs/) | Design notes |

## Roadmap

- Android client app (QR pairing, file browser, photo backup)
- WebDAV access keys for third-party apps
- Signed releases on GitHub

## License

[MIT](LICENSE). The Windows installer bundles rclone (MIT) and WinFsp (GPLv3 with FLOSS
exception); see [THIRD-PARTY-NOTICES](client-windows/installer/THIRD-PARTY-NOTICES.txt).
NASfone is not affiliated with Tailscale Inc.
