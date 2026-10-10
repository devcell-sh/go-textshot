package ppocr

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"github.com/devcell-sh/go-textshot/internal/imgutil"
)

// DetConfig configures the DB (Differentiable Binarization) text detector.
// Zero values take the defaults from the model's bundled inference.yml.
type DetConfig struct {
	// MaxSideLen caps the longer image side before inference (default
	// 1920). Images are only ever downscaled; small images are padded, not
	// upscaled, so screenshot text keeps its native pixel size.
	MaxSideLen int
	// Threshold binarizes the probability map (default 0.2).
	Threshold float32
	// BoxThresh drops boxes whose mean probability is below it (default
	// 0.45; negative disables).
	BoxThresh float32
	// MinBoxArea drops components smaller than this many pixels in the
	// resized space (default 10).
	MinBoxArea int
	// MinBoxSide drops boxes whose shorter side is below this many pixels
	// in the resized space (default 3; negative disables).
	MinBoxSide int
	// UnclipRatio expands boxes to undo the DB shrink (default 1.4).
	UnclipRatio float32
}

func (c DetConfig) withDefaults() DetConfig {
	if c.MaxSideLen <= 0 {
		c.MaxSideLen = 1920
	}
	if c.Threshold <= 0 {
		c.Threshold = 0.2
	}
	if c.BoxThresh == 0 {
		c.BoxThresh = 0.45
	}
	if c.MinBoxArea <= 0 {
		c.MinBoxArea = 10
	}
	if c.MinBoxSide == 0 {
		c.MinBoxSide = 3
	}
	if c.UnclipRatio <= 0 {
		c.UnclipRatio = 1.4
	}
	return c
}

// ImageNet normalization used by the PP-OCR detection models.
var (
	detMean = [3]float32{0.485, 0.456, 0.406}
	detStd  = [3]float32{0.229, 0.224, 0.225}
)

// Detector finds axis-aligned text line boxes in an image.
type Detector struct {
	run Runner
	cfg DetConfig
}

// NewDetector wraps a detection model runner.
func NewDetector(run Runner, cfg DetConfig) *Detector {
	return &Detector{run: run, cfg: cfg.withDefaults()}
}

// Detect returns text boxes in img's coordinate space, unordered.
func (d *Detector) Detect(ctx context.Context, img image.Image) ([]image.Rectangle, error) {
	src := imgutil.ToRGBA(img)
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, nil
	}
	in, rw, rh, sx, sy := prepareDet(src, d.cfg.MaxSideLen)

	shape, prob, err := d.run.Run(ctx, []int64{1, 3, int64(rh), int64(rw)}, in)
	if err != nil {
		return nil, fmt.Errorf("ppocr: det inference: %w", err)
	}
	if len(shape) != 4 || shape[0] != 1 || shape[1] != 1 {
		return nil, fmt.Errorf("ppocr: det output shape %v, want [1 1 h w]", shape)
	}
	oh, ow := int(shape[2]), int(shape[3])
	if len(prob) < oh*ow {
		return nil, fmt.Errorf("ppocr: det output has %d values, shape %v needs %d", len(prob), shape, oh*ow)
	}
	// The DB head upsamples back to the input size; if a variant does not,
	// account for the extra scale.
	sx *= float64(rw) / float64(ow)
	sy *= float64(rh) / float64(oh)

	boxes := extractBoxes(prob, oh, ow, d.cfg.Threshold, d.cfg.MinBoxArea)
	boxes = filterBoxes(prob, ow, boxes, d.cfg.MinBoxSide, d.cfg.BoxThresh)
	for i := range boxes {
		boxes[i] = unclipBox(boxes[i], d.cfg.UnclipRatio)
	}
	scaleBoxes(boxes, sx, sy, srcW, srcH)
	return boxes, nil
}

