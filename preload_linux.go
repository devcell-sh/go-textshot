//go:build cgo && linux

package textshot

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
*/
import "C"

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// preloadDependencies makes libstdc++ and libgcc_s resolvable for the
// downloaded libonnxruntime on hosts whose dynamic loader cannot find them
// by default (Nix environments expose them only through
// NIX_LD_LIBRARY_PATH, which applies to foreign executables, not to dlopen
// from a Nix-linked Go binary). Each library is loaded RTLD_GLOBAL from the
// first directory on those paths that has it; failures are ignored, since
// the subsequent dlopen of libonnxruntime reports the real problem.
func preloadDependencies() {
	var dirs []string
	for _, env := range []string{"NIX_LD_LIBRARY_PATH", "LD_LIBRARY_PATH"} {
		for _, d := range strings.Split(os.Getenv(env), ":") {
			if d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	if len(dirs) == 0 {
		return
	}
	for _, lib := range []string{"libgcc_s.so.1", "libstdc++.so.6"} {
		for _, d := range dirs {
			p := filepath.Join(d, lib)
			if _, err := os.Stat(p); err != nil {
				continue
			}
			cp := C.CString(p)
			h := C.dlopen(cp, C.RTLD_NOW|C.RTLD_GLOBAL)
			C.free(unsafe.Pointer(cp))
			if h != nil {
				break
			}
		}
	}
}
