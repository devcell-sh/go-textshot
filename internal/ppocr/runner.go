// Package ppocr implements the PP-OCRv6 text detection and recognition
// pipeline on top of an abstract model runner, so the image math is pure Go
// and testable without ONNX Runtime.
package ppocr

import "context"

// Runner executes a single-input, single-output float32 model.
type Runner interface {
	// Run feeds data with the given NCHW shape to the model and returns the
	// output tensor's shape and data. The returned slice is owned by the
	// caller.
	Run(ctx context.Context, shape []int64, data []float32) (outShape []int64, out []float32, err error)
}
