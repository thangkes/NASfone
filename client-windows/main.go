//go:build windows

// Command PocketNAS is the Windows client: a tray app that pairs with a
// PocketNAS server (via pocketnas:// links or a pasted invite) and mounts it
// as a drive letter through rclone + WinFsp.
//
//	PocketNAS.exe                     run the tray app
//	PocketNAS.exe pocketnas://pair?…  pair with the server in the link
//	PocketNAS.exe token               print a fresh bearer token (used by rclone)
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"pocketnas/core/auth"
	"pocketnas/core/client"
	"pocketnas/core/pair"
)

func main() {
	exe, _ := os.Executable()
	registerProtocol(exe) // keep the pocketnas:// handler pointing at this copy

	args := os.Args[1:]
	switch {
	case len(args) == 1 && args[0] == "token":
		os.Exit(printToken())
	case len(args) >= 1 && strings.HasPrefix(strings.ToLower(args[0]), "pocketnas:"):
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
// a pocketnas:// link, so pairing must never happen silently: the user sees
// which server and role they are agreeing to.
func pairFromInvite(invite string) bool {
	inv, err := pair.ParseInvite(invite)
	if err != nil {
		fail("Lời mời ghép đôi không hợp lệ.\n\n" + err.Error())
		return false
	}
	host := inv.URL
	if u, err := url.Parse(inv.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	role := "USER — chỉ xem và tải về"
	if inv.Role == auth.RoleAdmin {
		role = "ADMIN — toàn quyền (tải lên, ghi đè, xóa)"
	}
	msg := fmt.Sprintf("Ghép máy tính này với PocketNAS?\n\nServer: %s\nQuyền: %s\nVân tay server: %s\n\n"+
		"Chỉ đồng ý nếu chính bạn vừa bấm \"Kết nối app\" trên trang PocketNAS của mình.",
		host, role, pair.ShortFP(inv.FP))
	if old, err := loadConfig(); err == nil {
		msg += fmt.Sprintf("\n\nMáy này đang ghép với %s — ghép mới sẽ thay thế.", old.URL)
	}
	if !ask(msg) {
		return false
	}
	key, err := client.GenerateKey()
	if err != nil {
		fail("Không tạo được khóa: " + err.Error())
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cfg, err := client.Pair(ctx, httpClient, invite, key, "Windows – "+computerName(), "windows")
	if err != nil {
		fail("Ghép đôi thất bại.\n\n" + err.Error())
		return false
	}
	if err := saveKey(key); err != nil {
		fail("Không lưu được khóa: " + err.Error())
		return false
	}
	if err := saveConfig(cfg); err != nil {
		fail("Không lưu được cấu hình: " + err.Error())
		return false
	}
	info(fmt.Sprintf("Đã ghép đôi với %s (quyền %s).\n\nPocketNAS sẽ hiện thành một ổ đĩa trong File Explorer sau vài giây.", host, strings.ToUpper(string(cfg.Role))))
	return true
}
