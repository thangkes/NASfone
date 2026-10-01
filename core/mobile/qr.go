package mobile

import "rsc.io/qr"

// QRPNG renders text as a QR code PNG (for showing pairing invites on the
// phone screen). scale is the number of pixels per QR module.
func QRPNG(text string, scale int) ([]byte, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	if scale < 1 {
		scale = 8
	}
	c.Scale = scale
	return c.PNG(), nil
}
