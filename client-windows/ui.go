//go:build windows

package main

import (
	_ "embed"
	"net/url"
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
				Title:  appTitle,
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

// uiState is everything the window renders; the page polls it.
type uiState struct {
	Paired     bool     `json:"paired"`
	Host       string   `json:"host"`
	URL        string   `json:"url"`
	Role       string   `json:"role"`
	DeviceName string   `json:"deviceName"`
	ServerFP   string   `json:"serverFP"`
	PairedAt   string   `json:"pairedAt"`
	Status     string   `json:"status"` // ok | connecting | error | unpaired
	StatusText string   `json:"statusText"`
	Drive      string   `json:"drive"`
	Mounted    bool     `json:"mounted"`
	Autostart  bool     `json:"autostart"`
	PrefDrive  string   `json:"prefDrive"`
	FreeDrives []string `json:"freeDrives"`
	Busy       bool     `json:"busy"`
	Notice     string   `json:"notice"`
	DataDir    string   `json:"dataDir"`
	RcloneOK   bool     `json:"rcloneOK"`
	WinFspOK   bool     `json:"winfspOK"`
	Computer   string   `json:"computer"`
	Version    string   `json:"version"`
	Update     string   `json:"update"` // newer version on offer, "" if none
	Updating   bool     `json:"updating"`
}

func (a *app) state() uiState {
	a.mu.Lock()
	updating, upd := a.updating, ""
	if a.update != nil {
		upd = a.update.Version
	}
	cfg, paired, connErr, busy, notice, revoked := a.cfg, a.paired, a.connErr, a.busy, a.notice, a.revoked
	a.mu.Unlock()
	drive, mounted, mErr := a.mount.status()
	_, rcErr := findRclone()
	s := uiState{
		Paired: paired, URL: cfg.URL, Role: string(cfg.Role), DeviceName: cfg.Name,
		Drive: drive, Mounted: mounted, Autostart: autostartEnabled(),
		PrefDrive: getSettings().Drive, FreeDrives: freeLetters(), Busy: busy, Notice: notice,
		DataDir: dataDir(), RcloneOK: rcErr == nil, WinFspOK: winfspInstalled(), Computer: computerName(), Version: appVersion,
		Updating: updating, Update: upd,
	}
	if paired {
		s.Host = cfg.URL
		if u, err := url.Parse(cfg.URL); err == nil && u.Host != "" {
			s.Host = u.Host
		}
		s.ServerFP = pair.ShortFP(cfg.ServerFP)
		s.PairedAt = cfg.PairedAt.Local().Format("15:04 02/01/2006")
	}
	switch {
	case !paired:
		s.Status, s.StatusText = "unpaired", t("s_unpaired")
	case revoked:
		s.Status, s.StatusText = "revoked", t("s_revoked")
	case connErr != "":
		s.Status, s.StatusText = "error", t("s_conn_err")
	case mounted:
		s.Status, s.StatusText = "ok", t("s_ok")
	case mErr != "":
		s.Status, s.StatusText = "error", t("s_drive_err")
	default:
		s.Status, s.StatusText = "connecting", t("s_connecting")
	}
	if s.Status == "revoked" {
		s.Notice = t("revoked_msg")
	}
	if s.Status == "error" {
		if connErr != "" {
			s.Notice = connErr
		} else {
			s.Notice = mErr
		}
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
	w.Bind("pnOpenDrive", func() {
		if d, ok, _ := a.mount.status(); ok && d != "" {
			exec.Command("explorer.exe", d+`\`).Start()
		}
	})
	w.Bind("pnOpenWeb", func() {
		a.mu.Lock()
		u := a.cfg.URL
		a.mu.Unlock()
		if u != "" {
			openURL(u)
		}
	})
	w.Bind("pnOpenLogs", func() { exec.Command("explorer.exe", dataDir()).Start() })
	w.Bind("pnReconnect", func() {
		go func() {
			a.reload(true)
			a.checkConnection()
		}()
	})
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
				a.reload(true)
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
	w.Bind("pnSetDrive", func(letter string) {
		setDrive(letter)
		go a.reload(true) // remount on the new letter
	})
	w.Bind("pnUnpair", func() {
		go a.unpair()
	})
	// After a revocation: drop the dead pairing at once (no question) so the
	// pairing steps show again.
	w.Bind("pnUpdate", func() { go a.applyUpdate() })
	w.Bind("pnRepair", func() {
		a.mount.stopMount()
		forget()
		go a.reload(true)
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

func (a *app) unpair() {
	a.mu.Lock()
	u := a.cfg.URL
	a.mu.Unlock()
	if !ask(t("unpair_q", u)) {
		return
	}
	a.mount.stopMount()
	forget()
	a.reload(true)
}

func openURL(u string) {
	p, _ := windows.UTF16PtrFromString(u)
	verb, _ := windows.UTF16PtrFromString("open")
	windows.ShellExecute(0, verb, p, nil, nil, windows.SW_SHOWNORMAL)
}
