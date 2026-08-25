package chatruntime

import (
	"bytes"
	"image"
	"image/png"

	_ "image/gif"
	_ "image/jpeg"
)

// minVisionInputPixels is the smallest width*height Grok/xAI accepts on
// /v1/responses. Smaller images return HTTP 400 invalid_image
// ("below the minimum of 512 pixels") even when the PNG is valid and the
// Responses wire shape (input_image + data URL string) is correct.
const minVisionInputPixels = 512

// prepareVisionImage returns provider-safe image bytes for model assembly.
// Images at or above minVisionInputPixels are passed through unchanged
// (original codec preserved). Smaller decodable images are nearest-neighbor
// scaled by an integer factor and re-encoded as PNG. Undecodable bodies
// (truncated bytes, webp without a decoder, …) are passed through so
// allowlisted types are not dropped.
func prepareVisionImage(body []byte, mime string) (out []byte, outMIME string) {
	if len(body) == 0 {
		return body, mime
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil || decoded == nil {
		return body, mime
	}
	bounds := decoded.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	n := visionIntegerScale(w, h, minVisionInputPixels)
	if n <= 1 {
		return body, mime
	}
	scaled := scaleImageNearest(decoded, w*n, h*n)
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return body, mime
	}
	return buf.Bytes(), "image/png"
}

func visionIntegerScale(w, h, minPixels int) int {
	if w <= 0 || h <= 0 || minPixels <= 0 {
		return 1
	}
	if int64(w)*int64(h) >= int64(minPixels) {
		return 1
	}
	n := 2
	for int64(n)*int64(w)*int64(n)*int64(h) < int64(minPixels) {
		n++
		if n > 1024 {
			return n
		}
	}
	return n
}

func scaleImageNearest(src image.Image, nw, nh int) *image.RGBA {
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*sh/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*sw/nw
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
