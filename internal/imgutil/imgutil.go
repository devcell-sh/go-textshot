// Package imgutil holds the pure-Go image helpers shared by the model
// pipelines: RGBA conversion, bilinear resizing and cropping.
package imgutil

import (
	"cmp"
	"image"
	"image/draw"
	"slices"
)

// ToRGBA returns img as an *image.RGBA with bounds at the origin, copying
// only when needed.
func ToRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// ResizeBilinear resizes src (bounds at origin) to w x h with bilinear
// interpolation. Source samples are center-aligned: destination pixel dx
// maps to source coordinate dx*sx + 0.5*sx - 0.5.
func ResizeBilinear(src *image.RGBA, w, h int) *image.RGBA {
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if srcW == 0 || srcH == 0 || w == 0 || h == 0 {
		return dst
	}
	sx := float64(srcW) / float64(w)
	sy := float64(srcH) / float64(h)
	clamp := func(v, n int) int {
		if v < 0 {
			return 0
		}
		if v >= n {
			return n - 1
		}
		return v
	}
	for dy := 0; dy < h; dy++ {
		y := float64(dy)*sy + 0.5*sy - 0.5
		yi := int(y)
		if y < 0 {
			yi = -1
		}
		y0 := clamp(yi, srcH)
		y1 := clamp(yi+1, srcH)
		fy := float32(y - float64(yi))
		if fy < 0 {
			fy = 0
		}
		top := src.PixOffset(0, y0)
		bot := src.PixOffset(0, y1)
		row := dst.Pix[dy*dst.Stride:]
		for dx := 0; dx < w; dx++ {
			x := float64(dx)*sx + 0.5*sx - 0.5
			xi := int(x)
			if x < 0 {
				xi = -1
			}
			x0 := clamp(xi, srcW)
			x1 := clamp(xi+1, srcW)
			fx := float32(x - float64(xi))
			if fx < 0 {
				fx = 0
			}
			w00 := (1 - fx) * (1 - fy)
			w10 := fx * (1 - fy)
			w01 := (1 - fx) * fy
			w11 := fx * fy
			off := dx * 4
			for c := 0; c < 4; c++ {
				v := float32(src.Pix[top+x0*4+c])*w00 +
					float32(src.Pix[top+x1*4+c])*w10 +
					float32(src.Pix[bot+x0*4+c])*w01 +
					float32(src.Pix[bot+x1*4+c])*w11
				row[off+c] = byte(v + 0.5)
			}
		}
	}
	return dst
}

// CropRGBA copies the region r (in img's coordinate space) into a new RGBA
// image with bounds at the origin. The region is clamped to img's bounds;
// an empty result is returned when nothing remains.
func CropRGBA(img image.Image, r image.Rectangle) *image.RGBA {
	r = r.Intersect(img.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	if r.Empty() {
		return dst
	}
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	return dst
}

// SortReadingOrder orders items top-to-bottom, then left-to-right by their
// rectangles. Items whose vertical centres differ by at most half the
// average height share a row.
func SortReadingOrder[T any](items []T, rect func(T) image.Rectangle) {
	if len(items) < 2 {
		return
	}
	total := 0
	for _, it := range items {
		total += rect(it).Dy()
	}
	tol := max(1, total/len(items)/2)
	slices.SortStableFunc(items, func(a, b T) int {
		ra, rb := rect(a), rect(b)
		ca := (ra.Min.Y + ra.Max.Y) / 2
		cb := (rb.Min.Y + rb.Max.Y) / 2
		if d := ca - cb; d > tol || d < -tol {
			return cmp.Compare(ca, cb)
		}
		return cmp.Compare(ra.Min.X, rb.Min.X)
	})
}
