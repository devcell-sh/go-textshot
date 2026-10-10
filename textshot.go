// Package textshot extracts text lines and UI elements from screenshots.
//
// It runs the PP-OCRv6 detection and recognition models, and on demand the
// OmniParser icon_detect_v3 element detector, through ONNX Runtime. The
// runtime shared library and the models are fetched on first use into a
// cache directory, verified against pinned SHA-256 digests, and reused
// afterwards. No system packages are required beyond a C compiler at build
// time (the ONNX Runtime binding is a thin cgo shim that dlopens the
// library).
//
// Typical use:
//
//	eng, err := textshot.New(ctx, textshot.Options{})
//	...
//	defer eng.Close()
//	results, err := eng.DetectText(ctx, img)
//	for _, line := range textshot.Lines(results) { ... }
//
//	elements, err := eng.DetectElements(ctx, img)   // buttons, icons and text with boxes
//
// Everything is derived from pixels; there is no accessibility tree.
// Window and panel hierarchy, the mouse cursor, font metrics, exact design
// token colours and checked state are deliberately not reported (see the
// Limitations section of the README). The library exposes no plug-in
// interface: Engine owns runtime and model loading end to end. The seam a
// contributor would implement to add a backend is the Runner interface in
// internal/ppocr and internal/uidet, which lives under internal/ and so
// needs a pull request against this module. Issues and pull requests:
// https://github.com/devcell-sh/go-textshot (see CONTRIBUTING.md).
package textshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/devcell-sh/go-textshot/internal/imgutil"
	"github.com/devcell-sh/go-textshot/internal/ppocr"
	"github.com/devcell-sh/go-textshot/internal/screen"
	"github.com/devcell-sh/go-textshot/internal/uidet"
)

// Environment variables honoured when the matching Options field is empty.
const (
	// EnvCacheDir overrides the cache directory.
	EnvCacheDir = "TEXTSHOT_CACHE_DIR"
	// EnvLibraryPath points at a libonnxruntime shared library (file or the
	// directory containing it) to use instead of downloading one.
	EnvLibraryPath = "TEXTSHOT_ONNXRUNTIME_LIB"
)

// issuesURL is where ErrUnavailable messages send unsupported platforms.
const issuesURL = "https://github.com/devcell-sh/go-textshot/issues"

// ErrUnavailable is returned (wrapped) when OCR cannot run on this build or
// platform: the binary was built without cgo, or no ONNX Runtime release
// exists for the host OS/architecture.
var ErrUnavailable = errors.New("textshot: OCR unavailable")

// Options configures New. The zero value downloads everything into the
// default cache and uses all CPUs.
type Options struct {
	// CacheDir holds the downloaded runtime and models. Defaults to
	// $TEXTSHOT_CACHE_DIR, then os.UserCacheDir()/textshot.
	CacheDir string
	// LibraryPath is an explicit libonnxruntime shared library (file or the
	// directory containing it). Defaults to $TEXTSHOT_ONNXRUNTIME_LIB, then
	// the cached download. When set, nothing is downloaded for the runtime
	// and a missing file is an error.
	LibraryPath string
	// MaxSideLen caps the longer image side fed to the detector (default
	// 1920). Images are only downscaled, never upscaled. Lower values are
	// faster but shrink small UI text.
	MaxSideLen int
	// Threads is the intra-op thread count for inference (default:
	// runtime.GOMAXPROCS(0), which respects container CPU limits).
	Threads int
	// MinScore drops lines whose recognition confidence is below it
	// (default 0.5; negative keeps everything). Icon glyphs and decorations
	// typically decode as low-confidence junk.
	MinScore float32
	// ElementMaxSideLen caps the longer image side fed to the element
	// detector (default 1280, downscale only). The detector is far heavier
	// than OCR: on two CPUs a 1080p frame takes ~5 s at 1280 and ~12 s at
	// 1920, while 640 misses most UI controls.
	ElementMaxSideLen int
	// ElementMinScore drops detected elements whose confidence is below it
	// (default 0.1). Icons on busy screens often score 0.1-0.3.
	ElementMinScore float32
	// Logger receives download progress and diagnostics; nil discards.
	Logger *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.CacheDir == "" {
		o.CacheDir = os.Getenv(EnvCacheDir)
	}
	if o.CacheDir == "" {
		o.CacheDir = DefaultCacheDir()
	}
	if o.LibraryPath == "" {
		o.LibraryPath = os.Getenv(EnvLibraryPath)
	}
	if o.MaxSideLen <= 0 {
		o.MaxSideLen = 1920
	}
	if o.Threads <= 0 {
		o.Threads = runtime.GOMAXPROCS(0)
	}
	if o.MinScore == 0 {
		o.MinScore = 0.5
	}
	if o.ElementMaxSideLen <= 0 {
		o.ElementMaxSideLen = 1280
	}
	if o.ElementMinScore <= 0 {
		o.ElementMinScore = 0.1
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

// DefaultCacheDir is os.UserCacheDir()/textshot (for example
// ~/.cache/textshot on Linux, ~/Library/Caches/textshot on macOS), falling
// back to the temp directory when the user cache dir is unknown.
func DefaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "textshot")
}

