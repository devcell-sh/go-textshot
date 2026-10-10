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

// fixture is one screenshot under test/testdata with everything the suite
// expects from it on each path. Add a fixture here, not a test.
type fixture struct {
	name string
	// text are substrings DetectText must read at the default settings.
	text []string
	// fast are substrings that must survive MaxSideLen 1280; nil means
	// the fixture is not run on the fast path, except to check progress.
	fast []string
	// progress is the quantity the screen's progress line reports, if
	// any: it must come out on TextResult.Value/UOM, survive the fast
	// path and be the only KindProgress element on the element path.
	progress *textshot.Quantity
	// marker is a substring of the progress line, near is where it sits
	// and score the least acceptable confidence for it.
	marker string
	near   image.Rectangle
	score  float32
	// theme is the expected Screen.Theme.
	theme textshot.Theme
	// screen holds the fixture's own element-path assertions.
	screen func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element)
}

var fixtures = []fixture{
	// macOS Recovery: the screens mackit waits for before typing.
	{
		name:  "macos-catalina/recovery-loading.png",
		text:  []string{"macOS Recovery", "Examining volumes", "Language Chooser"},
		theme: textshot.ThemeDark,
	},
	{
		name:  "macos-catalina/recovery-utilities.png",
		text:  []string{"macOS Utilities", "Reinstall macOS", "Disk Utility", "Restore From Time Machine Backup"},
		fast:  []string{"macOS Utilities", "Reinstall macOS", "Disk Utility"},
		theme: textshot.ThemeDark,
		screen: func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element) {
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
		},
	},
	{
		name:  "macos-catalina/recovery-terminal.png",
		text:  []string{"Terminal", "-bash", "3.2#"},
		theme: textshot.ThemeDark,
		screen: func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element) {
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
			require.NotNil(t, withText(scr.Elements, "3.2#"), "shell prompt")
		},
	},

	// macOS installer progress: the black full-screen phase and the window.
	{
		name:     "macos-catalina/install-progress-finishing.png",
		text:     []string{"About 3 minutes remaining"},
		progress: &textshot.Quantity{Value: 3, UOM: textshot.UnitMinute, Text: "3 minutes"},
		marker:   "remaining",
		near:     image.Rect(880, 738, 1040, 760),
		score:    0.9,
		theme:    textshot.ThemeDark,
	},
	{
		name:     "macos-catalina/install-progress-starting.png",
		text:     []string{"Install macOS Catalina", "macOS Catalina will be installed on the disk", "Macintosh HD", "About 20 minutes remaining", "Cancel"},
		progress: &textshot.Quantity{Value: 20, UOM: textshot.UnitMinute, Text: "20 minutes"},
		marker:   "remaining",
		near:     image.Rect(875, 630, 1045, 650),
		score:    0.9,
		theme:    textshot.ThemeDark,
	},

	// Windows: the screens go-winkit keys on.
	{
		name:  "windows/boot-uefi.png",
		text:  []string{"Core", "Firmware"},
		theme: textshot.ThemeDark,
		screen: func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element) {
			require.Equal(t, color.RGBA{0, 0, 0, 255}, scr.Backdrop)
			require.NotNil(t, withText(scr.Elements, "Core"), "TianoCore logo text")
			require.NotNil(t, withText(scr.Elements, "Firmware"), "logo tagline")
		},
	},
	{
		name:  "windows/pe-console.png",
		text:  []string{"winkit-service", "DrvLoad", "completed successfully"},
		theme: textshot.ThemeDark,
		screen: func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element) {
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
		},
	},
	{
		name:  "windows/setup-blank-blue.png",
		theme: textshot.ThemeDark,
		screen: func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element) {
			require.Empty(t, scr.Elements, "nothing on a blank screen")
			require.Greater(t, int(scr.Backdrop.B), int(scr.Backdrop.R), "blue backdrop: %v", scr.Backdrop)
		},
	},
	{
		name:     "windows/installation-progress-10.png",
		text:     []string{"Installing Windows 11", "Your PC will restart several times", "10% complete", "Cancel"},
		progress: &textshot.Quantity{Value: 10, UOM: textshot.UnitPercent, Text: "10%"},
		marker:   "complete",
		near:     image.Rect(160, 190, 260, 210),
		score:    0.9,
		theme:    textshot.ThemeDark,
		screen: func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element) {
			require.Greater(t, int(scr.Backdrop.B), int(scr.Backdrop.R), "blue backdrop: %v", scr.Backdrop)
			require.NotNil(t, withText(scr.Elements, "Installing Windows 11"), "title")
			cancel := byKey["button:Cancel"]
			require.NotNil(t, cancel, "Cancel push button")
			require.False(t, cancel.Style.Disabled, "Cancel is enabled: contrast %.2f", cancel.Style.Contrast)
			require.Empty(t, cancel.UOM, "Cancel carries no quantity")
		},
	},
	{
		// 700x467 web-quality capture; the spinner dots glue onto the
		// line (":**· 30% complete") and pull its mean confidence down.
		name:     "windows/installation-progress-30.png",
		text:     []string{"Installing Windows 10", "Your PC will restart several times", "30% complete"},
		progress: &textshot.Quantity{Value: 30, UOM: textshot.UnitPercent, Text: "30%"},
		marker:   "complete",
		near:     image.Rect(160, 278, 245, 292),
		score:    0.85,
		theme:    textshot.ThemeDark,
		screen: func(t *testing.T, scr *textshot.Screen, byKey map[string]*textshot.Element) {
			require.Greater(t, int(scr.Backdrop.B), int(scr.Backdrop.R), "blue backdrop: %v", scr.Backdrop)
			require.NotNil(t, withText(scr.Elements, "Installing Windows 10"), "title")
		},
	},
}

