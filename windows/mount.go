//go:build windows

package main

import (
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"nasfone/core/auth"
	"nasfone/core/client"
)

const createNoWindow = 0x08000000

// findRclone looks next to this exe first (bundled), then on PATH, then in
// the winget package folder.
func findRclone() (string, error) {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "rclone.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("rclone"); err == nil {
		return p, nil
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		m, _ := filepath.Glob(filepath.Join(la, `Microsoft\WinGet\Packages\Rclone.Rclone_*\rclone-*\rclone.exe`))
		if len(m) > 0 {
			return m[len(m)-1], nil
		}
	}
	return "", errors.New(t("no_rclone"))
}

func winfspInstalled() bool {
	for _, p := range []string{`C:\Program Files (x86)\WinFsp\bin\winfsp-x64.dll`, `C:\Program Files\WinFsp\bin\winfsp-x64.dll`} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// Drive letters handed to running mounts, so two servers starting at the
// same moment never pick the same free letter.
var (
	claimMu sync.Mutex
	claimed = map[string]string{} // "P:" -> server id
)

// claimDrive picks a free letter for id (its preference first) and reserves it.
func claimDrive(id, pref string) string {
	claimMu.Lock()
	defer claimMu.Unlock()
	for l, owner := range claimed {
		if owner == id {
			delete(claimed, l)
		}
	}
	var skip []string
	for l := range claimed {
		skip = append(skip, l)
	}
	d := freeDriveLetter(pref, skip)
	if d != "" {
		claimed[d] = id
	}
	return d
}

func releaseDrive(id string) {
	claimMu.Lock()
	defer claimMu.Unlock()
	for l, owner := range claimed {
		if owner == id {
			delete(claimed, l)
		}
	}
}

// volumeName labels the drive in File Explorer after the server's host,
// e.g. "NASfone" or "NASfone (office-pc)".
func volumeName(cfg client.Config) string {
	host := cfg.URL
	if u, err := url.Parse(cfg.URL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	label := host
	if net.ParseIP(host) == nil {
		label, _, _ = strings.Cut(host, ".")
	}
	if label == "" || strings.EqualFold(label, "nasfone") {
		return "NASfone"
	}
	return "NASfone (" + label + ")"
}

// mounter keeps one `rclone mount` running for one paired server and
// restarts it with backoff if it exits.
type mounter struct {
	id      string
	mu      sync.Mutex
	cmd     *exec.Cmd
	drive   string
	stop    chan struct{}
	lastErr string
	running bool
}

func (m *mounter) status() (drive string, running bool, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.drive, m.running, m.lastErr
}

// start mounts cfg at a free drive letter (the server's preferred one if
// free). Calling it again first unmounts the previous mount.
func (m *mounter) start(cfg client.Config) {
	m.stopMount()
	m.mu.Lock()
	m.stop = make(chan struct{})
	stop := m.stop
	m.mu.Unlock()
	go m.loop(cfg, stop)
}

func (m *mounter) loop(cfg client.Config, stop chan struct{}) {
	backoff := 3 * time.Second
	for {
		started := time.Now()
		err := m.runOnce(cfg, stop)
		select {
		case <-stop:
			return
		default:
		}
		m.mu.Lock()
		m.running = false
		if err != nil {
			m.lastErr = err.Error()
		}
		m.mu.Unlock()
		if time.Since(started) > time.Minute {
			backoff = 3 * time.Second // it ran fine for a while; retry quickly
		}
		select {
		case <-stop:
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (m *mounter) runOnce(cfg client.Config, stop chan struct{}) error {
	rclone, err := findRclone()
	if err != nil {
		return err
	}
	if !winfspInstalled() {
		return errors.New(t("no_winfsp"))
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	drive := claimDrive(m.id, serverDrive(m.id))
	if drive == "" {
		return errors.New(t("no_drive"))
	}
	tokenCmd := exe + " token " + m.id
	if strings.ContainsRune(exe, ' ') {
		tokenCmd = `"` + exe + `" token ` + m.id
	}
	logFile := filepath.Join(dataDir(), "rclone-"+m.id+".log")
	args := []string{
		"mount", ":webdav:", drive,
		"--webdav-url=" + cfg.URL,
		"--webdav-vendor=other",
		"--webdav-bearer-token-command=" + tokenCmd,
		"--volname=" + volumeName(cfg),
		"--vfs-cache-mode=full",
		"--vfs-cache-max-size=2G",
		"--dir-cache-time=15s",
		"--log-file=" + logFile,
		"--log-level=INFO",
	}
	if cfg.Role != auth.RoleAdmin {
		args = append(args, "--read-only") // user role: browse and download only
	}
	cmd := exec.Command(rclone, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		releaseDrive(m.id)
		return errors.New(t("rclone_start", err))
	}
	m.mu.Lock()
	m.cmd, m.drive, m.running, m.lastErr = cmd, drive, true, ""
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer releaseDrive(m.id)
	select {
	case <-stop:
		cmd.Process.Kill()
		<-done
		return nil
	case err := <-done:
		if err == nil {
			err = errors.New(t("rclone_stopped"))
		}
		return errors.New(t("drive_lost", err, logFile))
	}
}

func (m *mounter) stopMount() {
	m.mu.Lock()
	stop := m.stop
	m.stop = nil
	m.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	// Kill rclone here too: at exit the process may end before loop gets to it.
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
	}
	m.mu.Unlock()
	// Wait briefly for the drive to disappear.
	for i := 0; i < 20; i++ {
		m.mu.Lock()
		cmd := m.cmd
		m.mu.Unlock()
		if cmd == nil || cmd.ProcessState != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	m.mu.Lock()
	m.cmd, m.running = nil, false
	m.mu.Unlock()
}
