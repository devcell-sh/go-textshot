// Package e2e runs the OCR pipeline against real models. These tests
// download ONNX Runtime and the PP-OCRv6 models (~40 MB) into the cache on
// first run and are gated by TEXTSHOT_E2E=1.
//
//	TEXTSHOT_E2E=1 go test -v ./test/e2e/
//	TEXTSHOT_E2E=1 go test -run XXX -bench . -benchtime 5x ./test/e2e/
package e2e

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-textshot"
	"github.com/devcell-sh/go-textshot/internal/testutil"
)

func skipUnlessE2E(t testing.TB) {
	if os.Getenv("TEXTSHOT_E2E") != "1" {
		t.Skip("set TEXTSHOT_E2E=1 to run model-backed tests")
	}
}

func loadFixture(t testing.TB, name string) image.Image {
	t.Helper()
	f, err := os.Open(testutil.Testdata(name))
	require.NoError(t, err)
	defer f.Close()
	img, _, err := image.Decode(f)
	require.NoError(t, err)
	return img
}

func newEngine(t testing.TB, o textshot.Options) *textshot.Engine {
	t.Helper()
	skipUnlessE2E(t)
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	eng, err := textshot.New(context.Background(), o)
	require.NoError(t, err)
	t.Cleanup(func() { eng.Close() })
	return eng
}

// writeResults stores the recognized lines next to the fixture name under
// the test's result dir, as plain text, JSON and an annotated screenshot.
func writeResults(t testing.TB, dir, fixture string, img image.Image, rs []textshot.TextResult, elapsed time.Duration) {
	t.Helper()
	base := resultBase(fixture)
	require.NoError(t, os.WriteFile(filepath.Join(dir, base+".txt"), []byte(strings.Join(textshot.Lines(rs), "\n")+"\n"), 0o644))
	out := struct {
		Fixture string                `json:"fixture"`
		Elapsed string                `json:"elapsed"`
		Lines   []textshot.TextResult `json:"lines"`
	}{fixture, elapsed.String(), rs}
	data, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, base+".json"), data, 0o644))

	marks := make([]testutil.Mark, len(rs))
	for i, r := range rs {
		marks[i] = testutil.Mark{Rect: r.Box, Label: strconv.Itoa(i), Color: kindColors[textshot.KindText]}
	}
	require.NoError(t, testutil.WritePNG(filepath.Join(dir, base+".png"), testutil.Annotate(img, marks)))
}

var kindColors = map[textshot.Kind]color.RGBA{
	textshot.KindButton: {R: 0, G: 160, B: 60, A: 255},
	textshot.KindIcon:   {R: 230, G: 120, B: 0, A: 255},
	textshot.KindText:   {R: 30, G: 90, B: 220, A: 255},
}

// writeElements stores the screen (theme, backdrop, elements) as JSON and as an annotated
// screenshot (boxes coloured by kind, labelled with the element ID).
func writeElements(t testing.TB, dir, fixture string, img image.Image, scr *textshot.Screen, elapsed time.Duration) {
	els := scr.Elements
	t.Helper()
	base := resultBase(fixture) + ".elements"
	out := struct {
		Fixture string           `json:"fixture"`
		Elapsed string           `json:"elapsed"`
		Screen  *textshot.Screen `json:"screen"`
	}{fixture, elapsed.String(), scr}
	data, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, base+".json"), data, 0o644))

	marks := make([]testutil.Mark, len(els))
	for i, e := range els {
		label := strconv.Itoa(e.ID)
		if e.Style.Disabled {
			label += " off"
		}
		if e.Style.Focused {
			label += " focus"
		}
		marks[i] = testutil.Mark{Rect: e.Box, Label: label, Color: kindColors[e.Kind]}
	}
	require.NoError(t, testutil.WritePNG(filepath.Join(dir, base+".png"), testutil.Annotate(img, marks)))
}

