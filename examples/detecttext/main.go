// Command detecttext prints the text lines found on a screenshot, one per
// output line. It is the smallest complete use of the library:
//
//	go run github.com/devcell-sh/go-textshot/examples/detecttext@latest screenshot.png
//
// The first run downloads ONNX Runtime and the PP-OCRv6 models (about
// 40 MB) into the cache; later runs reuse them.
package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"

	"github.com/devcell-sh/go-textshot"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: detecttext screenshot.png")
		os.Exit(2)
	}
	img, err := load(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	eng, err := textshot.New(ctx, textshot.Options{})
	if errors.Is(err, textshot.ErrUnavailable) {
		log.Fatal("no OCR on this build or host: ", err)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer eng.Close()

	results, err := eng.DetectText(ctx, img)
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range textshot.Lines(results) {
		fmt.Println(line)
	}
}

func load(name string) (image.Image, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
