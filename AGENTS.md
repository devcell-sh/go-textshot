# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

textshot extracts text lines and UI elements from screenshots. It is a Go
library plus a small CLI that runs the PP-OCRv6 detection and recognition
models, and on demand the OmniParser `icon_detect_v3` element detector,
through ONNX Runtime. The runtime shared library and the models are
downloaded on first use, verified against pinned SHA-256 digests, and
cached. It is the screen-reading layer behind
[go-mackit](https://github.com/devcell-sh/go-mackit) (waiting for a known
macOS screen before sending keystrokes) and is meant to be reusable by
go-winkit.

No system packages are required: the ONNX Runtime Go binding is a thin cgo
shim that dlopens `libonnxruntime` at runtime, so only a C compiler is needed
at build time. `CGO_ENABLED=0` builds compile and return `ErrUnavailable`.

## Build and test

```sh
task build        # builds the CLI -> ./bin/textshot
task test         # hermetic unit tests (no downloads, excludes test/e2e)
task test:race
task test:e2e     # TEXTSHOT_E2E=1: downloads runtime + models (~40 MB) once, OCRs the fixtures
task bench        # per-frame latency on the 1080p fixture
task test:nocgo   # CGO_ENABLED=0 stub compiles
task models:export  # one-off: export icon_detect_v3 to dist/models/ (needs uv + 281 MB download)
task models:check   # dist/models/*.sha256 must be pinned in models.go
go test ./internal/ppocr/                  # single package
go test -run TestDecode ./internal/ppocr/  # single test
```

In the devcell container `go` lives in `~/.local/share/mise/installs/go/<ver>/bin`
and cgo needs `CC=<nix clang-wrapper>/bin/clang`; the e2e tests also need
`LD_LIBRARY_PATH` pointing at the nix `gcc-*-lib/lib` and `zlib/lib` dirs.

## Package map

| Package | Purpose |
|---------|---------|
| `.` (textshot) | Public API: `Options`, `New`, `Engine.DetectText`, `Engine.DetectScreen`, `Engine.DetectElements`, `Engine.DetectInteractables`, `ComposeScreen`, `MergeElements`, `Diff`, `Lines`, `ParseQuantity`/`Quantities`/`ParseDuration`/`ParsePercent` (number + unit of measure out of an OCR line, pure Go), `Prefetch`, `PrefetchElements`; runtime/model download and cache layout |
| `typeface.go`, `internal/typo/` | Pure-Go typography of one text line against caller-supplied faces: `DetectTypeface` (render-and-compare: face, size, similarity), `MeasureSlant`, `MeasureStroke`, `MeasureFace`, `FlagSizeOutliers`, `GoFaces`; no model, runs under CGO_ENABLED=0 |
| `internal/screen/` | Pure-Go pixel measurements: per-box dominant background/foreground colours (4-bit histogram, mean per bin), WCAG contrast, backdrop/theme, saturated ring detection, frame diff |
| `internal/ppocr/` | Pure-Go PP-OCRv6 pipeline over a `Runner` interface: det preprocessing, DB post-processing (CCL, merge, unclip), rec preprocessing, CTC decode, embedded dictionary |
| `internal/uidet/` | Pure-Go OmniParser `icon_detect_v3` (YOLOv9-E) stage over a multi-output `Runner`: letterbox to /32, head decode (3 strides, ltrb distances), NMS |
| `internal/imgutil/` | Shared RGBA conversion, bilinear resize, crop, reading-order sort |
| `internal/fetch/` | Atomic HTTP download with SHA-256 verification and progress logging |
| `internal/testutil/` | `ResultDir(t)` and `Testdata(name)` for e2e tests |
| `cmd/textshot/` | CLI; `-json` runs `DetectScreen` and prints one JSON array of elements with style and value/uom (theme goes to stderr with `-t`) |
| `examples/detecttext/` | Smallest complete program (the README Quick start); built by `task test:nocgo` and CI so it cannot rot |
| `example_test.go` | `go doc` / pkg.go.dev examples: `MergeElements` and `Diff` run under `task test`; the model-backed ones compile only |
| `tools/export-icon-detect/` | uv project + script that converts the TorchScript `icon_detect_v3` to ONNX (dev-time only) |
| `.github/workflows/ci.yaml` | On push / pull request to `main`: `task vet`, `task test`, `task test:race`, `task test:nocgo` |
| `.github/workflows/release.yaml` | On `models-v*` tags: export, check the digest against `models.go`, publish as release assets |
| `.github/dependabot.yml` | Weekly grouped Go module and GitHub Actions update PRs; actions are pinned to release commit SHAs with a `# vX.Y.Z` comment that Dependabot keeps in sync |
| `CONTRIBUTING.md`, `CHANGELOG.md` | Setup, design rules and release process; newest-first change log (add an `Unreleased` entry with each change) |
| `test/e2e/` | Model-backed tests, gated by `TEXTSHOT_E2E=1`: one `fixtures` table (per screenshot: wanted lines, fast-path lines, progress quantity, theme, element checks) driven by `TestDetectText`, `TestDetectText1280` and `TestDetectScreen`; add a fixture, not a test |
| `test/testdata/fonts/` | Latin-1 subsets of open faces standing in for system fonts (Selawik for Segoe UI, Carlito for Calibri, Liberation for Arial/Times/Courier, Cascadia Mono, Inter instances for SF Pro, DejaVu Sans Mono for Menlo, Roboto, Source Sans 3, JetBrains Mono); `typeface_catalog_test.go` renders each at 13/16/24 px and must get family, weight and size back |
| `test/testdata/` | 1920x1080 screenshots: `macos-catalina/` (Recovery loading, Utilities, Terminal; installer progress at the start and in the black full-screen phase, both with an "About N minutes remaining" line) and `windows/` (TianoCore UEFI boot, WinPE console, blank Setup blue which must yield zero elements; Windows 11 and Windows 10 Setup "N% complete" screens, the latter a 700x467 web capture) |
| `test/results/` | Durable e2e output (`<ts>-<Test>/<fixture>.{txt,json,png}` and `<fixture>.elements.{json,png}`; the PNGs are the screenshot with boxes drawn, coloured by kind and labelled by ID), gitignored |

