//go:build windows

package main

import (
	_ "embed"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"nasfone/core/pair"
)

//go:embed ui.html
var uiHTML string

var (
	winMu  sync.Mutex
	winRef webview2.WebView // the open window, if any

	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
)

// openWindow shows the app window, or brings the existing one to the front.
// Each window runs its own message loop on a dedicated OS thread, separate
// from the tray's loop.
func (a *app) openWindow() {
	winMu.Lock()
	if w := winRef; w != nil {
		winMu.Unlock()
		w.Dispatch(func() {
			h := uintptr(w.Window())
			procShowWindow.Call(h, 9) // SW_RESTORE
			procSetForegroundWindow.Call(h)
		})
		return
	}
	winMu.Unlock()

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			DataPath:  dataDir() + `\webview`,
			AutoFocus: true,
			WindowOptions: webview2.WindowOptions{
				Title:  appName,
				IconId: 1, // RT_GROUP_ICON #1 from winres/winres.json
				Width:  460,
				Height: 720,
				Center: true,
			},
		})
		if w == nil {
			fail(t("no_webview"))
			return
		}
		a.bind(w)
		w.SetHtml(strings.Replace(uiHTML, "{{ICON}}", iconDataURL(), 1))
		winMu.Lock()
		winRef = w
		winMu.Unlock()
		w.Run() // returns when the window is closed
		winMu.Lock()
		winRef = nil
		winMu.Unlock()
		w.Destroy()
	}()
}

// uiServer is one paired server as the window shows it.
type uiServer struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	URL        string `json:"url"`
	Role       string `json:"role"`
	DeviceName string `json:"deviceName"`
	ServerFP   string `json:"serverFP"`
	PairedAt   string `json:"pairedAt"`
	Status     string `json:"status"` // ok | connecting | error | revoked
	StatusText string `json:"statusText"`
	Error      string `json:"error"`
	Drive      string `json:"drive"` // mounted drive, e.g. "P:"
	Mounted    bool   `json:"mounted"`
	PrefDrive  string `json:"prefDrive"` // preferred letter, e.g. "P"
	Uploading  string `json:"uploading"` // "12 files (3.4 GB)" still to upload, "" if none
}

// uiState is everything the window renders; the page polls it.
type uiState struct {
	Servers    []uiServer `json:"servers"`
	Status     string     `json:"status"` // overall: ok | connecting | error | unpaired
	StatusText string     `json:"statusText"`
	Autostart  bool       `json:"autostart"`
	FreeDrives []string   `json:"freeDrives"`
	Busy       bool       `json:"busy"`
	Notice     string     `json:"notice"`
	DataDir    string     `json:"dataDir"`
	RcloneOK   bool       `json:"rcloneOK"`
	WinFspOK   bool       `json:"winfspOK"`
	Computer   string     `json:"computer"`
	Version    string     `json:"version"`
	Update     string     `json:"update"` // newer version on offer, "" if none
	Updating   bool       `json:"updating"`
}

func (a *app) state() uiState {
	a.mu.Lock()
	updating, upd := a.updating, ""
	if a.update != nil {
		upd = a.update.Version
	}
	busy, notice := a.busy, a.notice
	a.mu.Unlock()
	_, rcErr := findRclone()
	s := uiState{
		Autostart: autostartEnabled(), FreeDrives: freeLetters(), Busy: busy, Notice: notice,
		DataDir: dataDir(), RcloneOK: rcErr == nil, WinFspOK: winfspInstalled(), Computer: computerName(), Version: appVersion,
		Updating: updating, Update: upd, Servers: []uiServer{},
	}
	counts := map[string]int{}
	for _, sv := range a.list() {
		code, errText := a.serverStatus(sv)
		drive, mounted, _ := sv.mount.status()
		a.mu.Lock()
		cfg, connErr, upFiles, upBytes := sv.cfg, sv.connErr, sv.upFiles, sv.upBytes
		a.mu.Unlock()
		u := uiServer{
			ID: sv.id, Host: hostOf(cfg.URL), URL: cfg.URL, Role: string(cfg.Role), DeviceName: cfg.Name,
			ServerFP: pair.ShortFP(cfg.ServerFP), PairedAt: cfg.PairedAt.Local().Format("15:04 02/01/2006"),
			Status: code, Error: errText, Drive: drive, Mounted: mounted, PrefDrive: serverDrive(sv.id),
		}
		if upFiles > 0 {
			u.Uploading = t("w_upload_n", upFiles, humanBytes(upBytes))
		}
		switch code {
		case "ok":
			u.StatusText = t("s_ok")
		case "revoked":
			u.StatusText, u.Error = t("s_revoked"), t("revoked_msg", u.Host)
		case "error":
			u.StatusText = t("s_conn_err")
			if connErr == "" {
				u.StatusText = t("s_drive_err")
			}
		default:
			u.StatusText = t("s_connecting")
		}
		if u.PrefDrive == "" {
			u.PrefDrive = strings.TrimSuffix(drive, ":")
		}
		counts[code]++
		s.Servers = append(s.Servers, u)
	}
	n := len(s.Servers)
	switch {
	case n == 0:
		s.Status, s.StatusText = "unpaired", t("s_unpaired")
	case counts["ok"] == n:
		s.Status, s.StatusText = "ok", t("s_ok")
		if n > 1 {
			s.StatusText = t("s_ok_n", n)
		}
	case counts["error"]+counts["revoked"] > 0:
		s.Status, s.StatusText = "error", t("s_some_err", counts["error"]+counts["revoked"], n)
		if n == 1 {
			s.StatusText = s.Servers[0].StatusText
		}
	default:
		s.Status, s.StatusText = "connecting", t("s_connecting")
	}
	return s
}

