//go:build windows

package main

import (
	"errors"
	"fmt"
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
	return "", errors.New("không tìm thấy rclone.exe (đặt cạnh NASfone.exe hoặc cài bằng: winget install Rclone.Rclone)")
}

func winfspInstalled() bool {
	for _, p := range []string{`C:\Program Files (x86)\WinFsp\bin\winfsp-x64.dll`, `C:\Program Files\WinFsp\bin\winfsp-x64.dll`} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// mounter keeps one `rclone mount` running for the paired server and
// restarts it with backoff if it exits.
type mounter struct {
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

// start mounts cfg at a free drive letter (P: preferred). Calling it again
// first unmounts the previous mount.
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
		return errors.New("chưa cài WinFsp (winget install WinFsp.WinFsp)")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	drive := freeDriveLetter(getSettings().Drive)
	if drive == "" {
		return errors.New("không còn ký tự ổ đĩa trống")
	}
	tokenCmd := exe + " token"
	if strings.ContainsRune(exe, ' ') {
		tokenCmd = `"` + exe + `" token`
	}
	args := []string{
		"mount", ":webdav:", drive,
		"--webdav-url=" + cfg.URL,
		"--webdav-vendor=other",
		"--webdav-bearer-token-command=" + tokenCmd,
		"--volname=NASfone",
		"--vfs-cache-mode=full",
		"--vfs-cache-max-size=2G",
		"--dir-cache-time=15s",
		"--log-file=" + filepath.Join(dataDir(), "rclone.log"),
		"--log-level=INFO",
	}
	if cfg.Role != auth.RoleAdmin {
		args = append(args, "--read-only") // user role: browse and download only
	}
	cmd := exec.Command(rclone, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("không chạy được rclone: %w", err)
	}
	m.mu.Lock()
	m.cmd, m.drive, m.running, m.lastErr = cmd, drive, true, ""
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-stop:
		cmd.Process.Kill()
		<-done
		return nil
	case err := <-done:
		if err == nil {
			err = errors.New("rclone đã dừng")
		}
		return fmt.Errorf("ổ đĩa bị ngắt: %v (xem %s)", err, filepath.Join(dataDir(), "rclone.log"))
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
