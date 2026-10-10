// Package testutil holds helpers for e2e tests.
package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	mu      sync.Mutex
	dirs    = map[string]string{}
	runTime = time.Now().UTC().Format("20060102T150405")
)

// ResultDir returns a durable per-test directory under test/results/
// (<timestamp>-<TestName>), created on first use. Results survive go test
// so OCR output can be inspected after a run.
func ResultDir(t testing.TB) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if d, ok := dirs[t.Name()]; ok {
		return d
	}
	dir := filepath.Join(ProjectRoot(), "test", "results", runTime+"-"+sanitizeName(t.Name()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating result dir: %v", err)
	}
	dirs[t.Name()] = dir
	return dir
}

// Testdata returns the absolute path of a file under test/testdata.
func Testdata(name string) string {
	return filepath.Join(ProjectRoot(), "test", "testdata", name)
}

// ProjectRoot is the repository root (two levels above this package).
func ProjectRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(f)))
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	return strings.ReplaceAll(name, " ", "_")
}
