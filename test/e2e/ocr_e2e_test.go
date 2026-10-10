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
	base := strings.TrimSuffix(fixture, filepath.Ext(fixture))
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

// writeElements stores the elements as JSON and as an annotated
// screenshot (boxes coloured by kind, labelled with the element ID).
func writeElements(t testing.TB, dir, fixture string, img image.Image, els []textshot.Element, elapsed time.Duration) {
	t.Helper()
	base := strings.TrimSuffix(fixture, filepath.Ext(fixture)) + ".elements"
	out := struct {
		Fixture  string             `json:"fixture"`
		Elapsed  string             `json:"elapsed"`
		Elements []textshot.Element `json:"elements"`
	}{fixture, elapsed.String(), els}
	data, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, base+".json"), data, 0o644))

	marks := make([]testutil.Mark, len(els))
	for i, e := range els {
		marks[i] = testutil.Mark{Rect: e.Box, Label: strconv.Itoa(e.ID), Color: kindColors[e.Kind]}
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
		"recovery-loading.png":   {"macOS Recovery", "Examining volumes", "Language Chooser"},
		"recovery-utilities.png": {"macOS Utilities", "Reinstall macOS", "Disk Utility", "Restore From Time Machine Backup"},
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
	results, err := eng.DetectText(context.Background(), loadFixture(t, "recovery-utilities.png"))
	require.NoError(t, err)
	joined := strings.Join(textshot.Lines(results), "\n")
	for _, w := range []string{"macOS Utilities", "Reinstall macOS", "Disk Utility"} {
		require.Contains(t, joined, w)
	}
}

func BenchmarkDetectText1080p(b *testing.B) {
	eng := newEngine(b, textshot.Options{Logger: slog.New(slog.DiscardHandler)})
	img := loadFixture(b, "recovery-utilities.png")
	b.ResetTimer()
	for b.Loop() {
		if _, err := eng.DetectText(context.Background(), img); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDetectText1280(b *testing.B) {
	eng := newEngine(b, textshot.Options{MaxSideLen: 1280, Logger: slog.New(slog.DiscardHandler)})
	img := loadFixture(b, "recovery-utilities.png")
	b.ResetTimer()
	for b.Loop() {
		if _, err := eng.DetectText(context.Background(), img); err != nil {
			b.Fatal(err)
		}
	}
}

// TestRecoveryElements runs the element detector on the Utilities screen
// and checks that the Continue button and the window controls are found
// and that OCR labels land on the right elements.
func TestRecoveryElements(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	img := loadFixture(t, "recovery-utilities.png")
	start := time.Now()
	elements, err := eng.DetectElements(context.Background(), img)
	elapsed := time.Since(start)
	require.NoError(t, err)
	writeElements(t, resultDir, "recovery-utilities.png", img, elements, elapsed)

	var kinds = map[textshot.Kind]int{}
	var continueBtn *textshot.Element
	for i := range elements {
		e := &elements[i]
		kinds[e.Kind]++
		t.Logf("%-6s %v %.2f %q", e.Kind, e.Box, e.Score, e.Text)
		require.True(t, e.Box.In(img.Bounds()), "box %v", e.Box)
		if e.Kind == textshot.KindButton && e.Text == "Continue" {
			continueBtn = e
		}
	}
	t.Logf("%d elements in %s: %v", len(elements), elapsed.Round(time.Millisecond), kinds)
	require.NotNil(t, continueBtn, "Continue button labelled")
	require.Equal(t, "button:Continue", continueBtn.Key)
	require.Equal(t, continueBtn, &elements[continueBtn.ID], "ID is the index in the returned list")
	require.GreaterOrEqual(t, continueBtn.Score, float32(0.5))
	require.True(t, continueBtn.Box.Overlaps(image.Rect(1160, 554, 1243, 575)), "Continue box %v", continueBtn.Box)
	require.GreaterOrEqual(t, kinds[textshot.KindIcon], 3, "window controls")
	require.GreaterOrEqual(t, kinds[textshot.KindText], 5, "list rows stay text, not swallowed by the panel")
}

func BenchmarkDetectInteractables1280(b *testing.B) {
	eng := newEngine(b, textshot.Options{Logger: slog.New(slog.DiscardHandler)})
	img := loadFixture(b, "recovery-utilities.png")
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
