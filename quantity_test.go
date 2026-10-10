package textshot

import (
	"encoding/json"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The first three lines are what DetectText reads off the install-progress
// fixtures in test/testdata (macOS Catalina installer, Windows 11 Setup).
func TestQuantities(t *testing.T) {
	cases := []struct {
		line string
		want []Quantity
	}{
		{"About 3 minutes remaining...", []Quantity{{3, UnitMinute, "3 minutes"}}},
		{"About 20 minutes remaining", []Quantity{{20, UnitMinute, "20 minutes"}}},
		{"• 10% complete", []Quantity{{10, UnitPercent, "10%"}}},
		{"About 1 hour and 5 minutes remaining", []Quantity{{1, UnitHour, "1 hour"}, {5, UnitMinute, "5 minutes"}}},
		{"Less than a minute remaining", []Quantity{{1, UnitMinute, "a minute"}}},
		{"About an hour remaining", []Quantity{{1, UnitHour, "an hour"}}},
		{"Time remaining: 2h 30m", []Quantity{{2, UnitHour, "2h"}, {30, UnitMinute, "30m"}}},
		{"Downloading 45% (12 minutes left)", []Quantity{{45, UnitPercent, "45%"}, {12, UnitMinute, "12 minutes"}}},
		{"1.5 hours, 12,5 percent, 2 days, 30 secs", []Quantity{{1.5, UnitHour, "1.5 hours"}, {12.5, UnitPercent, "12,5 percent"}, {2, UnitDay, "2 days"}, {30, UnitSecond, "30 secs"}}},
		{"Installing Windows 11", nil},
		{"Calculating time remaining…", nil},
		{"Disk Utility 12:34", nil},
		{"", nil},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, Quantities(tc.line), "%q", tc.line)
	}
}

func TestParseDurationAndPercent(t *testing.T) {
	d, ok := ParseDuration("About 3 minutes remaining...")
	require.True(t, ok)
	require.Equal(t, 3*time.Minute, d)

	d, ok = ParseDuration("About 1 hour and 5 minutes remaining")
	require.True(t, ok)
	require.Equal(t, 65*time.Minute, d)

	d, ok = ParseDuration("1.5 hours")
	require.True(t, ok)
	require.Equal(t, 90*time.Minute, d)

	d, ok = ParseDuration("2 days")
	require.True(t, ok)
	require.Equal(t, 48*time.Hour, d)

	_, ok = ParseDuration("• 10% complete")
	require.False(t, ok, "percent is not a duration")

	p, ok := ParsePercent("• 10% complete")
	require.True(t, ok)
	require.Equal(t, 10.0, p)

	_, ok = ParsePercent("About 20 minutes remaining")
	require.False(t, ok)

	_, ok = Quantity{Value: 10, UOM: UnitPercent}.Duration()
	require.False(t, ok)
	require.True(t, UnitDay.IsTime())
	require.False(t, UnitPercent.IsTime())
}

func TestParseQuantity(t *testing.T) {
	cases := []struct {
		line string
		want Quantity
		ok   bool
	}{
		{"About 3 minutes remaining...", Quantity{3, UnitMinute, "3 minutes"}, true},
		{"About 20 minutes remaining", Quantity{20, UnitMinute, "20 minutes"}, true},
		{"• 10% complete", Quantity{10, UnitPercent, "10%"}, true},
		{":**· 30% complete", Quantity{30, UnitPercent, "30%"}, true},
		{"About 1 hour and 5 minutes remaining", Quantity{65, UnitMinute, "1 hour and 5 minutes"}, true},
		{"Time remaining: 2h 30m", Quantity{150, UnitMinute, "2h 30m"}, true},
		{"1.5 hours", Quantity{1.5, UnitHour, "1.5 hours"}, true},
		{"2 days", Quantity{2, UnitDay, "2 days"}, true},
		{"Less than a minute remaining", Quantity{1, UnitMinute, "a minute"}, true},
		{"Downloading 45% (12 minutes left)", Quantity{45, UnitPercent, "45%"}, true},
		{"Installing Windows 11", Quantity{}, false},
		{"Calculating time remaining…", Quantity{}, false},
	}
	for _, tc := range cases {
		q, ok := ParseQuantity(tc.line)
		require.Equal(t, tc.ok, ok, "%q", tc.line)
		require.Equal(t, tc.want, q, "%q", tc.line)
	}
}

