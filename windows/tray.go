//go:build windows

package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"nasfone/core/auth"
	"nasfone/core/client"
	"nasfone/core/update"
)

// server is one paired NASfone server and its drive.
type server struct {
	id    string
	mount *mounter

	// guarded by app.mu
	cfg       client.Config
	stamp     time.Time // config.json mtime when loaded
	connected bool
	connErr   string
	revoked   bool // the server revoked this computer; drive stopped
}

func (s *server) host() string { return hostOf(s.cfg.URL) }

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return u
}

type app struct {
	mu       sync.Mutex
	servers  map[string]*server
	order    []string        // ids, oldest pairing first
	busy     bool            // pairing in progress
	notice   string          // message for the window
	update   *update.Release // newer release offered to the user, if any
	updating bool

	showAtStart bool

	mWindow, mStatus, mUpdate, mOpen, mWeb, mPair, mAuto, mForget, mQuit *systray.MenuItem
}

// runTray runs the tray icon; showWindow opens the app window right away
// (normal launch) as opposed to a quiet start with Windows.
func runTray(showWindow bool) {
	a := &app{showAtStart: showWindow, servers: map[string]*server{}}
	systray.Run(a.onReady, a.stopAll)
	// systray.Quit runs onExit on the caller's goroutine while the message
	// loop ends, so Run can return before the drives are gone: make sure
	// here, before the process exits.
	a.stopAll()
}

// stopAll unmounts every drive in parallel and waits for all of them.
func (a *app) stopAll() {
	var wg sync.WaitGroup
	for _, s := range a.list() {
		wg.Add(1)
		go func(m *mounter) {
			defer wg.Done()
			m.stopMount()
		}(s.mount)
	}
	wg.Wait()
}

// list returns the servers in display order.
func (a *app) list() []*server {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*server, 0, len(a.order))
	for _, id := range a.order {
		out = append(out, a.servers[id])
	}
	return out
}

func (a *app) get(id string) *server {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.servers[id]
}

