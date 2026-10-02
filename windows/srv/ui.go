//go:build windows

package srv

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	webview2 "github.com/jchv/go-webview2"

	"nasfone/core/mobile"
)

//go:embed ui.html
var uiHTML string

var (
	winMu  sync.Mutex
	winRef webview2.WebView // the open window, if any
)

// openWindow shows the window, or brings the existing one to the front.
// Each window runs its own message loop on a dedicated OS thread.
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
			DataPath:  filepath.Join(dataDir(), "webview"),
			AutoFocus: true,
			WindowOptions: webview2.WindowOptions{
				Title:  appName,
				IconId: 1, // RT_GROUP_ICON #1 from winres/winres.json
				Width:  560,
				Height: 820,
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
		w.Run()
		winMu.Lock()
		winRef = nil
		winMu.Unlock()
		w.Destroy()
	}()
}

// uiState is everything the window renders; the page polls it every second.
type uiState struct {
	Configured bool            `json:"configured"`
	Running    bool            `json:"running"`
	StatusCode string          `json:"statusCode"` // ok | login | starting | stopped | error
	StatusText string          `json:"statusText"`
	Status     map[string]any  `json:"status"`
	Codes      json.RawMessage `json:"codes"`
	Devices    json.RawMessage `json:"devices"`
	Paired     json.RawMessage `json:"paired"`
	Pending    json.RawMessage `json:"pending"`
	Settings   settings        `json:"settings"`
	Autostart  bool            `json:"autostart"`
	Version    string          `json:"version"`
	Update     string          `json:"update"`
	Updating   bool            `json:"updating"`
	FP         string          `json:"fp"`
	Logs       []string        `json:"logs"`
	DataDir    string          `json:"dataDir"`
}

func raw(s, empty string) json.RawMessage {
	if strings.TrimSpace(s) == "" {
		s = empty
	}
	return json.RawMessage(s)
}

func (a *app) state() uiState {
	code, text := a.statusText()
	s := uiState{
		Configured: getSettings().Configured, StatusCode: code, StatusText: text,
		Settings: getSettings(), Autostart: autostartEnabled(), Version: appVersion, DataDir: dataDir(),
		Codes: raw("", "null"), Devices: raw("", "[]"), Paired: raw("", "[]"), Pending: raw("", "[]"),
	}
	a.mu.Lock()
	s.Running = a.running
	s.Status = a.status
	if a.update != nil {
		s.Update = a.update.Version
	}
	s.Updating = a.updating
	from := len(a.logs) - 80
	if from < 0 {
		from = 0
	}
	s.Logs = append([]string(nil), a.logs[from:]...)
	a.mu.Unlock()
	if s.Running {
		s.Codes = raw(mobile.LoginCode(), "null")
		s.Devices = raw(mobile.Devices(), "[]")
		s.Paired = raw(mobile.PairedDevices(), "[]")
		s.Pending = raw(mobile.LanPending(), "[]")
		s.FP = mobile.ServerFingerprint()
	}
	return s
}

