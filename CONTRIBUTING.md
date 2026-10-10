# Contributing

Bug reports, fixture screenshots that OCR badly, and pull requests are all
welcome at <https://github.com/devcell-sh/go-textshot>. Small, focused
pull requests are usually reviewed within a few days.

## Setup

```sh
git clone https://github.com/devcell-sh/go-textshot.git
cd go-textshot
task test           # hermetic unit tests, no downloads (or: go test ./...)
task test:race
task test:nocgo     # CGO_ENABLED=0 build + vet; also builds examples/
task test:e2e       # TEXTSHOT_E2E=1: downloads runtime + models (~40 MB) once
```

Only a Go toolchain (version in `go.mod`), a C compiler and
[Task](https://taskfile.dev) are needed. The ONNX Runtime library and the
models are downloaded on first use; nothing is vendored.

## Where things live

| Path | Purpose |
|------|---------|
| `textshot.go`, `engine_cgo.go`, `runtime.go`, `models.go` | Public API, session setup, runtime and model download, pins |
| `internal/ppocr/` | PP-OCRv6 pre/post-processing over a `Runner` interface |
| `internal/uidet/` | OmniParser `icon_detect_v3` decode and NMS over a `Runner` interface |
| `internal/screen/` | Pixel measurements: colours, contrast, focus ring, theme, frame diff |
| `internal/imgutil/`, `internal/fetch/` | Image helpers; verified atomic downloads |
| `cmd/textshot/` | CLI |
| `examples/` | Small complete programs; built by `task test:nocgo` so they cannot rot |
| `test/e2e/`, `test/testdata/` | Model-backed tests and the 1080p fixtures |
| `tools/export-icon-detect/` | TorchScript to ONNX export of the element model (dev-time only) |

## Design rules

- `engine_cgo.go` is the only file that imports the ONNX Runtime binding.
  `internal/ppocr` and `internal/uidet` take a `Runner` interface, so
  every pixel and tensor rule is pure Go and unit-tested with fakes. A new
  backend implements those two interfaces in a pull request here; they are
  under `internal/` on purpose and are not a public plug-in point.
- Everything the library reports is derived from pixels. Do not add
  output that depends on an accessibility tree, the OS, or a theme.
- Thresholds in the pipeline (see the Pipeline section of
  [AGENTS.md](AGENTS.md)) are verified against the fixtures. A change
  needs a fixture that shows why, and `task test:e2e` output in
  `test/results/` that shows the effect.
- Model and runtime pins are SHA-256 digests. Bumping one is described in
  the Pinned artifacts section of [AGENTS.md](AGENTS.md).
- Only the MIT `icon_detect_v3` (YOLOv9-E) checkpoint may be used; the
  older `icon_detect` (YOLOv8) is AGPL.

## Releases

Versions are semver tags (`vX.Y.Z`) on `main`. Add an entry to
[CHANGELOG.md](CHANGELOG.md) under `Unreleased` with each change and move
the section under the new version when tagging. `models-vN` tags are a
separate stream that publishes the exported element model as a release
asset (see `.github/workflows/release.yaml`).
