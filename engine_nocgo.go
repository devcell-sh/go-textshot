//go:build !cgo

package textshot

import (
	"context"
	"fmt"
)

func newEngine(context.Context, Options) (*Engine, error) {
	return nil, fmt.Errorf("%w: built with CGO_ENABLED=0", ErrUnavailable)
}
