package imgutil

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResizeBilinearPreservesFlatColour(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for i := range src.Pix {
		src.Pix[i] = 200
	}
	dst := ResizeBilinear(src, 3, 7)
	require.Equal(t, image.Rect(0, 0, 3, 7), dst.Rect)
	for i, v := range dst.Pix {
		require.Equal(t, uint8(200), v, "pix %d", i)
	}
	empty := ResizeBilinear(image.NewRGBA(image.Rect(0, 0, 0, 0)), 4, 4)
	require.Equal(t, 4*4*4, len(empty.Pix))
}

func TestCropRGBAClamps(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	src.SetRGBA(9, 9, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	crop := CropRGBA(src, image.Rect(8, 8, 20, 20))
	require.Equal(t, image.Rect(0, 0, 2, 2), crop.Rect)
	require.Equal(t, color.RGBA{R: 1, G: 2, B: 3, A: 255}, crop.RGBAAt(1, 1))
	require.True(t, CropRGBA(src, image.Rect(20, 20, 30, 30)).Rect.Empty())
}

func TestToRGBAReusesOriginBacked(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	require.Same(t, src, ToRGBA(src))
	off := image.NewRGBA(image.Rect(2, 2, 6, 6))
	off.SetRGBA(2, 2, color.RGBA{R: 9, A: 255})
	got := ToRGBA(off)
	require.Equal(t, image.Rect(0, 0, 4, 4), got.Rect)
	require.Equal(t, color.RGBA{R: 9, A: 255}, got.RGBAAt(0, 0))
}
