package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func serve(t *testing.T, body []byte) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestDownloadVerifiesAndCaches(t *testing.T) {
	body := []byte("hello textshot")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	srv, hits := serve(t, body)
	dest := filepath.Join(t.TempDir(), "sub", "file.bin")

	require.NoError(t, Download(context.Background(), srv.URL, dest, Options{SHA256: want}))
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, body, got)
	_, err = os.Stat(dest + ".tmp")
	require.True(t, os.IsNotExist(err), "tmp file must be removed")

	// Second call reuses the cached file without touching the server.
	require.NoError(t, Download(context.Background(), srv.URL, dest, Options{SHA256: want}))
	require.Equal(t, 1, *hits)
}

func TestDownloadChecksumMismatch(t *testing.T) {
	srv, _ := serve(t, []byte("payload"))
	dest := filepath.Join(t.TempDir(), "file.bin")
	err := Download(context.Background(), srv.URL, dest, Options{SHA256: "00"})
	require.ErrorIs(t, err, ErrChecksum)
	_, statErr := os.Stat(dest)
	require.True(t, os.IsNotExist(statErr), "dest must not exist after a failed download")
}

func TestDownloadRedownloadsStaleCache(t *testing.T) {
	body := []byte("fresh")
	sum := sha256.Sum256(body)
	srv, hits := serve(t, body)
	dest := filepath.Join(t.TempDir(), "file.bin")
	require.NoError(t, os.WriteFile(dest, []byte("stale"), 0o644))

	require.NoError(t, Download(context.Background(), srv.URL, dest, Options{SHA256: hex.EncodeToString(sum[:])}))
	require.Equal(t, 1, *hits)
	got, _ := os.ReadFile(dest)
	require.Equal(t, body, got)
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	err := Download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x"), Options{})
	require.ErrorContains(t, err, "404")
}

func TestDownloadTruncatedReportsProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 100))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // drop the connection mid-body
	}))
	t.Cleanup(srv.Close)

	err := Download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x"), Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "after 100 of 1000 bytes (10%)")
}
