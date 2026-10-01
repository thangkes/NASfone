//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"nasfone/core/auth"
	"nasfone/core/client"
)

type app struct {
	mount mounter

	mu        sync.Mutex
	cfg       client.Config
	paired    bool
	cfgStamp  time.Time
	connected bool
	connErr   string
	revoked   bool   // the server revoked this computer; drive stopped
	busy      bool   // pairing in progress
	notice    string // message for the window

	showAtStart bool

	mWindow, mStatus, mOpen, mWeb, mPair, mAuto, mForget, mQuit *systray.MenuItem
}

// runTray runs the tray icon; showWindow opens the app window right away
// (normal launch) as opposed to a quiet start with Windows.
func runTray(showWindow bool) {
	a := &app{showAtStart: showWindow}
	systray.Run(a.onReady, func() { a.mount.stopMount() })
}

func (a *app) onReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle(appTitle)
	systray.SetTooltip(appTitle)
	systray.SetOnTapped(a.openWindow) // left click opens the window; right click shows the menu

	a.mWindow = systray.AddMenuItem(t("m_window"), "")
	systray.AddSeparator()
	a.mStatus = systray.AddMenuItem(t("m_starting"), "")
	a.mStatus.Disable()
	systray.AddSeparator()
	a.mOpen = systray.AddMenuItem(t("m_open"), t("m_open_tip"))
	a.mWeb = systray.AddMenuItem(t("m_web"), "")
	systray.AddSeparator()
	a.mPair = systray.AddMenuItem(t("m_pair"), t("m_pair_tip"))
	a.mAuto = systray.AddMenuItemCheckbox(t("m_auto"), "", autostartEnabled())
	a.mForget = systray.AddMenuItem(t("m_forget"), "")
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem(t("m_quit"), "")

	a.reload(true)
	go a.watch()
	go a.menuLoop()
	go waitShowRequests(a.openWindow) // a second launch of the exe opens this window
	go waitQuitRequests(systray.Quit) // installer/uninstaller asks us to exit cleanly
	if a.showAtStart {
		a.openWindow()
	}
}

