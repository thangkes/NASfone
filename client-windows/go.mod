module pocketnas/winclient

go 1.27.1

require (
	fyne.io/systray v1.12.2
	golang.org/x/sys v0.48.0
	pocketnas/core v0.0.0
)

require github.com/godbus/dbus/v5 v5.2.2 // indirect

replace pocketnas/core => ../core