func (a *app) onReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle(appName)
	systray.SetTooltip(appName)
	systray.SetOnTapped(a.openWindow) // left click opens the window; right click shows the menu

	a.mWindow = systray.AddMenuItem(t("m_window"), "")
	systray.AddSeparator()
	a.mStatus = systray.AddMenuItem(t("m_starting"), "")
	a.mStatus.Disable()
	a.mUpdate = systray.AddMenuItem("", "")
	a.mUpdate.Hide() // shown once a newer release is found
	systray.AddSeparator()
	a.mOpen = systray.AddMenuItem(t("m_open"), t("m_open_tip"))
	a.mWeb = systray.AddMenuItem(t("m_web"), "")
	systray.AddSeparator()
	a.mPair = systray.AddMenuItem(t("m_pair"), t("m_pair_tip"))
	a.mAuto = systray.AddMenuItemCheckbox(t("m_auto"), "", autostartEnabled())
	a.mForget = systray.AddMenuItem(t("m_forget"), "")
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem(t("m_quit"), "")

	a.reload()
	go a.watch()
	go a.menuLoop()
	go waitShowRequests(a.openWindow) // a second launch of the exe opens this window
	a.serveLocalPairing()             // the web page hands invites over via 127.0.0.1
	go waitQuitRequests(systray.Quit) // installer/uninstaller asks us to exit cleanly
	go a.updateLoop()
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
		case <-a.mUpdate.ClickedCh:
			go a.applyUpdate()
		case <-a.mOpen.ClickedCh:
			var drives []string
			for _, s := range a.list() {
				if d, ok, _ := s.mount.status(); ok && d != "" {
					drives = append(drives, d)
				}
			}
			switch len(drives) {
			case 0:
				warn(t("drive_not_ready", a.statusText()))
			case 1:
				exec.Command("explorer.exe", drives[0]+`\`).Start()
			default:
				exec.Command("explorer.exe", "shell:MyComputerFolder").Start() // "This PC" shows all drives
			}
		case <-a.mWeb.ClickedCh:
			if l := a.list(); len(l) == 1 {
				a.mu.Lock()
				u := l[0].cfg.URL
				a.mu.Unlock()
				openURL(u)
			} else {
				a.openWindow()
			}
		case <-a.mPair.ClickedCh:
			text, err := clipboardText()
			if err != nil || !strings.Contains(text, "nasfone1:") {
				warn(t("no_invite_clip"))
				continue
			}
			if pairFromInvite(text) {
				a.reload()
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
			if l := a.list(); len(l) == 1 {
				a.unpair(l[0].id)
			} else {
				a.openWindow()
			}
		case <-a.mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// reload syncs the servers with the pairings on disk: it mounts new or
// re-paired servers and unmounts removed ones.
func (a *app) reload() {
	ids := listServers()
	seen := map[string]bool{}
	var started []*server
	for _, id := range ids {
		seen[id] = true
		var stamp time.Time
		if st, err := os.Stat(configPath(id)); err == nil {
			stamp = st.ModTime()
		}
		a.mu.Lock()
		s := a.servers[id]
		if s != nil && stamp.Equal(s.stamp) {
			a.mu.Unlock()
			continue
		}
		if s == nil {
			s = &server{id: id, mount: &mounter{id: id}}
			a.servers[id] = s
		}
		s.stamp = stamp
		a.mu.Unlock()

		cfg, err := loadConfig(id)
		if err != nil {
			continue
		}
		if up, changed := upgradeURL(id, cfg); changed {
			cfg = up
			if st, err := os.Stat(configPath(id)); err == nil {
				stamp = st.ModTime() // our own write; don't reload again
			}
		}
		a.mu.Lock()
		s.cfg, s.stamp = cfg, stamp
		s.connected, s.connErr, s.revoked = false, "", false
		a.mu.Unlock()
		started = append(started, s)
	}

	a.mu.Lock()
	var gone []*server
	for id, s := range a.servers {
		if !seen[id] {
			gone = append(gone, s)
			delete(a.servers, id)
		}
	}
	a.order = ids
	a.mu.Unlock()

	for _, s := range gone {
		s.mount.stopMount()
	}
	for _, s := range started {
		a.mu.Lock()
		cfg := s.cfg
		a.mu.Unlock()
		s.mount.start(cfg)
		go a.checkConnection(s.id)
	}
	a.refreshMenu()
}

// remount restarts one server's drive (new drive letter, reconnect).
func (a *app) remount(id string) {
	s := a.get(id)
	if s == nil {
		return
	}
	a.mu.Lock()
	cfg := s.cfg
	s.revoked = false
	a.mu.Unlock()
	s.mount.start(cfg)
	a.checkConnection(id)
}

// watch notices pairings made by another process (a nasfone:// link) and
// keeps the connection status fresh.
func (a *app) watch() {
	t := time.NewTicker(2 * time.Second)
	n := 0
	for range t.C {
		a.reload()
		n++
		for _, s := range a.list() {
			_, running, _ := s.mount.status()
			a.mu.Lock()
			revoked := s.revoked
			a.mu.Unlock()
			if n%8 == 0 || (!revoked && !running && n%2 == 0) { // ~15 s, or ~4 s while the drive is down
				go a.checkConnection(s.id)
			}
		}
		a.refreshMenu()
	}
}

func (a *app) checkConnection(id string) {
	s := a.get(id)
	if s == nil {
		return
	}
	a.mu.Lock()
	cfg := s.cfg
	a.mu.Unlock()
	key, err := loadKey(id)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var tok client.Token
		tok, err = client.Authenticate(ctx, httpClient, cfg, key)
		cancel()
		if err == nil && tok.Role != cfg.Role { // role changed on the server
			cfg.Role = tok.Role
			saveConfig(id, cfg)
		}
	}
	revoked := errors.Is(err, client.ErrRevoked)
	a.mu.Lock()
	first := revoked && !s.revoked
	s.revoked = revoked
	s.connected = err == nil
	s.connErr = ""
	if err != nil && !revoked {
		s.connErr = err.Error()
	}
	host := s.host()
	a.mu.Unlock()
	if first {
		// Stop retrying the mount: the server will refuse this device until it pairs again.
		s.mount.stopMount()
		go warn(t("revoked_msg", host))
	}
	a.refreshMenu()
}

// serverStatus is one server's state for the window: a code (ok,
// connecting, error, revoked) and the error text, if any.
func (a *app) serverStatus(s *server) (code, errText string) {
	_, mounted, mErr := s.mount.status()
	a.mu.Lock()
	revoked, connErr := s.revoked, s.connErr
	a.mu.Unlock()
	switch {
	case revoked:
		return "revoked", ""
	case connErr != "":
		return "error", connErr
	case mounted:
		return "ok", ""
	case mErr != "":
		return "error", mErr
	default:
		return "connecting", ""
	}
}

// statusText is the one-line summary for the tray menu and tooltip.
func (a *app) statusText() string {
	l := a.list()
	if len(l) == 0 {
		return t("st_unpaired_hint")
	}
	if len(l) == 1 {
		s := l[0]
		a.mu.Lock()
		revoked, connErr, connected, role := s.revoked, s.connErr, s.connected, s.cfg.Role
		a.mu.Unlock()
		if revoked {
			return t("st_revoked")
		}
		r := t("st_role_user")
		if role == auth.RoleAdmin {
			r = t("st_role_admin")
		}
		drive, running, mErr := s.mount.status()
		switch {
		case connErr != "":
			return t("st_conn_err", short(connErr))
		case running:
			return t("st_ok", r, drive)
		case mErr != "":
			return "⚠ " + short(mErr)
		case connected:
			return t("st_mounting", r)
		default:
			return t("st_connecting", r)
		}
	}
	var drives []string
	problems := 0
	for _, s := range l {
		if d, ok, _ := s.mount.status(); ok {
			drives = append(drives, d)
		}
		if code, _ := a.serverStatus(s); code == "error" || code == "revoked" {
			problems++
		}
	}
	text := t("st_multi", len(drives), len(l), strings.Join(drives, " "))
	if problems > 0 {
		text = "⚠ " + text
	}
	return text
}

func (a *app) refreshMenu() {
	if a.mStatus == nil {
		return
	}
	s := a.statusText()
	a.mStatus.SetTitle(s)
	systray.SetTooltip(appName + "\n" + s)
	l := a.list()
	mounted := false
	for _, sv := range l {
		if _, ok, _ := sv.mount.status(); ok {
			mounted = true
		}
	}
	if mounted {
		a.mOpen.Enable()
	} else {
		a.mOpen.Disable()
	}
	if len(l) == 0 {
		a.mWeb.Disable()
		a.mForget.Disable()
	} else {
		a.mWeb.Enable()
		a.mForget.Enable()
	}
	a.labelServerItems(len(l))
}

// labelServerItems: with one server the tray acts on it directly; with more,
// those items open the window, where each server has its own buttons.
func (a *app) labelServerItems(n int) {
	if n > 1 {
		a.mWeb.SetTitle(t("m_web_many"))
		a.mForget.SetTitle(t("m_forget_many"))
	} else {
		a.mWeb.SetTitle(t("m_web"))
		a.mForget.SetTitle(t("m_forget"))
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
	a.mPair.SetTitle(t("m_pair"))
	a.mPair.SetTooltip(t("m_pair_tip"))
	a.mAuto.SetTitle(t("m_auto"))
	a.mQuit.SetTitle(t("m_quit"))
	a.mu.Lock()
	if r := a.update; r != nil {
		a.mUpdate.SetTitle(t("m_update", r.Version))
	}
	a.mu.Unlock()
	a.refreshMenu()
}
