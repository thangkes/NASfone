//go:build windows

package main

import (
	"encoding/json"
	"os/exec"
	"time"

	"fyne.io/systray"

	"nasfone/core/mobile"
)

var (
	mWindow, mStatus, mUpdate, mFolder, mWeb, mAuto, mQuit *systray.MenuItem
)

// runTray runs the tray icon; showWindow opens the window right away
// (normal launch) as opposed to a quiet start with Windows.
func runTray(showWindow bool) {
	a := &app{lanSeen: map[string]bool{}}
	systray.Run(func() { a.onReady(showWindow) }, func() { a.stop() })
}

func (a *app) onReady(showWindow bool) {
	systray.SetIcon(trayIcon())
	systray.SetTitle(appName)
	systray.SetTooltip(appName)
	systray.SetOnTapped(a.openWindow) // left click opens the window; right click shows the menu

	mWindow = systray.AddMenuItem(t("m_window"), "")
	systray.AddSeparator()
	mStatus = systray.AddMenuItem(t("st_stopped"), "")
	mStatus.Disable()
	mUpdate = systray.AddMenuItem("", "")
	mUpdate.Hide()
	systray.AddSeparator()
	mFolder = systray.AddMenuItem(t("m_folder"), "")
	mWeb = systray.AddMenuItem(t("m_web"), "")
	systray.AddSeparator()
	mAuto = systray.AddMenuItemCheckbox(t("m_auto"), "", autostartEnabled())
	systray.AddSeparator()
	mQuit = systray.AddMenuItem(t("m_quit"), "")

	// Status changes arrive often (logs, traffic); refresh the tray at most once a second.
	dirty := make(chan struct{}, 1)
	a.onChange = func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	go func() {
		for range dirty {
			a.refreshTray()
			time.Sleep(time.Second)
		}
	}()

	go a.menuLoop()
	go waitEvent(showEventName, a.openWindow) // a second launch of the exe opens the window
	go waitEvent(quitEventName, systray.Quit) // the installer/uninstaller asks us to exit
	go a.updateLoop()
	go a.watchLAN()

	if getSettings().Configured {
		go a.start()
	} else {
		showWindow = true // first run: pick the shared folder first
	}
	if showWindow {
		a.openWindow()
	}
	a.refreshTray()
}

func (a *app) menuLoop() {
	for {
		select {
		case <-mWindow.ClickedCh:
			a.openWindow()
		case <-mUpdate.ClickedCh:
			go a.applyUpdate()
		case <-mFolder.ClickedCh:
			exec.Command("explorer.exe", getSettings().RootDir).Start()
		case <-mWeb.ClickedCh:
			if u := a.webURL(); u != "" {
				openURL(u)
			}
		case <-mAuto.ClickedCh:
			on := !mAuto.Checked()
			if err := setAutostart(on); err != nil {
				fail(t("autostart_fail", err.Error()))
				continue
			}
			if on {
				mAuto.Check()
			} else {
				mAuto.Uncheck()
			}
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// statusText summarises the server state for the tray and the window.
func (a *app) statusText() (code, text string) {
	a.mu.Lock()
	running, starting, startErr := a.running, a.starting, a.startErr
	backend, _ := a.status["backendState"].(string)
	stErr, _ := a.status["error"].(string)
	a.mu.Unlock()
	switch {
	case starting:
		return "starting", t("st_starting")
	case !running && startErr != "":
		return "error", t("st_error", startErr)
	case !running:
		return "stopped", t("st_stopped")
	case backend == "Running":
		return "ok", t("st_running")
	case backend == "NeedsLogin":
		return "login", t("st_login")
	case stErr != "":
		return "error", t("st_error", stErr)
	default:
		return "starting", t("st_starting")
	}
}

func (a *app) refreshTray() {
	_, s := a.statusText()
	mStatus.SetTitle(s)
	systray.SetTooltip(appName + "\n" + s)
	if a.webURL() != "" {
		mWeb.Enable()
	} else {
		mWeb.Disable()
	}
}

// relabel re-applies menu texts after the language changes.
func (a *app) relabel() {
	mWindow.SetTitle(t("m_window"))
	mFolder.SetTitle(t("m_folder"))
	mWeb.SetTitle(t("m_web"))
	mAuto.SetTitle(t("m_auto"))
	mQuit.SetTitle(t("m_quit"))
	a.mu.Lock()
	if r := a.update; r != nil {
		mUpdate.SetTitle(t("m_update", r.Version))
	}
	a.mu.Unlock()
	a.refreshTray()
}

// watchLAN brings the window up when a browser on the LAN asks to sign in
// with a QR code: a PC has no camera, so the request is approved there.
func (a *app) watchLAN() {
	for range time.Tick(time.Second) {
		if !a.isRunning() {
			continue
		}
		var pending []struct {
			Ticket string `json:"ticket"`
		}
		json.Unmarshal([]byte(mobile.LanPending()), &pending)
		fresh := false
		a.mu.Lock()
		now := map[string]bool{}
		for _, p := range pending {
			now[p.Ticket] = true
			if !a.lanSeen[p.Ticket] {
				fresh = true
			}
		}
		a.lanSeen = now
		a.mu.Unlock()
		if fresh {
			a.openWindow()
		}
	}
}
