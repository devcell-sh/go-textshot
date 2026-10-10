// Package fetch downloads files atomically with optional SHA-256
// verification and coarse progress logging.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Options controls a Download.
type Options struct {
	// SHA256 is the expected hex digest of the file. When set, an existing
	// destination that matches is reused and a downloaded file that does
	// not match is discarded with an error.
	SHA256 string
	// Logger receives progress messages at Info level; nil disables logging.
	Logger *slog.Logger
	// Client overrides http.DefaultClient.
	Client *http.Client
}

// ErrChecksum is returned (wrapped) when a downloaded file does not match
// Options.SHA256.
var ErrChecksum = errors.New("sha256 mismatch")

// Download fetches url into dest. The file is written to dest+".tmp" and
// renamed into place only after it is complete and verified, so dest is
// never left partially written. If dest already exists and matches
// Options.SHA256 (or no digest was given), nothing is downloaded.
func Download(ctx context.Context, url, dest string, o Options) error {
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	want := strings.ToLower(o.SHA256)

	if ok, err := exists(dest, want); err != nil {
		return err
	} else if ok {
		logger.Debug("cached", "path", dest)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	defer os.Remove(tmp)

	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	total, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	logger.Info("downloading", "url", url, "dest", dest, "total", total)

	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	w := io.MultiWriter(out, h)
	if err := copyWithProgress(w, resp.Body, total, logger); err != nil {
		out.Close()
		return fmt.Errorf("GET %s: %w", url, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if want != "" && got != want {
		return fmt.Errorf("%s: %w: got %s, want %s", url, ErrChecksum, got, want)
	}
	return os.Rename(tmp, dest)
}

// copyWithProgress copies src to dst, logging every 10% when total is known.
func copyWithProgress(dst io.Writer, src io.Reader, total int64, logger *slog.Logger) error {
	buf := make([]byte, 256*1024)
	var written int64
	lastDecile := -1
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, err := dst.Write(buf[:n]); err != nil {
				return err
			}
			written += int64(n)
			if total > 0 {
				if d := int(written * 10 / total); d > lastDecile {
					lastDecile = d
					logger.Info("downloading", "progress", fmt.Sprintf("%d%%", d*10), "bytes", written, "total", total)
				}
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// exists reports whether path is a regular file that matches want (hex
// sha256). An empty want only checks for existence.
func exists(path, want string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s: not a regular file", path)
	}
	if want == "" {
		return true, nil
	}
	got, err := FileSHA256(path)
	if err != nil {
		return false, err
	}
	return got == want, nil
}

// FileSHA256 returns the lowercase hex SHA-256 digest of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
