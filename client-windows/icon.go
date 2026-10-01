//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// trayIcon draws a small "drive" glyph and wraps the PNG in an ICO container
// (Windows accepts PNG-compressed ICO images).
func trayIcon() []byte {
	const n = 32
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	bg := color.RGBA{0x2f, 0x6f, 0xed, 0xff}
	fg := color.RGBA{0xff, 0xff, 0xff, 0xff}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			// rounded square
			dx, dy := min(x, n-1-x), min(y, n-1-y)
			if dx+dy < 4 && dx < 3 && dy < 3 {
				continue
			}
			img.Set(x, y, bg)
		}
	}
	// two stacked drive bays with an LED
	for _, top := range []int{8, 18} {
		for y := top; y < top+6; y++ {
			for x := 7; x < 25; x++ {
				img.Set(x, y, fg)
			}
		}
		img.Set(21, top+2, bg)
		img.Set(22, top+2, bg)
		img.Set(21, top+3, bg)
		img.Set(22, top+3, bg)
	}
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, img)

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, 1})                  // reserved, type=icon, count
	ico.Write([]byte{n, n, 0, 0})                                               // width, height, colors, reserved
	binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})                    // planes, bpp
	binary.Write(&ico, binary.LittleEndian, []uint32{uint32(pngBuf.Len()), 22}) // size, offset
	ico.Write(pngBuf.Bytes())
	return ico.Bytes()
}
