package textshot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devcell-sh/go-textshot/internal/fetch"
)

// RuntimeAsset is a pinned ONNX Runtime release tarball for one platform.
type RuntimeAsset struct {
	Version string
	Name    string // tarball file name in the GitHub release
	SHA256  string
}

// URL is the GitHub release download URL.
func (a RuntimeAsset) URL() string {
	return fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s/%s", a.Version, a.Name)
}

// runtimeAssets maps GOOS/GOARCH to the pinned release. Microsoft stopped
// shipping Intel macOS builds after 1.23.2, so darwin/amd64 pins that
// release; the Go binding accepts ORT C API 17..27, which both cover.
var runtimeAssets = map[string]RuntimeAsset{
	"linux/amd64":  {Version: "1.30.0", Name: "onnxruntime-linux-x64-1.30.0.tgz", SHA256: "a5ed5a3cac51fbb2e90da632ae43d19212faaa20e76484e62bcb7c23ddb3b3fd"},
	"linux/arm64":  {Version: "1.30.0", Name: "onnxruntime-linux-aarch64-1.30.0.tgz", SHA256: "e16a27a8ed330bbc698df7330b0cf56e722f354e3bcc92118682c74ef3c3e3da"},
	"darwin/arm64": {Version: "1.30.0", Name: "onnxruntime-osx-arm64-1.30.0.tgz", SHA256: "6ebb5062a934537c352937821f9fe9718e7de1a2db1122a93dd363ffd53a7012"},
	"darwin/amd64": {Version: "1.23.2", Name: "onnxruntime-osx-x86_64-1.23.2.tgz", SHA256: "d10359e16347b57d9959f7e80a225a5b4a66ed7d7e007274a15cae86836485a6"},
}

// RuntimeAssetFor returns the pinned ONNX Runtime release for a platform.
func RuntimeAssetFor(goos, goarch string) (RuntimeAsset, bool) {
	a, ok := runtimeAssets[goos+"/"+goarch]
	return a, ok
}

// libraryFileName is the unversioned shared library name for goos.
func libraryFileName(goos string) string {
	if goos == "darwin" {
		return "libonnxruntime.dylib"
	}
	return "libonnxruntime.so"
}

// ensureRuntime resolves the ONNX Runtime shared library path: an explicit
// Options.LibraryPath (file or directory) wins and is never downloaded;
// otherwise the pinned release for this platform is fetched into the cache
// on first use.
func ensureRuntime(ctx context.Context, o Options) (string, error) {
	if o.LibraryPath != "" {
		return resolveExplicitLibrary(o.LibraryPath)
	}
	asset, ok := RuntimeAssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", fmt.Errorf("%w: no ONNX Runtime release for %s/%s; set %s to a local libonnxruntime",
			ErrUnavailable, runtime.GOOS, runtime.GOARCH, EnvLibraryPath)
	}
	dir := filepath.Join(o.CacheDir, "onnxruntime", asset.Version)
	dest := filepath.Join(dir, libraryFileName(runtime.GOOS))
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	tgz := filepath.Join(dir, asset.Name)
	if err := fetch.Download(ctx, asset.URL(), tgz, fetch.Options{SHA256: asset.SHA256, Logger: o.Logger}); err != nil {
		return "", fmt.Errorf("textshot: onnxruntime %s: %w", asset.Version, err)
	}
	defer os.Remove(tgz)
	if err := extractLibrary(tgz, dest); err != nil {
		return "", fmt.Errorf("textshot: onnxruntime %s: %w", asset.Version, err)
	}
	o.Logger.Info("onnxruntime ready", "version", asset.Version, "path", dest)
	return dest, nil
}

func resolveExplicitLibrary(p string) (string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("textshot: onnxruntime library %s: %w", p, err)
	}
	if info.IsDir() {
		p = filepath.Join(p, libraryFileName(runtime.GOOS))
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("textshot: onnxruntime library %s: %w", p, err)
		}
	}
	return p, nil
}

// extractLibrary copies the single real libonnxruntime shared object out
// of a release tarball to dest, skipping symlinks, provider bridges and
// debug symbols. The write is atomic (tmp + rename).
func extractLibrary(tgz, dest string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s: no libonnxruntime shared library in archive", filepath.Base(tgz))
		}
		if err != nil {
			return err
		}
		if !isRuntimeLibrary(hdr) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		tmp := dest + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
		if err := out.Close(); err != nil {
			os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, dest)
	}
}

// isRuntimeLibrary matches the regular-file entry lib/libonnxruntime.<ver>.dylib
// or lib/libonnxruntime.so.<ver> (the symlinks libonnxruntime.{so,dylib}
// point at it).
func isRuntimeLibrary(hdr *tar.Header) bool {
	if hdr.Typeflag != tar.TypeReg {
		return false
	}
	name := path.Clean(hdr.Name)
	if strings.Contains(name, ".dSYM/") || path.Base(path.Dir(name)) != "lib" {
		return false
	}
	base := path.Base(name)
	return strings.HasPrefix(base, "libonnxruntime.") && !strings.Contains(base, "providers")
}
