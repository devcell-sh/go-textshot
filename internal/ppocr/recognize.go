package ppocr

import (
	"context"
	"fmt"
	"image"
	"math"

	"github.com/devcell-sh/go-textshot/internal/imgutil"
)

// RecHeight is the fixed input height of the PP-OCRv6 recognition models.
const RecHeight = 48

// Result is the recognized text of one line.
type Result struct {
	Text string
	// Score is the mean probability of the emitted characters.
	Score float32
}

// Recognizer runs the CTC recognition model on single text-line crops.
type Recognizer struct {
	run  Runner
	dict *Dict
}

// NewRecognizer wraps a recognition model runner. dict must match the
// model's output class count (see Dict.Validate).
func NewRecognizer(run Runner, dict *Dict) *Recognizer {
	return &Recognizer{run: run, dict: dict}
}

// Recognize decodes the text in a line image.
func (r *Recognizer) Recognize(ctx context.Context, img image.Image) (Result, error) {
	in, w := prepareRec(imgutil.ToRGBA(img))
	if w == 0 {
		return Result{}, nil
	}
	shape, out, err := r.run.Run(ctx, []int64{1, 3, RecHeight, int64(w)}, in)
	if err != nil {
		return Result{}, fmt.Errorf("ppocr: rec inference: %w", err)
	}
	if len(shape) != 3 || shape[0] != 1 {
		return Result{}, fmt.Errorf("ppocr: rec output shape %v, want [1 T classes]", shape)
	}
	return Decode(out, int(shape[1]), int(shape[2]), r.dict), nil
}

// prepareRec resizes a line crop to height RecHeight keeping its aspect
// ratio (width = ceil(48*w/h)) and normalizes with (pix/255 - 0.5)/0.5 in
// BGR NCHW layout. It returns the tensor and the resized width.
func prepareRec(src *image.RGBA) ([]float32, int) {
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, 0
	}
	w := max(1, int(math.Ceil(float64(RecHeight)*float64(srcW)/float64(srcH))))
	resized := imgutil.ResizeBilinear(src, w, RecHeight)
	return nchw(resized, [3]float32{0.5, 0.5, 0.5}, [3]float32{0.5, 0.5, 0.5}), w
}

// Decode applies greedy CTC decoding to seqLen rows of classes values:
// argmax per step, collapse repeats, drop blanks (class 0), map through the
// dictionary. Blanks reset the repeat memory so "aa_aa" decodes to "aa".
func Decode(probs []float32, seqLen, classes int, d *Dict) Result {
	if seqLen < 1 || classes < 1 {
		return Result{}
	}
	if len(probs) < seqLen*classes {
		seqLen = len(probs) / classes
	}
	var text []rune
	var scoreSum float32
	emitted := 0
	prev := -1
	for t := 0; t < seqLen; t++ {
		row := probs[t*classes : (t+1)*classes]
		if !isDistribution(row) {
			row = softmax(row)
		}
		best, p := argmax(row)
		if best == 0 {
			prev = -1
			continue
		}
		if best == prev {
			continue
		}
		prev = best
		ch, ok := d.At(best)
		if !ok {
			continue
		}
		text = append(text, ch)
		scoreSum += p
		emitted++
	}
	res := Result{Text: string(text)}
	if emitted > 0 {
		res.Score = scoreSum / float32(emitted)
	}
	return res
}

func argmax(row []float32) (int, float32) {
	best, bestP := 0, float32(math.Inf(-1))
	for i, v := range row {
		if v > bestP {
			best, bestP = i, v
		}
	}
	return best, bestP
}

// isDistribution reports whether a row already sums to ~1 with values in
// [0,1]. The PP-OCRv6 export ends in a Softmax node; applying softmax
// again would flatten confidences, so rows are only normalized when they
// look like raw logits.
func isDistribution(row []float32) bool {
	var sum float32
	for _, v := range row {
		if v < 0 || v > 1 {
			return false
		}
		sum += v
	}
	return sum > 0.99 && sum < 1.01
}

func softmax(row []float32) []float32 {
	out := make([]float32, len(row))
	m := float32(math.Inf(-1))
	for _, v := range row {
		m = max(m, v)
	}
	var sum float32
	for i, v := range row {
		out[i] = float32(math.Exp(float64(v - m)))
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}
