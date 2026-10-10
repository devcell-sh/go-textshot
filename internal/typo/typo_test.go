package typo

import (
	"fmt"
	"image"
	"math"
	"testing"

	"golang.org/x/image/font/opentype"
)

const sample = "Hello World 42"

func render(t *testing.T, c Candidate, size float64) image.Image {
	t.Helper()
	f, err := opentype.Parse(c.TTF)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Render(f, sample, size)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// 1. Family and face: each candidate rendered at a few sizes must be
// recognised as itself among all candidates.
func TestDetectFamily(t *testing.T) {
	cands := DefaultCandidates()
	for _, truth := range DefaultCandidates() {
		for _, size := range []float64{14, 22, 36} {
			t.Run(fmt.Sprintf("%s@%v", truth.Name, size), func(t *testing.T) {
				st, err := Detect(render(t, truth, size), sample, cands)
				if err != nil {
					t.Fatal(err)
				}
				if st.Face != truth.Name {
					t.Errorf("got %s (sim %.3f), want %s", st.Face, st.Similarity, truth.Name)
				}
				// Similarity is a product of several agreement terms, so it is
				// a ranking signal, not a probability: an exact face scores
				// 0.45..0.95 depending on size. Only the floor is checked.
				if st.Similarity < 0.4 {
					t.Errorf("similarity %.3f too low for an exact match", st.Similarity)
				}
			})
		}
	}
}

// 2. Size: the estimate must land within 1 px (or 5%) of the rendered
// size, and the page-level pass must flag the single heading as the
// outlier while leaving the body lines alone.
func TestDetectSize(t *testing.T) {
	cands := DefaultCandidates()
	truth := cands[0]
	sizes := []float64{14, 14, 15, 14, 28, 14}
	styles := make([]Style, len(sizes))
	for i, size := range sizes {
		st, err := Detect(render(t, truth, size), sample, cands)
		if err != nil {
			t.Fatal(err)
		}
		if tol := math.Max(1, 0.05*size); math.Abs(st.SizePx-size) > tol {
			t.Errorf("size %v: estimated %.2f", size, st.SizePx)
		}
		styles[i] = st
	}
	FlagOutliers(styles)
	for i, st := range styles {
		want := sizes[i] == 28
		if st.Outlier != want {
			t.Errorf("line %d (size %v): outlier=%v want %v", i, sizes[i], st.Outlier, want)
		}
	}
}

// 3. Slant: italic faces measure a clear positive shear, upright faces
// stay near zero, independent of the face classifier.
func TestDetectSlant(t *testing.T) {
	for _, c := range DefaultCandidates() {
		t.Run(c.Name, func(t *testing.T) {
			ink := Binarize(render(t, c, 28))
			deg := Slant(ink)
			switch {
			case c.Italic && deg < 6:
				t.Errorf("italic face measured %.0f°, want >= 6", deg)
			case !c.Italic && math.Abs(deg) > 3:
				t.Errorf("upright face measured %.0f°, want |deg| <= 3", deg)
			}
			t.Logf("%s: %.0f°", c.Name, deg)
		})
	}
}

// Weight heuristic: bold is heavier than regular of the same family at
// the same size, by a margin wide enough to threshold on page-relative.
func TestStrokeRatio(t *testing.T) {
	c := DefaultCandidates()
	reg := StrokeRatio(Binarize(render(t, c[0], 28)))
	bold := StrokeRatio(Binarize(render(t, c[1], 28)))
	t.Logf("regular %.3f bold %.3f", reg, bold)
	if bold < reg*1.3 {
		t.Errorf("bold/regular stroke ratio %.2f, want >= 1.3", bold/reg)
	}
}

// Polarity: light text on a dark background binarizes to the same ink.
func TestBinarizePolarity(t *testing.T) {
	c := DefaultCandidates()[0]
	img := render(t, c, 28).(*image.Gray)
	inv := image.NewGray(img.Rect)
	for i, v := range img.Pix {
		inv.Pix[i] = 255 - v
	}
	a, b := inkBounds(Binarize(img)), inkBounds(Binarize(inv))
	if a != b {
		t.Fatalf("ink bounds differ by polarity: %v vs %v", a, b)
	}
}
