//go:build windows

package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"nasfone/core/update"
)

type releaseInfo = update.Release

// updateLoop looks for a newer release a minute after start and then every
// few hours. It only offers it (tray item + window banner).
func (a *app) updateLoop() {
	time.Sleep(time.Minute)
	for {
		a.checkUpdate()
		time.Sleep(6 * time.Hour)
	}
}

func isServerSetup(name string) bool {
	return strings.HasPrefix(name, "NASfone-Windows-Server-Setup-") && strings.HasSuffix(name, ".exe")
}

func (a *app) checkUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := update.Check(ctx, http.DefaultClient, appVersion, isServerSetup)
	if err != nil || r == nil {
		return
	}
	a.mu.Lock()
	a.update = r
	a.mu.Unlock()
	mUpdate.SetTitle(t("m_update", r.Version))
	mUpdate.Show()
	a.changed()
}

// applyUpdate downloads the installer (checksum-verified) and runs it; the
// installer stops this app, upgrades in place and starts it again.
func (a *app) applyUpdate() {
	a.mu.Lock()
	r, busy := a.update, a.updating
	if r != nil && !busy {
		a.updating = true
	}
	a.mu.Unlock()
	if r == nil || busy {
		return
	}
	done := func() {
		a.mu.Lock()
		a.updating = false
		a.mu.Unlock()
		a.changed()
	}
	a.changed()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	dest := filepath.Join(os.TempDir(), r.AssetName)
	if err := update.Download(ctx, http.DefaultClient, r, dest); err != nil {
		done()
		fail(t("update_fail", err.Error()))
		return
	}
	// ShellExecute so Windows shows the UAC prompt the installer needs.
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(dest)
	args, _ := windows.UTF16PtrFromString("/SILENT /SUPPRESSMSGBOXES /NORESTART")
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		done()
		fail(t("update_fail", err.Error()))
		return
	}
	done()
}
