package textshot

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-textshot/internal/ppocr"
)

func osName() string   { return runtime.GOOS }
func archName() string { return runtime.GOARCH }

func TestOptionsDefaultsAndEnv(t *testing.T) {
	t.Setenv(EnvCacheDir, "")
	t.Setenv(EnvLibraryPath, "")
	o := Options{}.withDefaults()
	require.Equal(t, DefaultCacheDir(), o.CacheDir)
	require.Empty(t, o.LibraryPath)
	require.Equal(t, 1920, o.MaxSideLen)
	require.Equal(t, runtime.GOMAXPROCS(0), o.Threads)
	require.Equal(t, float32(0.5), o.MinScore)
	require.Equal(t, float32(-1), Options{MinScore: -1}.withDefaults().MinScore)
	require.NotNil(t, o.Logger)

	t.Setenv(EnvCacheDir, "/env/cache")
	t.Setenv(EnvLibraryPath, "/env/lib.so")
	o = Options{}.withDefaults()
	require.Equal(t, "/env/cache", o.CacheDir)
	require.Equal(t, "/env/lib.so", o.LibraryPath)

	o = Options{CacheDir: "/opt/c", LibraryPath: "/opt/l"}.withDefaults()
	require.Equal(t, "/opt/c", o.CacheDir, "explicit option beats env")
	require.Equal(t, "/opt/l", o.LibraryPath)
}

func TestModelPaths(t *testing.T) {
	require.Equal(t, "https://huggingface.co/PaddlePaddle/PP-OCRv6_small_det_onnx/resolve/28fe5895c24fd108c19eb3e8479f4ab385fbfc62/inference.onnx", DetModel.URL)
	require.Equal(t, filepath.Join("/c", "models", "PP-OCRv6_small_det_onnx-28fe5895c24f.onnx"), DetModel.CachePath("/c"))
	require.Len(t, RecModel.SHA256, 64)
}

func TestLines(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, Lines([]TextResult{{Text: "a"}, {Text: "b"}}))
	require.Empty(t, Lines(nil))
}

func TestMergeElements(t *testing.T) {
	interactables := []Element{
		{Kind: KindIcon, Box: image.Rect(1160, 554, 1243, 575), Score: 0.75}, // Continue button
		{Kind: KindIcon, Box: image.Rect(676, 301, 1241, 541), Score: 0.51},  // list panel
		{Kind: KindIcon, Box: image.Rect(700, 310, 730, 340), Score: 0.38},   // row icon
		{Kind: KindIcon, Box: image.Rect(670, 192, 683, 204), Score: 0.26},   // window control
		{Kind: KindIcon, Box: image.Rect(691, 192, 703, 204), Score: 0.38},   // window control
	}
	lines := []TextResult{
		{Box: image.Rect(833, 237, 1085, 276), Text: "macOS Utilities", Score: 0.98},
		{Box: image.Rect(744, 306, 998, 327), Text: "Restore From Time Machine Backup", Score: 0.99},
		{Box: image.Rect(745, 325, 1084, 343), Text: "You have a backup of your system that you want to restore.", Score: 0.99},
		{Box: image.Rect(1170, 553, 1232, 575), Text: " Continue ", Score: 0.99},
	}
	got := MergeElements(interactables, lines)

	keys := make([]string, len(got))
	for i, e := range got {
		require.Equal(t, i, e.ID)
		keys[i] = e.Key
	}
	require.Equal(t, []string{
		"icon@670,192", "icon@691,192",
		"text:macOS Utilities",
		"icon:Restore From Time Machine Backup", "text:Restore From Time Machine Backup",
		"text:You have a backup of your system that you want to restore.", "icon@676,301",
		"button:Continue",
	}, keys)
	require.Equal(t, KindIcon, got[3].Kind, "row icon keeps its kind, gains the adjacent title, not the description below it")

	btn := got[len(got)-1]
	require.Equal(t, KindButton, btn.Kind)
	require.Equal(t, "Continue", btn.Text)
	require.Equal(t, float32(0.75), btn.Score, "detector score, not OCR score")
	require.Equal(t, image.Rect(1160, 554, 1243, 575), btn.Box)

	require.Equal(t, KindIcon, got[6].Kind, "panel is far taller than its rows, so it keeps no label")
	require.Empty(t, got[6].Text)
	require.Equal(t, KindText, got[4].Kind)
	require.Equal(t, float32(0.99), got[4].Score)
}

func TestAssignIDsDisambiguatesRepeats(t *testing.T) {
	els := []Element{
		{Kind: KindButton, Text: "OK", Box: image.Rect(0, 0, 10, 10)},
		{Kind: KindButton, Text: "OK", Box: image.Rect(20, 0, 30, 10)},
		{Kind: KindIcon, Box: image.Rect(40, 0, 50, 10)},
		{Kind: KindIcon, Box: image.Rect(40, 0, 50, 10)},
		{Kind: KindText, Text: "  two   words ", Box: image.Rect(0, 20, 50, 30)},
	}
	assignIDs(els)
	require.Equal(t, "button:OK", els[0].Key)
	require.Equal(t, "button:OK#2", els[1].Key)
	require.Equal(t, "icon@40,0", els[2].Key)
	require.Equal(t, "icon@40,0#2", els[3].Key)
	require.Equal(t, "text:two words", els[4].Key)
	require.Equal(t, 4, els[4].ID)
	require.Empty(t, MergeElements(nil, nil))
}

