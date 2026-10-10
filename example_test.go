package textshot_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"

	"github.com/devcell-sh/go-textshot"
)

// Example reads the text lines off a screenshot. It needs the ONNX Runtime
// library and the OCR models, which New downloads into the cache on the
// first call, so it has no Output comment and is compiled but not run by
// go test.
func Example() {
	ctx := context.Background()
	eng, err := textshot.New(ctx, textshot.Options{})
	if errors.Is(err, textshot.ErrUnavailable) {
		log.Fatal("no OCR on this build or host:", err)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer eng.Close()

	var img image.Image // decode a screenshot with image.Decode
	results, err := eng.DetectText(ctx, img)
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range textshot.Lines(results) {
		fmt.Println(line)
	}
}

// ExampleEngine_DetectScreen finds the clickable elements on a screenshot
// and what their pixels say about them. The element model (232 MB) is
// downloaded on the first elements call.
func ExampleEngine_DetectScreen() {
	ctx := context.Background()
	eng, err := textshot.New(ctx, textshot.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer eng.Close()

	var img image.Image // decode a screenshot with image.Decode
	screen, err := eng.DetectScreen(ctx, img)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(screen.Theme)
	for _, e := range screen.Elements {
		// e.g. 32 button:Continue (1160,554)-(1243,575) disabled=true
		fmt.Println(e.ID, e.Key, e.Box, "disabled="+fmt.Sprint(e.Style.Disabled))
	}
}

// ExampleMergeElements fuses detector boxes with OCR lines without any
// model: a line inside an interactable labels it (button), an interactable
// without text stays an icon, and a line outside every interactable is
// free text.
func ExampleMergeElements() {
	interactables := []textshot.Element{
		{Box: image.Rect(1160, 554, 1243, 575), Score: 0.75}, // a push button
		{Box: image.Rect(670, 192, 683, 204), Score: 0.26},   // a window control
	}
	lines := []textshot.TextResult{
		{Box: image.Rect(833, 237, 1085, 276), Text: "macOS Utilities", Score: 0.98},
		{Box: image.Rect(1170, 553, 1232, 575), Text: "Continue", Score: 0.99},
	}
	for _, e := range textshot.MergeElements(interactables, lines) {
		fmt.Println(e.ID, e.Key, e.Kind, e.Box)
	}
	// Output:
	// 0 icon@670,192 icon (670,192)-(683,204)
	// 1 text:macOS Utilities text (833,237)-(1085,276)
	// 2 button:Continue button (1160,554)-(1243,575)
}

// ExampleDiff reports how much of the screen changed between two frames,
// for polling until a screen has settled.
func ExampleDiff() {
	prev := image.NewRGBA(image.Rect(0, 0, 64, 64))
	next := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			next.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	box, fraction := textshot.Diff(prev, next)
	fmt.Println(box, fraction)
	// Output:
	// (0,0)-(16,16) 0.0625
}
