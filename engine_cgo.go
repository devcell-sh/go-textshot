//go:build cgo

package textshot

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"

	ort "github.com/microsoft/onnxruntime/go/onnxruntime"

	"github.com/devcell-sh/go-textshot/internal/ppocr"
	"github.com/devcell-sh/go-textshot/internal/uidet"
)

var initMu sync.Mutex

// initRuntime loads the ONNX Runtime library once per process. The binding
// ignores later path changes after a successful Init, so the first Engine
// in a process decides which library is used.
func initRuntime(libPath string) error {
	initMu.Lock()
	defer initMu.Unlock()
	if ort.IsInitialized() {
		return nil
	}
	preloadDependencies()
	ort.SetSharedLibraryPath(libPath)
	if err := ort.Init(); err != nil {
		return fmt.Errorf("textshot: load onnxruntime from %s: %w", libPath, err)
	}
	return nil
}

func newEngine(ctx context.Context, o Options) (*Engine, error) {
	libPath, err := ensureRuntime(ctx, o)
	if err != nil {
		return nil, err
	}
	detPath, recPath, err := ensureModels(ctx, o)
	if err != nil {
		return nil, err
	}
	if err := initRuntime(libPath); err != nil {
		return nil, err
	}
	if v, err := ort.GetVersion(); err == nil {
		o.Logger.Debug("onnxruntime loaded", "version", v, "api", ort.APIVersion(), "path", libPath)
	}

	opts, err := newSessionOptions(o)
	if err != nil {
		return nil, err
	}
	defer opts.Close()

	e := &Engine{minScore: o.MinScore, logger: o.Logger}
	det, err := openSession(detPath, opts, 1)
	if err != nil {
		return nil, fmt.Errorf("textshot: det model: %w", err)
	}
	e.closers = append(e.closers, det.sess.Close)
	if in := det.sess.Inputs()[0].Shape; len(in) != 4 || in[1] != 3 {
		e.Close()
		return nil, fmt.Errorf("textshot: det model input shape %v, want [N 3 H W]", in)
	}

	rec, err := openSession(recPath, opts, 1)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("textshot: rec model: %w", err)
	}
	e.closers = append(e.closers, rec.sess.Close)
	dict := ppocr.DefaultDict()
	out := rec.sess.Outputs()[0].Shape
	if len(out) != 3 {
		e.Close()
		return nil, fmt.Errorf("textshot: rec model output shape %v, want [N T classes]", out)
	}
	if err := dict.Validate(int(out[2])); err != nil {
		e.Close()
		return nil, fmt.Errorf("textshot: rec model: %w", err)
	}

	e.pipeline = &ppocr.Pipeline{
		Det:    ppocr.NewDetector(&singleRunner{det}, ppocr.DetConfig{MaxSideLen: o.MaxSideLen}),
		Rec:    ppocr.NewRecognizer(&singleRunner{rec}, dict),
		Logger: o.Logger,
	}
	e.openElements = func(ctx context.Context) (*uidet.Detector, func() error, error) {
		return openElements(ctx, o)
	}
	return e, nil
}

func newSessionOptions(o Options) (*ort.SessionOptions, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("textshot: session options: %w", err)
	}
	if err := opts.SetIntraOpNumThreads(o.Threads); err != nil {
		opts.Close()
		return nil, fmt.Errorf("textshot: session options: %w", err)
	}
	if err := opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelAll); err != nil {
		opts.Close()
		return nil, fmt.Errorf("textshot: session options: %w", err)
	}
	return opts, nil
}

