// Package uidet finds interactable UI elements (buttons, icons, menu items,
// fields) with the OmniParser icon_detect_v3 model, a single-class YOLOv9-E
// detector, on top of an abstract model runner so the image math is pure Go
// and testable without ONNX Runtime.
package uidet

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"

	"github.com/devcell-sh/go-textshot/internal/imgutil"
)

// Output is one model output tensor.
type Output struct {
	Shape []int64
	Data  []float32
}

// Runner executes a single-input float32 model and returns every output in
// model order. Returned slices are owned by the caller.
type Runner interface {
	Run(ctx context.Context, shape []int64, data []float32) ([]Output, error)
}

// Config configures the detector. Zero values take the defaults.
type Config struct {
	// MaxSideLen caps the longer image side before inference (default
	// 1280). Images are only ever downscaled; the model is fully
	// convolutional, so the input is padded to a multiple of 32 rather than
	// tiled. 1280 keeps 1080p UI controls detectable; 640 loses most of them.
	MaxSideLen int
	// MinScore drops detections below this confidence (default 0.1).
	MinScore float32
	// IoU is the non-maximum-suppression overlap threshold (default 0.7).
	IoU float32
}

func (c Config) withDefaults() Config {
	if c.MaxSideLen <= 0 {
		c.MaxSideLen = 1280
	}
	if c.MinScore <= 0 {
		c.MinScore = 0.1
	}
	if c.IoU <= 0 {
		c.IoU = 0.7
	}
	return c
}

// Box is one detected element in source image coordinates.
type Box struct {
	Rect  image.Rectangle
	Score float32
}

// Strides of the three detection heads; the model emits, per stride, a
// logits tensor [N classes H/s W/s] followed by an ltrb distance tensor
// [N 4 H/s W/s].
var strides = [3]int{8, 16, 32}

// padValue is the YOLO letterbox grey.
const padValue = 114

// Detector finds UI elements in an image.
type Detector struct {
	run Runner
	cfg Config
}

// NewDetector wraps an icon_detect_v3 model runner.
func NewDetector(run Runner, cfg Config) *Detector {
	return &Detector{run: run, cfg: cfg.withDefaults()}
}

// Detect returns the elements found in img in reading order (top to
// bottom, then left to right), after non-maximum suppression.
func (d *Detector) Detect(ctx context.Context, img image.Image) ([]Box, error) {
	src := imgutil.ToRGBA(img)
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, nil
	}
	in, w, h, scale := prepare(src, d.cfg.MaxSideLen)
	outs, err := d.run.Run(ctx, []int64{1, 3, int64(h), int64(w)}, in)
	if err != nil {
		return nil, err
	}
	boxes, err := decode(outs, w, h, scale, srcW, srcH, d.cfg.MinScore)
	if err != nil {
		return nil, err
	}
	boxes = nms(boxes, d.cfg.IoU)
	sortBoxes(boxes)
	return boxes, nil
}

// prepare downscales src so its longer side is at most maxSide (never
// upscaling), pads the right and bottom edges with grey to a multiple of
// 32, and returns the RGB NCHW tensor in [0,1] plus the padded size and the
// scale from source to tensor coordinates.
func prepare(src *image.RGBA, maxSide int) (in []float32, w, h int, scale float64) {
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	scale = 1
	if longest := max(srcW, srcH); longest > maxSide {
		scale = float64(maxSide) / float64(longest)
	}
	rw := max(1, int(math.Round(float64(srcW)*scale)))
	rh := max(1, int(math.Round(float64(srcH)*scale)))
	w = (rw + 31) / 32 * 32
	h = (rh + 31) / 32 * 32

	content := src
	if scale < 1 {
		content = imgutil.ResizeBilinear(src, rw, rh)
	}
	padded := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(padded, padded.Rect, image.NewUniform(color.RGBA{padValue, padValue, padValue, 255}), image.Point{}, draw.Src)
	draw.Draw(padded, content.Rect, content, image.Point{}, draw.Src)

	in = make([]float32, 3*w*h)
	plane := w * h
	const k = 1.0 / 255.0
	for y := 0; y < h; y++ {
		row := padded.Pix[y*padded.Stride:]
		for x := 0; x < w; x++ {
			i := x * 4
			in[y*w+x] = float32(row[i]) * k
			in[plane+y*w+x] = float32(row[i+1]) * k
			in[2*plane+y*w+x] = float32(row[i+2]) * k
		}
	}
	return in, w, h, scale
}

