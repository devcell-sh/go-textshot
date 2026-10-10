package textshot

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/devcell-sh/go-textshot/internal/fetch"
)

// Model is a pinned ONNX model download.
type Model struct {
	Name   string // file name under <cache>/models
	URL    string
	SHA256 string
	Size   int64
}

// hfModel pins a file in a Hugging Face repo at a commit.
func hfModel(repo, commit, file, sha256 string, size int64) Model {
	return Model{
		Name:   fmt.Sprintf("%s-%s%s", path.Base(repo), commit[:12], path.Ext(file)),
		URL:    fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s", repo, commit, file),
		SHA256: sha256,
		Size:   size,
	}
}

// releaseModel pins an asset of a go-textshot GitHub release.
func releaseModel(tag, file, sha256 string, size int64) Model {
	return Model{
		Name:   fmt.Sprintf("%s-%s", tag, file),
		URL:    fmt.Sprintf("https://github.com/devcell-sh/go-textshot/releases/download/%s/%s", tag, file),
		SHA256: sha256,
		Size:   size,
	}
}

var (
	// PP-OCRv6 text detection and recognition (Apache-2.0, official PaddlePaddle exports).
	DetModel = hfModel("PaddlePaddle/PP-OCRv6_small_det_onnx", "28fe5895c24fd108c19eb3e8479f4ab385fbfc62", "inference.onnx",
		"d73e0058b7a8086bbd57f3d10b8bcd4ff95363f67e06e2762b5e814fe9c9410e", 9880512)
	RecModel = hfModel("PaddlePaddle/PP-OCRv6_small_rec_onnx", "b8f84f0b80c529de40b4fbb3544b84fa7233a513", "inference.onnx",
		"5435fd747c9e0efe15a96d0b378d5bd157e9492ed8fd80edf08f30d02fa24634", 21159378)

	// OmniParser icon_detect_v3 (MIT, YOLOv9-E interactable-region detector),
	// exported to ONNX by tools/export-icon-detect and published by the release workflow.
	ElementModel = releaseModel("models-v1", "icon_detect_v3.onnx",
		"a34d6ff3bd7448d4dad6db70df0cdd80b42440f5b53eab2d4469dc6694fc004a", 232306145)
)

// CachePath is where the model lives under cacheDir.
func (m Model) CachePath(cacheDir string) string {
	return filepath.Join(cacheDir, "models", m.Name)
}

func ensureModel(ctx context.Context, o Options, m Model) (string, error) {
	dest := m.CachePath(o.CacheDir)
	err := fetch.Download(ctx, m.URL, dest, fetch.Options{SHA256: m.SHA256, Logger: o.Logger})
	if err != nil {
		return "", fmt.Errorf("textshot: model %s: %w", m.Name, err)
	}
	return dest, nil
}

// ensureModels returns the detection and recognition model paths,
// downloading them when missing.
func ensureModels(ctx context.Context, o Options) (det, rec string, err error) {
	if det, err = ensureModel(ctx, o, DetModel); err != nil {
		return "", "", err
	}
	if rec, err = ensureModel(ctx, o, RecModel); err != nil {
		return "", "", err
	}
	return det, rec, nil
}
