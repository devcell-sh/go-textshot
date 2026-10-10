package screen

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func canvas(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

func rect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
}

var (
	dark  = color.RGBA{40, 42, 48, 255}
	grey  = color.RGBA{236, 236, 236, 255}
	white = color.RGBA{255, 255, 255, 255}
	black = color.RGBA{0, 0, 0, 255}
	blue  = color.RGBA{0, 122, 255, 255}
	faint = color.RGBA{170, 170, 170, 255}
)

func TestColorsAndContrast(t *testing.T) {
	img := canvas(200, 40, white)
	rect(img, image.Rect(20, 12, 120, 28), black)
	bg, fg, c := Colors(img, img.Rect)
	require.Equal(t, white, bg)
	require.Equal(t, black, fg)
	require.InDelta(t, 21, c, 0.01)

	img = canvas(200, 40, white)
	rect(img, image.Rect(20, 12, 120, 28), faint)
	_, fg, c = Colors(img, img.Rect)
	require.Equal(t, faint, fg)
	require.Less(t, c, 3.0, "grey-on-white reads as disabled")

	bg, fg, c = Colors(canvas(50, 50, blue), image.Rect(0, 0, 50, 50))
	require.Equal(t, blue, bg)
	require.Equal(t, bg, fg, "no contrasting colour: fg falls back to bg")
	require.InDelta(t, 1, c, 1e-9)
}

func TestRingAndAccent(t *testing.T) {
	img := canvas(300, 200, grey)
	rect(img, image.Rect(47, 47, 253, 153), blue) // frame
	rect(img, image.Rect(50, 50, 250, 150), white)
	c, ok := Ring(img, image.Rect(50, 50, 250, 150))
	require.True(t, ok)
	require.Equal(t, blue, c)

	_, ok = Ring(img, image.Rect(100, 80, 200, 120))
	require.False(t, ok, "no ring inside the plain white panel")

	c, ok = Ring(img, image.Rect(42, 44, 256, 158))
	require.True(t, ok, "box a few px outside the frame still finds it")
	require.Equal(t, blue, c)

	require.True(t, Accent(blue))
	require.False(t, Accent(grey))
	require.False(t, Accent(black))
}

func TestBackdropAndLuminance(t *testing.T) {
	img := canvas(100, 100, dark)
	rect(img, image.Rect(10, 10, 50, 50), white)
	require.Equal(t, dark, Backdrop(img))
	require.Less(t, Luminance(dark), 128.0)
	require.Greater(t, Luminance(white), 128.0)
}

func TestDiff(t *testing.T) {
	a := canvas(100, 80, white)
	b := canvas(100, 80, white)
	r, f := Diff(a, b, 8)
	require.True(t, r.Empty())
	require.Zero(t, f)

	rect(b, image.Rect(10, 20, 30, 40), black)
	r, f = Diff(a, b, 8)
	require.Equal(t, image.Rect(10, 20, 30, 40), r)
	require.InDelta(t, 400.0/8000, f, 1e-9)

	r, f = Diff(a, canvas(50, 50, white), 8)
	require.Equal(t, image.Rect(0, 0, 50, 50), r)
	require.Equal(t, 1.0, f)
}

func TestRingRejectsFilledIcons(t *testing.T) {
	// A solid blue square (a Safari-like icon) has a saturated perimeter at
	// every inset, so it is a fill, not a ring.
	img := canvas(100, 100, white)
	rect(img, image.Rect(30, 30, 60, 60), blue)
	_, ok := Ring(img, image.Rect(30, 30, 60, 60))
	require.False(t, ok, "solid fill is not a ring")
	_, ok = Ring(img, image.Rect(28, 28, 62, 62))
	require.False(t, ok, "loose box around a solid fill is not a ring either")

	// A 2 px frame around a white field is a ring.
	rect(img, image.Rect(30, 30, 60, 60), white)
	rect(img, image.Rect(30, 30, 60, 60), blue)
	rect(img, image.Rect(32, 32, 58, 58), white)
	c, ok := Ring(img, image.Rect(30, 30, 60, 60))
	require.True(t, ok)
	require.Equal(t, blue, c)
}

// Thin anti-aliased text: a one-pixel stroke flanked by lighter edge
// pixels on both sides, so edge pixels outnumber stroke pixels two to one.
func antialiased(stroke, edge color.RGBA) *image.RGBA {
	img := canvas(200, 40, white)
	for x := 20; x < 120; x += 6 {
		rect(img, image.Rect(x-1, 10, x, 30), edge)
		rect(img, image.Rect(x, 10, x+1, 30), stroke)
		rect(img, image.Rect(x+1, 10, x+2, 30), edge)
	}
	return img
}

func TestColorsReadsStrokeNotEdges(t *testing.T) {
	_, fg, c := Colors(antialiased(black, color.RGBA{190, 190, 190, 255}), image.Rect(0, 0, 200, 40))
	require.Equal(t, black, fg, "the stroke colour, not the more numerous edge grey")
	require.Greater(t, c, 4.5, "black text on white is enabled")

	_, fg, c = Colors(antialiased(faint, color.RGBA{215, 215, 215, 255}), image.Rect(0, 0, 200, 40))
	require.Equal(t, faint, fg)
	require.Less(t, c, 3.0, "grey text on white is disabled")
}

// Catalina's macOS Utilities Continue button, cropped from a build's
// frames before and after a list row was selected: enabled text is black
// with grey anti-aliasing, disabled text is light grey throughout.
func TestColorsOnCatalinaContinueButton(t *testing.T) {
	load := func(name string) *image.RGBA {
		f, err := os.Open("../../test/testdata/macos-catalina/" + name)
		require.NoError(t, err)
		defer f.Close()
		src, err := png.Decode(f)
		require.NoError(t, err)
		img := image.NewRGBA(src.Bounds())
		draw.Draw(img, img.Rect, src, src.Bounds().Min, draw.Src)
		return img
	}
	// The text box the detector reports, relative to the crop.
	box := image.Rect(10, 6, 93, 27)

	_, _, c := Colors(load("continue-disabled.png"), box)
	require.Less(t, c, 3.0, "disabled Continue")

	_, _, c = Colors(load("continue-enabled.png"), box)
	require.Greater(t, c, 4.5, "enabled Continue")
}