func TestProgressElements(t *testing.T) {
	// MergeElements turns a free line with a quantity into KindProgress and
	// fills Value/UOM on any element whose text carries one.
	els := MergeElements(
		[]Element{{Kind: KindIcon, Box: image.Rect(10, 100, 120, 130), Score: 0.6}},
		[]TextResult{
			{Box: image.Rect(10, 10, 200, 30), Text: "• 10% complete", Score: 0.94},
			{Box: image.Rect(20, 108, 110, 124), Text: "Wait 5 seconds", Score: 0.9},
			{Box: image.Rect(10, 50, 200, 70), Text: "Installing Windows 11", Score: 0.96},
			{Box: image.Rect(10, 140, 200, 160), Text: "About 1 hour and 5 minutes remaining", Score: 0.98},
		})
	byKey := map[string]Element{}
	for _, e := range els {
		byKey[e.Key] = e
	}
	require.Equal(t, []string{"progress:• 10% complete", "text:Installing Windows 11", "button:Wait 5 seconds", "progress:About 1 hour and 5 minutes remaining"}, func() (ks []string) {
		for _, e := range els {
			ks = append(ks, e.Key)
		}
		return
	}())
	p := byKey["progress:• 10% complete"]
	require.Equal(t, KindProgress, p.Kind)
	require.Equal(t, 10.0, p.Value)
	require.Equal(t, UnitPercent, p.UOM)
	eta := byKey["progress:About 1 hour and 5 minutes remaining"]
	require.Equal(t, 65.0, eta.Value)
	require.Equal(t, UnitMinute, eta.UOM)
	btn := byKey["button:Wait 5 seconds"]
	require.Equal(t, KindButton, btn.Kind, "a button keeps its kind")
	require.Equal(t, 5.0, btn.Value)
	require.Equal(t, UnitSecond, btn.UOM)
	title := byKey["text:Installing Windows 11"]
	require.Equal(t, KindText, title.Kind)
	require.Equal(t, Unit(""), title.UOM)

	// JSON carries value and uom, omitted when there is no quantity.
	data, err := json.Marshal(p)
	require.NoError(t, err)
	require.Contains(t, string(data), `"kind":"progress"`)
	require.Contains(t, string(data), `"text":"• 10% complete","value":10,"uom":"percent"`)
	data, err = json.Marshal(title)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"uom"`)
	require.NotContains(t, string(data), `"value"`)

	data, err = json.Marshal(TextResult{Box: image.Rect(143, 188, 262, 210), Text: "• 10% complete", Score: 0.94, Value: 10, UOM: UnitPercent})
	require.NoError(t, err)
	require.JSONEq(t, `{"x0":143,"y0":188,"x1":262,"y1":210,"score":0.94,"text":"• 10% complete","value":10,"uom":"percent"}`, string(data))
	data, err = json.Marshal(TextResult{Text: "Installing Windows 11", Score: 0.96})
	require.NoError(t, err)
	require.JSONEq(t, `{"x0":0,"y0":0,"x1":0,"y1":0,"score":0.96,"text":"Installing Windows 11"}`, string(data))

	// A low-confidence detector box that is just the status line again is
	// not a button: the line stays progress and the box is gone.
	els = MergeElements(
		[]Element{
			{Kind: KindIcon, Box: image.Rect(886, 743, 1036, 755), Score: 0.13},  // tight on the glyphs
			{Kind: KindIcon, Box: image.Rect(1160, 554, 1243, 575), Score: 0.75}, // Continue: hit area around the label
		},
		[]TextResult{
			{Box: image.Rect(884, 742, 1040, 759), Text: "About 3 minutes remaining...", Score: 0.96, Value: 3, UOM: UnitMinute},
			{Box: image.Rect(1170, 553, 1232, 575), Text: "Continue", Score: 0.99},
		})
	require.Len(t, els, 2)
	require.Equal(t, "button:Continue", els[0].Key)
	require.Equal(t, "progress:About 3 minutes remaining...", els[1].Key)
	require.Equal(t, 3.0, els[1].Value)

	// ComposeScreen treats progress like text: contrast-based Disabled,
	// never a focus ring.
	img := image.NewRGBA(image.Rect(0, 0, 300, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 300; x++ {
			img.Set(x, y, color.RGBA{1, 88, 155, 255})
		}
	}
	scr := ComposeScreen(img, nil, []TextResult{{Box: image.Rect(10, 10, 120, 30), Text: "10% complete", Score: 0.9, Value: 10, UOM: UnitPercent}})
	require.Len(t, scr.Elements, 1)
	require.Equal(t, KindProgress, scr.Elements[0].Kind)
	require.Equal(t, UnitPercent, scr.Elements[0].UOM)
	require.False(t, scr.Elements[0].Style.Focused)
}