// TestRecoveryScreens OCRs the two macOS Recovery screenshots and checks
// that the labels mackit keys on are recognized at the default settings.
func TestRecoveryScreens(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	cases := map[string][]string{
		"macos-catalina/recovery-loading.png":   {"macOS Recovery", "Examining volumes", "Language Chooser"},
		"macos-catalina/recovery-utilities.png": {"macOS Utilities", "Reinstall macOS", "Disk Utility", "Restore From Time Machine Backup"},
		"macos-catalina/recovery-terminal.png":  {"Terminal", "-bash", "3.2#"},
	}
	for name, wants := range cases {
		t.Run(name, func(t *testing.T) {
			img := loadFixture(t, name)
			start := time.Now()
			results, err := eng.DetectText(context.Background(), img)
			elapsed := time.Since(start)
			require.NoError(t, err)
			writeResults(t, resultDir, name, img, results, elapsed)

			joined := strings.Join(textshot.Lines(results), "\n")
			t.Logf("%d lines in %s:\n%s", len(results), elapsed.Round(time.Millisecond), joined)
			for _, w := range wants {
				require.Contains(t, joined, w)
			}
			for _, r := range results {
				require.True(t, r.Box.In(img.Bounds()), "box %v", r.Box)
				require.GreaterOrEqual(t, r.Score, float32(0.5), "min-score filter: %q", r.Text)
			}
		})
	}
}

// TestMaxSideLenStillReadsLabels checks that the downscaled fast path keeps
// the UI labels readable.
func TestMaxSideLenStillReadsLabels(t *testing.T) {
	eng := newEngine(t, textshot.Options{MaxSideLen: 1280})
	results, err := eng.DetectText(context.Background(), loadFixture(t, "macos-catalina/recovery-utilities.png"))
	require.NoError(t, err)
	joined := strings.Join(textshot.Lines(results), "\n")
	for _, w := range []string{"macOS Utilities", "Reinstall macOS", "Disk Utility"} {
		require.Contains(t, joined, w)
	}
}

