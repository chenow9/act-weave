package chatruntime

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestVisionIntegerScale(t *testing.T) {
	t.Parallel()
	cases := []struct {
		w, h, want int
	}{
		{8, 8, 3},   // 64 → 24x24 = 576
		{1, 1, 23},  // 1 → 23x23 = 529
		{16, 16, 2}, // 256 → 32x32 = 1024
		{22, 22, 2}, // 484 → 44x44
		{23, 23, 1}, // 529
		{32, 32, 1},
		{64, 8, 1}, // 512
		{0, 8, 1},
	}
	for _, tc := range cases {
		got := visionIntegerScale(tc.w, tc.h, minVisionInputPixels)
		if got != tc.want {
			t.Errorf("%dx%d scale=%d want %d", tc.w, tc.h, got, tc.want)
		}
	}
}

func TestPrepareVisionImage_UpscalesBelow512Pixels(t *testing.T) {
	t.Parallel()
	src := solidPNG(8, 8, color.RGBA{255, 0, 0, 255})
	out, mime := prepareVisionImage(src, "image/png")
	if mime != "image/png" {
		t.Fatalf("mime=%s", mime)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 24 || cfg.Height != 24 {
		t.Fatalf("dims=%dx%d want 24x24", cfg.Width, cfg.Height)
	}
	if cfg.Width*cfg.Height < minVisionInputPixels {
		t.Fatalf("pixels=%d", cfg.Width*cfg.Height)
	}
}

func TestPrepareVisionImage_PassesThroughLargePNG(t *testing.T) {
	t.Parallel()
	src := solidPNG(32, 32, color.RGBA{0, 255, 0, 255})
	out, mime := prepareVisionImage(src, "image/png")
	if mime != "image/png" {
		t.Fatalf("mime=%s", mime)
	}
	if !bytes.Equal(out, src) {
		t.Fatal("32x32 PNG must not be re-encoded")
	}
}

func TestPrepareVisionImage_PassesThroughUndecodable(t *testing.T) {
	t.Parallel()
	garbage := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	out, mime := prepareVisionImage(garbage, "image/png")
	if mime != "image/png" || !bytes.Equal(out, garbage) {
		t.Fatalf("undecodable must pass through, mime=%s len=%d", mime, len(out))
	}
}

func TestPrepareVisionImage_Lab8x8Fixture(t *testing.T) {
	t.Parallel()
	// Exact 8x8 red PNG used by the NeiOps e2e lab (64 pixels → Grok invalid_image).
	src, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAIAAABLbSncAAAAEklEQVR4nGP4z8CAFWEXHbQSACj/P8Fu7N9hAAAAAElFTkSuQmCC")
	if err != nil {
		t.Fatal(err)
	}
	out, mime := prepareVisionImage(src, "image/png")
	if mime != "image/png" {
		t.Fatalf("mime=%s", mime)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 24 || cfg.Height != 24 {
		t.Fatalf("dims=%dx%d want 24x24", cfg.Width, cfg.Height)
	}
}

func TestPrepareVisionImage_UpscalesTinyJPEGAsPNG(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	red := color.RGBA{200, 10, 10, 255}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, red)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	out, mime := prepareVisionImage(buf.Bytes(), "image/jpeg")
	if mime != "image/png" {
		t.Fatalf("upscaled jpeg mime=%s want image/png", mime)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width*cfg.Height < minVisionInputPixels {
		t.Fatalf("pixels=%d", cfg.Width*cfg.Height)
	}
}

func solidPNG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
