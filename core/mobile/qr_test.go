package mobile

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestQRPNG(t *testing.T) {
	invite := "pnas1:" + strings.Repeat("A", 230) // a realistic invite length
	b, err := QRPNG(invite, 6)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w < 200 || w > 1200 {
		t.Fatalf("unexpected size %d", w)
	}
}