// decode turns the six head tensors into boxes in source coordinates,
// keeping those scoring at least minScore.
func decode(outs []Output, w, h int, scale float64, srcW, srcH int, minScore float32) ([]Box, error) {
	if len(outs) != 2*len(strides) {
		return nil, fmt.Errorf("uidet: model returned %d outputs, want %d", len(outs), 2*len(strides))
	}
	var boxes []Box
	for k, stride := range strides {
		logits, dist := outs[2*k], outs[2*k+1]
		gw, gh := w/stride, h/stride
		if len(logits.Shape) != 4 || logits.Shape[0] != 1 || logits.Shape[2] != int64(gh) || logits.Shape[3] != int64(gw) {
			return nil, fmt.Errorf("uidet: stride %d logits shape %v, want [1 C %d %d]", stride, logits.Shape, gh, gw)
		}
		if len(dist.Shape) != 4 || dist.Shape[0] != 1 || dist.Shape[1] != 4 || dist.Shape[2] != int64(gh) || dist.Shape[3] != int64(gw) {
			return nil, fmt.Errorf("uidet: stride %d distance shape %v, want [1 4 %d %d]", stride, dist.Shape, gh, gw)
		}
		classes := int(logits.Shape[1])
		cells := gw * gh
		for y := 0; y < gh; y++ {
			for x := 0; x < gw; x++ {
				cell := y*gw + x
				best := float32(math.Inf(-1))
				for c := 0; c < classes; c++ {
					best = max(best, logits.Data[c*cells+cell])
				}
				score := sigmoid(best)
				if score < minScore {
					continue
				}
				ax := (float64(x) + 0.5) * float64(stride)
				ay := (float64(y) + 0.5) * float64(stride)
				s := float64(stride)
				l := float64(dist.Data[0*cells+cell]) * s
				t := float64(dist.Data[1*cells+cell]) * s
				r := float64(dist.Data[2*cells+cell]) * s
				b := float64(dist.Data[3*cells+cell]) * s
				rect := image.Rect(
					clampRound((ax-l)/scale, srcW), clampRound((ay-t)/scale, srcH),
					clampRound((ax+r)/scale, srcW), clampRound((ay+b)/scale, srcH),
				)
				if rect.Empty() {
					continue
				}
				boxes = append(boxes, Box{Rect: rect, Score: score})
			}
		}
	}
	return boxes, nil
}

func sigmoid(x float32) float32 {
	return float32(1 / (1 + math.Exp(-float64(x))))
}

func clampRound(v float64, n int) int {
	return min(max(int(math.Round(v)), 0), n)
}

// nms keeps the highest-scoring box of every group overlapping by more
// than iou (greedy, highest score first).
func nms(boxes []Box, iou float32) []Box {
	slices.SortStableFunc(boxes, func(a, b Box) int { return cmp.Compare(b.Score, a.Score) })
	kept := boxes[:0:0]
	for _, b := range boxes {
		suppressed := false
		for _, k := range kept {
			if overlap(b.Rect, k.Rect) > iou {
				suppressed = true
				break
			}
		}
		if !suppressed {
			kept = append(kept, b)
		}
	}
	return kept
}

// overlap is the intersection-over-union of two rectangles.
func overlap(a, b image.Rectangle) float32 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	ia := inter.Dx() * inter.Dy()
	union := a.Dx()*a.Dy() + b.Dx()*b.Dy() - ia
	if union <= 0 {
		return 0
	}
	return float32(ia) / float32(union)
}

func sortBoxes(boxes []Box) {
	imgutil.SortReadingOrder(boxes, func(b Box) image.Rectangle { return b.Rect })
}
