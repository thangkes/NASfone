//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image/png"

	"nasfone/winclient/iconart"
)

// trayIcon wraps the 32×32 NASfone icon PNG in an ICO container
// (Windows accepts PNG-compressed ICO images).
func trayIcon() []byte {
	const n = 32
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, iconart.Draw(n))

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, 1})                  // reserved, type=icon, count
	ico.Write([]byte{n, n, 0, 0})                                               // width, height, colors, reserved
	binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})                    // planes, bpp
	binary.Write(&ico, binary.LittleEndian, []uint32{uint32(pngBuf.Len()), 22}) // size, offset
	ico.Write(pngBuf.Bytes())
	return ico.Bytes()
}

// iconDataURL is the app icon as a PNG data: URL for the window's HTML.
func iconDataURL() string {
	var b bytes.Buffer
	png.Encode(&b, iconart.Draw(96))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
