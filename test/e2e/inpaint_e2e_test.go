package e2e

import (
	"context"
	"image"
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-textshot"
	"github.com/devcell-sh/go-textshot/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestInpaint erases every OCR'd line of a fixture with MI-GAN and keeps
// the result next to the original in test/results/. Pixels outside the
// mask must be untouched; inside, the text must be gone (far fewer ink
// pixels than before).
func TestInpaint(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	img := loadFixture(t, "macos-catalina/continue-enabled.png")
	ctx := context.Background()
	lines, err := eng.DetectText(ctx, img)
	require.NoError(t, err)
	require.NotEmpty(t, lines)

	var boxes []image.Rectangle
	for _, l := range lines {
		boxes = append(boxes, l.Box)
	}
	b := img.Bounds()
	mask := textshot.InpaintMask(b.Dx(), b.Dy(), boxes, 2)
	out, err := eng.Inpaint(ctx, img, mask)
	require.NoError(t, err)

	dir := testutil.ResultDir(t)
	require.NoError(t, testutil.WritePNG(filepath.Join(dir, "continue-enabled.inpainted.png"), out))

	// Untouched outside the mask.
	changedOutside := 0
	for y := 0; y < b.Dy(); y += 7 {
		for x := 0; x < b.Dx(); x += 7 {
			if mask.GrayAt(x, y).Y < 128 {
				r1, g1, b1, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				c2 := out.RGBAAt(x, y)
				if uint8(r1>>8) != c2.R || uint8(g1>>8) != c2.G || uint8(b1>>8) != c2.B {
					changedOutside++
				}
			}
		}
	}
	require.Zero(t, changedOutside, "pixels outside the mask changed")

	// Text gone: OCR on the result finds far fewer lines.
	after, err := eng.DetectText(ctx, out)
	require.NoError(t, err)
	t.Logf("lines before %d, after inpaint %d", len(lines), len(after))
	require.Less(t, len(after), len(lines)/2+1)
}
