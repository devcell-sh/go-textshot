// Package typo estimates the typography of one recognized text line from
// its pixels: which of the caller's candidate faces reproduces it best
// (render-and-compare), the em size that reproduces its ink height, the
// baseline shear (slant), and a size-independent stroke weight. Pure Go
// over image/draw and x/image/font; no model.
//
// Everything here is a measurement, not a certainty. Face is "the
// candidate that reproduces these pixels best" and Similarity says how
// well; a screenshot set in a face the caller did not supply still gets an
// answer, with a lower score. The public surface is textshot.DetectTypeface.
package typo

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Candidate is one face the detector may answer with.
type Candidate struct {
	Name   string // "Go Bold Italic"
	Family string // "Go"
	Bold   bool
	Italic bool
	TTF    []byte

	font *opentype.Font
}

// Style is the estimate for one line.
type Style struct {
	Family     string  `json:"family"`
	Face       string  `json:"face"`
	Bold       bool    `json:"bold"`
	Italic     bool    `json:"italic"`
	SizePx     float64 `json:"sizePx"`     // em size that reproduces the ink height
	SlantDeg   float64 `json:"slantDeg"`   // measured shear, independent of Face
	Stroke     float64 `json:"stroke"`     // median stroke width / ink height
	Similarity float64 `json:"similarity"` // 0..1 ink overlap with the chosen face
	Outlier    bool    `json:"outlier"`    // size far from the page's dominant size
}

// DefaultCandidates is the Go font family: regular, bold, italic,
// bold-italic and mono. Callers append their own (the app adds Inter).
func DefaultCandidates() []Candidate {
	return []Candidate{
		{Name: "Go Regular", Family: "Go", TTF: goregular.TTF},
		{Name: "Go Bold", Family: "Go", Bold: true, TTF: gobold.TTF},
		{Name: "Go Italic", Family: "Go", Italic: true, TTF: goitalic.TTF},
		{Name: "Go Bold Italic", Family: "Go", Bold: true, Italic: true, TTF: gobolditalic.TTF},
		{Name: "Go Mono", Family: "Go Mono", TTF: gomono.TTF},
	}
}

func (c *Candidate) parsed() (*opentype.Font, error) {
	if c.font == nil {
		f, err := opentype.Parse(c.TTF)
		if err != nil {
			return nil, fmt.Errorf("typo: %s: %w", c.Name, err)
		}
		c.font = f
	}
	return c.font, nil
}

// Detect estimates the style of crop, which must contain exactly the text
// the OCR read. It renders text in every candidate at the size that
// reproduces the crop's ink height and keeps the best overlap.
func Detect(crop image.Image, text string, candidates []Candidate) (Style, error) {
	inkMap := InkMap(crop)
	ink := threshold(inkMap)
	bbox := inkBounds(ink)
	if bbox.Empty() || text == "" {
		return Style{}, fmt.Errorf("typo: no ink in crop")
	}
	st := Style{
		SlantDeg: Slant(ink),
		Stroke:   StrokeRatio(ink),
	}
	best := -1.0
	for i := range candidates {
		c := &candidates[i]
		f, err := c.parsed()
		if err != nil {
			return st, err
		}
		base, _, err := fitSize(f, text, bbox.Dy())
		if err != nil {
			return st, err
		}
		// The ink-height estimate is within a few percent; rasterisation
		// differs visibly between 23.8 and 24 px, so refine the size on the
		// score itself over a small bracket.
		for _, scale := range []float64{0.94, 0.97, 1, 1.03, 1.06} {
			size := base * scale
			rendered, err := Render(f, text, size)
			if err != nil {
				return st, err
			}
			rm := InkMap(rendered)
			sim := Similarity(inkMap, rm)
			// Slant and stroke agreement: neighbouring weights and
			// italic/upright overlap almost entirely at small sizes, but
			// their measured shear and stroke width do not.
			rb := threshold(rm)
			sim *= 1 - math.Min(1, math.Abs(st.SlantDeg-Slant(rb))/20)
			if rs := StrokeRatio(rb); rs > 0 && st.Stroke > 0 {
				d := math.Abs(st.Stroke-rs) / math.Max(st.Stroke, rs)
				sim *= math.Pow(1-d, 3)
			}
			if sim > best {
				best = sim
				st.Family, st.Face, st.Bold, st.Italic = c.Family, c.Name, c.Bold, c.Italic
				st.SizePx, st.Similarity = size, sim
			}
		}
	}
	return st, nil
}

