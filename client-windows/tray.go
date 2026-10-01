//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"pocketnas/core/auth"
	"pocketnas/core/client"
)

type app struct {
	mount mounter

	mu        sync.Mutex
	cfg       client.Config
	paired    bool
	cfgStamp  time.Time
	connected bool
	connErr   string
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

	a.mWindow = systray.AddMenuItem("Mở cửa sổ PocketNAS", "")
	systray.AddSeparator()
	a.mStatus = systray.AddMenuItem("Đang khởi động…", "")
	a.mStatus.Disable()
	systray.AddSeparator()
	a.mOpen = systray.AddMenuItem("Mở ổ PocketNAS", "Mở trong File Explorer")
	a.mWeb = systray.AddMenuItem("Mở trang web PocketNAS", "")
	systray.AddSeparator()
	a.mPair = systray.AddMenuItem("Ghép đôi bằng lời mời đã sao chép", "Dán lời mời pnas1:… từ clipboard")
	a.mAuto = systray.AddMenuItemCheckbox("Khởi động cùng Windows", "", autostartEnabled())
	a.mForget = systray.AddMenuItem("Hủy ghép đôi với server này", "")
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem("Thoát", "")

	a.reload(true)
	go a.watch()
	go a.menuLoop()
	go waitShowRequests(a.openWindow) // a second launch of the exe opens this window
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
				warn("Ổ PocketNAS chưa sẵn sàng.\n\n" + a.statusText())
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
			if err != nil || !strings.Contains(text, "pnas1:") {
				warn("Clipboard không có lời mời PocketNAS.\n\nTrên điện thoại hoặc trang web PocketNAS, chọn \"Ghép thiết bị\" → \"Sao chép lời mời\", rồi bấm lại mục này.")
				continue
			}
			if pairFromInvite(text) {
				a.reload(true)
			}
		case <-a.mAuto.ClickedCh:
			on := !a.mAuto.Checked()
			if err := setAutostart(on, exe); err != nil {
				fail("Không đổi được cài đặt khởi động: " + err.Error())
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
	a.connected, a.connErr = false, ""
	a.mu.Unlock()

	if a.paired {
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

// watch notices pairings made by another process (a pocketnas:// link) and
// keeps the connection status fresh.
func (a *app) watch() {
	t := time.NewTicker(2 * time.Second)
	n := 0
	for range t.C {
		a.reload(false)
		n++
		if n%15 == 0 { // ~30s
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
	a.mu.Lock()
	a.connected = err == nil
	a.connErr = ""
	if err != nil {
		a.connErr = err.Error()
	}
	a.mu.Unlock()
	a.refreshMenu()
}

func (a *app) statusText() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.paired {
		return "Chưa ghép đôi — bấm \"Kết nối app\" trên trang web PocketNAS"
	}
	role := "User (chỉ xem)"
	if a.cfg.Role == auth.RoleAdmin {
		role = "Admin"
	}
	drive, running, mErr := a.mount.status()
	switch {
	case a.connErr != "":
		return "⚠ Không kết nối được server: " + short(a.connErr)
	case running:
		return fmt.Sprintf("✔ Đã kết nối • %s • ổ %s", role, drive)
	case mErr != "":
		return "⚠ " + short(mErr)
	case a.connected:
		return "Đang gắn ổ đĩa… • " + role
	default:
		return "Đang kết nối… • " + role
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