// prepareDet produces the NCHW input tensor for the detector: the image is
// downscaled so its longer side is at most maxSide (never upscaled), padded
// right and bottom with white to multiples of 32, and ImageNet-normalized.
// sx and sy convert resized (unpadded) pixels back to source pixels.
func prepareDet(src *image.RGBA, maxSide int) (data []float32, rw, rh int, sx, sy float64) {
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	w, h := srcW, srcH
	if longer := max(srcW, srcH); longer > maxSide {
		ratio := float64(maxSide) / float64(longer)
		w = max(1, int(float64(srcW)*ratio+0.5))
		h = max(1, int(float64(srcH)*ratio+0.5))
	}
	rw = (w + 31) / 32 * 32
	rh = (h + 31) / 32 * 32

	var content *image.RGBA
	if w == srcW && h == srcH {
		content = src
	} else {
		content = imgutil.ResizeBilinear(src, w, h)
	}
	padded := content
	if rw != w || rh != h {
		padded = image.NewRGBA(image.Rect(0, 0, rw, rh))
		draw.Draw(padded, padded.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(padded, image.Rect(0, 0, w, h), content, image.Point{}, draw.Src)
	}
	return nchw(padded, detMean, detStd), rw, rh, float64(srcW) / float64(w), float64(srcH) / float64(h)
}

// extractBoxes thresholds a probability map and returns the bounding box of
// every 8-connected component with at least minArea pixels, merged into
// text lines.
func extractBoxes(prob []float32, h, w int, thresh float32, minArea int) []image.Rectangle {
	bin := make([]uint8, h*w)
	for i := range bin {
		if prob[i] > thresh {
			bin[i] = 1
		}
	}
	labels, n := ccl(bin, w, h)
	minX := make([]int, n+1)
	minY := make([]int, n+1)
	maxX := make([]int, n+1)
	maxY := make([]int, n+1)
	for i := 1; i <= n; i++ {
		minX[i], minY[i] = w, h
	}
	for i, lb := range labels {
		if lb == 0 {
			continue
		}
		x, y := i%w, i/w
		minX[lb] = min(minX[lb], x)
		minY[lb] = min(minY[lb], y)
		maxX[lb] = max(maxX[lb], x)
		maxY[lb] = max(maxY[lb], y)
	}
	out := make([]image.Rectangle, 0, n)
	for i := 1; i <= n; i++ {
		b := image.Rect(minX[i], minY[i], maxX[i]+1, maxY[i]+1)
		if b.Dx()*b.Dy() >= minArea {
			out = append(out, b)
		}
	}
	return mergeLineBoxes(splitAtGaps(bin, w, out))
}

// splitAtGaps cuts a component box wherever a run of empty columns at
// least 0.35x its height wide separates two runs of text. The DB map
// blurs text outwards by a few pixels, so it bridges word spaces entirely
// but leaves the gaps between menu-bar items and table cells that a reader
// sees as separate lines; word spaces are far narrower than that.
func splitAtGaps(bin []uint8, w int, boxes []image.Rectangle) []image.Rectangle {
	out := boxes[:0:0]
	for _, b := range boxes {
		minGap := max(4, b.Dy()*35/100)
		start, empty := b.Min.X, 0
		for x := b.Min.X; x < b.Max.X; x++ {
			occupied := false
			for y := b.Min.Y; y < b.Max.Y; y++ {
				if bin[y*w+x] != 0 {
					occupied = true
					break
				}
			}
			if !occupied {
				empty++
				continue
			}
			if empty >= minGap && x-empty > start {
				out = append(out, image.Rect(start, b.Min.Y, x-empty, b.Max.Y))
				start = x
			}
			empty = 0
		}
		out = append(out, image.Rect(start, b.Min.Y, b.Max.X, b.Max.Y))
	}
	return out
}

// ccl labels 8-connected components of a binary grid with a two-pass
// union-find. Labels are compacted to 1..k so every label has pixels.
func ccl(bin []uint8, w, h int) ([]int32, int) {
	labels := make([]int32, len(bin))
	parent := []int32{0}
	next := int32(1)

	find := func(l int32) int32 {
		for parent[l] != l {
			parent[l] = parent[parent[l]]
			l = parent[l]
		}
		return l
	}
	union := func(a, b int32) {
		ra, rb := find(a), find(b)
		switch {
		case ra < rb:
			parent[rb] = ra
		case rb < ra:
			parent[ra] = rb
		}
	}

	offs := [4][2]int{{-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
	var ns [4]int32
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if bin[y*w+x] == 0 {
				continue
			}
			k := 0
			for _, o := range offs {
				nx, ny := x+o[0], y+o[1]
				if nx >= 0 && nx < w && ny >= 0 {
					if l := labels[ny*w+nx]; l > 0 {
						ns[k] = l
						k++
					}
				}
			}
			if k == 0 {
				labels[y*w+x] = next
				parent = append(parent, next)
				next++
				continue
			}
			minL := ns[0]
			for _, l := range ns[1:k] {
				minL = min(minL, l)
			}
			labels[y*w+x] = minL
			for _, l := range ns[:k] {
				union(minL, l)
			}
		}
	}

	remap := make(map[int32]int32)
	var k int32
	for i, l := range labels {
		if l == 0 {
			continue
		}
		r := find(l)
		id, ok := remap[r]
		if !ok {
			k++
			id = k
			remap[r] = id
		}
		labels[i] = id
	}
	return labels, int(k)
}

// mergeLineBoxes unions boxes on the same visual line: vertical overlap of
// at least half the shorter box and a horizontal gap under 0.35x the
// shorter height, the complement of splitAtGaps (a word space is bridged
// by the DB map, a menu-bar gap is not). Repeats until stable.
func mergeLineBoxes(boxes []image.Rectangle) []image.Rectangle {
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(boxes); i++ {
			for j := i + 1; j < len(boxes); j++ {
				if sameLine(boxes[i], boxes[j]) {
					boxes[i] = boxes[i].Union(boxes[j])
					boxes = append(boxes[:j], boxes[j+1:]...)
					changed = true
					j--
				}
			}
		}
	}
	return boxes
}

