package e2e

import (
	"context"
	"encoding/json"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-textshot"
	"github.com/devcell-sh/go-textshot/internal/testutil"
)

// TestFilelistBoxes runs detection + OCR on two single-line crops from a
// macOS Finder file list (one filename, one date) and writes annotated
// images and JSON for inspecting bounding-box padding and text accuracy.
func TestFilelistBoxes(t *testing.T) {
	eng := newEngine(t, textshot.Options{})
	dir := testutil.ResultDir(t)
	t.Logf("results: %s", dir)

	cases := []struct {
		fixture string
		expect  string
	}{
		{"macos-finder/macos-filelist-filename.jpg", "Applications"},
		{"macos-finder/macos-filelist-date.jpg", "Aug 8, 2020 at 10:00"},
		{"macos-finder/macos-filelist-panel.png", "Applications"},
	}

	for _, tc := range cases {
		name := resultBase(tc.fixture)
		t.Run(name, func(t *testing.T) {
			img := loadFixture(t, tc.fixture)
			t.Logf("image: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())

			// DetectText: OCR with bounding boxes.
			start := time.Now()
			results, err := eng.DetectText(context.Background(), img)
			elapsed := time.Since(start)
			require.NoError(t, err)

			writeResults(t, dir, tc.fixture, img, results, elapsed)

			t.Logf("%d lines in %s", len(results), elapsed.Round(time.Millisecond))
			for i, r := range results {
				t.Logf("  [%d] %q  score=%.3f  box=%v  (w=%d h=%d)",
					i, r.Text, r.Score, r.Box,
					r.Box.Dx(), r.Box.Dy())
				// Log padding: how much box extends beyond a tight fit.
				t.Logf("       box-left=%d box-top=%d box-right=%d box-bottom=%d (image %dx%d)",
					r.Box.Min.X, r.Box.Min.Y,
					img.Bounds().Max.X-r.Box.Max.X, img.Bounds().Max.Y-r.Box.Max.Y,
					img.Bounds().Dx(), img.Bounds().Dy())
			}
			require.NotEmpty(t, results, "should detect text")

			// ComposeScreen: element path with grouping + style.
			start2 := time.Now()
			scr, err := eng.DetectScreen(context.Background(), img)
			elapsed2 := time.Since(start2)
			require.NoError(t, err)
			writeElements(t, dir, tc.fixture, img, scr, elapsed2)

			t.Logf("%d elements in %s", len(scr.Elements), elapsed2.Round(time.Millisecond))
			for _, e := range scr.Elements {
				t.Logf("  [%d] kind=%s %q  box=%v  group=%d  fg=%v bg=%v",
					e.ID, e.Kind, e.Text, e.Box, e.Group, e.Style.Foreground, e.Style.Background)
			}

			// Write combined report.
			report := struct {
				Fixture  string             `json:"fixture"`
				ImageW   int                `json:"imageWidth"`
				ImageH   int                `json:"imageHeight"`
				OCR      []textshot.TextResult `json:"ocr"`
				Elements []textshot.Element `json:"elements"`
			}{tc.fixture, img.Bounds().Dx(), img.Bounds().Dy(), results, scr.Elements}
			data, err := json.MarshalIndent(report, "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, name+"-report.json"), data, 0o644))
		})
	}
}
