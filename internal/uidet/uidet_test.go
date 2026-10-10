package uidet

import (
	"context"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeRunner records the input and returns heads built by build.
type fakeRunner struct {
	shape []int64
	build func(w, h int) []Output
}

func (f *fakeRunner) Run(_ context.Context, shape []int64, data []float32) ([]Output, error) {
	f.shape = shape
	w, h := int(shape[3]), int(shape[2])
	if len(data) != 3*w*h {
		return nil, context.Canceled
	}
	return f.build(w, h), nil
}

// emptyHeads returns six zero-filled head tensors for a w x h input.
func emptyHeads(w, h int) []Output {
	var outs []Output
	for _, s := range strides {
		gw, gh := w/s, h/s
		outs = append(outs,
			Output{Shape: []int64{1, 1, int64(gh), int64(gw)}, Data: make([]float32, gw*gh)},
			Output{Shape: []int64{1, 4, int64(gh), int64(gw)}, Data: make([]float32, 4*gw*gh)},
		)
	}
	// A zero logit is sigmoid 0.5, so start every cell well below MinScore.
	for k := range strides {
		for i := range outs[2*k].Data {
			outs[2*k].Data[i] = -20
		}
	}
	return outs
}

// put writes a detection at grid cell (x, y) of the given stride head with
// the ltrb distances (in grid units) and a logit giving score.
func put(outs []Output, head, x, y int, l, t, r, b float32, logit float32) {
	gw := int(outs[2*head].Shape[3])
	cells := int(outs[2*head].Shape[2]) * gw
	cell := y*gw + x
	outs[2*head].Data[cell] = logit
	for i, v := range []float32{l, t, r, b} {
		outs[2*head+1].Data[i*cells+cell] = v
	}
}

func TestPreparePadsAndNeverUpscales(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	in, w, h, scale := prepare(src, 1280)
	require.Equal(t, 1.0, scale)
	require.Equal(t, 128, w)
	require.Equal(t, 64, h)
	require.Len(t, in, 3*128*64)
	require.InDelta(t, 1.0, in[0], 1e-6, "content pixel")
	require.InDelta(t, float32(padValue)/255, in[64*128-1], 1e-6, "pad pixel (bottom right)")
	require.InDelta(t, float32(padValue)/255, in[2*128*64+64*128-1], 1e-6, "pad pixel, blue plane")
}

func TestPrepareDownscalesToMaxSide(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	_, w, h, scale := prepare(src, 1280)
	require.InDelta(t, 2.0/3.0, scale, 1e-9)
	require.Equal(t, 1280, w)
	require.Equal(t, 736, h) // 720 rounded up to a multiple of 32
}

func TestPrepareChannelOrderIsRGB(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 32, 32))
	src.SetRGBA(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	in, w, h, _ := prepare(src, 1280)
	require.Equal(t, 32, w)
	require.Equal(t, 32, h)
	require.InDelta(t, 1.0, in[0], 1e-6)
	require.InDelta(t, 0.0, in[w*h], 1e-6)
	require.InDelta(t, 0.0, in[2*w*h], 1e-6)
}

func TestDetectDecodesMapsAndSuppresses(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	run := &fakeRunner{build: func(w, h int) []Output {
		outs := emptyHeads(w, h)
		// Stride-8 cell (100, 50): anchor (804, 404) in tensor space with
		// distances 2,1,3,2 grid units -> tensor box (788,396)-(828,420),
		// which is (1182,594)-(1242,630) in source space at scale 2/3.
		put(outs, 0, 100, 50, 2, 1, 3, 2, 3) // sigmoid(3) ~ 0.95
		// Overlapping duplicate from the neighbouring cell with a lower score.
		put(outs, 0, 101, 50, 3, 1, 2, 2, 1) // ~0.73
		// Far away, above the row: stride-32 cell (2, 1) -> anchor (80, 48).
		put(outs, 2, 2, 1, 1, 1, 1, 1, 0) // sigmoid(0) = 0.5
		// Below MinScore.
		put(outs, 1, 10, 10, 1, 1, 1, 1, -5)
		return outs
	}}
	boxes, err := NewDetector(run, Config{}).Detect(context.Background(), img)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3, 736, 1280}, run.shape)
	require.Len(t, boxes, 2)

	require.Equal(t, image.Rect(72, 24, 168, 120), boxes[0].Rect, "reading order: top row first")
	require.InDelta(t, 0.5, boxes[0].Score, 1e-6)
	require.Equal(t, image.Rect(1182, 594, 1242, 630), boxes[1].Rect)
	require.InDelta(t, 1/(1+math.Exp(-3)), boxes[1].Score, 1e-6)
}

func TestDetectClampsToImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	run := &fakeRunner{build: func(w, h int) []Output {
		outs := emptyHeads(w, h)
		put(outs, 0, 0, 0, 10, 10, 1, 1, 2) // extends past the top-left corner
		return outs
	}}
	boxes, err := NewDetector(run, Config{}).Detect(context.Background(), img)
	require.NoError(t, err)
	require.Len(t, boxes, 1)
	require.Equal(t, image.Rect(0, 0, 12, 12), boxes[0].Rect)
}

func TestDetectRejectsWrongHeads(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	run := &fakeRunner{build: func(w, h int) []Output { return emptyHeads(w, h)[:4] }}
	_, err := NewDetector(run, Config{}).Detect(context.Background(), img)
	require.ErrorContains(t, err, "4 outputs")

	run = &fakeRunner{build: func(w, h int) []Output {
		outs := emptyHeads(w, h)
		outs[1].Shape = []int64{1, 4, 1, 1}
		return outs
	}}
	_, err = NewDetector(run, Config{}).Detect(context.Background(), img)
	require.ErrorContains(t, err, "distance shape")
}

func TestDetectEmptyImage(t *testing.T) {
	boxes, err := NewDetector(&fakeRunner{}, Config{}).Detect(context.Background(), image.NewRGBA(image.Rect(0, 0, 0, 0)))
	require.NoError(t, err)
	require.Nil(t, boxes)
}

func TestNMSDropsNestedDuplicatesButKeepsContents(t *testing.T) {
	kept := nms([]Box{
		{Rect: image.Rect(47, 0, 168, 22), Score: 0.17},      // loose box around a menu item
		{Rect: image.Rect(54, 3, 160, 19), Score: 0.47},      // tight box, same item
		{Rect: image.Rect(168, 0, 209, 22), Score: 0.12},     // loose box around "File"
		{Rect: image.Rect(178, 3, 201, 20), Score: 0.43},     // tight box, 43% of the loose one
		{Rect: image.Rect(676, 301, 1241, 541), Score: 0.51}, // panel
		{Rect: image.Rect(700, 310, 730, 340), Score: 0.38},  // icon inside the panel
	}, 0.7)
	require.Len(t, kept, 4)
	require.Equal(t, image.Rect(676, 301, 1241, 541), kept[0].Rect)
	require.Equal(t, image.Rect(54, 3, 160, 19), kept[1].Rect, "tight, higher-scoring box wins")
	require.Equal(t, image.Rect(178, 3, 201, 20), kept[2].Rect, "tight box wins even at a third of the loose one")
	require.Equal(t, image.Rect(700, 310, 730, 340), kept[3].Rect, "small icon is not a duplicate of the panel")
}

func TestOverlap(t *testing.T) {
	a := image.Rect(0, 0, 10, 10)
	require.InDelta(t, 1.0, overlap(a, a), 1e-6)
	require.InDelta(t, 0.0, overlap(a, image.Rect(10, 10, 20, 20)), 1e-6)
	require.InDelta(t, 0.25, overlap(a, image.Rect(0, 0, 10, 40)), 1e-6)
}
