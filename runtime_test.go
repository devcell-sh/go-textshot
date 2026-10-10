package textshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeTarball(t *testing.T, entries map[string]any) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, v := range entries {
		switch v := v.(type) {
		case string: // symlink target
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: v}))
		case []byte:
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(v))}))
			_, err := tw.Write(v)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	p := filepath.Join(t.TempDir(), "rt.tgz")
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o644))
	return p
}

func TestExtractLibraryPicksRealSharedObject(t *testing.T) {
	want := []byte("ELF-ish payload")
	tgz := writeTarball(t, map[string]any{
		"./onnxruntime-osx-arm64-1.30.0/lib/libonnxruntime.1.30.0.dylib":                                                           want,
		"./onnxruntime-osx-arm64-1.30.0/lib/libonnxruntime.dylib":                                                                  "libonnxruntime.1.30.0.dylib",
		"./onnxruntime-osx-arm64-1.30.0/lib/libonnxruntime.1.30.0.dylib.dSYM/Contents/Resources/DWARF/libonnxruntime.1.30.0.dylib": []byte("debug"),
		"onnxruntime-osx-arm64-1.30.0/lib/libonnxruntime_providers_shared.so":                                                      []byte("providers"),
		"onnxruntime-osx-arm64-1.30.0/include/onnxruntime_c_api.h":                                                                 []byte("header"),
	})
	dest := filepath.Join(t.TempDir(), "out", "libonnxruntime.dylib")
	require.NoError(t, extractLibrary(tgz, dest))
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, want, got)
	info, _ := os.Stat(dest)
	require.NotZero(t, info.Mode()&0o100, "library must be executable")
}

func TestExtractLibraryLinuxLayout(t *testing.T) {
	tgz := writeTarball(t, map[string]any{
		"onnxruntime-linux-aarch64-1.30.0/lib/libonnxruntime.so":        "libonnxruntime.so.1.30.0",
		"onnxruntime-linux-aarch64-1.30.0/lib/libonnxruntime.so.1":      "libonnxruntime.so.1.30.0",
		"onnxruntime-linux-aarch64-1.30.0/lib/libonnxruntime.so.1.30.0": []byte("so"),
	})
	dest := filepath.Join(t.TempDir(), "libonnxruntime.so")
	require.NoError(t, extractLibrary(tgz, dest))
	got, _ := os.ReadFile(dest)
	require.Equal(t, []byte("so"), got)
}

func TestExtractLibraryMissing(t *testing.T) {
	tgz := writeTarball(t, map[string]any{"x/lib/libonnxruntime_providers_shared.so": []byte("p")})
	require.ErrorContains(t, extractLibrary(tgz, filepath.Join(t.TempDir(), "lib.so")), "no libonnxruntime")
}

func TestEnsureRuntimeExplicitPath(t *testing.T) {
	dir := t.TempDir()
	_, err := ensureRuntime(context.Background(), Options{LibraryPath: filepath.Join(dir, "missing.so")}.withDefaults())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrUnavailable)

	lib := filepath.Join(dir, libraryFileName("linux"))
	require.NoError(t, os.WriteFile(lib, nil, 0o755))
	if libraryFileName("linux") == libraryFileName(osName()) {
		got, err := ensureRuntime(context.Background(), Options{LibraryPath: dir}.withDefaults())
		require.NoError(t, err)
		require.Equal(t, lib, got, "directory resolves to the platform library name")
	}
	got, err := ensureRuntime(context.Background(), Options{LibraryPath: lib}.withDefaults())
	require.NoError(t, err)
	require.Equal(t, lib, got)
}

func TestEnsureRuntimeUsesCachedLibrary(t *testing.T) {
	cache := t.TempDir()
	asset, ok := RuntimeAssetFor(osName(), archName())
	if !ok {
		t.Skip("no runtime asset for this platform")
	}
	lib := filepath.Join(cache, "onnxruntime", asset.Version, libraryFileName(osName()))
	require.NoError(t, os.MkdirAll(filepath.Dir(lib), 0o755))
	require.NoError(t, os.WriteFile(lib, nil, 0o755))
	got, err := ensureRuntime(context.Background(), Options{CacheDir: cache}.withDefaults())
	require.NoError(t, err)
	require.Equal(t, lib, got)
}

func TestRuntimeAssetsPinned(t *testing.T) {
	for _, p := range []string{"linux/amd64", "linux/arm64", "darwin/arm64", "darwin/amd64"} {
		a, ok := runtimeAssets[p]
		require.True(t, ok, p)
		require.Len(t, a.SHA256, 64, p)
		require.Contains(t, a.Name, a.Version, p)
		require.Contains(t, a.URL(), "/v"+a.Version+"/", p)
	}
	_, ok := RuntimeAssetFor("plan9", "mips")
	require.False(t, ok)
}
