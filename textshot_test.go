package textshot

import (
	"image"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
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
		"icon@700,310", "text:Restore From Time Machine Backup", "icon@676,301",
		"button:Continue",
	}, keys)

	btn := got[len(got)-1]
	require.Equal(t, KindButton, btn.Kind)
	require.Equal(t, "Continue", btn.Text)
	require.Equal(t, float32(0.75), btn.Score, "detector score, not OCR score")
	require.Equal(t, image.Rect(1160, 554, 1243, 575), btn.Box)

	require.Equal(t, KindIcon, got[5].Kind, "panel is far taller than its rows, so it keeps no label")
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
