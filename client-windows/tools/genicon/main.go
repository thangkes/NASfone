// Command genicon writes the app icon sources:
//   - winres/icon.png  (256×256, embedded into NASfone.exe by go-winres)
//   - installer/app.ico (16–256 px, used by the setup program)
package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"

	"nasfone/winclient/iconart"
)

func main() {
	f, err := os.Create("winres/icon.png")
	if err != nil {
		panic(err)
	}
	if err := png.Encode(f, iconart.Draw(256)); err != nil {
		panic(err)
	}
	f.Close()

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var imgs [][]byte
	for _, n := range sizes {
		var b bytes.Buffer
		png.Encode(&b, iconart.Draw(n))
		imgs = append(imgs, b.Bytes())
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, n := range sizes {
		dim := byte(n)
		if n >= 256 {
			dim = 0 // 0 means 256 in ICO headers
		}
		ico.Write([]byte{dim, dim, 0, 0})
		binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&ico, binary.LittleEndian, []uint32{uint32(len(imgs[i])), uint32(offset)})
		offset += len(imgs[i])
	}
	for _, b := range imgs {
		ico.Write(b)
	}
	if err := os.WriteFile("installer/app.ico", ico.Bytes(), 0o644); err != nil {
		panic(err)
	}
}