The ONNX Runtime binding is only imported from `engine_cgo.go`; everything
under `internal/ppocr` and `internal/uidet` takes a `Runner` so it is
testable with fakes. The element model is opened lazily on the first
`DetectInteractables`/`DetectElements` call (download + session), so
text-only users never pay for its 232 MB. That session tries the GPU
execution provider (CoreML on darwin, CUDA on linux/windows) and falls back
to CPU; the OCR sessions stay on CPU. ONNX Runtime has no Vulkan provider.

## Pipeline

```
image -> downscale to MaxSideLen (never upscale), pad to /32, ImageNet-normalize (BGR NCHW)
  -> det model (PP-OCRv6 small det) -> probability map
  -> threshold 0.2 -> 8-connected components -> split a component at column gaps >= 35% of its
     height (min 4 px) -> merge same-line boxes closer than 35% of the shorter height
  -> box_thresh 0.45 -> unclip 1.4
  -> crop lines from the original image, sort in reading order
  -> resize to h=48, (x/255-0.5)/0.5 -> rec model (PP-OCRv6 small rec) -> CTC greedy decode
  -> drop lines with Score < MinScore (0.5)

elements (on demand):
image -> downscale to ElementMaxSideLen (1280, never upscale), pad right/bottom with grey 114 to /32, RGB/255 NCHW
  -> icon_detect_v3 (YOLOv9-E, single class) -> 3 heads x [logits, ltrb] at strides 8/16/32
  -> sigmoid, score >= ElementMinScore (0.1), anchors +- ltrb*stride, map back
  -> NMS IoU 0.7, plus: a box >= 80% inside a larger one and >= 1/3 of its area is the same
     item (tight text box vs loose hit-area box around a menu item), lower score dropped
  -> MergeElements with the OCR lines: a detector box with score < 0.3 that one OCR line covers
     by >= 90% is dropped (a real control has a hit area around its label; this is noise on
     text); then a line labels the smallest interactable that covers
     half of it and is at most ~3x its height -> button (labelled) / icon (no text) / text (free);
     an unlabelled icon takes the topmost free line starting right of it on the same row
     (gap <= 1.5x its height) as Text and keeps KindIcon ("icon:Disk Utility"); one- or
     two-rune CJK lines in a square box are detector noise on icons and are dropped
  -> a free line that reports a quantity (regexp, no model: "10% complete", "About 3 minutes
     remaining...", "1 hour and 5 minutes" -> 65 minute) is kind progress with Value + UOM
     (second/minute/hour/day/percent); a button or icon whose label has one keeps its kind
     and gets Value + UOM too; bare numbers and clock times ("12:34") are not quantities
  -> ID = index in reading order (per frame), Key = "kind:text" or "icon@x,y", "#n" on repeats
  -> Style per box (no model): bg = dominant colour, fg = dominant colour with |luminance diff| > 48,
     Contrast = WCAG(fg, bg); Disabled = contrast < 3 (button/text/progress only); Highlighted = bg is
     saturated (S >= 0.35, V >= 0.25); Focused = a 1 px ring at some inset from -3 to +8 px is
     >= 60% saturated AND saturation drops below 30% within max(6, side/4) px further in
     (a ring is a line; a colourful icon is a fill); never checked on text/progress or on accent fills
  -> Theme = dark when the screenshot's dominant colour has luminance < 128
```

