// Package screen derives style facts from screenshot pixels without any
// model: per-box colours and contrast, the screen theme, focus rings and
// frame differences. Everything is deterministic.
package screen

import (
	"image"
	"image/color"
	"math"
)

// Colors returns the dominant background colour of r, the dominant colour
// that contrasts with it (text or glyph), and their WCAG contrast ratio.
// When nothing contrasts, fg equals bg and the ratio is 1.
func Colors(img *image.RGBA, r image.Rectangle) (bg, fg color.RGBA, contrast float64) {
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return color.RGBA{}, color.RGBA{}, 1
	}
	h := newHistogram()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[(y-img.Rect.Min.Y)*img.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			p := row[(x-img.Rect.Min.X)*4:]
			h.add(p[0], p[1], p[2])
		}
	}
	bg = h.top(nil)
	bgLum := Luminance(bg)
	fg = h.top(func(c color.RGBA) bool { return math.Abs(Luminance(c)-bgLum) > 48 })
	if fg.A == 0 {
		fg = bg
	}
	return bg, fg, Contrast(fg, bg)
}

// Backdrop returns the dominant colour of the whole image: the desktop or
// the app background the screen is drawn on.
func Backdrop(img *image.RGBA) color.RGBA {
	bg, _, _ := Colors(img, img.Rect)
	return bg
}

// Ring reports whether a saturated outline (a focus ring, a selection
// frame) runs along r's edge, and returns its colour. Detected boxes are
// rarely pixel-exact, so one-pixel rings from 3 px outside to 8 px inside
// the edge are tried and the first that is at least 60% saturated wins. A
// ring is a line, not a fill: the saturation must drop below 30% within
// a few pixels further in (6, or a quarter of the shorter side for large
// boxes), otherwise the box is a colourful icon and no ring is reported.
func Ring(img *image.RGBA, r image.Rectangle) (color.RGBA, bool) {
	for inset := -3; inset <= 8; inset++ {
		ring := r.Inset(inset)
		if ring.Dx() < 4 || ring.Dy() < 4 {
			break
		}
		c, frac := ringSaturation(img, ring)
		if frac < 0.6 {
			continue
		}
		depth := max(6, min(r.Dx(), r.Dy())/4)
		for d := 1; d <= depth; d++ {
			inner := ring.Inset(d)
			if inner.Dx() < 4 || inner.Dy() < 4 {
				break
			}
			if _, f := ringSaturation(img, inner); f < 0.3 {
				return c, true
			}
		}
		return color.RGBA{}, false
	}
	return color.RGBA{}, false
}

// ringSaturation samples the one-pixel perimeter of ring and returns the
// mean colour of its saturated pixels and their fraction.
func ringSaturation(img *image.RGBA, ring image.Rectangle) (color.RGBA, float64) {
	total, hit := 0, 0
	var sr, sg, sb int
	probe := func(x, y int) {
		if !(image.Point{x, y}).In(img.Rect) {
			return
		}
		p := img.Pix[(y-img.Rect.Min.Y)*img.Stride+(x-img.Rect.Min.X)*4:]
		total++
		if s, v := saturationValue(p[0], p[1], p[2]); s >= 0.45 && v >= 0.4 {
			hit++
			sr += int(p[0])
			sg += int(p[1])
			sb += int(p[2])
		}
	}
	for x := ring.Min.X; x < ring.Max.X; x++ {
		probe(x, ring.Min.Y)
		probe(x, ring.Max.Y-1)
	}
	for y := ring.Min.Y + 1; y < ring.Max.Y-1; y++ {
		probe(ring.Min.X, y)
		probe(ring.Max.X-1, y)
	}
	if total == 0 || hit == 0 {
		return color.RGBA{}, 0
	}
	return color.RGBA{uint8(sr / hit), uint8(sg / hit), uint8(sb / hit), 255}, float64(hit) / float64(total)
}

// Accent reports whether c is a saturated, visible colour (an accent fill
// rather than a neutral surface). The value floor is low enough for the
// dark reds and blues of dark-theme controls.
func Accent(c color.RGBA) bool {
	s, v := saturationValue(c.R, c.G, c.B)
	return s >= 0.35 && v >= 0.25
}

// Luminance is the relative luminance of c in [0,255].
func Luminance(c color.RGBA) float64 {
	return 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
}

// Contrast is the WCAG 2 contrast ratio between two colours, in [1,21].
func Contrast(a, b color.RGBA) float64 {
	la, lb := linearLuminance(a), linearLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func linearLuminance(c color.RGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func saturationValue(r, g, b uint8) (s, v float64) {
	mx := float64(max(r, g, b)) / 255
	mn := float64(min(r, g, b)) / 255
	if mx == 0 {
		return 0, 0
	}
	return (mx - mn) / mx, mx
}

// histogram bins colours at 4 bits per channel and averages the members
// of each bin, so the reported colour is the real mean, not a bin centre.
type histogram struct {
	n   [4096]int32
	sum [4096][3]int64
}

func newHistogram() *histogram { return &histogram{} }

func (h *histogram) add(r, g, b uint8) {
	i := int(r>>4)<<8 | int(g>>4)<<4 | int(b>>4)
	h.n[i]++
	h.sum[i][0] += int64(r)
	h.sum[i][1] += int64(g)
	h.sum[i][2] += int64(b)
}

func (h *histogram) mean(i int) color.RGBA {
	n := int64(h.n[i])
	return color.RGBA{uint8(h.sum[i][0] / n), uint8(h.sum[i][1] / n), uint8(h.sum[i][2] / n), 255}
}

func (h *histogram) count(c color.RGBA) int32 {
	return h.n[int(c.R>>4)<<8|int(c.G>>4)<<4|int(c.B>>4)]
}

// top returns the mean colour of the most populated bin accepted by keep
// (nil accepts all), or the zero colour when no bin qualifies.
func (h *histogram) top(keep func(color.RGBA) bool) color.RGBA {
	best, bestN := -1, int32(0)
	for i, n := range h.n {
		if n <= bestN {
			continue
		}
		if keep != nil && !keep(h.mean(i)) {
			continue
		}
		best, bestN = i, n
	}
	if best < 0 {
		return color.RGBA{}
	}
	return h.mean(best)
}