func sameLine(a, b image.Rectangle) bool {
	overlapY := min(a.Max.Y, b.Max.Y) - max(a.Min.Y, b.Min.Y)
	shorter := min(a.Dy(), b.Dy())
	if overlapY*2 < shorter {
		return false
	}
	gap := 0
	switch {
	case b.Min.X > a.Max.X:
		gap = b.Min.X - a.Max.X
	case a.Min.X > b.Max.X:
		gap = a.Min.X - b.Max.X
	}
	return gap*100 < shorter*35
}

// filterBoxes drops boxes whose shorter side is below minSide or whose mean
// probability is below boxThresh. Negative values disable a filter.
func filterBoxes(prob []float32, w int, boxes []image.Rectangle, minSide int, boxThresh float32) []image.Rectangle {
	out := boxes[:0]
	for _, b := range boxes {
		if minSide > 0 && min(b.Dx(), b.Dy()) < minSide {
			continue
		}
		if boxThresh > 0 {
			var sum float32
			var n int
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					sum += prob[y*w+x]
					n++
				}
			}
			if n == 0 || sum/float32(n) < boxThresh {
				continue
			}
		}
		out = append(out, b)
	}
	return out
}

// unclipBox expands a box by the DB offset d = area*ratio/perimeter on all
// sides (at least 1 pixel).
func unclipBox(b image.Rectangle, ratio float32) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	perimeter := 2 * (w + h)
	if perimeter == 0 {
		return b
	}
	d := max(1, int(float32(w*h)*ratio/float32(perimeter)+0.5))
	return b.Inset(-d)
}

// scaleBoxes maps boxes from resized to source coordinates (multiplying by
// source pixels per resized pixel) and clamps them to the source bounds.
func scaleBoxes(boxes []image.Rectangle, sx, sy float64, srcW, srcH int) {
	bounds := image.Rect(0, 0, srcW, srcH)
	for i, b := range boxes {
		boxes[i] = image.Rect(
			int(float64(b.Min.X)*sx+0.5),
			int(float64(b.Min.Y)*sy+0.5),
			int(float64(b.Max.X)*sx+0.5),
			int(float64(b.Max.Y)*sy+0.5),
		).Intersect(bounds)
	}
}
