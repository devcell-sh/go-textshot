package ppocr

import (
	"context"
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

// cropRec decodes a crop's text from its size so the pipeline test can
// check cropping and ordering without a real model.
type cropRec struct{ byWidth map[int][]int }

func (c cropRec) Run(_ context.Context, shape []int64, _ []float32) ([]int64, []float32, error) {
	steps := c.byWidth[int(shape[3])]
	if steps == nil {
		steps = []int{0}
	}
	return []int64{1, int64(len(steps)), 6}, probs(6, steps), nil
}

func TestSortBoxes(t *testing.T) {
	boxes := []image.Rectangle{
		image.Rect(50, 40, 90, 60),
		image.Rect(10, 42, 40, 62), // same line as the first, further left
		image.Rect(10, 0, 40, 20),  // top line
	}
	sortBoxes(boxes)
	require.Equal(t, []image.Rectangle{
		image.Rect(10, 0, 40, 20),
		image.Rect(10, 42, 40, 62),
		image.Rect(50, 40, 90, 60),
	}, boxes)
}

func TestPipelineOrdersAndSkipsEmpty(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 320, 160))
	// Three blobs; after unclip they become 52x22 (d=6), 30x20 (d=5) and
	// 100x22 (d=6) -> rec widths ceil(48*w/h): 114, 72, 219.
	fd := &fakeDet{blobs: []image.Rectangle{
		image.Rect(20, 100, 60, 110), // bottom line, text "ab"
		image.Rect(20, 10, 40, 20),   // top line, text "c"
		image.Rect(100, 10, 188, 20), // top line, right, empty text
	}}
	rec := cropRec{byWidth: map[int][]int{114: {1, 2}, 72: {3}, 219: {0}}}
	p := &Pipeline{Det: NewDetector(fd, DetConfig{}), Rec: NewRecognizer(rec, NewDict([]rune{'a', 'b', 'c', 'd'}))}

	lines, err := p.Recognize(context.Background(), img)
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, "c", lines[0].Text)
	require.Equal(t, image.Rect(15, 5, 45, 25), lines[0].Box)
	require.Equal(t, "ab", lines[1].Text)
	require.Equal(t, image.Rect(14, 94, 66, 116), lines[1].Box)
}