// TextResult is one recognized line.
type TextResult struct {
	// Box is the line's bounding box in the source image's coordinates.
	Box image.Rectangle
	// Text is the recognized string.
	Text string
	// Score is the mean per-character confidence in [0,1].
	Score float32
	// Value and UOM are the quantity the line reports, when it does: a
	// percentage ("10% complete" is 10 percent) or a span of time ("About
	// 3 minutes remaining..." is 3 minute, "1 hour and 5 minutes" is 65
	// minute). UOM is empty on lines without one. See ParseQuantity.
	Value float64
	UOM   Unit
}

// Lines returns the text of each result, in order.
func Lines(rs []TextResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Text
	}
	return out
}

// Kind classifies an Element.
type Kind string

const (
	// KindButton is an interactable region with a text label inside it:
	// a push button, menu item, list row, link, labelled field.
	KindButton Kind = "button"
	// KindIcon is an interactable region without text: a toolbar icon,
	// window control, checkbox, unlabelled field.
	KindIcon Kind = "icon"
	// KindText is a text line that is not part of any interactable region:
	// a title, description, status message.
	KindText Kind = "text"
	// KindProgress is a text line (as KindText) that reports a quantity:
	// a percentage ("10% complete") or a span of time ("About 3 minutes
	// remaining..."). Value and UOM carry the number and its unit.
	KindProgress Kind = "progress"
)

// Theme is the overall brightness of a screen.
type Theme string

const (
	// ThemeLight is a predominantly light screen: the backdrop's luminance
	// is at least mid-grey (128).
	ThemeLight Theme = "light"
	// ThemeDark is a predominantly dark screen: the backdrop's luminance is
	// below mid-grey (128).
	ThemeDark Theme = "dark"
)

// Style holds what the pixels inside and around an element's box say
// about it. Colours are measured, not design tokens: a disabled control
// is detected by its low contrast, a selected row or default button by
// its accent fill, a focused control by the saturated ring around it.
type Style struct {
	// Background is the dominant colour inside the box.
	Background color.RGBA
	// Foreground is the dominant colour that contrasts with Background
	// (text, glyph); equals Background when nothing does.
	Foreground color.RGBA
	// Contrast is the WCAG contrast ratio of Foreground over Background,
	// in [1,21]. Readable text is above 4.5, greyed-out text below 3.
	Contrast float64
	// Disabled is true for buttons and text whose Contrast is below 3.
	Disabled bool
	// Highlighted is true when Background is an accent (saturated) colour:
	// a selected row, a default button, a tinted field.
	Highlighted bool
	// Focused is true when a saturated ring surrounds the box.
	Focused bool
	// Ring is the colour of the focus ring; zero unless Focused.
	Ring color.RGBA
}

// Screen is everything DetectScreen reads off one screenshot.
type Screen struct {
	// Size is the screenshot's width and height in pixels.
	Size image.Point
	// Theme is dark when the backdrop is darker than mid-grey.
	Theme Theme
	// Backdrop is the dominant colour of the whole screenshot.
	Backdrop color.RGBA
	// Elements in reading order: buttons, icons, text.
	Elements []Element
}

