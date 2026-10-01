//go:build windows

package main

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const quitEventName = `Local\PocketNASQuit`

// requestQuit asks a running instance to exit cleanly (it unmounts the drive
// first). It reports whether an instance was running.
func requestQuit() bool {
	name, _ := windows.UTF16PtrFromString(quitEventName)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	windows.SetEvent(h)
	return true
}

// waitQuitRequests calls quit when an installer or uninstaller asks the app to exit.
func waitQuitRequests(quit func()) {
	name, _ := windows.UTF16PtrFromString(quitEventName)
	h, err := windows.CreateEvent(nil, 0, 0, name)
	if err != nil {
		return
	}
	if ev, _ := windows.WaitForSingleObject(h, windows.INFINITE); ev == windows.WAIT_OBJECT_0 {
		quit()
	}
}

// waitForExit polls until no tray instance owns the single-instance mutex.
func waitForExit(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	name, _ := windows.UTF16PtrFromString(`Local\PocketNASTray`)
	for time.Now().Before(deadline) {
		h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
		if err != nil {
			return // gone
		}
		windows.CloseHandle(h)
		time.Sleep(200 * time.Millisecond)
	}
}

// cleanup undoes per-user integration for the uninstaller: stops the app,
// removes the pocketnas:// handler and the autostart entry. Pairing data in
// %APPDATA%\PocketNAS is kept unless removeData is set.
func cleanup(removeData bool) {
	if requestQuit() {
		waitForExit(15 * time.Second)
	}
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\pocketnas\shell\open\command`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\pocketnas\shell\open`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\pocketnas\shell`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\pocketnas`)
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		k.DeleteValue(appTitle)
		k.Close()
	}
	if removeData {
		os.RemoveAll(dataDir())
	}
}
