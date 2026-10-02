//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/systray"
	webview2 "github.com/jchv/go-webview2"
)

// What this PC does: "client" mounts a NASfone server as a drive, "server"
// shares a folder of this PC (package srv). Chosen on first launch,
// changeable later; one role at a time.
const (
	roleClient = "client"
	roleServer = "server"
)

func rolePath() string { return filepath.Join(dataDir(), "role.txt") }

// loadRole returns the saved role, or "" when not chosen yet. Installs from
// before the choice existed keep doing what they did.
func loadRole() string {
	if b, err := os.ReadFile(rolePath()); err == nil {
		if r := strings.TrimSpace(string(b)); r == roleClient || r == roleServer {
			return r
		}
	}
	if len(listServers()) > 0 {
		saveRole(roleClient) // already paired: a client
		return roleClient
	}
	if base, err := os.UserConfigDir(); err == nil {
		if _, err := os.Stat(filepath.Join(base, "NASfone-Server", "settings.json")); err == nil {
			saveRole(roleServer) // the Windows server beta was set up here
			return roleServer
		}
	}
	return ""
}

func saveRole(r string) { os.WriteFile(rolePath(), []byte(r+"\n"), 0o600) }

// switchRole saves the new role and restarts NASfone in it. The new process
// waits until this one has exited (--switched).
func switchRole(to string) {
	saveRole(to)
	exe, _ := os.Executable()
	exec.Command(exe, "--switched").Start()
	systray.Quit()
}

const chooserHTML = `<!doctype html><html><head><meta charset="utf-8"><style>
:root{--bg:#f4f5f7;--card:#fff;--fg:#1d2330;--muted:#6b7385;--line:#e3e6ec}
@media (prefers-color-scheme:dark){:root{--bg:#0f1218;--card:#181c24;--fg:#e6e9ef;--muted:#949cad;--line:#2a303c}}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 "Segoe UI Variable Text","Segoe UI",system-ui,sans-serif;padding:22px;user-select:none}
h1{font-size:22px;margin:0}p{color:var(--muted);margin:4px 0 18px}
.c{display:block;width:100%;text-align:left;border:0;border-radius:14px;padding:16px 18px;margin-bottom:12px;color:#fff;cursor:pointer;font:inherit}
.c b{font-size:17px;display:block;margin-bottom:4px}.s{background:#1f9d55}.k{background:#2f6fed}.c:hover{filter:brightness(1.08)}
small{color:var(--muted)}</style></head><body>
<h1>NASfone</h1><p>{{Q}}</p>
<button class="c s" onclick="pick('server')"><b>📦 {{S}}</b>{{SD}}</button>
<button class="c k" onclick="pick('client')"><b>💻 {{C}}</b>{{CD}}</button>
<small>{{N}}</small></body></html>`

// chooseRole shows the first-launch chooser and returns the pick ("" if closed).
func chooseRole() string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(dataDir(), "webview"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "NASfone", IconId: 1, Width: 460, Height: 470, Center: true,
		},
	})
	if w == nil {
		// No WebView2: fall back to a plain question.
		if ask(t("role_fallback_q")) {
			return roleServer
		}
		return roleClient
	}
	defer w.Destroy()
	picked := ""
	w.Bind("pick", func(r string) {
		if r == roleClient || r == roleServer {
			picked = r
		}
		w.Terminate()
	})
	html := strings.NewReplacer("{{Q}}", t("role_q"), "{{S}}", t("role_server"), "{{SD}}", t("role_server_desc"),
		"{{C}}", t("role_client"), "{{CD}}", t("role_client_desc"), "{{N}}", t("role_note")).Replace(chooserHTML)
	w.SetHtml(html)
	w.Run()
	return picked
}