// TestDetectText OCRs every fixture at the default settings: the labels
// mackit and winkit key on are read, every box is inside the image, the
// MinScore filter holds, and a progress line (if the screen has one)
// carries its value and unit of measure while no other line does.
func TestDetectText(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			img := loadFixture(t, f.name)
			start := time.Now()
			results, err := eng.DetectText(context.Background(), img)
			elapsed := time.Since(start)
			require.NoError(t, err)
			writeResults(t, resultDir, f.name, img, results, elapsed)

			joined := strings.Join(textshot.Lines(results), "\n")
			t.Logf("%d lines in %s:\n%s", len(results), elapsed.Round(time.Millisecond), joined)
			for _, w := range f.text {
				require.Contains(t, joined, w)
			}
			if f.text == nil {
				require.Empty(t, results, "nothing on a blank screen")
			}
			for _, r := range results {
				require.True(t, r.Box.In(img.Bounds()), "box %v", r.Box)
				require.GreaterOrEqual(t, r.Score, float32(0.5), "min-score filter: %q", r.Text)
			}

			var line *textshot.TextResult
			for i := range results {
				if f.marker != "" && strings.Contains(results[i].Text, f.marker) {
					require.Nil(t, line, "one progress line, got also %q", results[i].Text)
					line = &results[i]
				}
			}
			if f.progress == nil {
				require.Nil(t, line)
				for _, r := range results {
					require.Empty(t, r.UOM, "stray quantity on %q", r.Text)
				}
				return
			}
			require.NotNil(t, line, "progress line")
			require.GreaterOrEqual(t, line.Score, f.score, "progress line confidence: %q", line.Text)
			require.True(t, line.Box.Overlaps(f.near), "progress line box %v, want near %v", line.Box, f.near)
			require.Equal(t, f.progress.Value, line.Value, "value of %q", line.Text)
			require.Equal(t, f.progress.UOM, line.UOM, "uom of %q", line.Text)
			q, ok := textshot.ParseQuantity(line.Text)
			require.True(t, ok)
			require.Equal(t, *f.progress, q, "quantity in %q", line.Text)
			for _, r := range results {
				if r.Text != line.Text {
					require.Empty(t, r.UOM, "stray quantity on %q", r.Text)
				}
			}
		})
	}
}

// TestDetectText1280 checks the downscaled fast path on the fixtures that
// declare what must survive it: UI labels and 13 px progress lines.
func TestDetectText1280(t *testing.T) {
	eng := newEngine(t, textshot.Options{MaxSideLen: 1280})
	for _, f := range fixtures {
		if f.fast == nil && f.progress == nil {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			results, err := eng.DetectText(context.Background(), loadFixture(t, f.name))
			require.NoError(t, err)
			joined := strings.Join(textshot.Lines(results), "\n")
			for _, w := range f.fast {
				require.Contains(t, joined, w)
			}
			if f.progress != nil {
				q, ok := textshot.ParseQuantity(joined)
				require.True(t, ok, "no quantity in:\n%s", joined)
				require.Equal(t, f.progress.Value, q.Value)
				require.Equal(t, f.progress.UOM, q.UOM)
			}
		})
	}
}

// TestDetectScreen runs the element path on every fixture: theme, the
// progress element with its value and unit of measure (exactly one, and
// none on screens without a progress line), then the fixture's own checks
// on buttons, icons, window controls and style.
func TestDetectScreen(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			scr, byKey := detectScreen(t, eng, resultDir, f.name)
			require.Equal(t, f.theme, scr.Theme)

			var progress []*textshot.Element
			for i := range scr.Elements {
				e := &scr.Elements[i]
				if e.Kind == textshot.KindProgress || e.UOM != "" {
					progress = append(progress, e)
				}
			}
			if f.progress == nil {
				require.Empty(t, progress, "no quantity on this screen")
			} else {
				require.Len(t, progress, 1, "exactly one element carries a quantity")
				p := progress[0]
				require.Equal(t, textshot.KindProgress, p.Kind, "key %q", p.Key)
				require.True(t, strings.HasPrefix(p.Key, "progress:"), "key %q", p.Key)
				require.Contains(t, p.Text, f.marker)
				require.True(t, p.Box.Overlaps(f.near), "progress box %v, want near %v", p.Box, f.near)
				require.Equal(t, f.progress.Value, p.Value)
				require.Equal(t, f.progress.UOM, p.UOM)
				require.False(t, p.Style.Disabled, "progress text is readable: contrast %.2f", p.Style.Contrast)
				require.False(t, p.Style.Focused, "text never has a focus ring")
			}
			if f.screen != nil {
				f.screen(t, scr, byKey)
			}
		})
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
