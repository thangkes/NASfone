module nasfone/winclient

go 1.27.1

require (
	fyne.io/systray v1.12.2
	golang.org/x/sys v0.48.0
	nasfone/core v0.0.0
)

require (
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
)

replace nasfone/core => ../core
