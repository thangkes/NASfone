# NASfone

**English** · [Tiếng Việt](README.vi.md)

Turn a spare Android phone (or a Windows PC) into a NAS you can reach from anywhere.

NASfone is **one app per platform**. On first launch you pick what the device does:

| | 📦 **Server** — stores the files | 📲💻 **Client** — uses a NAS elsewhere |
|---|---|---|
| **Android** | Shares the phone's storage | Browse, upload/download, NAS in the Files app, photo backup |
| **Windows** | Shares a folder of the PC | Mounts the NAS as a drive letter (`P:`) |
| **iPhone / iPad** (beta) | — (iOS cannot run a server in the background) | Browse, view, upload/download, photo backup |

The role can be changed later in Settings. Servers join your own
[Tailscale](https://tailscale.com) network (embedded, no separate Tailscale app needed).
Any browser works as a client too.

> Status: early (0.x). Works day to day on the author's devices; expect rough edges.
> **Download:** [latest release](https://github.com/thangkes/NASfone/releases/latest):
> `NASfone-Android-<version>.apk`, `NASfone-Windows-Setup-<version>.exe` and `NASfone-iOS-<version>.ipa` (beta, see below).

## Features

**Server role**

- **Runs in the background.** On Android it is a foreground service that survives "clear all", keeps running offline and reconnects as soon as the network returns. On Windows it is a tray app that starts with Windows.
- **Bring your own Tailscale account.** Sign in, sign out or switch accounts in the app; a custom control server such as [Headscale](https://github.com/juanfont/headscale) works too. Nothing is hardcoded.
- **Public HTTPS link via Tailscale Funnel** (optional).
- **Web file browser and WebDAV.** When an upload already exists, you choose to overwrite, keep both or skip.
- **No passwords.** Browsers sign in with rolling 6-digit codes:
  - an **Admin** code (full access) and a **User** code (view and download only), never equal;
  - sessions end when the browser closes (24 h server-side cap).
- **Paired apps.** Each client device gets its own key and a role (Admin or User), which can be changed or revoked at any time.
- **LAN access without internet** (optional). Browsers on the same network open `http://<server-ip>:8080` and sign in with a code or a **QR code**:
  - on a phone server, the phone scans the browser's QR code;
  - on a Windows server, you approve a 4-character check code in its window;
  - QR sessions are view-only, never saved, and end after 1 h idle or when the server's IP changes;
  - LAN traffic is plain HTTP, so use it on networks you trust.
- **Automatic configuration backup** on Android (`Download/NASfone-config-backup.zip`), offered for restore after a reinstall.

**Client role**

- **Pairing.** Scan the server's QR code, click "Connect the app on this computer" on its web page, or paste an invite. The app always asks for confirmation.
- **Fastest path.** The client picks LAN, then tailnet, then Funnel, and checks the server's identity on every sign-in.
- **Android:**
  - device key held by the Android Keystore;
  - browse, open, share, download, and upload (Admin);
  - the NAS appears in the **Files app** and in file pickers;
  - **automatic photo & video backup**, optionally Wi-Fi only.
- **Windows:** mounts the NAS as a drive with its real free space (read-only for the User role) and detects revocation.

**Both:** updates from GitHub Releases in one tap, with checksums verified and your setup kept. English and Vietnamese everywhere.

## How it works

```
                ┌──────────────────── core (Go) ─────────────────────┐
                │ codes · pairing · WebDAV · web UI · LAN · updates   │
                │ embedded Tailscale (tsnet) · Funnel · client core   │
                └──────────────┬───────────────────────┬─────────────┘
                      gomobile │                        │ go build
                 ┌─────────────▼──────────┐   ┌─────────▼──────────────┐
                 │ android/  (one APK)    │   │ windows/  (one EXE)    │
                 │ server · client roles  │   │ server · client roles  │
                 └────────────────────────┘   └────────────────────────┘
```

- [docs/DESIGN.md](docs/DESIGN.md) — architecture and decisions
- [docs/PAIRING.md](docs/PAIRING.md) — pairing and authentication protocol

## Getting started

1. **Server:**
   1. Install NASfone on the device that will hold the files and choose **Be the server**.
   2. Grant the requested permissions.
   3. Under **Tailscale account**, tap **Sign in** and use your own account.
   4. Optionally turn on Funnel (public link) or LAN access.
2. **Browser:** open the address shown by the server and sign in with the Admin or User code.
3. **Client:** install NASfone on the other device, choose **Be a client**, and pair:
   - scan the QR code from "Pair a new device" on the server, or
   - on Windows, click **Connect the app on this computer** on the server's web page.

### iPhone / iPad (beta)

`NASfone-iOS-<version>.ipa` is built by GitHub Actions and is **not signed** yet (no App Store
or TestFlight listing so far). Install it with [Sideloadly](https://sideloadly.io) or
[AltStore](https://altstore.io) using your own Apple ID. With a free Apple ID the app must be
re-signed every 7 days. Then pair it like the Android client: scan the server's QR code.
Downloaded files appear in the Files app under *On My iPhone › NASfone*.

> Some Android brands kill background apps aggressively. Allow NASfone to run in the
> background / auto-launch in the phone's battery settings.

## Building from source

**Requirements:**
- Go 1.27+, [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile), Android SDK + NDK, and the JDK bundled with Android Studio.
- For the Windows installer: [go-winres](https://github.com/tc-hib/go-winres), [Inno Setup 6](https://jrsoftware.org/isinfo.php), [rclone](https://rclone.org) (`winget install Rclone.Rclone`), and the signed [WinFsp](https://github.com/winfsp/winfsp/releases) MSI in `windows/installer/deps/`.

```powershell
.\scripts\build-android.ps1                                  # Go core -> AAR -> APK
.\scripts\build-android.ps1 -Install -Device <adb serial>    # ...and install it
.\scripts\build-android.ps1 -Emulator -Install -Device emulator-5554   # with x86_64 for the emulator
.\scripts\build-windows.ps1                                  # NASfone.exe + windows\dist\NASfone-Windows-Setup-<version>.exe
.\scripts\release.ps1 0.2.1 -NotesFile notes.md              # bump, build both, tag v0.2.1, publish
```

For quick web UI work there is a local dev server: `cd core; go run ./cmd/devserver`
(add `-lan` to try the QR sign-in page).

> Release APKs are signed with the key in `~/.nasfone-signing/keystore.properties`
> (kept outside the repo). Without it, builds fall back to the debug key. Android only
> accepts updates signed with the same key, so keep a backup of it.

## Project layout

| Folder | What |
|---|---|
| [`core/`](core/) | Shared Go code: server (`server`, `mobile`), client (`client`, `mobileclient`), pairing, auth, updates |
| [`android/`](android/) | Android app: `com.nasfone.server` (server role, role chooser) and `com.nasfone.client` (client role) |
| [`windows/`](windows/) | Windows app: client role (drive mount) and server role (`srv/`), installer |
| [`ios/`](ios/) | iOS client (SwiftUI + the Go client core); built by [`.github/workflows/ios.yml`](.github/workflows/ios.yml) |
| [`scripts/`](scripts/) | Build and release scripts |
| [`docs/`](docs/) | Design notes |

## Roadmap

- WebDAV access keys for third-party apps
- Code-signed Windows installer
- iOS: TestFlight / App Store, NAS in the Files app

## License

[MIT](LICENSE). The Windows installer bundles rclone (MIT) and WinFsp (GPLv3 with FLOSS
exception); see [THIRD-PARTY-NOTICES](windows/installer/THIRD-PARTY-NOTICES.txt).
NASfone is not affiliated with Tailscale Inc.
