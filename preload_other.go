//go:build !(cgo && linux)

package textshot

// preloadDependencies is a no-op outside Linux cgo builds.
func preloadDependencies() {}
