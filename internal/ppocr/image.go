package ppocr

import (
	"image"
)

// nchw writes the 3 colour channels of an RGBA image (bounds at origin) in
// NCHW float32 layout, applying (pix/255 - mean[c]) / std[c]. Channels are
// emitted in BGR order, as PaddleOCR models consume OpenCV BGR frames.
func nchw(src *image.RGBA, mean, std [3]float32) []float32 {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	out := make([]float32, 3*h*w)
	const scale = 1.0 / 255.0
	plane := h * w
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride:]
		for x := 0; x < w; x++ {
			i := x * 4
			// Channel 0 <- B, channel 1 <- G, channel 2 <- R.
			out[0*plane+y*w+x] = (float32(row[i+2])*scale - mean[0]) / std[0]
			out[1*plane+y*w+x] = (float32(row[i+1])*scale - mean[1]) / std[1]
			out[2*plane+y*w+x] = (float32(row[i])*scale - mean[2]) / std[2]
		}
	}
	return out
}
