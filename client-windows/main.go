//go:build windows

// Command NASfone is the Windows client: a tray app that pairs with a
// NASfone server (via nasfone:// links or a pasted invite) and mounts it
// as a drive letter through rclone + WinFsp.
//
//	NASfone.exe                     run the tray app
//	NASfone.exe nasfone://pair?…  pair with the server in the link
//	NASfone.exe token               print a fresh bearer token (used by rclone)
//	NASfone.exe --quit              ask the running app to exit (unmounts first)
//	NASfone.exe --cleanup[-all]     uninstall hook: quit, remove nasfone:// and
//	                                  autostart (and with -all, pairing data)
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"nasfone/core/auth"
	"nasfone/core/client"
	"nasfone/core/pair"
)

func main() {
	args := os.Args[1:]
	// Installer / uninstaller hooks; they must not re-register anything.
	if len(args) == 1 {
		switch args[0] {
		case "--quit":
			if requestQuit() {
				waitForExit(15 * time.Second)
			}
			return
		case "--cleanup":
			cleanup(false)
			return
		case "--cleanup-all":
			cleanup(true)
			return
		}
	}

	exe, _ := os.Executable()
	registerProtocol(exe) // keep the nasfone:// handler pointing at this copy

	switch {
	case len(args) == 1 && args[0] == "token":
		os.Exit(printToken())
	case len(args) >= 1 && strings.HasPrefix(strings.ToLower(args[0]), "nasfone:"):
		pairFromInvite(args[0])
		if !singleInstance() {
			requestShow() // the running tray picks up the pairing; show its window
			return
		}
		runTray(true)
	default:
		if !singleInstance() {
			requestShow() // already running: bring its window up instead
			return
		}
		runTray(!(len(args) == 1 && args[0] == "--minimized"))
	}
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// appVersion is set at build time (-X main.appVersion=…).
var appVersion = "dev"

// printToken is called by rclone (--webdav-bearer-token-command). It must
// print only the token on stdout.
func printToken() int {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	key, err := loadKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := client.Authenticate(ctx, httpClient, cfg, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Print(tok.Value)
	return 0
}

// pairFromInvite asks the user to confirm, then pairs. Any web page can open
// a nasfone:// link, so pairing must never happen silently: the user sees
// which server and role they are agreeing to.
func pairFromInvite(invite string) bool {
	inv, err := pair.ParseInvite(invite)
	if err != nil {
		fail(t("invite_bad", err.Error()))
		return false
	}
	host := inv.URL
	if u, err := url.Parse(inv.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	role := t("role_user_long")
	if inv.Role == auth.RoleAdmin {
		role = t("role_admin_long")
	}
	msg := t("pair_confirm", host, role, pair.ShortFP(inv.FP))
	if old, err := loadConfig(); err == nil {
		msg += t("pair_replaces", old.URL)
	}
	if !ask(msg) {
		return false
	}
	key, err := client.GenerateKey()
	if err != nil {
		fail(t("key_fail", err.Error()))
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cfg, err := client.Pair(ctx, httpClient, invite, key, "Windows – "+computerName(), "windows")
	if err != nil {
		fail(t("pair_fail", err.Error()))
		return false
	}
	if err := saveKey(key); err != nil {
		fail(t("key_save_fail", err.Error()))
		return false
	}
	if err := saveConfig(cfg); err != nil {
		fail(t("cfg_save_fail", err.Error()))
		return false
	}
	info(t("pair_done", host, strings.ToUpper(string(cfg.Role))))
	return true
}
