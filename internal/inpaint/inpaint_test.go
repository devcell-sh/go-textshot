package inpaint

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// fakeRunner fills masked pixels with the mean of the unmasked ones: enough
// to prove which pixels Inpaint touches and which it leaves alone.
type fakeRunner struct{ calls int }

func (f *fakeRunner) Run(_ context.Context, img, mask []uint8, h, w int) ([]uint8, error) {
	f.calls++
	n := w * h
	out := append([]uint8(nil), img...)
	for c := 0; c < 3; c++ {
		sum, cnt := 0, 0
		for i := 0; i < n; i++ {
			if mask[i] == 255 {
				sum += int(img[c*n+i])
				cnt++
			}
		}
		mean := uint8(sum / max(cnt, 1))
		for i := 0; i < n; i++ {
			if mask[i] == 0 {
				out[c*n+i] = mean
			}
		}
	}
	return out, nil
}

func TestTilesCoverImage(t *testing.T) {
	for _, size := range [][2]int{{100, 50}, {512, 512}, {1920, 1080}, {513, 600}} {
		w, h := size[0], size[1]
		covered := image.NewGray(image.Rect(0, 0, w, h))
		for _, r := range Tiles(w, h) {
			if r.Dx() > Tile || r.Dy() > Tile || !r.In(covered.Rect) {
				t.Fatalf("%dx%d: bad tile %v", w, h, r)
			}
			draw.Draw(covered, r, image.NewUniform(color.Gray{Y: 255}), image.Point{}, draw.Src)
		}
		for i, v := range covered.Pix {
			if v != 255 {
				t.Fatalf("%dx%d: pixel %d not covered", w, h, i)
			}
		}
	}
	if n := len(Tiles(1920, 1080)); n != 15 {
		t.Fatalf("1080p tiles: %d, want 15 (5x3)", n)
	}
}

func TestInpaintTouchesOnlyMaskedTiles(t *testing.T) {
	w, h := 1000, 600
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{200, 200, 200, 255}), image.Point{}, draw.Src)
	box := image.Rect(100, 100, 200, 130)
	draw.Draw(img, box, image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)

	r := &fakeRunner{}
	out, err := New(r).Inpaint(context.Background(), img, MaskRects(w, h, []image.Rectangle{box}, 2))
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatalf("runner called %d times, want 1 (one tile holds the mask)", r.calls)
	}
	if c := out.RGBAAt(150, 115); c.R < 190 {
		t.Fatalf("masked pixel not filled: %v", c)
	}
	if c := out.RGBAAt(900, 500); c != (color.RGBA{200, 200, 200, 255}) {
		t.Fatalf("untouched pixel changed: %v", c)
	}
	if c := img.RGBAAt(150, 115); c != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("source image modified")
	}
}

func TestInpaintBoxesSeesOnlyTheRing(t *testing.T) {
	w, h := 400, 200
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{200, 200, 200, 255}), image.Point{}, draw.Src)
	// Far-away dark panel the model must never be shown.
	draw.Draw(img, image.Rect(300, 0, 400, 200), image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
	box := image.Rect(100, 100, 200, 120)
	draw.Draw(img, box, image.NewUniform(color.RGBA{10, 10, 10, 255}), image.Point{}, draw.Src)

	var seenW, seenH int
	r := &recordingRunner{fakeRunner: &fakeRunner{}, onRun: func(w, h int) { seenW, seenH = w, h }}
	out, err := New(r).InpaintBoxes(context.Background(), img, []image.Rectangle{box}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if seenW != 106 || seenH != 26 {
		t.Fatalf("model saw %dx%d, want the box plus a 3 px ring (106x26)", seenW, seenH)
	}
	if c := out.RGBAAt(150, 110); c.R < 190 {
		t.Fatalf("box not filled from the ring: %v", c)
	}
	if c := out.RGBAAt(99, 110); c != (color.RGBA{200, 200, 200, 255}) {
		t.Fatalf("ring pixel changed: %v", c)
	}
}

type recordingRunner struct {
	*fakeRunner
	onRun func(w, h int)
}

func (r *recordingRunner) Run(ctx context.Context, img, mask []uint8, h, w int) ([]uint8, error) {
	r.onRun(w, h)
	return r.fakeRunner.Run(ctx, img, mask, h, w)
}
