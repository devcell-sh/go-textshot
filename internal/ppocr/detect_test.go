package ppocr

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareDetDownscalesAndPads(t *testing.T) {
	// 970x1417 at max 960: long side 1417 -> 960, short 970 -> 657, then
	// padded to 672x960 (multiples of 32). Scale factors exclude padding.
	img := image.NewRGBA(image.Rect(0, 0, 970, 1417))
	data, rw, rh, sx, sy := prepareDet(img, 960)
	require.Equal(t, 672, rw)
	require.Equal(t, 960, rh)
	require.Len(t, data, 3*672*960)
	require.InDelta(t, 970.0/657.0, sx, 1e-9)
	require.InDelta(t, 1417.0/960.0, sy, 1e-9)
}

func TestPrepareDetNeverUpscales(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	_, rw, rh, sx, sy := prepareDet(img, 1920)
	require.Equal(t, 128, rw)
	require.Equal(t, 64, rh)
	require.Equal(t, 1.0, sx)
	require.Equal(t, 1.0, sy)
}

func TestPrepareDetPadsWhiteAndNormalizesBGR(t *testing.T) {
	// 1x1 pure-red image padded to 32x32: pixel (0,0) is red, the padding
	// is white. Channel 0 is B, channel 2 is R.
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	data, rw, rh, _, _ := prepareDet(img, 1920)
	plane := rw * rh
	norm := func(v float32, c int) float32 { return (v - detMean[c]) / detStd[c] }
	require.InDelta(t, norm(0, 0), data[0], 1e-5, "B of red pixel")
	require.InDelta(t, norm(0, 1), data[plane], 1e-5, "G of red pixel")
	require.InDelta(t, norm(1, 2), data[2*plane], 1e-5, "R of red pixel")
	// Padding pixel (5,5) is white in all channels.
	i := 5*rw + 5
	for c := range 3 {
		require.InDelta(t, norm(1, c), data[c*plane+i], 1e-5, "white padding channel %d", c)
	}
}

func TestExtractBoxesThresholdAndMinArea(t *testing.T) {
	const w, h = 40, 12
	prob := make([]float32, w*h)
	for y := 4; y < 8; y++ {
		for x := 10; x < 16; x++ {
			prob[y*w+x] = 0.9
		}
	}
	for y := 1; y < 3; y++ {
		for x := 30; x < 32; x++ {
			prob[y*w+x] = 0.2 // below threshold
		}
	}
	boxes := extractBoxes(prob, h, w, 0.3, 10)
	require.Equal(t, []image.Rectangle{image.Rect(10, 4, 16, 8)}, boxes)
	require.Empty(t, extractBoxes(prob, h, w, 0.3, 6*4+1))
}

func TestCCLCompactsLabels(t *testing.T) {
	// Three isolated pixels: labels must be exactly 1..3 with no gaps, or
	// extractBoxes would emit phantom inverted boxes for missing labels.
	const w, h = 20, 6
	bin := make([]uint8, w*h)
	for _, p := range [][2]int{{1, 1}, {5, 1}, {10, 3}} {
		bin[p[1]*w+p[0]] = 1
	}
	labels, n := ccl(bin, w, h)
	require.Equal(t, 3, n)
	seen := map[int32]int{}
	for _, l := range labels {
		if l > 0 {
			seen[l]++
		}
	}
	require.Equal(t, map[int32]int{1: 1, 2: 1, 3: 1}, seen)
}

func TestCCLMergesDiagonalAndUShape(t *testing.T) {
	// A U shape whose arms are first labelled separately and joined by the
	// bottom row: must end as one component. Diagonal touch counts too.
	const w, h = 7, 4
	rows := []string{
		"#.#...#",
		"#.#..#.",
		"###....",
		".......",
	}
	bin := make([]uint8, w*h)
	for y, r := range rows {
		for x, c := range r {
			if c == '#' {
				bin[y*w+x] = 1
			}
		}
	}
	_, n := ccl(bin, w, h)
	require.Equal(t, 2, n)
}

func TestMergeLineBoxes(t *testing.T) {
	// Two word boxes on one line merge; a box on the next line does not.
	boxes := []image.Rectangle{
		image.Rect(10, 10, 40, 30),
		image.Rect(45, 11, 80, 31),
		image.Rect(10, 50, 80, 70),
	}
	merged := mergeLineBoxes(boxes)
	require.Equal(t, []image.Rectangle{image.Rect(10, 10, 80, 31), image.Rect(10, 50, 80, 70)}, merged)

	// Same line but a wide gap (columns) stays separate.
	cols := []image.Rectangle{image.Rect(0, 0, 30, 20), image.Rect(60, 0, 90, 20)}
	require.Len(t, mergeLineBoxes(cols), 2)
}

