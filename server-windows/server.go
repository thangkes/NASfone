//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nasfone/core/mobile"
)

// app is the tray process: it owns the embedded server and the window.
type app struct {
	mu         sync.Mutex
	running    bool
	starting   bool
	startErr   string
	status     map[string]any // last status JSON from the core
	logs       []string
	logFile    *os.File
	authOpened string // auth URL already opened in the browser
	lanSeen    map[string]bool
	update     *releaseInfo
	updating   bool
	notice     string

	onChange func() // tray refresh
}

// ---- mobile.Host ------------------------------------------------------------

type host struct{ a *app }

func (h host) OnStatus(js string) {
	var st map[string]any
	if json.Unmarshal([]byte(js), &st) != nil {
		return
	}
	h.a.mu.Lock()
	h.a.status = st
	authURL, _ := st["authURL"].(string)
	open := authURL != "" && authURL != h.a.authOpened && h.a.authOpened == "wanted"
	if open {
		h.a.authOpened = authURL
	}
	h.a.mu.Unlock()
	if open {
		openURL(authURL) // the user asked to sign in: take them to the Tailscale page
	}
	h.a.changed()
}

func (h host) OnLog(line string) { h.a.log(line) }

func (h host) OnEvent(kind, detail string) { h.a.log("[" + kind + "] " + detail) }

// Interfaces reports the PC's network interfaces in the format the core
// expects (Android needs this from the app; on Windows Go can list them).
func (h host) Interfaces() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, ni := range ifs {
		flags := ""
		if ni.Flags&net.FlagUp != 0 {
			flags += "u"
		}
		if ni.Flags&net.FlagLoopback != 0 {
			flags += "l"
		}
		if ni.Flags&net.FlagPointToPoint != 0 {
			flags += "p"
		}
		if ni.Flags&net.FlagMulticast != 0 {
			flags += "m"
		}
		if ni.Flags&net.FlagBroadcast != 0 {
			flags += "b"
		}
		var addrs []string
		if as, err := ni.Addrs(); err == nil {
			for _, a := range as {
				addrs = append(addrs, a.String())
			}
		}
		fmt.Fprintf(&sb, "%s|%d|%d|%s|%s\n", ni.Name, ni.Index, ni.MTU, flags, strings.Join(addrs, ","))
	}
	return sb.String()
}

// ---- logs ---------------------------------------------------------------------

const maxLogLines = 300

func (a *app) log(line string) {
	line = time.Now().Format("15:04:05") + "  " + line
	a.mu.Lock()
	a.logs = append(a.logs, line)
	if len(a.logs) > maxLogLines {
		a.logs = a.logs[len(a.logs)-maxLogLines:]
	}
	if a.logFile == nil {
		p := filepath.Join(dataDir(), "server.log")
		if st, err := os.Stat(p); err == nil && st.Size() > 2<<20 {
			os.Rename(p, p+".1") // keep one old log
		}
		a.logFile, _ = os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	if a.logFile != nil {
		a.logFile.WriteString(time.Now().Format("2006-01-02 ") + line + "\r\n")
	}
	a.mu.Unlock()
	a.changed()
}

func (a *app) changed() {
	if f := a.onChange; f != nil {
		f()
	}
}

// ---- start / stop -------------------------------------------------------------

func (a *app) start() {
	a.mu.Lock()
	if a.running || a.starting {
		a.mu.Unlock()
		return
	}
	a.starting, a.startErr = true, ""
	a.mu.Unlock()
	a.changed()

	s := getSettings()
	lanPort := 0
	if s.LANEnabled {
		lanPort = s.LANPort
	}
	cfg := map[string]any{
		"stateDir":   filepath.Join(dataDir(), "tsnet"),
		"authFile":   filepath.Join(dataDir(), "auth.json"),
		"rootDir":    s.RootDir,
		"hostname":   s.Hostname,
		"controlURL": s.ControlURL,
		"funnel":     s.Funnel,
		"verbose":    s.Verbose,
		"lanPort":    lanPort,
	}
	b, _ := json.Marshal(cfg)
	mobile.SetCrashFile(filepath.Join(dataDir(), "crash.txt"))
	err := mobile.Start(string(b), host{a})

	a.mu.Lock()
	a.starting = false
	a.running = err == nil
	if err != nil {
		a.startErr = err.Error()
	}
	a.mu.Unlock()
	if err != nil {
		a.log(t("log_start_fail", err.Error()))
	}
	a.changed()
}

func (a *app) stop() {
	a.mu.Lock()
	was := a.running
	a.running = false
	a.status = nil
	a.mu.Unlock()
	if was {
		mobile.Stop()
	}
	a.changed()
}

func (a *app) restart() {
	a.stop()
	a.start()
}

func (a *app) isRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// statusString returns a string field of the last status.
func (a *app) statusString(key string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, _ := a.status[key].(string)
	return v
}

// webURL is the best address to open this server in a browser.
func (a *app) webURL() string {
	if u := a.statusString("funnelURL"); u != "" {
		return u
	}
	if d := a.statusString("dnsName"); d != "" {
		return "http://" + d
	}
	return ""
}