func TestComposeScreenMeasuresStyleAndTheme(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	fill := func(r image.Rectangle, c color.RGBA) {
		draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
	}
	fill(img.Rect, color.RGBA{40, 42, 48, 255})                          // dark backdrop
	fill(image.Rect(100, 100, 300, 200), color.RGBA{236, 236, 236, 255}) // window
	fill(image.Rect(200, 160, 280, 184), color.RGBA{255, 255, 255, 255}) // disabled button face
	fill(image.Rect(212, 168, 268, 176), color.RGBA{170, 170, 170, 255}) // greyed label
	fill(image.Rect(110, 160, 190, 184), color.RGBA{0, 122, 255, 255})   // default (accent) button
	fill(image.Rect(122, 168, 178, 176), color.RGBA{255, 255, 255, 255}) // its label
	fill(image.Rect(117, 107, 283, 153), color.RGBA{0, 122, 255, 255})   // focus ring around the field
	fill(image.Rect(120, 110, 280, 150), color.RGBA{255, 255, 255, 255}) // field

	s := ComposeScreen(img,
		[]Element{
			{Kind: KindIcon, Box: image.Rect(200, 160, 280, 184), Score: 0.9},
			{Kind: KindIcon, Box: image.Rect(110, 160, 190, 184), Score: 0.8},
			{Kind: KindIcon, Box: image.Rect(120, 110, 280, 150), Score: 0.7},
		},
		[]TextResult{
			{Box: image.Rect(212, 168, 268, 176), Text: "Cancel", Score: 0.9},
			{Box: image.Rect(122, 168, 178, 176), Text: "OK", Score: 0.9},
		})
	require.Equal(t, ThemeDark, s.Theme)
	require.Equal(t, color.RGBA{40, 42, 48, 255}, s.Backdrop)
	require.Equal(t, image.Pt(400, 300), s.Size)

	byKey := map[string]Element{}
	for _, e := range s.Elements {
		byKey[e.Key] = e
	}
	cancel := byKey["button:Cancel"]
	require.True(t, cancel.Style.Disabled, "grey label on white: contrast %.2f", cancel.Style.Contrast)
	require.False(t, cancel.Style.Highlighted)
	require.Equal(t, color.RGBA{255, 255, 255, 255}, cancel.Style.Background)
	require.Equal(t, color.RGBA{170, 170, 170, 255}, cancel.Style.Foreground)

	ok := byKey["button:OK"]
	require.True(t, ok.Style.Highlighted, "accent fill")
	require.False(t, ok.Style.Disabled, "white on blue: contrast %.2f", ok.Style.Contrast)

	field := byKey["icon@120,110"]
	require.True(t, field.Style.Focused)
	require.Equal(t, color.RGBA{0, 122, 255, 255}, field.Style.Ring)
	require.False(t, field.Style.Disabled, "icons never report disabled")
}

func TestDiff(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 100, 80))
	b := image.NewRGBA(image.Rect(0, 0, 100, 80))
	r, f := Diff(a, b)
	require.True(t, r.Empty())
	require.Zero(t, f)
	draw.Draw(b, image.Rect(10, 20, 30, 40), image.White, image.Point{}, draw.Src)
	r, f = Diff(a, b)
	require.Equal(t, image.Rect(10, 20, 30, 40), r)
	require.InDelta(t, 0.05, f, 1e-9)
}

func TestElementJSON(t *testing.T) {
	s := Screen{Size: image.Pt(4, 3), Theme: ThemeDark, Backdrop: color.RGBA{52, 52, 58, 255}, Elements: []Element{{
		ID: 2, Key: "button:OK", Kind: KindButton, Box: image.Rect(1, 2, 3, 4), Score: 0.5, Text: "OK",
		Style: Style{Background: color.RGBA{255, 255, 255, 255}, Foreground: color.RGBA{0, 122, 255, 255}, Contrast: 4.123, Focused: true, Ring: color.RGBA{0, 122, 255, 255}},
	}, {Kind: KindIcon, Box: image.Rect(0, 0, 1, 1)}}}
	data, err := json.Marshal(s)
	require.NoError(t, err)
	require.JSONEq(t, `{"width":4,"height":3,"theme":"dark","backdrop":"#34343a","elements":[
		{"id":2,"key":"button:OK","kind":"button","x0":1,"y0":2,"x1":3,"y1":4,"score":0.5,"text":"OK",
		 "style":{"background":"#ffffff","foreground":"#007aff","contrast":4.12,"disabled":false,"highlighted":false,"focused":true,"ring":"#007aff"}},
		{"id":0,"key":"","kind":"icon","x0":0,"y0":0,"x1":1,"y1":1,"score":0,
		 "style":{"background":"#000000","foreground":"#000000","contrast":0,"disabled":false,"highlighted":false,"focused":false}}]}`, string(data))
}

func TestIconGlyph(t *testing.T) {
	require.True(t, iconGlyph(ppocr.Line{Text: "鬥", Box: image.Rect(0, 0, 26, 22)}))
	require.True(t, iconGlyph(ppocr.Line{Text: " 日 ", Box: image.Rect(0, 0, 13, 14)}))
	require.False(t, iconGlyph(ppocr.Line{Text: "中文字", Box: image.Rect(0, 0, 60, 20)}), "real CJK text")
	require.False(t, iconGlyph(ppocr.Line{Text: "中", Box: image.Rect(0, 0, 60, 20)}), "wide box is not an icon")
	require.False(t, iconGlyph(ppocr.Line{Text: "OK", Box: image.Rect(0, 0, 20, 20)}))
}