Deliberately not derived from pixels (unreliable or impossible): window and
panel hierarchy (tried a flood-fill region detector; nested panels break the
fill-ratio rule, dropped for now), mouse cursor (template depends on OS,
theme and scale), exact design-token colours, checked state, element
classes beyond button/icon/text (would need a multi-class model).

Font identity is the one exception, and it is scoped: `DetectTypeface`
never names a font the caller did not hand it. It renders the OCR text in
each supplied `Face`, scales to the crop's ink height and scores ink
overlap (1 px tolerance) times ink-density agreement times slant
agreement; the answer is the best candidate plus that score. Size, slant
and stroke weight are measured on the pixels alone. Score = gray ink-map
correlation (blurred, best of a 1 px shift) x width agreement x
column/row profile correlation x slant agreement x stroke agreement, with
the size refined over a 0.94..1.06 bracket. Verified in
`typeface_catalog_test.go` with 25 open faces standing in for the Windows
11 and macOS system fonts, rendered at 13, 16 and 24 px: 70 of 75 exact;
the misses are monospace siblings at <= 16 px and Carlito vs Liberation
Sans at 16 px (accepted as lookalikes by the test, listed there). Size is
within 1.5 px everywhere; weight steps (Regular/Medium/SemiBold/Bold) all
resolve. Cost is about 50 ms per line for 6 faces on one CPU.

Known limits, verified against the fixtures (see `test/results/` after an
e2e run):