// fitSize renders text at a probe size, then scales so the rendered ink
// height equals inkHeight (ink height is linear in em size for one string).
func fitSize(f *opentype.Font, text string, inkHeight int) (float64, *image.Gray, error) {
	const probe = 64.0
	r, err := Render(f, text, probe)
	if err != nil {
		return 0, nil, err
	}
	h := inkBounds(r).Dy()
	if h == 0 {
		return 0, nil, fmt.Errorf("typo: %q renders no ink", text)
	}
	size := probe * float64(inkHeight) / float64(h)
	r, err = Render(f, text, size)
	return size, r, err
}

// Render draws text in f at size: black ink on white, tightly padded.
func Render(f *opentype.Font, text string, size float64) (*image.Gray, error) {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	m := face.Metrics()
	w := font.MeasureString(face, text).Ceil() + int(size)
	h := (m.Ascent + m.Descent).Ceil() + int(size)
	img := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	d := font.Drawer{Dst: img, Src: image.Black, Face: face, Dot: fixed.P(int(size/2), int(size/2)+m.Ascent.Ceil())}
	d.DrawString(text)
	return img, nil
}

// InkMap is the grayscale ink intensity of a crop: 0 is background, 255 is
// full ink, anti-aliased edges in between. Polarity comes from the border
// (the border is background) and the range is stretched so light-grey text
// on white measures like black text. Keeping the gray levels is what makes
// faces separable at 13 px, where a binary mask throws away most of the
// information.
func InkMap(src image.Image) *image.Gray {
	b := src.Bounds()
	g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(g, g.Bounds(), src, b.Min, draw.Src)
	w, h := g.Rect.Dx(), g.Rect.Dy()
	// Background luminance: median of the border.
	var border []int
	for x := 0; x < w; x++ {
		border = append(border, int(g.GrayAt(x, 0).Y), int(g.GrayAt(x, h-1).Y))
	}
	for y := 0; y < h; y++ {
		border = append(border, int(g.GrayAt(0, y).Y), int(g.GrayAt(w-1, y).Y))
	}
	sort.Ints(border)
	bg := border[len(border)/2]
	maxDiff := 1
	for _, v := range g.Pix {
		if d := abs(int(v) - bg); d > maxDiff {
			maxDiff = d
		}
	}
	out := image.NewGray(g.Rect)
	for i, v := range g.Pix {
		out.Pix[i] = uint8(abs(int(v)-bg) * 255 / maxDiff)
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// threshold turns an ink map into a mask at 50% ink.
func threshold(m *image.Gray) *image.Gray {
	out := image.NewGray(m.Rect)
	for i, v := range m.Pix {
		if v >= 128 {
			out.Pix[i] = 255
		}
	}
	return out
}

// Binarize returns ink=true where the pixel is text (InkMap at 50%).
func Binarize(src image.Image) *image.Gray { return threshold(InkMap(src)) }

// binarizeOtsu is the previous Otsu-on-luminance form, kept for reference
// in tests of thresholding behaviour.
func binarizeOtsu(src image.Image) *image.Gray {
	b := src.Bounds()
	g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(g, g.Bounds(), src, b.Min, draw.Src)
	t := otsu(g)
	var borderDark, borderN int
	for x := 0; x < g.Rect.Dx(); x++ {
		for _, y := range []int{0, g.Rect.Dy() - 1} {
			if g.GrayAt(x, y).Y < t {
				borderDark++
			}
			borderN++
		}
	}
	darkBackground := borderDark*2 > borderN
	out := image.NewGray(g.Rect)
	for y := 0; y < g.Rect.Dy(); y++ {
		for x := 0; x < g.Rect.Dx(); x++ {
			dark := g.GrayAt(x, y).Y < t
			if dark != darkBackground {
				out.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	return out
}

func otsu(g *image.Gray) uint8 {
	var hist [256]int
	n := 0
	for _, v := range g.Pix {
		hist[v]++
		n++
	}
	sum := 0.0
	for i, c := range hist {
		sum += float64(i * c)
	}
	var sumB, wB float64
	best, bestT := -1.0, 0
	for t := 0; t < 256; t++ {
		wB += float64(hist[t])
		if wB == 0 {
			continue
		}
		wF := float64(n) - wB
		if wF == 0 {
			break
		}
		sumB += float64(t * hist[t])
		mB, mF := sumB/wB, (sum-sumB)/wF
		v := wB * wF * (mB - mF) * (mB - mF)
		if v > best {
			best, bestT = v, t
		}
	}
	return uint8(bestT)
}

// inkBounds is the bounding box of ink pixels (ink is Y==255 after
// Binarize; rendered images are inverted here so both paths agree).
func inkBounds(g *image.Gray) image.Rectangle {
	var r image.Rectangle
	isInk := inkTest(g)
	for y := 0; y < g.Rect.Dy(); y++ {
		for x := 0; x < g.Rect.Dx(); x++ {
			if isInk(x, y) {
				p := image.Rect(x, y, x+1, y+1)
				if r.Empty() {
					r = p
				} else {
					r = r.Union(p)
				}
			}
		}
	}
	return r
}

// inkTest handles both conventions: Binarize output (ink white on black)
// and Render output (ink black on white) by looking at the corner pixel.
func inkTest(g *image.Gray) func(x, y int) bool {
	bg := g.GrayAt(0, 0).Y
	if bg < 128 {
		return func(x, y int) bool { return g.GrayAt(x, y).Y >= 128 }
	}
	return func(x, y int) bool { return g.GrayAt(x, y).Y < 128 }
}

// Similarity compares two ink maps (InkMap output) rendered at the same
// ink height. b is aligned to a at the ink box's top-left without
// stretching: advance widths are part of a face's identity, so a candidate
// that runs wider or narrower loses on the width term and on the
// correlation itself.
//
// Score = normalised cross-correlation of the gray ink over the union
//
//	(anti-aliased edges carry sub-pixel stroke and shape information)
//	x width agreement (squared)
//	x column- and row-profile correlation (advance rhythm, x-height,
//	  ascender share, serifs).
func Similarity(a, b *image.Gray) float64 {
	ra, rb := inkBounds(a), inkBounds(b)
	if ra.Empty() || rb.Empty() {
		return 0
	}
	ma, mb := blur(grayOf(a, ra)), blur(grayOf(b, rb))
	w := max(ma.Rect.Dx(), mb.Rect.Dx())
	h := max(ma.Rect.Dy(), mb.Rect.Dy())
	// Best of a one-pixel shift neighbourhood: the two rasterisations never
	// share a sub-pixel phase exactly.
	ncc := 0.0
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			va := make([]float64, 0, w*h)
			vb := make([]float64, 0, w*h)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					va = append(va, at(ma, x, y))
					vb = append(vb, at(mb, x+dx, y+dy))
				}
			}
			ncc = math.Max(ncc, pearson(va, vb))
		}
	}
	wa, wb := float64(ma.Rect.Dx()), float64(mb.Rect.Dx())
	widthAgreement := 1 - math.Abs(wa-wb)/math.Max(wa, wb)
	cols := math.Max(0, pearson(profile(ma, false, 256), profile(mb, false, 256)))
	rows := math.Max(0, pearson(profile(ma, true, 64), profile(mb, true, 64)))
	return ncc * widthAgreement * widthAgreement * cols * rows
}

func at(m *image.Gray, x, y int) float64 {
	if x < 0 || y < 0 || x >= m.Rect.Dx() || y >= m.Rect.Dy() {
		return 0
	}
	return float64(m.GrayAt(x, y).Y) / 255
}

// blur is a 3x3 box blur: softens the dependence on sub-pixel phase.
func blur(m *image.Gray) *image.Gray {
	out := image.NewGray(m.Rect)
	w, h := m.Rect.Dx(), m.Rect.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum, n := 0, 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if x+dx >= 0 && y+dy >= 0 && x+dx < w && y+dy < h {
						sum += int(m.GrayAt(x+dx, y+dy).Y)
						n++
					}
				}
			}
			out.SetGray(x, y, color.Gray{Y: uint8(sum / n)})
		}
	}
	return out
}

