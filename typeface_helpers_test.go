package textshot_test

import (
	"image"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// renderLine draws text black on white with tight padding.
func renderLine(f *opentype.Font, text string, size float64) (*image.Gray, error) {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	m := face.Metrics()
	w := font.MeasureString(face, text).Ceil() + 8
	h := (m.Ascent + m.Descent).Ceil() + 8
	img := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	d := font.Drawer{Dst: img, Src: image.Black, Face: face, Dot: fixed.P(4, 4+m.Ascent.Ceil())}
	d.DrawString(text)
	return img, nil
}
