// Package iconart draws the PocketNAS icon (a rounded blue square with two
// drive bays) at any size, for the tray icon and the .exe icon.
package iconart

import (
	"image"
	"image/color"
	"math"
)

var (
	Blue  = color.RGBA{0x2f, 0x6f, 0xed, 0xff}
	White = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// Draw returns an n×n RGBA icon with soft (anti-aliased) rounded corners.
func Draw(n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	f := float64(n)
	r := f * 0.18 // corner radius
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			a := coverage(float64(x)+0.5, float64(y)+0.5, 0, 0, f, f, r)
			if a > 0 {
				img.Set(x, y, blend(color.RGBA{}, Blue, a))
			}
		}
	}
	// Two drive bays with an indicator light.
	bayX0, bayX1 := f*0.22, f*0.78
	for _, top := range []float64{f * 0.25, f * 0.55} {
		h := f * 0.2
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				a := coverage(float64(x)+0.5, float64(y)+0.5, bayX0, top, bayX1, top+h, f*0.04)
				if a > 0 {
					img.Set(x, y, blend(img.RGBAAt(x, y), White, a))
				}
				// LED
				cx, cy, lr := bayX1-f*0.1, top+h/2, f*0.035
				d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
				if la := clamp(lr - d + 0.5); la > 0 {
					img.Set(x, y, blend(img.RGBAAt(x, y), Blue, la))
				}
			}
		}
	}
	return img
}

// coverage approximates how much of pixel (px,py) lies inside a rounded rect.
func coverage(px, py, x0, y0, x1, y1, r float64) float64 {
	cx := math.Max(x0+r, math.Min(px, x1-r))
	cy := math.Max(y0+r, math.Min(py, y1-r))
	d := math.Hypot(px-cx, py-cy)
	return clamp(r - d + 0.5)
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func blend(dst, src color.RGBA, a float64) color.RGBA {
	mix := func(d, s uint8) uint8 { return uint8(float64(d)*(1-a) + float64(s)*a + 0.5) }
	da := float64(dst.A) / 255
	outA := a + da*(1-a)
	if da == 0 {
		return color.RGBA{src.R, src.G, src.B, uint8(a*255 + 0.5)}
	}
	return color.RGBA{mix(dst.R, src.R), mix(dst.G, src.G), mix(dst.B, src.B), uint8(outA*255 + 0.5)}
}
