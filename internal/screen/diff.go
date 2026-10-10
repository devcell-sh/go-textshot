package screen

import "image"

// Diff compares two same-sized images and returns the bounding box of the
// pixels whose colour moved by more than tol on any channel, plus the
// fraction of pixels that changed. Images of different sizes count as
// fully changed.
func Diff(a, b *image.RGBA, tol int) (image.Rectangle, float64) {
	if a.Rect.Size() != b.Rect.Size() {
		return b.Rect.Sub(b.Rect.Min), 1
	}
	w, h := a.Rect.Dx(), a.Rect.Dy()
	if w == 0 || h == 0 {
		return image.Rectangle{}, 0
	}
	minX, minY, maxX, maxY := w, h, -1, -1
	changed := 0
	for y := 0; y < h; y++ {
		ra := a.Pix[y*a.Stride:]
		rb := b.Pix[y*b.Stride:]
		for x := 0; x < w; x++ {
			i := x * 4
			if absDiff(ra[i], rb[i]) > tol || absDiff(ra[i+1], rb[i+1]) > tol || absDiff(ra[i+2], rb[i+2]) > tol {
				changed++
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	if changed == 0 {
		return image.Rectangle{}, 0
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), float64(changed) / float64(w*h)
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
