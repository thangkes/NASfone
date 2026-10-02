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

// updateLoop looks for a newer release shortly after start and then every
// few hours. Finding one only offers it (tray item + window banner); nothing
// is installed until the user clicks.
func (a *app) updateLoop() {
	time.Sleep(time.Minute) // let the drive mount first
	for {
		a.checkUpdate()
		time.Sleep(6 * time.Hour)
	}
}

func isSetupAsset(name string) bool {
	return strings.HasPrefix(name, "NASfone-Setup-") && strings.HasSuffix(name, ".exe")
}

func (a *app) checkUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := update.Check(ctx, http.DefaultClient, appVersion, isSetupAsset)
	if err != nil || r == nil {
		return
	}
	a.mu.Lock()
	a.update = r
	a.mu.Unlock()
	a.mUpdate.SetTitle(t("m_update", r.Version))
	a.mUpdate.Show()
}

// applyUpdate downloads the new installer (checksum-verified) and runs it.
// The installer asks this app to quit, upgrades in place and starts it again.
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
	}
	a.mUpdate.SetTitle(t("m_updating", r.Version))
	a.mUpdate.Disable()
	defer func() {
		a.mUpdate.SetTitle(t("m_update", r.Version))
		a.mUpdate.Enable()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	dest := filepath.Join(os.TempDir(), r.AssetName)
	if err := update.Download(ctx, http.DefaultClient, r, dest); err != nil {
		done()
		fail(t("update_fail", err.Error()))
		return
	}
	// ShellExecute (not CreateProcess) so Windows shows the UAC prompt the
	// installer needs. /SILENT keeps a progress window but asks nothing.
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(dest)
	args, _ := windows.UTF16PtrFromString("/SILENT /SUPPRESSMSGBOXES /NORESTART")
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		done()
		fail(t("update_fail", err.Error())) // e.g. the UAC prompt was declined
		return
	}
	done()
}
