package ppocr

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"time"

	"github.com/devcell-sh/go-textshot/internal/imgutil"
)

// Line is one recognized text line in source image coordinates.
type Line struct {
	Box   image.Rectangle
	Text  string
	Score float32
}

// Pipeline chains detection, cropping and recognition.
type Pipeline struct {
	Det *Detector
	Rec *Recognizer
	// Logger receives per-call timing at Debug level; nil discards.
	Logger *slog.Logger
}

// Recognize returns every non-empty text line in img in reading order
// (top to bottom, then left to right).
func (p *Pipeline) Recognize(ctx context.Context, img image.Image) ([]Line, error) {
	start := time.Now()
	boxes, err := p.Det.Detect(ctx, img)
	if err != nil {
		return nil, err
	}
	detDone := time.Now()
	sortBoxes(boxes)
	src := imgutil.ToRGBA(img)
	lines := make([]Line, 0, len(boxes))
	for _, b := range boxes {
		crop := imgutil.CropRGBA(img, b)
		if crop.Rect.Empty() {
			continue
		}
		res, err := p.Rec.Recognize(ctx, crop)
		if err != nil {
			return nil, fmt.Errorf("ppocr: box %v: %w", b, err)
		}
		if res.Text == "" {
			continue
		}
		b = imgutil.TrimToInk(src, b, 30)
		lines = append(lines, Line{Box: b, Text: res.Text, Score: res.Score})
	}
	if p.Logger != nil {
		p.Logger.Debug("ppocr: recognize", "boxes", len(boxes), "lines", len(lines),
			"det", detDone.Sub(start).Round(time.Millisecond), "rec", time.Since(detDone).Round(time.Millisecond))
	}
	return lines, nil
}

// sortBoxes orders boxes in reading order.
func sortBoxes(boxes []image.Rectangle) {
	imgutil.SortReadingOrder(boxes, func(r image.Rectangle) image.Rectangle { return r })
}