func TestSplitAtGaps(t *testing.T) {
	// A 10-row map: text at x 0-19 and 40-59 bridged by one pixel on
	// row 5 (so CCL sees one component), then a word-sized 3 px gap to
	// text at 63-79.
	const w, h = 80, 10
	bin := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < 20 || (x >= 40 && x < 60) || x >= 63 {
				bin[y*w+x] = 1
			}
		}
	}
	bin[5*w+30] = 1
	got := splitAtGaps(bin, w, []image.Rectangle{image.Rect(0, 0, 80, 10)})
	require.Equal(t, []image.Rectangle{image.Rect(0, 0, 20, 10), image.Rect(30, 0, 31, 10), image.Rect(40, 0, 80, 10)}, got,
		"wide gaps split, the word-sized gap at 60-63 does not; the bridge pixel becomes a sliver that filterBoxes' minSide drops")
}

func TestFilterBoxes(t *testing.T) {
	const w, h = 20, 10
	prob := make([]float32, w*h)
	for y := 1; y < 4; y++ {
		for x := 1; x < 6; x++ {
			prob[y*w+x] = 0.9
		}
		for x := 10; x < 15; x++ {
			prob[y*w+x] = 0.2
		}
	}
	boxes := []image.Rectangle{
		image.Rect(1, 1, 6, 4),
		image.Rect(10, 1, 15, 4),
		image.Rect(18, 8, 19, 9),
	}
	got := filterBoxes(prob, w, append([]image.Rectangle(nil), boxes...), 3, 0.6)
	require.Equal(t, []image.Rectangle{image.Rect(1, 1, 6, 4)}, got)
	require.Len(t, filterBoxes(prob, w, append([]image.Rectangle(nil), boxes...), -1, -1), 3)
}

func TestUnclipAndScaleGolden(t *testing.T) {
	// Reference golden: box {84,214,301,226} on a 970x1417 page resized to
	// 657x960 maps to {124,316,444,334} when multiplied by src/resized.
	boxes := []image.Rectangle{image.Rect(84, 214, 301, 226)}
	scaleBoxes(boxes, 970.0/657.0, 1417.0/960.0, 970, 1417)
	require.Equal(t, image.Rect(124, 316, 444, 334), boxes[0])

	clamped := []image.Rectangle{image.Rect(-20, -5, 1200, 1500)}
	scaleBoxes(clamped, 1, 1, 970, 1417)
	require.Equal(t, image.Rect(0, 0, 970, 1417), clamped[0])

	// 20x10 box: d = round(200*1.4/60) = 5.
	require.Equal(t, image.Rect(-5, -5, 25, 15), unclipBox(image.Rect(0, 0, 20, 10), 1.4))
	require.Equal(t, image.Rect(-1, -1, 2, 2), unclipBox(image.Rect(0, 0, 1, 1), 1.4), "minimum offset is 1")
}

// fakeDet returns a probability map with blobs at the given boxes, in
// whatever input size it is asked for.
type fakeDet struct {
	blobs []image.Rectangle
	seen  []int64
}

func (f *fakeDet) Run(_ context.Context, shape []int64, _ []float32) ([]int64, []float32, error) {
	f.seen = shape
	h, w := int(shape[2]), int(shape[3])
	prob := make([]float32, h*w)
	for _, b := range f.blobs {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				prob[y*w+x] = 0.95
			}
		}
	}
	return []int64{1, 1, int64(h), int64(w)}, prob, nil
}

func TestDetectorEndToEnd(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	fd := &fakeDet{blobs: []image.Rectangle{image.Rect(20, 10, 60, 20), image.Rect(20, 50, 100, 60)}}
	det := NewDetector(fd, DetConfig{})
	boxes, err := det.Detect(context.Background(), img)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3, 128, 224}, fd.seen, "padded to multiples of 32")
	require.Len(t, boxes, 2)
	for _, b := range boxes {
		require.True(t, b.In(img.Bounds()), "box %v inside image", b)
	}
	// Unclip grows the 40x10 blob by d = round(400*1.4/100) = 6.
	require.Contains(t, boxes, image.Rect(14, 4, 66, 26))
}
