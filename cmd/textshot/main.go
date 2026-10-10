// Command textshot prints the text lines or the UI elements found in
// screenshots.
//
//	textshot [flags] image.png [image2.png ...]   # one text line per output line
//	textshot -json image.png                      # JSON array of elements: kind, box, score, text
//	textshot -prefetch                            # download runtime + models, then exit
//
// The ONNX Runtime library and the models are downloaded into the cache
// directory on first use (see -cache, -lib, and the TEXTSHOT_CACHE_DIR and
// TEXTSHOT_ONNXRUNTIME_LIB environment variables). -json additionally runs
// the element detector, which fetches its 232 MB model on first use and
// is several times slower than OCR alone.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"

	"os"
	"os/signal"
	"runtime"
	"strconv"
	"time"

	"github.com/devcell-sh/go-textshot"
)

func main() {
	fs := flag.NewFlagSet("textshot", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "detect UI elements and print a JSON array with kind (button, icon, text), box, score and text")
	prefetch := fs.Bool("prefetch", false, "download the runtime and all models into the cache and exit")
	cache := fs.String("cache", "", "cache directory (default $TEXTSHOT_CACHE_DIR or "+textshot.DefaultCacheDir()+")")
	lib := fs.String("lib", "", "libonnxruntime file or directory (default $TEXTSHOT_ONNXRUNTIME_LIB or cached download)")
	maxSide := fs.Int("max-side", 1920, "downscale images for OCR so the longer side is at most this many px (never upscales)")
	elemMaxSide := fs.Int("element-max-side", 1280, "downscale images for element detection so the longer side is at most this many px (never upscales)")
	threads := fs.Int("threads", runtime.GOMAXPROCS(0), "intra-op inference threads")
	minScore := fs.Float64("min-score", 0.5, "drop text lines with confidence below this (-1 keeps all)")
	elemMinScore := fs.Float64("element-min-score", 0.1, "drop elements with confidence below this")
	quiet := fs.Bool("q", false, "suppress progress logging")
	verbose := fs.Bool("v", false, "debug logging")
	timing := fs.Bool("t", false, "print per-image line/element counts and elapsed time to stderr")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: textshot [flags] image.png [image2.png ...]\n       textshot -prefetch\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[1:])
	if !*prefetch && fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}

	level := slog.LevelInfo
	if *quiet {
		level = slog.LevelError
	}
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	opts := textshot.Options{
		CacheDir: *cache, LibraryPath: *lib, Threads: *threads, Logger: logger,
		MaxSideLen: *maxSide, MinScore: float32(*minScore),
		ElementMaxSideLen: *elemMaxSide, ElementMinScore: float32(*elemMinScore),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *prefetch {
		if err := textshot.Prefetch(ctx, opts); err != nil {
			fatal(err)
		}
		if err := textshot.PrefetchElements(ctx, opts); err != nil {
			fatal(err)
		}
		if fs.NArg() == 0 {
			return
		}
	}

	eng, err := textshot.New(ctx, opts)
	if err != nil {
		fatal(err)
	}
	defer eng.Close()

	var elements []jsonElement
	for _, name := range fs.Args() {
		img, err := load(name)
		if err != nil {
			fatal(err)
		}
		start := time.Now()
		if *asJSON {
			scr, err := eng.DetectScreen(ctx, img)
			if err != nil {
				fatal(fmt.Errorf("%s: %w", name, err))
			}
			if *timing {
				fmt.Fprintf(os.Stderr, "%s: %d elements, %s theme, in %s\n", name, len(scr.Elements), scr.Theme, time.Since(start).Round(time.Millisecond))
			}
			for _, e := range scr.Elements {
				elements = append(elements, jsonElement{File: name, Element: e})
			}
			continue
		}
		results, err := eng.DetectText(ctx, img)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", name, err))
		}
		if *timing {
			fmt.Fprintf(os.Stderr, "%s: %d lines in %s\n", name, len(results), time.Since(start).Round(time.Millisecond))
		}
		for _, r := range results {
			fmt.Println(r.Text)
		}
	}
	if *asJSON {
		if elements == nil {
			elements = []jsonElement{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(elements); err != nil {
			fatal(err)
		}
	}
}

func load(name string) (image.Image, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: decode: %w", name, err)
	}
	return img, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "textshot:", err)
	os.Exit(1)
}

// jsonElement is a textshot.Element tagged with the file it came from.
type jsonElement struct {
	File    string
	Element textshot.Element
}

func (j jsonElement) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(j.Element)
	if err != nil {
		return nil, err
	}
	return append([]byte(`{"file":`+strconv.Quote(j.File)+`,`), data[1:]...), nil
}