// openElements downloads the element model when missing and opens it,
// preferring the platform's GPU execution provider when the loaded runtime
// has one and falling back to the CPU otherwise.
func openElements(ctx context.Context, o Options) (*uidet.Detector, func() error, error) {
	path, err := ensureModel(ctx, o, ElementModel)
	if err != nil {
		return nil, nil, err
	}
	sess, err := openElementSession(path, o, true)
	if err != nil {
		o.Logger.Debug("textshot: element model failed on the GPU provider, retrying on CPU", "err", err)
		sess, err = openElementSession(path, o, false)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("textshot: element model: %w", err)
	}
	if in := sess.sess.Inputs()[0].Shape; len(in) != 4 || in[1] != 3 {
		sess.sess.Close()
		return nil, nil, fmt.Errorf("textshot: element model input shape %v, want [N 3 H W]", in)
	}
	det := uidet.NewDetector(&multiRunner{sess}, uidet.Config{MaxSideLen: o.ElementMaxSideLen, MinScore: o.ElementMinScore})
	return det, sess.sess.Close, nil
}

func openElementSession(path string, o Options, gpu bool) (*ortSession, error) {
	opts, err := newSessionOptions(o)
	if err != nil {
		return nil, err
	}
	defer opts.Close()
	if gpu && !appendGPUProvider(opts, o.Logger) {
		return nil, fmt.Errorf("no GPU execution provider")
	}
	return openSession(path, opts, 6)
}

// appendGPUProvider adds the platform's GPU execution provider (CoreML on
// macOS, CUDA elsewhere) and reports whether the runtime accepted it.
func appendGPUProvider(opts *ort.SessionOptions, logger *slog.Logger) bool {
	var name string
	var kv map[string]string
	switch runtime.GOOS {
	case "darwin":
		name = "CoreML"
		kv = map[string]string{"ModelFormat": "MLProgram", "MLComputeUnits": "ALL"}
	case "linux", "windows":
		name = "CUDA"
	default:
		return false
	}
	if err := opts.AppendExecutionProvider(name, kv); err != nil {
		logger.Debug("textshot: GPU execution provider unavailable", "provider", name, "err", err)
		return false
	}
	logger.Debug("textshot: using GPU execution provider", "provider", name)
	return true
}

// ortSession is a single-input ORT session with a fixed output list.
type ortSession struct {
	sess *ort.Session
	in   string
	outs []string
}

func openSession(modelPath string, opts *ort.SessionOptions, nOut int) (*ortSession, error) {
	sess, err := ort.NewSession(modelPath, opts)
	if err != nil {
		return nil, err
	}
	ins, outs := sess.Inputs(), sess.Outputs()
	if len(ins) != 1 || len(outs) != nOut {
		sess.Close()
		return nil, fmt.Errorf("model has %d inputs and %d outputs, want 1/%d", len(ins), len(outs), nOut)
	}
	s := &ortSession{sess: sess, in: ins[0].Name}
	for _, o := range outs {
		s.outs = append(s.outs, o.Name)
	}
	return s, nil
}

// run feeds one float32 tensor and returns every output, copied out of the
// runtime's memory in model order.
func (s *ortSession) run(ctx context.Context, shape []int64, data []float32) ([]uidet.Output, error) {
	t, err := ort.CreateTensor[float32](shape, data)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	res, err := s.sess.Run(ctx, map[string]*ort.Tensor{s.in: t}, s.outs)
	if err != nil {
		return nil, err
	}
	outs := make([]uidet.Output, 0, len(s.outs))
	for _, name := range s.outs {
		o := res[name]
		view, err := ort.TensorData[float32](o)
		if err != nil {
			for _, r := range res {
				r.Close()
			}
			return nil, err
		}
		outs = append(outs, uidet.Output{Shape: append([]int64(nil), o.Shape()...), Data: append([]float32(nil), view...)})
	}
	for _, r := range res {
		r.Close()
	}
	return outs, nil
}

// singleRunner adapts a one-output session to ppocr.Runner.
type singleRunner struct{ *ortSession }

func (r *singleRunner) Run(ctx context.Context, shape []int64, data []float32) ([]int64, []float32, error) {
	outs, err := r.run(ctx, shape, data)
	if err != nil {
		return nil, nil, err
	}
	return outs[0].Shape, outs[0].Data, nil
}

// multiRunner adapts a session to uidet.Runner.
type multiRunner struct{ *ortSession }

func (r *multiRunner) Run(ctx context.Context, shape []int64, data []float32) ([]uidet.Output, error) {
	return r.run(ctx, shape, data)
}