func (a *app) menuLoop() {
	exe, _ := os.Executable()
	for {
		select {
		case <-a.mWindow.ClickedCh:
			a.openWindow()
		case <-a.mOpen.ClickedCh:
			if d, ok, _ := a.mount.status(); ok && d != "" {
				exec.Command("explorer.exe", d+`\`).Start()
			} else {
				warn(t("drive_not_ready", a.statusText()))
			}
		case <-a.mWeb.ClickedCh:
			a.mu.Lock()
			u, paired := a.cfg.URL, a.paired
			a.mu.Unlock()
			if paired {
				exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
			}
		case <-a.mPair.ClickedCh:
			text, err := clipboardText()
			if err != nil || !strings.Contains(text, "nasfone1:") {
				warn(t("no_invite_clip"))
				continue
			}
			if pairFromInvite(text) {
				a.reload(true)
			}
		case <-a.mAuto.ClickedCh:
			on := !a.mAuto.Checked()
			if err := setAutostart(on, exe); err != nil {
				fail(t("autostart_fail", err.Error()))
				continue
			}
			if on {
				a.mAuto.Check()
			} else {
				a.mAuto.Uncheck()
			}
		case <-a.mForget.ClickedCh:
			a.unpair()
		case <-a.mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// reload reads the pairing from disk and (re)mounts if it changed.
func (a *app) reload(force bool) {
	st, _ := os.Stat(configPath())
	var stamp time.Time
	if st != nil {
		stamp = st.ModTime()
	}
	a.mu.Lock()
	if !force && stamp.Equal(a.cfgStamp) {
		a.mu.Unlock()
		return
	}
	a.cfgStamp = stamp
	cfg, err := loadConfig()
	a.paired = err == nil
	a.cfg = cfg
	a.connected, a.connErr, a.revoked = false, "", false
	a.mu.Unlock()

	if a.paired {
		if up, changed := upgradeURL(cfg); changed {
			cfg = up
			a.mu.Lock()
			a.cfg = up
			if st, err := os.Stat(configPath()); err == nil {
				a.cfgStamp = st.ModTime() // our own write; don't reload again
			}
			a.mu.Unlock()
		}
		a.mount.start(cfg)
		a.mForget.Enable()
		a.mWeb.Enable()
		go a.checkConnection()
	} else {
		a.mount.stopMount()
		a.mForget.Disable()
		a.mWeb.Disable()
	}
	a.refreshMenu()
}

// watch notices pairings made by another process (a nasfone:// link) and
// keeps the connection status fresh.
func (a *app) watch() {
	t := time.NewTicker(2 * time.Second)
	n := 0
	for range t.C {
		a.reload(false)
		n++
		_, running, _ := a.mount.status()
		a.mu.Lock()
		revoked, paired := a.revoked, a.paired
		a.mu.Unlock()
		if n%8 == 0 || (paired && !revoked && !running && n%2 == 0) { // ~15 s, or ~4 s while the drive is down
			a.checkConnection()
		}
		a.refreshMenu()
	}
}

func (a *app) checkConnection() {
	a.mu.Lock()
	cfg, paired := a.cfg, a.paired
	a.mu.Unlock()
	if !paired {
		return
	}
	key, err := loadKey()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var tok client.Token
		tok, err = client.Authenticate(ctx, httpClient, cfg, key)
		cancel()
		if err == nil && tok.Role != cfg.Role { // role changed on the server
			cfg.Role = tok.Role
			saveConfig(cfg)
		}
	}
	revoked := errors.Is(err, client.ErrRevoked)
	a.mu.Lock()
	first := revoked && !a.revoked
	a.revoked = revoked
	a.connected = err == nil
	a.connErr = ""
	if err != nil && !revoked {
		a.connErr = err.Error()
	}
	a.mu.Unlock()
	if first {
		// Stop retrying the mount: the server will refuse this device until it pairs again.
		a.mount.stopMount()
		go warn(t("revoked_msg"))
	}
	a.refreshMenu()
}

func (a *app) statusText() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.paired {
		return t("st_unpaired_hint")
	}
	if a.revoked {
		return t("st_revoked")
	}
	role := t("st_role_user")
	if a.cfg.Role == auth.RoleAdmin {
		role = t("st_role_admin")
	}
	drive, running, mErr := a.mount.status()
	switch {
	case a.connErr != "":
		return t("st_conn_err", short(a.connErr))
	case running:
		return t("st_ok", role, drive)
	case mErr != "":
		return "⚠ " + short(mErr)
	case a.connected:
		return t("st_mounting", role)
	default:
		return t("st_connecting", role)
	}
}

func (a *app) refreshMenu() {
	s := a.statusText()
	a.mStatus.SetTitle(s)
	systray.SetTooltip(appTitle + "\n" + s)
	if _, running, _ := a.mount.status(); running {
		a.mOpen.Enable()
	} else {
		a.mOpen.Disable()
	}
}

func short(s string) string {
	if r := []rune(s); len(r) > 90 {
		return string(r[:90]) + "…"
	}
	return s
}

// relabel re-applies menu texts after the language setting changes.
func (a *app) relabel() {
	a.mWindow.SetTitle(t("m_window"))
	a.mOpen.SetTitle(t("m_open"))
	a.mOpen.SetTooltip(t("m_open_tip"))
	a.mWeb.SetTitle(t("m_web"))
	a.mPair.SetTitle(t("m_pair"))
	a.mPair.SetTooltip(t("m_pair_tip"))
	a.mAuto.SetTitle(t("m_auto"))
	a.mForget.SetTitle(t("m_forget"))
	a.mQuit.SetTitle(t("m_quit"))
	a.refreshMenu()
}
