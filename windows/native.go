//go:build windows

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/winfsp/cgofuse/fuse"

	"nasfone/core/auth"
	"nasfone/core/client"
	"nasfone/winclient/drive"
)

// errOldServer means the server predates the direct-write API; the drive is
// then mounted through rclone instead.
var errOldServer = errors.New("server without direct writes")

// driveHTTP has no overall timeout (a 4 MB chunk over a slow relay can take
// longer than any fixed limit); each operation carries its own deadline. A
// dead connection is noticed within a minute instead.
var driveHTTP = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 60 * time.Second, // after the request (incl. its chunk) is sent
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   8,
	ForceAttemptHTTP2:     true,
}}

// runNative mounts the server with NASfone's own drive (package drive):
// writes go straight into the file on the server, nothing is staged locally.
func (m *mounter) runNative(cfg client.Config, letter string, stop chan struct{}) error {
	key, err := loadKey(m.id)
	if err != nil {
		return err
	}
	r := &drive.Remote{
		Base: strings.TrimRight(cfg.URL, "/"),
		HC:   driveHTTP,
		Token: func(ctx context.Context) (string, time.Time, error) {
			tok, err := client.Authenticate(ctx, driveHTTP, cfg, key)
			return tok.Value, tok.Expires, err
		},
	}
	readOnly := cfg.Role != auth.RoleAdmin
	if !readOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ok, err := r.SupportsPatch(ctx)
		cancel()
		if err != nil && !errors.Is(err, drive.ErrDenied) {
			return err
		}
		if !ok && !errors.Is(err, drive.ErrDenied) {
			return errOldServer
		}
		readOnly = errors.Is(err, drive.ErrDenied) // role changed to user on the server
	}

	fs := drive.New(r, readOnly)
	mounted := make(chan struct{})
	fs.OnMount = func() { close(mounted) }
	fs.OnError = func(p string, err error) {
		go warn(t("write_failed", p, err.Error()))
	}
	host := fuse.NewFileSystemHost(fs)
	host.SetCapCaseInsensitive(true) // Windows names ignore case; drive maps them to the server's
	host.SetCapReaddirPlus(true)
	opts := []string{
		"-o", "uid=-1", "-o", "gid=-1",
		"-o", "volname=" + volumeName(cfg),
		"--FileSystemName=NASfone",
	}
	done := make(chan bool, 1)
	go func() { done <- host.Mount(letter, opts) }()

	select {
	case <-mounted:
	case <-done:
		return errors.New(t("mount_failed"))
	case <-stop:
		host.Unmount()
		<-done
		return nil
	}
	m.mu.Lock()
	m.fs, m.drive, m.running, m.lastErr = fs, letter, true, ""
	m.mu.Unlock()
	select {
	case <-stop:
		host.Unmount()
		<-done
		m.mu.Lock()
		m.fs = nil
		m.mu.Unlock()
		return nil
	case <-done:
		m.mu.Lock()
		m.fs = nil
		m.mu.Unlock()
		return errors.New(t("mount_lost"))
	}
}

// writing counts files on this drive still being written to the server.
func (m *mounter) writing() int {
	m.mu.Lock()
	fs := m.fs
	m.mu.Unlock()
	if fs == nil {
		return 0
	}
	return fs.Writing()
}
