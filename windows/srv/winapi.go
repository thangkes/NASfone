//go:build windows

package srv

import (
	"errors"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// runValue names the autostart registry value; both roles share it (NASfone.exe picks the role).
const runValue = "NASfone"

// appName is the name shown to people (window, tray, message boxes).
const appName = "NASfone for Windows (Server)"

const (
	mutexName      = `Local\NASfoneServerTray`
	showEventName  = `Local\NASfoneServerShow`
	quitEventName  = `Local\NASfoneServerQuit`
	runKey         = `Software\Microsoft\Windows\CurrentVersion\Run`
	idYes          = 6
	bifReturnDirs  = 0x0001
	bifNewDialog   = 0x0040
	bifEditBox     = 0x0010
	maxPathUnicode = 32768
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	ole32                   = windows.NewLazySystemDLL("ole32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procGlobalAlloc         = kernel32.NewProc("GlobalAlloc")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	procSHBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
)

func msgBox(text string, flags uint32) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(appName)
	r, _ := windows.MessageBox(0, t, c, flags|windows.MB_SETFOREGROUND|windows.MB_TOPMOST)
	return int(r)
}

func info(text string) { msgBox(text, windows.MB_OK|windows.MB_ICONINFORMATION) }
func fail(text string) { msgBox(text, windows.MB_OK|windows.MB_ICONERROR) }
func ask(text string) bool {
	return msgBox(text, windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2) == idYes
}

func computerName() string {
	n, _ := os.Hostname()
	if n == "" {
		n = "PC"
	}
	return n
}

func openURL(u string) {
	p, _ := windows.UTF16PtrFromString(u)
	verb, _ := windows.UTF16PtrFromString("open")
	windows.ShellExecute(0, verb, p, nil, nil, windows.SW_SHOWNORMAL)
}

// setClipboard puts Unicode text on the clipboard.
func setClipboard(text string) error {
	const cfUnicodeText, gmemMoveable = 13, 0x0002
	u, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	size := uintptr(len(u) * 2)
	h, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return err
	}
	p, _, _ := procGlobalLock.Call(h)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	procGlobalUnlock.Call(h)
	if r, _, err := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		return err
	}
	return nil
}

type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Callback    uintptr
	LParam      uintptr
	Image       int32
}

// chooseFolder shows the Windows folder picker owned by window owner.
// It returns "" when cancelled. Call it on a thread with COM initialised
// (the WebView window thread is).
func chooseFolder(owner uintptr, title string) string {
	name := make([]uint16, windows.MAX_PATH)
	t, _ := windows.UTF16PtrFromString(title)
	bi := browseInfo{Owner: owner, DisplayName: &name[0], Title: t, Flags: bifReturnDirs | bifNewDialog | bifEditBox}
	pidl, _, _ := procSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer procCoTaskMemFree.Call(pidl)
	path := make([]uint16, maxPathUnicode)
	if r, _, _ := procSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&path[0]))); r == 0 {
		return ""
	}
	return windows.UTF16ToString(path)
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func setAutostart(on bool) error {
	exe, _ := os.Executable()
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(runValue, `"`+exe+`" --minimized`)
	}
	if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// singleInstance holds a named mutex for the tray process; false if another
// instance already owns it.
func singleInstance() bool {
	name, _ := windows.UTF16PtrFromString(mutexName)
	_, err := windows.CreateMutex(nil, false, name)
	return !errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

func signal(event string) bool {
	name, _ := windows.UTF16PtrFromString(event)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	windows.SetEvent(h)
	return true
}

// waitEvent calls f every time event is signalled (auto-reset event).
func waitEvent(event string, f func()) {
	name, _ := windows.UTF16PtrFromString(event)
	h, err := windows.CreateEvent(nil, 0, 0, name)
	if err != nil {
		return
	}
	for {
		if ev, _ := windows.WaitForSingleObject(h, windows.INFINITE); ev == windows.WAIT_OBJECT_0 {
			f()
		}
	}
}

func requestShow() {
	if !signal(showEventName) {
		info(t("already_running"))
	}
}

func requestQuit() bool { return signal(quitEventName) }

// waitForExit polls until no tray instance owns the single-instance mutex.
func waitForExit(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	name, _ := windows.UTF16PtrFromString(mutexName)
	for time.Now().Before(deadline) {
		h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
		if err != nil {
			return
		}
		windows.CloseHandle(h)
		time.Sleep(200 * time.Millisecond)
	}
}

// mutexFree reports whether no server instance holds the single-instance mutex.
func mutexFree() bool {
	name, _ := windows.UTF16PtrFromString(mutexName)
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		return true
	}
	windows.CloseHandle(h)
	return false
}
