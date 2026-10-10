package testutil

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Mark is one box to draw on a screenshot.
type Mark struct {
	Rect  image.Rectangle
	Label string
	Color color.RGBA
}

// Annotate returns a copy of img with each mark outlined (2 px) and its
// label printed in a filled tag at the box's top-left corner.
func Annotate(img image.Image, marks []Mark) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Rect, img, b.Min, draw.Src)
	face := basicfont.Face7x13
	for _, m := range marks {
		r := m.Rect.Sub(b.Min).Intersect(dst.Rect)
		if r.Empty() {
			continue
		}
		fill := image.NewUniform(m.Color)
		draw.Draw(dst, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+2), fill, image.Point{}, draw.Src)
		draw.Draw(dst, image.Rect(r.Min.X, r.Max.Y-2, r.Max.X, r.Max.Y), fill, image.Point{}, draw.Src)
		draw.Draw(dst, image.Rect(r.Min.X, r.Min.Y, r.Min.X+2, r.Max.Y), fill, image.Point{}, draw.Src)
		draw.Draw(dst, image.Rect(r.Max.X-2, r.Min.Y, r.Max.X, r.Max.Y), fill, image.Point{}, draw.Src)
		if m.Label == "" {
			continue
		}
		w := font.MeasureString(face, m.Label).Ceil() + 4
		h := face.Height + 2
		tag := image.Rect(r.Min.X, r.Min.Y-h, r.Min.X+w, r.Min.Y)
		if tag.Min.Y < 0 {
			tag = tag.Add(image.Pt(0, h))
		}
		draw.Draw(dst, tag.Intersect(dst.Rect), fill, image.Point{}, draw.Src)
		d := font.Drawer{Dst: dst, Src: image.White, Face: face,
			Dot: fixed.P(tag.Min.X+2, tag.Min.Y+face.Ascent+1)}
		d.DrawString(m.Label)
	}
	return dst
}

// WritePNG encodes img to path.
func WritePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
