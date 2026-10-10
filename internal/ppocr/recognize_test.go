package ppocr

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareRecNormalizationAndWidth(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	data, w := prepareRec(img)
	require.Equal(t, 48, w, "ceil(48*2/2)")
	require.Len(t, data, 3*48*48)
	for i, v := range data {
		require.InDelta(t, 1.0, v, 1e-6, "white pixel at %d", i)
	}

	wide := image.NewRGBA(image.Rect(0, 0, 96, 48))
	_, w = prepareRec(wide)
	require.Equal(t, 96, w)
	tall := image.NewRGBA(image.Rect(0, 0, 1, 100))
	_, w = prepareRec(tall)
	require.Equal(t, 1, w, "width floors at 1")
	_, w = prepareRec(image.NewRGBA(image.Rect(0, 0, 0, 0)))
	require.Equal(t, 0, w)
}

func TestPrepareRecChannelOrderBGR(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	data, w := prepareRec(img)
	plane := 48 * w
	require.InDelta(t, -1.0, data[0], 1e-6, "channel 0 is B")
	require.InDelta(t, -1.0, data[plane], 1e-6, "channel 1 is G")
	require.InDelta(t, 1.0, data[2*plane], 1e-6, "channel 2 is R")
}

// probs builds post-softmax rows where class want[t] wins at step t.
func probs(classes int, want []int) []float32 {
	out := make([]float32, len(want)*classes)
	for t, best := range want {
		for c := 0; c < classes; c++ {
			out[t*classes+c] = 0.01
		}
		out[t*classes+best] = 1 - 0.01*float32(classes-1)
	}
	return out
}

func TestDecode(t *testing.T) {
	d := NewDict([]rune{'a', 'b', 'c', 'd'}) // 0 blank, 1..4 chars, 5 space
	cases := []struct {
		name  string
		steps []int
		want  string
	}{
		{"collapse and blank", []int{1, 1, 0, 2, 2}, "ab"},
		{"blank resets repeat", []int{1, 1, 0, 1, 1}, "aa"},
		{"space class", []int{1, 5, 2}, "a b"},
		{"all blank", []int{0, 0, 0}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Decode(probs(6, c.steps), len(c.steps), 6, d)
			require.Equal(t, c.want, res.Text)
			if c.want != "" {
				require.InDelta(t, 0.95, res.Score, 1e-5)
			}
		})
	}
}

func TestDecodeLogitsAreSoftmaxed(t *testing.T) {
	d := NewDict([]rune{'a'})
	logits := []float32{-5, 10, -5} // raw logits: class 1 wins with p ~ 1
	res := Decode(logits, 1, 3, d)
	require.Equal(t, "a", res.Text)
	require.Greater(t, res.Score, float32(0.99))
}

func TestDecodeEdgeCases(t *testing.T) {
	d := NewDict([]rune{'a'})
	require.Equal(t, Result{}, Decode(nil, 0, 3, d))
	require.Equal(t, Result{}, Decode(make([]float32, 12), 4, 3, d), "zero rows decode as blank")
	// Out-of-range class is skipped, not a panic. Short buffer truncates seqLen.
	bad := []float32{0, 0, 0, 0, 1}
	require.Equal(t, "", Decode(bad, 2, 5, d).Text)
}

type fakeRec struct{ steps []int }

func (f fakeRec) Run(_ context.Context, shape []int64, _ []float32) ([]int64, []float32, error) {
	classes := 6
	return []int64{1, int64(len(f.steps)), int64(classes)}, probs(classes, f.steps), nil
}

func TestRecognizer(t *testing.T) {
	rec := NewRecognizer(fakeRec{steps: []int{3, 0, 1, 1}}, NewDict([]rune{'a', 'b', 'c', 'd'}))
	res, err := rec.Recognize(context.Background(), image.NewRGBA(image.Rect(0, 0, 40, 20)))
	require.NoError(t, err)
	require.Equal(t, "ca", res.Text)
	res, err = rec.Recognize(context.Background(), image.NewRGBA(image.Rect(0, 0, 0, 0)))
	require.NoError(t, err)
	require.Equal(t, "", res.Text)
}
