package textshot_test

import (
	"image"
	"image/draw"
	"testing"

	"github.com/devcell-sh/go-textshot"
	"golang.org/x/image/font/opentype"
)

// DetectTypeface over a line pasted into a larger screenshot: the public
// entry point crops by box, so the answer must match the direct one.
func TestDetectTypefaceInScreenshot(t *testing.T) {
	faces := textshot.GoFaces()
	bold := faces[1]
	f, err := opentype.Parse(bold.TTF)
	if err != nil {
		t.Fatal(err)
	}
	line, err := renderLine(f, "Continue", 20)
	if err != nil {
		t.Fatal(err)
	}
	screen := image.NewRGBA(image.Rect(0, 0, 400, 200))
	draw.Draw(screen, screen.Bounds(), image.White, image.Point{}, draw.Src)
	box := image.Rect(50, 60, 50+line.Bounds().Dx(), 60+line.Bounds().Dy())
	draw.Draw(screen, box, line, image.Point{}, draw.Src)

	tf, err := textshot.DetectTypeface(screen, box, "Continue", faces)
	if err != nil {
		t.Fatal(err)
	}
	if tf.Face != bold.Name || !tf.Bold || tf.Italic {
		t.Fatalf("got %+v", tf)
	}
	if tf.SizePx < 19 || tf.SizePx > 21 {
		t.Fatalf("size %.2f, want ~20", tf.SizePx)
	}
	if s := textshot.MeasureSlant(line); s > 3 || s < -3 {
		t.Fatalf("slant %.0f on an upright face", s)
	}
}