func (a *app) bind(w webview2.WebView) {
	w.Bind("nsState", func() uiState { return a.state() })
	w.Bind("nsDict", func() map[string]string { return uiDict() })
	w.Bind("nsLang", func() string { return lang() })
	w.Bind("nsSetLang", func(l string) {
		updateSettings(func(s *settings) { s.Lang = l })
		a.relabel()
	})

	// First run and settings.
	w.Bind("nsDefaults", func() map[string]string {
		return map[string]string{"rootDir": defaultRoot(), "hostname": defaultHostname()}
	})
	w.Bind("nsChooseFolder", func(current string) string {
		p := chooseFolder(uintptr(w.Window()), t("pick_folder"))
		if p == "" {
			return current
		}
		return p
	})
	w.Bind("nsSaveSettings", func(in settings) string { return a.saveSettings(in) })
	w.Bind("nsSetAutostart", func(on bool) string {
		if err := setAutostart(on); err != nil {
			return err.Error()
		}
		if on {
			mAuto.Check()
		} else {
			mAuto.Uncheck()
		}
		return ""
	})

	// Server.
	w.Bind("nsToggle", func() {
		if a.isRunning() {
			go a.stop()
		} else {
			go a.start()
		}
	})
	w.Bind("nsLogin", func() string {
		if u := a.statusString("authURL"); u != "" {
			openURL(u)
			return ""
		}
		a.mu.Lock()
		a.authOpened = "wanted" // open the sign-in page as soon as the core reports it
		a.mu.Unlock()
		if err := mobile.Login(); err != nil {
			return err.Error()
		}
		return ""
	})
	w.Bind("nsLogout", func() string {
		if err := mobile.Logout(); err != nil {
			return err.Error()
		}
		return ""
	})
	w.Bind("nsSetFunnel", func(on bool) {
		updateSettings(func(s *settings) { s.Funnel = on })
		if a.isRunning() {
			go mobile.SetFunnel(on)
		}
	})
	w.Bind("nsSetLan", func(on bool, port int) string {
		if on && (port < 1024 || port > 65535) {
			return t("lan_port_bad")
		}
		updateSettings(func(s *settings) {
			s.LANEnabled = on
			if port != 0 {
				s.LANPort = port
			}
		})
		if !a.isRunning() {
			return ""
		}
		p := 0
		if on {
			p = port
		}
		if err := mobile.SetLan(p); err != nil {
			updateSettings(func(s *settings) { s.LANEnabled = false })
			return t("lan_open_fail", err.Error())
		}
		return ""
	})
	w.Bind("nsApproveLan", func(ticket string) string {
		if err := mobile.ApproveLanTicket(ticket); err != nil {
			return err.Error()
		}
		return ""
	})
	w.Bind("nsRejectLan", func(ticket string) { mobile.RejectLanTicket(ticket) })

	// Sign-in sessions and paired apps.
	w.Bind("nsRevokeDevice", func(id string) { mobile.RevokeDevice(id) })
	w.Bind("nsRevokeAllDevices", func() { mobile.RevokeAllDevices() })
	w.Bind("nsRevokePaired", func(id string) { mobile.RevokePaired(id) })
	w.Bind("nsSetRole", func(id, role string) { mobile.SetPairedRole(id, role) })
	w.Bind("nsNewInvite", func(role string) map[string]any {
		js, err := mobile.NewPairInvite(role)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		var inv map[string]any
		json.Unmarshal([]byte(js), &inv)
		if s, _ := inv["invite"].(string); s != "" {
			if png, err := mobile.QRPNG(s, 6); err == nil {
				inv["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			}
		}
		return inv
	})

	// Shell helpers.
	w.Bind("nsCopy", func(text string) { setClipboard(text) })
	w.Bind("nsOpenURL", func(u string) {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			openURL(u)
		}
	})
	w.Bind("nsOpenFolder", func() { exec.Command("explorer.exe", getSettings().RootDir).Start() })
	w.Bind("nsOpenLogs", func() { exec.Command("explorer.exe", dataDir()).Start() })
	w.Bind("nsUpdate", func() { go a.applyUpdate() })
	w.Bind("nsSwitchRole", func() {
		if SwitchRole != nil {
			go SwitchRole()
		}
	})
}

// saveSettings validates and stores settings; changes that the running core
// cannot pick up live (folder, name, control server, verbose) restart it.
func (a *app) saveSettings(in settings) string {
	in.Hostname = strings.ToLower(strings.TrimSpace(in.Hostname))
	in.RootDir = strings.TrimSpace(in.RootDir)
	in.ControlURL = strings.TrimSpace(in.ControlURL)
	if !hostRe.MatchString(in.Hostname) {
		return t("host_bad")
	}
	if in.ControlURL != "" && !strings.HasPrefix(in.ControlURL, "https://") {
		return t("control_bad")
	}
	if in.RootDir == "" || !filepath.IsAbs(in.RootDir) {
		return t("folder_bad")
	}
	if err := os.MkdirAll(in.RootDir, 0o755); err != nil {
		return t("folder_fail", err.Error())
	}
	old := getSettings()
	firstRun := !old.Configured
	err := updateSettings(func(s *settings) {
		s.Configured = true
		s.RootDir, s.Hostname, s.ControlURL, s.Verbose = in.RootDir, in.Hostname, in.ControlURL, in.Verbose
	})
	if err != nil {
		return err.Error()
	}
	changed := old.RootDir != in.RootDir || old.Hostname != in.Hostname || old.ControlURL != in.ControlURL || old.Verbose != in.Verbose
	go func() {
		if old.ControlURL != in.ControlURL && old.Configured {
			// The sign-in belongs to the old control server: start over there.
			a.stop()
			os.RemoveAll(filepath.Join(dataDir(), "tsnet"))
			a.log(t("log_control_changed"))
			a.start()
			return
		}
		switch {
		case firstRun:
			if autostartErr := setAutostart(true); autostartErr == nil {
				mAuto.Check()
			}
			a.start()
		case changed && a.isRunning():
			a.restart()
		}
	}()
	return ""
}
