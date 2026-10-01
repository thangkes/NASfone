//go:build windows

package main

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const appTitle = "NASfone"

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	openClipboard    = user32.NewProc("OpenClipboard")
	closeClipboard   = user32.NewProc("CloseClipboard")
	getClipboardData = user32.NewProc("GetClipboardData")
	globalLock       = kernel32.NewProc("GlobalLock")
	globalUnlock     = kernel32.NewProc("GlobalUnlock")
)

func msgBox(text string, flags uint32) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(appTitle)
	r, _ := windows.MessageBox(0, t, c, flags|windows.MB_SETFOREGROUND|windows.MB_TOPMOST)
	return int(r)
}

func info(text string) { msgBox(text, windows.MB_OK|windows.MB_ICONINFORMATION) }
func warn(text string) { msgBox(text, windows.MB_OK|windows.MB_ICONWARNING) }
func fail(text string) { msgBox(text, windows.MB_OK|windows.MB_ICONERROR) }
func ask(text string) bool {
	return msgBox(text, windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2) == idYes
}

// clipboardText returns the Unicode text on the clipboard.
func clipboardText() (string, error) {
	const cfUnicodeText = 13
	if r, _, err := openClipboard.Call(0); r == 0 {
		return "", err
	}
	defer closeClipboard.Call()
	h, _, err := getClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", err
	}
	p, _, err := globalLock.Call(h)
	if p == 0 {
		return "", err
	}
	defer globalUnlock.Call(h)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(p))), nil
}

// registerProtocol makes nasfone:// links open this executable (per user, no admin).
func registerProtocol(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\nasfone`, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()
	k.SetStringValue("", "URL:NASfone")
	k.SetStringValue("URL Protocol", "")
	cmd, _, err := registry.CreateKey(k, `shell\open\command`, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer cmd.Close()
	return cmd.SetStringValue("", `"`+exe+`" "%1"`)
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appTitle)
	return err == nil
}

func setAutostart(on bool, exe string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(appTitle, `"`+exe+`" --minimized`) // start quietly in the tray
	}
	if err := k.DeleteValue(appTitle); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// singleInstance holds a named mutex for the tray process. It reports false
// if another tray instance already owns it.
func singleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\NASfoneTray`)
	_, err := windows.CreateMutex(nil, false, name)
	return !errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

// freeDriveLetter returns preferred if unused, else the first free letter from P to Z.
func freeDriveLetter(preferred string) string {
	used, _ := windows.GetLogicalDrives()
	free := func(l byte) bool { return used&(1<<(l-'A')) == 0 }
	if p := strings.ToUpper(preferred); len(p) >= 1 && p[0] >= 'A' && p[0] <= 'Z' && free(p[0]) {
		return string(p[0]) + ":"
	}
	for l := byte('P'); l <= 'Z'; l++ {
		if free(l) {
			return string(l) + ":"
		}
	}
	return ""
}

func computerName() string {
	n, _ := os.Hostname()
	if n == "" {
		n = "PC"
	}
	return n
}

// idYes is the MessageBox return value for the Yes button.
const idYes = 6

const showEventName = `Local\NASfoneShowWindow`

// requestShow asks the running instance to open its window.
func requestShow() {
	name, _ := windows.UTF16PtrFromString(showEventName)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		info(t("already_running"))
		return
	}
	defer windows.CloseHandle(h)
	windows.SetEvent(h)
}

// waitShowRequests calls show whenever another launch of the exe asks for the window.
func waitShowRequests(show func()) {
	name, _ := windows.UTF16PtrFromString(showEventName)
	h, err := windows.CreateEvent(nil, 0, 0, name) // auto-reset
	if err != nil {
		return
	}
	for {
		if ev, _ := windows.WaitForSingleObject(h, windows.INFINITE); ev == windows.WAIT_OBJECT_0 {
			show()
		}
	}
}
