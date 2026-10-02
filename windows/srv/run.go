//go:build windows

// Package srv is the server role of NASfone for Windows: it shares a folder
// of this PC like the Android server app does (embedded Tailscale, Funnel,
// rolling sign-in codes, paired apps, LAN access), from a tray icon.
// NASfone.exe runs it when this PC was set up as a server.
package srv

import "time"

// appVersion is set by the main program (SetVersion) before Run.
var appVersion = "dev"

// SetVersion passes the build version in from the main program.
func SetVersion(v string) { appVersion = v }

// SwitchRole, when set by the main program, switches this PC to the client
// role (it saves the choice and restarts the app).
var SwitchRole func()

// Run runs the server tray app until it quits. showWindow opens the window
// right away (normal launch) rather than starting quietly in the tray.
func Run(showWindow bool) {
	if !singleInstance() {
		requestShow() // already running: bring its window up
		return
	}
	runTray(showWindow)
}

// Quit asks a running server instance to stop and waits for it (installer hook).
func Quit() {
	if requestQuit() {
		waitForExit(20 * time.Second)
	}
}

// Running reports whether a server instance is running.
func Running() bool { return !mutexFree() }

// WaitStopped waits until no server instance is left (after a role switch).
func WaitStopped(timeout time.Duration) { waitForExit(timeout) }