// Element is one UI element found on a screenshot.
type Element struct {
	// ID is the element's 0-based index in reading order within the list
	// it was returned in (a set-of-marks number). It is only meaningful
	// for that frame.
	ID int
	// Key names the element the way a role+name locator would:
	// "button:Continue", "text:Disk Utility", "icon@700,310" (top-left
	// corner for elements without text), with "#2", "#3" appended to
	// repeats in reading order. Keys are deterministic for a given frame
	// and stay the same across frames while the layout does not move, so
	// callers can find an element again by Key and confirm it by Box.
	Key string
	// Kind is button (interactable with a label), icon (interactable
	// without one), text (a line outside any interactable) or progress
	// (such a line that reports a quantity).
	Kind Kind
	// Box is the element's bounding box in the source image's coordinates.
	Box image.Rectangle
	// Score is the detector's confidence for interactables and the
	// recognition confidence for text, in [0,1].
	Score float32
	// Text is the label inside the element (reading order, space-joined),
	// the text line itself, or for an icon the text line right next to
	// it on the same row; empty otherwise.
	Text string
	// Style is what the pixels say about the element.
	Style Style
	// Value and UOM are the quantity Text reports, when it does: a
	// percentage or a span of time (see TextResult.Value). Always set on
	// KindProgress; also set on a button or icon whose label carries one.
	// UOM is empty otherwise.
	Value float64
	UOM   Unit
}

// Engine is a loaded OCR pipeline. It is safe for concurrent use.
type Engine struct {
	pipeline *ppocr.Pipeline
	minScore float32
	logger   *slog.Logger
	closers  []func() error

	// openElements loads the element detector on first use, so engines
	// that only read text never download or load its 232 MB model.
	openElements func(ctx context.Context) (*uidet.Detector, func() error, error)
	elemMu       sync.Mutex
	elements     *uidet.Detector
}

// New prepares an Engine: it resolves or downloads the ONNX Runtime
// library and the models, then loads them. The first call on a machine
// downloads roughly 40 MB; later calls reuse the cache. Errors wrapping
// ErrUnavailable mean OCR cannot work on this build or host at all.
func New(ctx context.Context, o Options) (*Engine, error) {
	return newEngine(ctx, o.withDefaults())
}

// Prefetch downloads the runtime library and the OCR models into the cache
// without loading them. It needs no cgo, so it can warm a cache from any
// build. The element model is fetched separately by PrefetchElements.
func Prefetch(ctx context.Context, o Options) error {
	o = o.withDefaults()
	if _, err := ensureRuntime(ctx, o); err != nil {
		return err
	}
	_, _, err := ensureModels(ctx, o)
	return err
}

// PrefetchElements downloads the element detection model (232 MB) into
// the cache without loading it.
func PrefetchElements(ctx context.Context, o Options) error {
	_, err := ensureModel(ctx, o.withDefaults(), ElementModel)
	return err
}

// DetectText finds and recognizes every text line in img, returned in
// reading order (top to bottom, left to right).
func (e *Engine) DetectText(ctx context.Context, img image.Image) ([]TextResult, error) {
	start := time.Now()
	lines, err := e.pipeline.Recognize(ctx, img)
	if err != nil {
		return nil, err
	}
	out := make([]TextResult, 0, len(lines))
	dropped := 0
	for _, l := range lines {
		if l.Score < e.minScore || iconGlyph(l) {
			dropped++
			continue
		}
		q, _ := ParseQuantity(l.Text)
		out = append(out, TextResult{Box: l.Box, Text: l.Text, Score: l.Score, Value: q.Value, UOM: q.UOM})
	}
	e.logger.Debug("textshot: detect text", "size", img.Bounds().Size(), "lines", len(out), "dropped", dropped, "elapsed", time.Since(start))
	return out, nil
}

