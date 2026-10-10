# Changelog

Newest first. Versions are semver tags on `main`; entries under
`Unreleased` move under the next tag when it is cut. `models-vN` tags
publish the exported element model and are tracked in `models.go`, not
here.

## Unreleased

### Added

- `Engine.DetectScreen` and `ComposeScreen` return a `Screen` with each
  element's pixel-measured `Style` (background, foreground, WCAG
  contrast, `Disabled`, `Highlighted`, `Focused` with the ring colour)
  and the screen's `Theme` and `Backdrop`, with no extra model.
- `Diff(prev, next)` reports the changed region and fraction between two
  frames, for polling until a screen settles.
- List-row icons take the adjacent title as their `Text` and keep
  `KindIcon`, giving a stable key such as `icon:Disk Utility`.
- CLI `-json` emits each element's measured style alongside its geometry;
  `-t` reports the screen theme on stderr.
- Windows fixtures (TianoCore UEFI boot, WinPE console, blank Setup
  screen) and a macOS Terminal fixture in the e2e suite.
- Runnable examples (`example_test.go`, `examples/detecttext`), a CI
  workflow for push and pull requests, Dependabot, `CONTRIBUTING.md` and
  this changelog.

### Changed

- OCR splits a detected text component at column gaps of at least 35% of
  its height and merges same-line boxes only when closer than 0.35x the
  height, so menu-bar items and table cells no longer read as one line.
- The element detector drops a box nested at least 80% inside a looser
  box of at most three times its area, so a menu item or list row yields
  one box instead of a hit-area box plus a text box.
- One- or two-rune CJK lines in a near-square box are dropped as icon
  misreads.
- `ErrUnavailable` messages name the remedy and the issue tracker; a
  download that fails mid-stream reports the bytes received so far.
- CLI `-max-side` and `-element-max-side` help text states the unit (px).
- README lists every field the API extracts, a real JSON record, the
  pure helpers and the CLI flag for each option.
- GitHub Actions are pinned to release commit SHAs (with the version in a
  trailing comment and the source URL above) and run on `ubuntu-26.04`.

## 2026-10-10 Initial commit (ff73318, untagged)

- Pure-Go PP-OCRv6 detection and recognition pipeline over a `Runner`
  interface: DB post-processing, CTC decode, embedded dictionary.
- OmniParser `icon_detect_v3` element detector: `DetectElements` and
  `DetectInteractables` return buttons, icons and text with per-frame IDs
  and role+name keys.
- ONNX Runtime cgo binding that dlopens the library at run time, with a
  `CGO_ENABLED=0` stub returning `ErrUnavailable`; the element session
  prefers CoreML or CUDA and falls back to CPU.
- Runtime library and models downloaded on first use into the cache and
  verified against pinned SHA-256 digests.
- `textshot` CLI: text lines by default, `-json` for elements,
  `-prefetch` to warm the cache.
- `tools/export-icon-detect` and the `models-v*` release workflow that
  convert the TorchScript checkpoint to ONNX and publish it.
- Model-backed e2e tests gated by `TEXTSHOT_E2E=1`.
