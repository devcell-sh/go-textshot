# go-textshot

Read the text and the clickable elements on a screenshot so a Go program
can wait for a known screen, then click or type. It is the screen-reading
layer behind GUI-automation tools such as
[go-mackit](https://github.com/devcell-sh/go-mackit), for the cases where
there is no accessibility tree or DOM to ask: VM consoles, recovery and
installer screens, remote desktops.

Under the hood it runs the PP-OCRv6 detection and recognition models, and
on demand the OmniParser `icon_detect_v3` element detector, through ONNX
Runtime. The runtime library and the models are downloaded on first use,
pinned by SHA-256, and cached. No system packages: only a C compiler at
build time.

## Quick start

```sh
go get github.com/devcell-sh/go-textshot
```

```go
package main

import (
	"context"
	"fmt"
	"image"
	_ "image/png"
	"log"
	"os"

	"github.com/devcell-sh/go-textshot"
)

func main() {
	f, err := os.Open("screenshot.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	eng, err := textshot.New(ctx, textshot.Options{}) // first call downloads ~40 MB into the cache
	if err != nil {
		log.Fatal(err) // errors.Is(err, textshot.ErrUnavailable): no OCR on this build/host
	}
	defer eng.Close()

	results, err := eng.DetectText(ctx, img) // []TextResult{Box, Text, Score}, reading order
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range textshot.Lines(results) {
		fmt.Println(line)
	}
}
```

The same program is in [`examples/detecttext`](examples/detecttext/main.go).
Elements and pixel state come from one more call:

```go
screen, err := eng.DetectScreen(ctx, img)       // Screen{Size, Theme, Backdrop, Elements}
for _, e := range screen.Elements {             // Element{ID, Key, Kind, Box, Score, Text, Style}
    fmt.Println(e.ID, e.Key, e.Box, e.Style.Disabled)   // 32 button:Continue (1160,554)-(1243,575) true
}

changed, fraction := textshot.Diff(prev, img)   // what moved since the last frame
```

The CLI does the same from a shell:

```sh
go install github.com/devcell-sh/go-textshot/cmd/textshot@latest
textshot screenshot.png            # one text line per output line
textshot -json screenshot.png      # JSON array of elements: id, key, kind, box, score, text, style
textshot -prefetch                 # warm the cache (runtime + all models) without running
```

## What you get

`DetectElements` fuses OCR with the element detector: an interactable
region with text inside is a `button` (push button, menu item, list row,
link), one without is an `icon` (toolbar icon, window control, checkbox),
and text outside any interactable is `text`. An icon with a title right
next to it on the same row (a list row's icon) takes that title as its
`Text` while staying an `icon`. Each element carries two
identifiers: `ID`, its index in reading order for that frame (a
set-of-marks number), and `Key`, a role+name style locator
(`button:Continue`, `text:Disk Utility`, `icon:Disk Utility` for the
row's icon, `icon@700,310` when unlabelled, `#2` on repeats)
that stays the same across frames while the layout does not move. Find an
element by `Key`, confirm it by `Box`, then act. `DetectInteractables`
runs only the detector, and `MergeElements` does the fusion for callers
that already have OCR results. The element model (232 MB) is downloaded
and loaded on the first elements call, so engines that only read text
never pay for it.

`DetectScreen` adds what the pixels say, with no model: each element's
`Style` (measured background and foreground colours, WCAG contrast,
`Disabled` for greyed-out buttons and text, `Highlighted` for an accent
fill such as a selected row or default button, `Focused` with the ring
colour when a saturated outline surrounds it) and the screen's `Theme`
(dark or light) and `Backdrop` colour. `ComposeScreen` is the pure part
for callers with their own detector output. `Diff` compares two frames
and returns the changed region and fraction, for waiting until a screen
settles. Colours are measurements, not design tokens: exact hex values,
font families and point sizes are not recoverable from a screenshot.

## Options

| Option | Env | Default |
|--------|-----|---------|
| `CacheDir` | `TEXTSHOT_CACHE_DIR` | `os.UserCacheDir()/textshot` |
| `LibraryPath` | `TEXTSHOT_ONNXRUNTIME_LIB` | downloaded into the cache |
| `MaxSideLen` | | 1920 (downscale only) |
| `Threads` | | `runtime.GOMAXPROCS(0)` |
| `MinScore` | | 0.5 |
| `ElementMaxSideLen` | | 1280 (downscale only) |
| `ElementMinScore` | | 0.1 |
| `Logger` | | discard |

Platforms: linux/amd64, linux/arm64, darwin/arm64 (ONNX Runtime 1.30.0),
darwin/amd64 (1.23.2, the last Intel macOS release). Other platforms can
point `LibraryPath` at a local `libonnxruntime`.

Per 1080p frame on two CPUs: OCR about 650 ms at full resolution, 430 ms
at `MaxSideLen: 1280`; element detection about 4 s at `ElementMaxSideLen:
1280` (the YOLOv9-E detector is heavy, so call it on demand rather than in
a polling loop). The element session tries the platform GPU execution
provider (CoreML on macOS, CUDA elsewhere) and falls back to the CPU when
the loaded runtime does not have it.

## Comparison

| Alternative | Use it when | textshot instead when |
|-------------|-------------|-----------------------|
| Accessibility APIs, [chromedp](https://github.com/chromedp/chromedp), [rod](https://github.com/go-rod/rod), Playwright | You control the app or browser and can ask it for its element tree: exact roles, names and states | All you have is pixels: a VM console, a boot or recovery screen, a remote desktop |
| [gosseract](https://github.com/otiai10/gosseract) / Tesseract | Document OCR with many languages, and a system `libtesseract` is acceptable | Screen text at UI sizes, no system packages, and buttons and icons fused with the text |
| PaddleOCR, RapidOCR (Python) | A Python pipeline that already has the same PP-OCR models | A Go program, with the models downloaded and pinned by digest at first use |
| Cloud OCR (Google Vision, AWS Textract, Azure AI Vision) | Highest accuracy on documents and handwriting, no local compute | Offline, no per-call cost, and screenshots never leave the machine |
| [OmniParser](https://github.com/microsoft/OmniParser) (Python) | A GPU and a captioning model for icon descriptions | The same `icon_detect_v3` detector on CPU, fused with OCR into role+name keys, from Go |

## Limitations

Everything is derived from pixels, so some things are out of reach and
some are known weak spots. Status and the package to change:

| Limitation | Status | Where |
|------------|--------|-------|
| A macOS menu bar OCRs as one line ("Terminal Shell Edit View Window Help"); the detector's per-item boxes carry no text | open, the det probability map saturates across the bar at every threshold tried | `internal/ppocr` |
| 11 px monospace text can lose a thin leading glyph (`-bash-3.2#` reads as `ash-3.2#`) | open | `internal/ppocr` |
| Logos read as elements (the TianoCore logo is a `Disabled` button); low-score icons on terminal output are detector noise | by design, raise `ElementMinScore` to 0.25 if they matter | `internal/uidet` |
| Window and panel hierarchy | out of scope for now: a flood-fill region detector was tried and dropped, nested panels break the fill-ratio rule | `internal/screen` |
| Mouse cursor | out of scope: the template depends on OS, theme and scale | |
| Font family and size, exact design-token colours | not recoverable from a screenshot; `Style` reports measured colours only | |
| Checked state, element classes beyond button/icon/text | out of scope: needs a multi-class model | `internal/uidet` |

The rules and thresholds behind these are in the pipeline section of
[AGENTS.md](AGENTS.md).

## Development

```sh
task test           # hermetic
task test:e2e       # TEXTSHOT_E2E=1, downloads runtime + models once
task bench
task models:export  # re-export icon_detect_v3 to ONNX (needs uv; one-off, see below)
task models:check   # verify dist/models/*.sha256 are pinned in models.go
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, where things live and
the design rules, and [CHANGELOG.md](CHANGELOG.md) for what changed.
CI runs the hermetic tests on every push and pull request.

The element model ships upstream only as TorchScript. `task models:export`
converts it with a pinned PyTorch environment (`tools/export-icon-detect`,
driven by `uv`) and writes `dist/models/icon_detect_v3.onnx` plus its
SHA-256. Pushing a `models-v*` tag runs the same export in CI
(`.github/workflows/release.yaml`), checks the digest against `models.go`,
and publishes the file as a release asset, which is the URL `ElementModel`
downloads from.

See [AGENTS.md](AGENTS.md) for the package map, pinned artifacts and cache
layout.

## License

Apache-2.0. The embedded `ppocrv6_dict.txt` and the PP-OCRv6 models are
Apache-2.0 from PaddlePaddle/PaddleOCR. The pipeline is a port of
[lib-x/ppocr-v6-go](https://github.com/lib-x/ppocr-v6-go). The element
detector is `icon_detect_v3` from
[microsoft/OmniParser-v2.0](https://huggingface.co/microsoft/OmniParser-v2.0),
MIT (the YOLOv9-E checkpoint; the older YOLOv8 `icon_detect` is AGPL and
is not used).
