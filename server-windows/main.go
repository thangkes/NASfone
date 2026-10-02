//go:build windows

// Command NASfoneServer is NASfone for Windows - Server: it shares a folder
// of this PC like the Android server app does (embedded Tailscale, Funnel,
// rolling sign-in codes, paired apps, LAN access), from a tray icon.
//
//	NASfoneServer.exe              run the tray app and open its window
//	NASfoneServer.exe --minimized  run quietly in the tray (autostart)
//	NASfoneServer.exe --quit       ask the running app to stop and exit
//	NASfoneServer.exe --cleanup    uninstall hook: quit and remove autostart
package main

import (
	"os"
	"time"
)

// appVersion is set at build time (-X main.appVersion=…).
var appVersion = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 1 {
		switch args[0] {
		case "--quit":
			if requestQuit() {
				waitForExit(20 * time.Second)
			}
			return
		case "--cleanup":
			cleanup()
			return
		}
	}
	if !singleInstance() {
		requestShow() // already running: bring its window up
		return
	}
	runTray(!(len(args) == 1 && args[0] == "--minimized"))
}