// grayOf crops an ink map to r, re-rooted at (0,0).
func grayOf(g *image.Gray, r image.Rectangle) *image.Gray {
	m := image.NewGray(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			m.SetGray(x-r.Min.X, y-r.Min.Y, g.GrayAt(x, y))
		}
	}
	return m
}

// profile is the ink count per column (or per row) resampled to n bins.
func profile(m *image.Gray, rows bool, n int) []float64 {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	length, depth := w, h
	if rows {
		length, depth = h, w
	}
	raw := make([]float64, length)
	for i := 0; i < length; i++ {
		for j := 0; j < depth; j++ {
			x, y := i, j
			if rows {
				x, y = j, i
			}
			raw[i] += float64(m.GrayAt(x, y).Y) / 255
		}
	}
	out := make([]float64, n)
	for k := 0; k < n; k++ {
		pos := float64(k) * float64(length-1) / float64(n-1)
		i := int(pos)
		f := pos - float64(i)
		if i+1 < length {
			out[k] = raw[i]*(1-f) + raw[i+1]*f
		} else {
			out[k] = raw[i]
		}
	}
	return out
}

func pearson(a, b []float64) float64 {
	n := float64(len(a))
	var sa, sb float64
	for i := range a {
		sa += a[i]
		sb += b[i]
	}
	ma, mb := sa/n, sb/n
	var cov, va, vb float64
	for i := range a {
		cov += (a[i] - ma) * (b[i] - mb)
		va += (a[i] - ma) * (a[i] - ma)
		vb += (b[i] - mb) * (b[i] - mb)
	}
	if va == 0 || vb == 0 {
		return 0
	}
	return cov / math.Sqrt(va*vb)
}