// DetectInteractables finds the interactable regions (buttons, icons, menu
// items, fields) in img with the OmniParser icon_detect_v3 model, in reading
// order. Every result has KindIcon and no Text; see MergeElements. The
// first call downloads the model when it is not cached and loads it.
func (e *Engine) DetectInteractables(ctx context.Context, img image.Image) ([]Element, error) {
	det, err := e.elementDetector(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	boxes, err := det.Detect(ctx, img)
	if err != nil {
		return nil, err
	}
	out := make([]Element, len(boxes))
	for i, b := range boxes {
		out[i] = Element{Kind: KindIcon, Box: b.Rect, Score: b.Score}
	}
	assignIDs(out)
	e.logger.Debug("textshot: detect interactables", "size", img.Bounds().Size(), "elements", len(out), "elapsed", time.Since(start))
	return out, nil
}

// assignIDs numbers elements in list order, normalizes their Text to
// single-spaced words and derives their Keys.
func assignIDs(els []Element) {
	seen := make(map[string]int, len(els))
	for i := range els {
		e := &els[i]
		e.ID = i
		key := string(e.Kind)
		e.Text = strings.Join(strings.Fields(e.Text), " ")
		if e.Text != "" {
			key += ":" + e.Text
		} else {
			key += "@" + strconv.Itoa(e.Box.Min.X) + "," + strconv.Itoa(e.Box.Min.Y)
		}
		seen[key]++
		if n := seen[key]; n > 1 {
			key += "#" + strconv.Itoa(n)
		}
		e.Key = key
	}
}

func (e *Engine) elementDetector(ctx context.Context) (*uidet.Detector, error) {
	e.elemMu.Lock()
	defer e.elemMu.Unlock()
	if e.elements != nil {
		return e.elements, nil
	}
	det, closer, err := e.openElements(ctx)
	if err != nil {
		return nil, err
	}
	e.elements = det
	e.closers = append(e.closers, closer)
	return det, nil
}

// DetectScreen runs OCR and the element detector on img and adds what the
// pixels show: per-element colours and state, and the screen theme. See
// ComposeScreen for the rules.
func (e *Engine) DetectScreen(ctx context.Context, img image.Image) (*Screen, error) {
	lines, err := e.DetectText(ctx, img)
	if err != nil {
		return nil, err
	}
	boxes, err := e.DetectInteractables(ctx, img)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	s := ComposeScreen(img, boxes, lines)
	e.logger.Debug("textshot: compose screen", "theme", s.Theme, "elements", len(s.Elements), "elapsed", time.Since(start))
	return s, nil
}

// DetectElements is DetectScreen's element list: buttons (interactables
// with their label), icons (interactables without text) and free text,
// in reading order.
func (e *Engine) DetectElements(ctx context.Context, img image.Image) ([]Element, error) {
	s, err := e.DetectScreen(ctx, img)
	if err != nil {
		return nil, err
	}
	return s.Elements, nil
}

// ComposeScreen builds a Screen from detector output and the pixels of
// img, with no model involved: interactables and lines are merged by
// MergeElements, each element's Style is measured from its box (colours,
// contrast, disabled, highlighted, focus ring), and Theme follows the
// backdrop's luminance.
func ComposeScreen(img image.Image, interactables []Element, lines []TextResult) *Screen {
	src := imgutil.ToRGBA(img)
	all := MergeElements(interactables, lines)
	for i := range all {
		all[i].Style = measureStyle(src, all[i])
	}
	backdrop := screen.Backdrop(src)
	theme := ThemeLight
	if screen.Luminance(backdrop) < 128 {
		theme = ThemeDark
	}
	return &Screen{Size: src.Rect.Size(), Theme: theme, Backdrop: backdrop, Elements: all}
}

// Diff reports the bounding box of what changed between two screenshots
// and the fraction of pixels that did (0 when nothing moved, 1 when the
// sizes differ). Poll it to know when a screen has settled.
func Diff(prev, next image.Image) (image.Rectangle, float64) {
	return screen.Diff(imgutil.ToRGBA(prev), imgutil.ToRGBA(next), 16)
}

// measureStyle reads an element's colours and state off its pixels.
// Icons are left without Disabled/Highlighted: their fills are colourful
// by nature, so neither contrast nor accent says anything about state.
func measureStyle(src *image.RGBA, e Element) Style {
	bg, fg, contrast := screen.Colors(src, e.Box)
	s := Style{Background: bg, Foreground: fg, Contrast: contrast}
	textual := e.Kind == KindText || e.Kind == KindProgress
	if e.Kind == KindButton || textual {
		s.Disabled = contrast < 3
		s.Highlighted = screen.Accent(bg)
	}
	// A saturated fill (a red close button) would read as its own ring.
	if !textual && !screen.Accent(bg) {
		if ring, ok := screen.Ring(src, e.Box); ok {
			s.Focused, s.Ring = true, ring
		}
	}
	return s
}

// MergeElements labels interactables with the text lines inside them and
// returns interactables and leftover lines as one list in reading order,
// with fresh IDs and Keys. A line belongs to the smallest interactable
// that covers at least half of it and is not much taller than it, so
// panels do not swallow their rows; an interactable with text becomes
// KindButton, one without stays KindIcon, and unassigned lines become
// KindText, or KindProgress when they report a quantity (see
// ParseQuantity; Value and UOM are filled on every element whose Text
// has one). A detector box below 0.3 that is just a text line again (the
// line covers 90% of it) is dropped first: it is noise, not a control. An icon with a free text line right next to it on the same
// row (a list row's icon and its title) takes that line as its Text,
// keeping KindIcon: the key reads "icon:Disk Utility".
func MergeElements(interactables []Element, lines []TextResult) []Element {
	interactables = dropRestatedText(interactables, lines)
	out := make([]Element, len(interactables), len(interactables)+len(lines))
	labels := make([][]string, len(interactables))
	for i, el := range interactables {
		out[i] = Element{Kind: KindIcon, Box: el.Box, Score: el.Score}
	}
	for _, l := range lines {
		best := -1
		for i, el := range interactables {
			if !labelOf(el.Box, l.Box) {
				continue
			}
			if best < 0 || area(el.Box) < area(interactables[best].Box) {
				best = i
			}
		}
		if best < 0 {
			out = append(out, Element{Kind: KindText, Box: l.Box, Score: l.Score, Text: l.Text})
			continue
		}
		labels[best] = append(labels[best], l.Text)
	}
	for i, ls := range labels {
		if len(ls) > 0 {
			out[i].Kind = KindButton
			out[i].Text = strings.Join(ls, " ")
		}
	}
	for i := range interactables {
		if out[i].Kind == KindIcon {
			out[i].Text = adjacentLabel(out[i].Box, out[len(interactables):])
		}
	}
	for i := range out {
		q, ok := ParseQuantity(out[i].Text)
		if !ok {
			continue
		}
		out[i].Value, out[i].UOM = q.Value, q.UOM
		if out[i].Kind == KindText {
			out[i].Kind = KindProgress
		}
	}
	imgutil.SortReadingOrder(out, func(e Element) image.Rectangle { return e.Box })
	assignIDs(out)
	return out
}

// dropRestatedText removes low-confidence detector boxes that are just an
// OCR line again: a box with Score below 0.3 that a single line covers by
// at least 90%. A real control has a hit area around its label, so a box
// as tight as the glyphs is detector noise on text (a status line on a
// blank install screen reads as a 0.13 "button"); the line stays free, as
// text or progress.
func dropRestatedText(interactables []Element, lines []TextResult) []Element {
	out := interactables[:0:0]
	for _, el := range interactables {
		restated := false
		if el.Score < 0.3 {
			for _, l := range lines {
				if 10*area(el.Box.Intersect(l.Box)) >= 9*area(el.Box) {
					restated = true
					break
				}
			}
		}
		if !restated {
			out = append(out, el)
		}
	}
	return out
}

// adjacentLabel returns the text of the topmost free text line that sits
// on the same row to the right of box (a list row's icon, a checkbox, a
// field with its caption), within 1.5x the box's height; empty when
// none. Topmost, not nearest: a two-line list row has its title above
// its description, and both start at the same x.
func adjacentLabel(box image.Rectangle, texts []Element) string {
	for _, t := range texts {
		overlapY := min(box.Max.Y, t.Box.Max.Y) - max(box.Min.Y, t.Box.Min.Y)
		if 2*overlapY < min(box.Dy(), t.Box.Dy()) {
			continue
		}
		if gap := t.Box.Min.X - box.Max.X; gap >= -2 && 2*gap <= 3*box.Dy() {
			return t.Text
		}
	}
	return ""
}

// labelOf reports whether the text box line labels the interactable el:
// el covers at least half of line, and line is at least 30% of el's
// height (a single row inside a tall panel is content, not a label).
func labelOf(el, line image.Rectangle) bool {
	inter := el.Intersect(line)
	if inter.Empty() || area(line) == 0 {
		return false
	}
	return 2*area(inter) >= area(line) && 10*line.Dy() >= 3*el.Dy()
}

func area(r image.Rectangle) int {
	return r.Dx() * r.Dy()
}

// iconGlyph reports whether a recognized line is an icon misread as a
// character: one or two CJK ideographs in a near-square box. The
// recognizer's dictionary is mostly Chinese, so a flag, a logo or a
// cursor decodes as a confident ideograph; real CJK text is wider.
func iconGlyph(l ppocr.Line) bool {
	runes := []rune(strings.TrimSpace(l.Text))
	if len(runes) == 0 || len(runes) > 2 || l.Box.Dx() > 3*l.Box.Dy()/2 {
		return false
	}
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

// Close releases the model sessions. The ONNX Runtime library itself stays
// loaded for the life of the process.
func (e *Engine) Close() error {
	var errs []error
	for i := len(e.closers) - 1; i >= 0; i-- {
		if err := e.closers[i](); err != nil {
			errs = append(errs, err)
		}
	}
	e.closers = nil
	return errors.Join(errs...)
}

// MarshalJSON renders the colours as "#rrggbb" and omits Ring unless the
// element is focused.
func (s Style) MarshalJSON() ([]byte, error) {
	out := struct {
		Background  string  `json:"background"`
		Foreground  string  `json:"foreground"`
		Contrast    float64 `json:"contrast"`
		Disabled    bool    `json:"disabled"`
		Highlighted bool    `json:"highlighted"`
		Focused     bool    `json:"focused"`
		Ring        string  `json:"ring,omitempty"`
	}{hexColor(s.Background), hexColor(s.Foreground), math.Round(s.Contrast*100) / 100, s.Disabled, s.Highlighted, s.Focused, ""}
	if s.Focused {
		out.Ring = hexColor(s.Ring)
	}
	return json.Marshal(out)
}

// MarshalJSON flattens the box to x0,y0,x1,y1 and omits value and uom
// when the line reports no quantity.
func (r TextResult) MarshalJSON() ([]byte, error) {
	out := struct {
		X0    int      `json:"x0"`
		Y0    int      `json:"y0"`
		X1    int      `json:"x1"`
		Y1    int      `json:"y1"`
		Score float32  `json:"score"`
		Text  string   `json:"text"`
		Value *float64 `json:"value,omitempty"`
		UOM   Unit     `json:"uom,omitempty"`
	}{r.Box.Min.X, r.Box.Min.Y, r.Box.Max.X, r.Box.Max.Y, r.Score, r.Text, nil, r.UOM}
	if r.UOM != "" {
		out.Value = &r.Value
	}
	return json.Marshal(out)
}

// MarshalJSON flattens the box to x0,y0,x1,y1 and omits empty text, and
// value and uom when the element reports no quantity.
func (e Element) MarshalJSON() ([]byte, error) {
	out := struct {
		ID    int      `json:"id"`
		Key   string   `json:"key"`
		Kind  Kind     `json:"kind"`
		X0    int      `json:"x0"`
		Y0    int      `json:"y0"`
		X1    int      `json:"x1"`
		Y1    int      `json:"y1"`
		Score float32  `json:"score"`
		Text  string   `json:"text,omitempty"`
		Value *float64 `json:"value,omitempty"`
		UOM   Unit     `json:"uom,omitempty"`
		Style Style    `json:"style"`
	}{e.ID, e.Key, e.Kind, e.Box.Min.X, e.Box.Min.Y, e.Box.Max.X, e.Box.Max.Y, e.Score, e.Text, nil, e.UOM, e.Style}
	if e.UOM != "" {
		out.Value = &e.Value
	}
	return json.Marshal(out)
}

// MarshalJSON renders the backdrop as "#rrggbb".
func (s Screen) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Width    int       `json:"width"`
		Height   int       `json:"height"`
		Theme    Theme     `json:"theme"`
		Backdrop string    `json:"backdrop"`
		Elements []Element `json:"elements"`
	}{s.Size.X, s.Size.Y, s.Theme, hexColor(s.Backdrop), s.Elements})
}

func hexColor(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}
