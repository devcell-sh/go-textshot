// Package inpaint fills masked regions of an image from their surroundings
// with the MI-GAN ONNX pipeline (Picsart, ICCV 2023, MIT), over a Runner
// interface so the tiling and compositing rules are pure Go and testable
// with a fake.
//
// The pipeline takes a uint8 RGB image and a uint8 mask (255 = keep, 0 =
// fill), crops around the masked area, runs the 512 px model and blends
// the result back. Feeding it a whole 1080p screenshot with masks spread
// across it would make that crop the whole frame, downscaled to 512 and
// back: blurry. So the image is split into 512 px tiles with overlap, only
// tiles that contain mask run, and only masked pixels are taken from the
// result.
package inpaint

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

// Runner executes the MI-GAN pipeline: image [1 3 H W] uint8, mask
// [1 1 H W] uint8 (255 keep, 0 fill); returns the result image planes in
// the same layout.
type Runner interface {
	Run(ctx context.Context, img []uint8, mask []uint8, height, width int) ([]uint8, error)
}

// Tile is the model's native working size; Overlap keeps seams off tile
// borders. Both in pixels.
const (
	Tile    = 512
	Overlap = 64
)

// Inpainter fills masked pixels.
type Inpainter struct {
	r Runner
}

// New wraps a Runner.
func New(r Runner) *Inpainter { return &Inpainter{r: r} }

// Inpaint returns a copy of img with every pixel where mask is >= 128
// filled from its surroundings. mask must have img's bounds size.
func (p *Inpainter) Inpaint(ctx context.Context, img image.Image, mask *image.Gray) (*image.RGBA, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if mask.Rect.Dx() != w || mask.Rect.Dy() != h {
		return nil, fmt.Errorf("inpaint: mask %dx%d does not match image %dx%d", mask.Rect.Dx(), mask.Rect.Dy(), w, h)
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)

	// A masked pixel inside two overlapping tiles is filled by the first
	// tile that runs; a tile whose masked pixels are all done is skipped.
	done := image.NewGray(image.Rect(0, 0, w, h))
	for _, t := range Tiles(w, h) {
		if !hasPending(mask, done, t) {
			continue
		}
		tw, th := t.Dx(), t.Dy()
		plane := make([]uint8, 3*tw*th)
		mplane := make([]uint8, tw*th)
		for y := 0; y < th; y++ {
			for x := 0; x < tw; x++ {
				c := out.RGBAAt(t.Min.X+x, t.Min.Y+y)
				i := y*tw + x
				plane[i] = c.R
				plane[tw*th+i] = c.G
				plane[2*tw*th+i] = c.B
				if mask.GrayAt(t.Min.X+x, t.Min.Y+y).Y >= 128 {
					mplane[i] = 0
				} else {
					mplane[i] = 255
				}
			}
		}
		res, err := p.r.Run(ctx, plane, mplane, th, tw)
		if err != nil {
			return nil, fmt.Errorf("inpaint: tile %v: %w", t, err)
		}
		if len(res) != len(plane) {
			return nil, fmt.Errorf("inpaint: tile %v: result has %d values, want %d", t, len(res), len(plane))
		}
		// Only masked pixels change, and only inside the tile's core (the
		// overlap band belongs to the neighbour that owns it).
		for y := 0; y < th; y++ {
			for x := 0; x < tw; x++ {
				i := y*tw + x
				if mplane[i] != 0 || done.GrayAt(t.Min.X+x, t.Min.Y+y).Y != 0 {
					continue
				}
				out.SetRGBA(t.Min.X+x, t.Min.Y+y, color.RGBA{res[i], res[tw*th+i], res[2*tw*th+i], 255})
				done.SetGray(t.Min.X+x, t.Min.Y+y, color.Gray{Y: 255})
			}
		}
	}
	return out, nil
}

// Tiles covers a w x h image with Tile-sized squares stepping by
// Tile-Overlap, clamped to the image. Images smaller than a tile are one
// tile. Later tiles overwrite earlier ones in the overlap band, which is
// why masked pixels are written per tile rather than merged.
func Tiles(w, h int) []image.Rectangle {
	step := Tile - Overlap
	var out []image.Rectangle
	for y := 0; ; y += step {
		y0 := y
		if y0+Tile > h {
			y0 = max(0, h-Tile)
		}
		for x := 0; ; x += step {
			x0 := x
			if x0+Tile > w {
				x0 = max(0, w-Tile)
			}
			out = append(out, image.Rect(x0, y0, min(x0+Tile, w), min(y0+Tile, h)))
			if x0+Tile >= w {
				break
			}
		}
		if y0+Tile >= h {
			break
		}
	}
	return out
}

func hasPending(mask, done *image.Gray, r image.Rectangle) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if mask.GrayAt(x, y).Y >= 128 && done.GrayAt(x, y).Y == 0 {
				return true
			}
		}
	}
	return false
}

// MaskRects builds a w x h mask with each rectangle, grown by pad pixels,
// marked for filling. Text boxes hug their glyphs; a few pixels of margin
// take the anti-aliased fringe with them.
func MaskRects(w, h int, rects []image.Rectangle, pad int) *image.Gray {
	m := image.NewGray(image.Rect(0, 0, w, h))
	for _, r := range rects {
		r = r.Inset(-pad).Intersect(m.Rect)
		draw.Draw(m, r, image.NewUniform(color.Gray{Y: 255}), image.Point{}, draw.Src)
	}
	return m
}

// InpaintBoxes fills each box from the ring of context pixels around it
// and nothing else: the pipeline is run on the crop box+context with the
// box as mask, and only the box is written back. The model never sees
// unrelated UI, cannot invent texture from it, and its feathered blend
// outside the box (the halo on a whole-image run) is discarded. Each crop
// is small and the pipeline resizes it to 512 internally, so the fill is
// a smooth continuation of the ring, which is what erased text should be.
func (p *Inpainter) InpaintBoxes(ctx context.Context, img image.Image, boxes []image.Rectangle, context int) (*image.RGBA, error) {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	if context < 1 {
		context = 1
	}
	for _, box := range boxes {
		box = box.Intersect(out.Bounds())
		if box.Empty() {
			continue
		}
		crop := box.Inset(-context).Intersect(out.Bounds())
		cw, ch := crop.Dx(), crop.Dy()
		plane := make([]uint8, 3*cw*ch)
		mplane := make([]uint8, cw*ch)
		for y := 0; y < ch; y++ {
			for x := 0; x < cw; x++ {
				c := out.RGBAAt(crop.Min.X+x, crop.Min.Y+y)
				i := y*cw + x
				plane[i], plane[cw*ch+i], plane[2*cw*ch+i] = c.R, c.G, c.B
				if (image.Point{crop.Min.X + x, crop.Min.Y + y}).In(box) {
					mplane[i] = 0
				} else {
					mplane[i] = 255
				}
			}
		}
		res, err := p.r.Run(ctx, plane, mplane, ch, cw)
		if err != nil {
			return nil, fmt.Errorf("inpaint: box %v: %w", box, err)
		}
		if len(res) != len(plane) {
			return nil, fmt.Errorf("inpaint: box %v: result has %d values, want %d", box, len(res), len(plane))
		}
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				i := (y-crop.Min.Y)*cw + (x - crop.Min.X)
				out.SetRGBA(x, y, color.RGBA{res[i], res[cw*ch+i], res[2*cw*ch+i], 255})
			}
		}
	}
	return out, nil
}