// Slant returns the shear angle in degrees (positive = leaning right) that
// makes the vertical projection of the ink sharpest, i.e. the angle at
// which stems stand upright. Sweeps -6..30 degrees.
func Slant(ink *image.Gray) float64 {
	r := inkBounds(ink)
	if r.Empty() {
		return 0
	}
	isInk := inkTest(ink)
	best, bestDeg := -1.0, 0.0
	for deg := -6.0; deg <= 30; deg += 1 {
		tan := math.Tan(deg * math.Pi / 180)
		cols := map[int]int{}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			shift := int(math.Round(float64(r.Max.Y-1-y) * tan))
			for x := r.Min.X; x < r.Max.X; x++ {
				if isInk(x, y) {
					cols[x-shift]++
				}
			}
		}
		sharp := 0.0
		for _, c := range cols {
			sharp += float64(c * c)
		}
		if sharp > best {
			best, bestDeg = sharp, deg
		}
	}
	return bestDeg
}

// StrokeRatio is the median horizontal ink run length divided by the ink
// height: a font-size independent weight measure. Bold is about 0.14 and
// up on Latin text, regular around 0.08..0.11; compare against neighbours
// on the same page rather than an absolute threshold.
func StrokeRatio(ink *image.Gray) float64 {
	r := inkBounds(ink)
	if r.Empty() {
		return 0
	}
	isInk := inkTest(ink)
	var runs []int
	for y := r.Min.Y; y < r.Max.Y; y++ {
		run := 0
		for x := r.Min.X; x <= r.Max.X; x++ {
			if x < r.Max.X && isInk(x, y) {
				run++
				continue
			}
			if run > 0 {
				runs = append(runs, run)
				run = 0
			}
		}
	}
	if len(runs) == 0 {
		return 0
	}
	// Mean of the runs up to twice the median: the median alone is a whole
	// pixel at UI sizes (2/13 vs 3/23), the mean keeps sub-pixel resolution
	// while the cap drops horizontal bars and underlines.
	sort.Ints(runs)
	capRun := 2 * runs[len(runs)/2]
	sum, n := 0, 0
	for _, run := range runs {
		if run <= capRun {
			sum += run
			n++
		}
	}
	return float64(sum) / float64(n) / float64(r.Dy())
}

// FlagOutliers marks sizes far from the dominant size on the page: lines
// usually share a size with their neighbours, so a lone different size is
// either a heading or a wrong estimate worth a second look. Tolerance is
// the larger of 2 px and 15% of the median.
func FlagOutliers(styles []Style) {
	sizes := make([]float64, 0, len(styles))
	for _, s := range styles {
		if s.SizePx > 0 {
			sizes = append(sizes, s.SizePx)
		}
	}
	if len(sizes) < 3 {
		return
	}
	sort.Float64s(sizes)
	median := sizes[len(sizes)/2]
	tol := math.Max(2, 0.15*median)
	for i := range styles {
		styles[i].Outlier = styles[i].SizePx > 0 && math.Abs(styles[i].SizePx-median) > tol
	}
}

// Metrics of text rendered in f at size: advance width, and the ink top and
// bottom relative to the baseline (top is negative, above it). Used to
// place a repaint so its ink lands exactly where the original ink was.
type Metrics struct {
	Width     float64
	InkTop    int
	InkBottom int
}

// Measure renders text once and reports Metrics.
func Measure(f *opentype.Font, text string, size float64) (Metrics, error) {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return Metrics{}, err
	}
	defer face.Close()
	width := float64(font.MeasureString(face, text)) / 64
	img, err := Render(f, text, size)
	if err != nil {
		return Metrics{}, err
	}
	baseline := int(size/2) + face.Metrics().Ascent.Ceil()
	ib := inkBounds(img)
	return Metrics{Width: width, InkTop: ib.Min.Y - baseline, InkBottom: ib.Max.Y - baseline}, nil
}

// Font returns the parsed face of a candidate.
func (c *Candidate) Font() (*opentype.Font, error) { return c.parsed() }

// InkHeight is the ink height of a binarized crop (test/debug helper).
func InkHeight(ink *image.Gray) int { return inkBounds(ink).Dy() }

// InkHeightOf renders text in f at size and returns its ink height.
func InkHeightOf(f *opentype.Font, text string, size float64) int {
	r, err := Render(f, text, size)
	if err != nil {
		return 0
	}
	return inkBounds(r).Dy()
}
