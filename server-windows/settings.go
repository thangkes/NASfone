//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// dataDir is %APPDATA%\NASfone-Server: settings, Tailscale state (tsnet\),
// server identity and paired apps (pair\), browser sessions, logs. It is
// kept on uninstall so a reinstall keeps the same server.
func dataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "NASfone-Server")
	os.MkdirAll(d, 0o700)
	return d
}

// settings of the server. Nothing about the Tailscale account lives here:
// the user signs in from the app, and any account or control server works.
type settings struct {
	Configured bool   `json:"configured"` // first-run setup done
	RootDir    string `json:"rootDir"`    // shared folder
	Hostname   string `json:"hostname"`   // machine name in the tailnet
	ControlURL string `json:"controlURL"` // "" = Tailscale; set for Headscale
	Funnel     bool   `json:"funnel"`
	LANEnabled bool   `json:"lanEnabled"`
	LANPort    int    `json:"lanPort"`
	Verbose    bool   `json:"verbose"`
	Lang       string `json:"lang"` // "", "en" or "vi"
}

var (
	settingsMu sync.Mutex
	settingsV  *settings
)

func settingsPath() string { return filepath.Join(dataDir(), "settings.json") }

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = `C:\`
	}
	return filepath.Join(home, "NASfone")
}

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// defaultHostname is "nasfone-<computer>" cleaned up for DNS.
func defaultHostname() string {
	n := strings.ToLower(computerName())
	n = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(n, "-")
	n = strings.Trim("nasfone-"+n, "-")
	if len(n) > 40 {
		n = strings.Trim(n[:40], "-")
	}
	if !hostRe.MatchString(n) {
		return "nasfone-pc"
	}
	return n
}

func getSettings() settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settingsV == nil {
		settingsV = &settings{RootDir: defaultRoot(), Hostname: defaultHostname(), LANPort: 8080}
		if b, err := os.ReadFile(settingsPath()); err == nil {
			json.Unmarshal(b, settingsV)
		}
		if settingsV.LANPort == 0 {
			settingsV.LANPort = 8080
		}
	}
	return *settingsV
}

func updateSettings(f func(*settings)) error {
	s := getSettings()
	f(&s)
	settingsMu.Lock()
	defer settingsMu.Unlock()
	settingsV = &s
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath())
}