- The macOS menu bar OCRs as one line ("Terminal Shell Edit View Window
  Help"): the det probability map is saturated across the whole bar even
  at 0.86, so no post-processing split can separate the items. The
  element detector does return one box per menu item; they carry no text.
- 11 px monospace text (the Recovery Terminal prompt `-bash-3.2#`) loses
  thin leading glyphs: det fragments `-b` into sub-threshold slivers and
  the line reads `ash-3.2#`. Tests match on `3.2#`.
- Logos read as elements: TianoCore on `windows/boot-uefi.png` is a
  `button` with `Disabled` set, because its grey tiles dominate the
  contrasting bin. Low-score (0.1-0.2) icons on terminal output are
  detector noise; raise `ElementMinScore` to 0.25 if they matter.

Element identity follows the GUI-agent conventions: `ID` is a per-frame
set-of-marks number (OmniParser, browser-use), `Key` is the role+name
locator equivalent (Playwright `getByRole(name)`) since there is no
accessibility tree. Cross-frame matching is the caller's job: look up by
`Key`, confirm by `Box` overlap, then act.

## Pinned artifacts

| Artifact | Source | Pin |
|----------|--------|-----|
| ONNX Runtime linux/amd64, linux/arm64, darwin/arm64 | GitHub release tarballs | 1.30.0, sha256 in `runtime.go` |
| ONNX Runtime darwin/amd64 | GitHub release tarball | 1.23.2 (last release with an Intel macOS build) |
| Det model | `PaddlePaddle/PP-OCRv6_small_det_onnx` on Hugging Face | commit + sha256 in `models.go` |
| Rec model | `PaddlePaddle/PP-OCRv6_small_rec_onnx` on Hugging Face | commit + sha256 in `models.go` |
| Element model | `microsoft/OmniParser-v2.0` `icon_detect_v3/model.pt` (MIT), exported to ONNX by `tools/export-icon-detect` and published on the go-textshot `models-v*` release | release tag + sha256 in `models.go`; source `.pt` revision + sha256 in `tools/export-icon-detect/export.py` |
| Go binding | `github.com/microsoft/onnxruntime/go` | pseudo-version in `go.mod` (untagged upstream) |

Bumping a pin: change the version/commit, then download the file and record
`sha256sum` output. The model output contract is validated at load time
(det input `[N 3 H W]`, rec output class count equals the embedded dict,
element model 1 input / 6 outputs). Bumping the element model: update the
source pin in `export.py`, run `task models:export`, put the printed
sha256/size into `ElementModel` with a new `models-vN` tag, push the tag;
CI re-runs the export, fails if the digest differs from `models.go`
(export is deterministic for the pinned torch/onnx versions), and uploads
the asset. Only the v3 (YOLOv9-E, MIT) checkpoint may be used; the older
`icon_detect` (YOLOv8) is AGPL.

## Cache layout

Default `os.UserCacheDir()/textshot` (`~/.cache/textshot` on Linux,
`~/Library/Caches/textshot` on macOS). Callers may pass their own
`Options.CacheDir` (mackit uses its own cache dir plus `textshot`).

```
<cache>/onnxruntime/<version>/libonnxruntime.{so,dylib}
<cache>/models/PP-OCRv6_small_det_onnx-<commit12>.onnx
<cache>/models/PP-OCRv6_small_rec_onnx-<commit12>.onnx
<cache>/models/models-v1-icon_detect_v3.onnx        # only after the first elements call or PrefetchElements
```

## Environment variables

| Variable | Purpose |
|----------|---------|
| `TEXTSHOT_CACHE_DIR` | Override the cache directory |
| `TEXTSHOT_ONNXRUNTIME_LIB` | Use this `libonnxruntime` (file or directory) instead of downloading |
| `TEXTSHOT_E2E` | `1` enables model-backed tests |

Explicit `Options` fields take precedence over the environment.

## Performance notes

Measured on the 1920x1080 Utilities fixture, 2-CPU Linux arm64 container:

| MaxSideLen | Threads | Per frame |
|-----------:|--------:|----------:|
| 1920 | 2 | ~650 ms (det ~400 ms, rec ~260 ms for 12 lines) |
| 1280 | 2 | ~430 ms |
| 1920 | 8 (oversubscribed) | ~1.3 s |

Threads default to `runtime.GOMAXPROCS(0)`, which honours cgroup CPU
limits; `runtime.NumCPU()` does not and oversubscription doubles latency.
UI labels are ~13 px tall at 1080p; 1280 still reads them, 960 is marginal.

Element detection on the same fixture and container (detector only):

| ElementMaxSideLen | Per frame | Notes |
|------------------:|----------:|-------|
| 1920 | ~12 s | Continue 0.86, window controls 0.5-0.6 |
| 1280 | ~4 s | Continue 0.75, window controls 0.26-0.39, list icons 0.14-0.38 |
| 640 | fast | Continue 0.07: unusable for 1080p UI |

YOLOv9-E is ~20x the compute of OCR, so call it on demand, never in the
polling loop. `DetectElements` adds one OCR pass on top.

## Linux dlopen note

The downloaded `libonnxruntime.so` needs `libstdc++.so.6` and
`libgcc_s.so.1`. On Nix hosts those live only under `NIX_LD_LIBRARY_PATH`;
`preload_linux.go` dlopens them `RTLD_GLOBAL` from `NIX_LD_LIBRARY_PATH` and
`LD_LIBRARY_PATH` before loading the runtime.
