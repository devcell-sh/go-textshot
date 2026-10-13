package textshot

import (
	"image"

	"github.com/devcell-sh/go-textshot/internal/typo"
)

// Face is a font the caller can render with: a TrueType/OpenType file plus
// how to describe it. DetectTypeface answers with one of these, so pass
// exactly the faces you are able to draw the replacement text in.
type Face = typo.Candidate

// Typeface is what DetectTypeface measured on one text line.
//
// Face, Family, Bold and Italic name the candidate that reproduces the
// pixels best; Similarity (0..1) is the ink overlap with that candidate
// and is the only honest statement about the real font. SizePx is the em
// size that gives the candidate the same ink height as the original.
// SlantDeg and Stroke are measured on the pixels alone and do not depend
// on the candidates: slant is the shear that makes stems upright (italics
// read 9-12 degrees), stroke is the median stroke width over the ink
// height (bold is about 1.3x regular of the same face).
type Typeface = typo.Style

// GoFaces is the Go font family (regular, bold, italic, bold italic, mono)
// that ships with golang.org/x/image: a candidate set for tests and a
// fallback when the caller has no fonts of its own.
func GoFaces() []Face { return typo.DefaultCandidates() }

// DetectTypeface measures the text line in box of img. text must be what
// the line reads (the OCR result), because the candidates are rendered
// with it and compared to the crop. faces are rendered lazily and cached
// inside the slice, so keep the same slice across calls.
//
// It is pure Go and needs no engine or model, so it also runs under
// CGO_ENABLED=0.
func DetectTypeface(img image.Image, box image.Rectangle, text string, faces []Face) (Typeface, error) {
	crop := cropImage(img, box)
	return typo.Detect(crop, text, faces)
}

// FlagSizeOutliers sets Outlier on the lines whose SizePx is far from the
// dominant size: lines on a screen usually share a size with their
// neighbours, so a lone different size is a heading or an estimate worth a
// second look. Tolerance is the larger of 2 px and 15% of the median.
func FlagSizeOutliers(lines []Typeface) { typo.FlagOutliers(lines) }

// MeasureSlant is the baseline shear of the text in crop, in degrees.
func MeasureSlant(crop image.Image) float64 { return typo.Slant(typo.Binarize(crop)) }

// MeasureStroke is the stroke weight of the text in crop: median stroke
// width divided by ink height.
func MeasureStroke(crop image.Image) float64 { return typo.StrokeRatio(typo.Binarize(crop)) }

// FaceMetrics is the advance width of text and its ink extent relative to
// the baseline (InkTop is negative, above it), for placing a repaint so its
// ink lands where the original ink was.
type FaceMetrics = typo.Metrics

// MeasureFace renders text in face at size and reports FaceMetrics.
func MeasureFace(face *Face, text string, size float64) (FaceMetrics, error) {
	f, err := face.Font()
	if err != nil {
		return FaceMetrics{}, err
	}
	return typo.Measure(f, text, size)
}

func cropImage(img image.Image, box image.Rectangle) image.Image {
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	box = box.Intersect(img.Bounds())
	if si, ok := img.(subImager); ok {
		return si.SubImage(box)
	}
	out := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			out.Set(x-box.Min.X, y-box.Min.Y, img.At(x, y))
		}
	}
	return out
}

// InkMap is the grayscale ink intensity of a text crop: 0 is background,
// 255 full ink, anti-aliased edges in between, with polarity taken from
// the border and the range stretched. Thresholding it at 128 gives the
// glyph strokes, which is the tightest possible erase mask for a line.
func InkMap(crop image.Image) *image.Gray { return typo.InkMap(crop) }
