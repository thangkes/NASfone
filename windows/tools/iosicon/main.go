// Command iosicon writes the iOS app icon (1024x1024, opaque: iOS rounds the
// corners itself) to ios/NASfone/Assets.xcassets/AppIcon.appiconset.
//
//	cd windows && go run ./tools/iosicon
package main

import (
	"image"
	"image/draw"
	"image/png"
	"os"

	"nasfone/winclient/iconart"
)

func main() {
	const n = 1024
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	draw.Draw(img, img.Bounds(), &image.Uniform{iconart.Blue}, image.Point{}, draw.Src)
	draw.Draw(img, img.Bounds(), iconart.Draw(n), image.Point{}, draw.Over)
	f, err := os.Create("../ios/NASfone/Assets.xcassets/AppIcon.appiconset/icon-1024.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
