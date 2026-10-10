# go-textshot

Text and UI elements from screenshots, in Go. Runs the PP-OCRv6 detection
and recognition models, and on demand the OmniParser `icon_detect_v3`
element detector, through ONNX Runtime; the runtime library and the models
are downloaded on first use, pinned by SHA-256, and cached. No system
packages.

```go
eng, err := textshot.New(ctx, textshot.Options{})
if err != nil { ... }           // errors.Is(err, textshot.ErrUnavailable): no OCR on this build/host
defer eng.Close()

results, err := eng.DetectText(ctx, img)   // []TextResult{Box, Text, Score}, reading order
for _, line := range textshot.Lines(results) {
    fmt.Println(line)
}

elements, err := eng.DetectElements(ctx, img)   // []Element{ID, Key, Kind, Box, Score, Text}
for _, e := range elements {
    fmt.Println(e.ID, e.Key, e.Box)             // 32 button:Continue (1160,554)-(1243,575)
}
```

`DetectElements` fuses OCR with the element detector: an interactable
region with text inside is a `button` (push button, menu item, list row,
link), one without is an `icon` (toolbar icon, window control, checkbox),
and text outside any interactable is `text`. Each element carries two
identifiers: `ID`, its index in reading order for that frame (a
set-of-marks number), and `Key`, a role+name style locator
(`button:Continue`, `text:Disk Utility`, `icon@700,310`, `#2` on repeats)
that stays the same across frames while the layout does not move. Find an
element by `Key`, confirm it by `Box`, then act. `DetectInteractables`
runs only the detector, and `MergeElements` does the fusion for callers
that already have OCR results. The element model (232 MB) is downloaded
and loaded on the first elements call, so engines that only read text
never pay for it.

```sh
go install github.com/devcell-sh/go-textshot/cmd/textshot@latest
textshot screenshot.png            # one text line per output line
textshot -json screenshot.png      # JSON array of elements: id, key, kind, box, score, text
textshot -prefetch                 # warm the cache (runtime + all models) without running
```

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

## Development

```sh
task test           # hermetic
task test:e2e       # TEXTSHOT_E2E=1, downloads runtime + models once
task bench
task models:export  # re-export icon_detect_v3 to ONNX (needs uv; one-off, see below)
task models:check   # verify dist/models/*.sha256 are pinned in models.go
```

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