func freeLetters() []string {
	used, _ := windows.GetLogicalDrives()
	var out []string
	for l := byte('D'); l <= 'Z'; l++ {
		if used&(1<<(l-'A')) == 0 {
			out = append(out, string(l))
		}
	}
	return out
}

func (a *app) bind(w webview2.WebView) {
	exe, _ := os.Executable()
	w.Bind("pnState", func() uiState { return a.state() })
	w.Bind("pnOpenDrive", func(id string) {
		if s := a.get(id); s != nil {
			if d, ok, _ := s.mount.status(); ok && d != "" {
				exec.Command("explorer.exe", d+`\`).Start()
			}
		}
	})
	w.Bind("pnOpenWeb", func(id string) {
		if s := a.get(id); s != nil {
			a.mu.Lock()
			u := s.cfg.URL
			a.mu.Unlock()
			if u != "" {
				openURL(u)
			}
		}
	})
	w.Bind("pnOpenLogs", func() { exec.Command("explorer.exe", dataDir()).Start() })
	w.Bind("pnReconnect", func(id string) { go a.remount(id) })
	w.Bind("pnClipboard", func() string {
		t, _ := clipboardText()
		return t
	})
	// pnPair runs in the background (it asks for confirmation and talks to
	// the server); the page follows progress through pnState.
	w.Bind("pnPair", func(text string) {
		go func() {
			a.setBusy(true, "")
			ok := pairFromInvite(text)
			a.setBusy(false, "")
			if ok {
				a.reload()
			}
		}()
	})
	w.Bind("pnSetAutostart", func(on bool) string {
		if err := setAutostart(on, exe); err != nil {
			return err.Error()
		}
		if a.mAuto != nil {
			if on {
				a.mAuto.Check()
			} else {
				a.mAuto.Uncheck()
			}
		}
		return ""
	})
	// pnSetDrive moves one server to another letter; a letter another
	// server prefers is swapped with it, so preferences never collide.
	w.Bind("pnSetDrive", func(id, letter string) {
		l := normLetter(letter)
		if a.get(id) == nil || l == "" {
			return
		}
		old := serverDrive(id)
		var other string
		for _, s := range a.list() {
			if s.id != id && serverDrive(s.id) == l {
				other = s.id
			}
		}
		setServerDrive(id, l)
		go func() {
			if o := a.get(other); o != nil && old != "" {
				setServerDrive(other, old)
				o.mount.stopMount() // free the letter first
				a.remount(id)
				a.remount(other)
				return
			}
			a.remount(id)
		}()
	})
	w.Bind("pnUnpair", func(id string) { go a.unpair(id) })
	w.Bind("pnUpdate", func() { go a.applyUpdate() })
	w.Bind("pnSwitchRole", func() {
		go func() {
			a.stopAll()
			switchRole(roleServer)
		}()
	})
	// After a revocation: drop the dead pairing at once (no question).
	w.Bind("pnRemove", func(id string) {
		if s := a.get(id); s != nil {
			s.mount.stopMount()
			forget(id)
			go a.reload()
		}
	})
	w.Bind("pnDict", func() map[string]string { return uiDict() })
	w.Bind("pnLang", func() string { return lang() })
	w.Bind("pnSetLang", func(l string) {
		setLang(l)
		a.relabel()
	})
}

func (a *app) setBusy(b bool, notice string) {
	a.mu.Lock()
	a.busy, a.notice = b, notice
	a.mu.Unlock()
}

func (a *app) unpair(id string) {
	s := a.get(id)
	if s == nil {
		return
	}
	a.mu.Lock()
	host := s.host()
	a.mu.Unlock()
	msg := t("unpair_q", host)
	if n, b := pendingUploads(id); n > 0 {
		msg += t("unpair_pending", n, humanBytes(b), cacheDir(id))
	}
	if !ask(msg) {
		return
	}
	s.mount.stopMount()
	forget(id)
	a.reload()
}

func openURL(u string) {
	p, _ := windows.UTF16PtrFromString(u)
	verb, _ := windows.UTF16PtrFromString("open")
	windows.ShellExecute(0, verb, p, nil, nil, windows.SW_SHOWNORMAL)
}