func BenchmarkDetectText1080p(b *testing.B) {
	eng := newEngine(b, textshot.Options{Logger: slog.New(slog.DiscardHandler)})
	img := loadFixture(b, "macos-catalina/recovery-utilities.png")
	b.ResetTimer()
	for b.Loop() {
		if _, err := eng.DetectText(context.Background(), img); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDetectText1280(b *testing.B) {
	eng := newEngine(b, textshot.Options{MaxSideLen: 1280, Logger: slog.New(slog.DiscardHandler)})
	img := loadFixture(b, "macos-catalina/recovery-utilities.png")
	b.ResetTimer()
	for b.Loop() {
		if _, err := eng.DetectText(context.Background(), img); err != nil {
			b.Fatal(err)
		}
	}
}

// detectScreen runs DetectScreen on a fixture, stores the results, logs
// every element and returns the screen plus a key -> element index.
func detectScreen(t *testing.T, eng *textshot.Engine, resultDir, name string) (*textshot.Screen, map[string]*textshot.Element) {
	t.Helper()
	img := loadFixture(t, name)
	start := time.Now()
	scr, err := eng.DetectScreen(context.Background(), img)
	elapsed := time.Since(start)
	require.NoError(t, err)
	writeElements(t, resultDir, name, img, scr, elapsed)

	kinds := map[textshot.Kind]int{}
	byKey := map[string]*textshot.Element{}
	for i := range scr.Elements {
		e := &scr.Elements[i]
		kinds[e.Kind]++
		byKey[e.Key] = e
		t.Logf("%2d %-6s %v %.2f %q bg=%v fg=%v contrast=%.1f disabled=%v highlighted=%v focused=%v", e.ID, e.Kind, e.Box, e.Score, e.Text, e.Style.Background, e.Style.Foreground, e.Style.Contrast, e.Style.Disabled, e.Style.Highlighted, e.Style.Focused)
		require.True(t, e.Box.In(img.Bounds()), "box %v", e.Box)
		require.Equal(t, i, e.ID, "ID is the index in the returned list")
	}
	t.Logf("%d elements in %s: %v, theme %s, backdrop %v", len(scr.Elements), elapsed.Round(time.Millisecond), kinds, scr.Theme, scr.Backdrop)
	return scr, byKey
}

// TestRecoveryElements runs the element detector on the Recovery screens
// and checks the elements mackit keys on: buttons with their labels and
// state, window controls, row icons with their titles.
func TestRecoveryElements(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	t.Run("macos-catalina/recovery-utilities.png", func(t *testing.T) {
		scr, byKey := detectScreen(t, eng, resultDir, "macos-catalina/recovery-utilities.png")
		require.Equal(t, textshot.ThemeDark, scr.Theme)

		btn := byKey["button:Continue"]
		require.NotNil(t, btn, "Continue button labelled")
		require.GreaterOrEqual(t, btn.Score, float32(0.5))
		require.True(t, btn.Box.Overlaps(image.Rect(1160, 554, 1243, 575)), "Continue box %v", btn.Box)
		require.True(t, btn.Style.Disabled, "greyed Continue: contrast %.2f", btn.Style.Contrast)

		for _, k := range []string{"icon:Restore From Time Machine Backup", "icon:Reinstall macOS", "icon:Get Help Online", "icon:Disk Utility"} {
			require.NotNil(t, byKey[k], "row icon labelled by its title: %s", k)
		}
		for _, k := range []string{"text:Restore From Time Machine Backup", "text:Disk Utility", "text:macOS Utilities"} {
			require.NotNil(t, byKey[k], "rows stay text, not swallowed by the panel: %s", k)
		}
		require.NotNil(t, byKey["icon@670,192"], "close window control")
		require.Equal(t, 1, countNear(scr.Elements, image.Rect(47, 0, 168, 22)), "one box per menu item, no nested duplicates")
		require.Equal(t, 1, countNear(scr.Elements, image.Rect(168, 0, 209, 22)), "File menu: tight and loose boxes collapse to one")

		panel := byKey["icon@676,301"]
		if panel == nil {
			panel = nearest(scr.Elements, image.Pt(680, 305))
		}
		require.NotNil(t, panel, "list panel")
		require.True(t, panel.Style.Focused, "the list has the 4 px blue focus ring")
		require.Greater(t, int(panel.Style.Ring.B), int(panel.Style.Ring.R), "blue ring: %v", panel.Style.Ring)
		safari := byKey["icon:Get Help Online"]
		require.NotNil(t, safari)
		require.False(t, safari.Style.Focused, "a blue icon is a fill, not a ring")
	})

	t.Run("macos-catalina/recovery-terminal.png", func(t *testing.T) {
		scr, byKey := detectScreen(t, eng, resultDir, "macos-catalina/recovery-terminal.png")
		require.Equal(t, textshot.ThemeDark, scr.Theme)
		title := withText(scr.Elements, "-bash —")
		require.NotNil(t, title, "window title")
		require.False(t, title.Style.Disabled)
		closeBtn := byKey["icon@47,66"]
		if closeBtn == nil {
			closeBtn = nearest(scr.Elements, image.Pt(53, 72))
		}
		require.NotNil(t, closeBtn, "close window control")
		require.Less(t, closeBtn.Box.Min.X, 60)
		require.Greater(t, int(closeBtn.Style.Background.R), 200, "red close control, window is active: %v", closeBtn.Style.Background)
		prompt := withText(scr.Elements, "3.2#")
		require.NotNil(t, prompt, "shell prompt")
	})
}

// countNear counts non-text elements whose box overlaps r by more than half of
// the smaller area.
func countNear(els []textshot.Element, r image.Rectangle) int {
	n := 0
	for _, e := range els {
		inter := e.Box.Intersect(r)
		if inter.Empty() {
			continue
		}
		if e.Kind != textshot.KindText && 2*inter.Dx()*inter.Dy() > min(e.Box.Dx()*e.Box.Dy(), r.Dx()*r.Dy()) {
			n++
		}
	}
	return n
}

// nearest returns the icon whose box contains p, or nil.
func nearest(els []textshot.Element, p image.Point) *textshot.Element {
	for i := range els {
		if els[i].Kind == textshot.KindIcon && p.In(els[i].Box) {
			return &els[i]
		}
	}
	return nil
}

func BenchmarkDetectInteractables1280(b *testing.B) {
	eng := newEngine(b, textshot.Options{Logger: slog.New(slog.DiscardHandler)})
	img := loadFixture(b, "macos-catalina/recovery-utilities.png")
	if _, err := eng.DetectInteractables(context.Background(), img); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := eng.DetectInteractables(context.Background(), img); err != nil {
			b.Fatal(err)
		}
	}
}

// withText returns the first element whose text contains s, or nil.
func withText(els []textshot.Element, s string) *textshot.Element {
	for i := range els {
		if strings.Contains(els[i].Text, s) {
			return &els[i]
		}
	}
	return nil
}

// resultBase flattens a fixture path into a result file stem:
// "macos-catalina/recovery-utilities.png" -> "macos-catalina-recovery-utilities".
func resultBase(fixture string) string {
	return strings.ReplaceAll(strings.TrimSuffix(fixture, filepath.Ext(fixture)), "/", "-")
}

// TestWindowsScreens covers the 800x600 Windows boot and WinPE fixtures
// go-winkit keys on, including a blank screen that must yield nothing.
func TestWindowsScreens(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	t.Run("windows/boot-uefi.png", func(t *testing.T) {
		scr, _ := detectScreen(t, eng, resultDir, "windows/boot-uefi.png")
		require.Equal(t, textshot.ThemeDark, scr.Theme)
		require.Equal(t, color.RGBA{0, 0, 0, 255}, scr.Backdrop)
		require.NotNil(t, withText(scr.Elements, "Core"), "TianoCore logo text")
		require.NotNil(t, withText(scr.Elements, "Firmware"), "logo tagline")
	})

	t.Run("windows/pe-console.png", func(t *testing.T) {
		scr, _ := detectScreen(t, eng, resultDir, "windows/pe-console.png")
		require.Equal(t, textshot.ThemeDark, scr.Theme)
		require.NotNil(t, withText(scr.Elements, "winkit-service"), "console title")
		require.NotNil(t, withText(scr.Elements, "DrvLoad"), "log line")
		require.NotNil(t, withText(scr.Elements, "completed successfully"), "log line")
		closeBtn := nearest(scr.Elements, image.Pt(773, 71))
		if closeBtn == nil {
			for i := range scr.Elements {
				if scr.Elements[i].Kind != textshot.KindText && image.Pt(773, 71).In(scr.Elements[i].Box) {
					closeBtn = &scr.Elements[i]
				}
			}
		}
		require.NotNil(t, closeBtn, "close window control")
		bg := closeBtn.Style.Background
		require.Greater(t, int(bg.R), int(bg.G)+int(bg.B), "red close control: %v", bg)
		lines := 0
		for _, e := range scr.Elements {
			if e.Kind == textshot.KindText {
				lines++
			}
		}
		require.GreaterOrEqual(t, lines, 15, "console log lines")
	})

	t.Run("windows/setup-blank-blue.png", func(t *testing.T) {
		scr, _ := detectScreen(t, eng, resultDir, "windows/setup-blank-blue.png")
		require.Equal(t, textshot.ThemeDark, scr.Theme)
		require.Empty(t, scr.Elements, "nothing on a blank screen")
		require.Greater(t, int(scr.Backdrop.B), int(scr.Backdrop.R), "blue backdrop: %v", scr.Backdrop)
	})
}
