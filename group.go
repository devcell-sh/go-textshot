package textshot

import (
	"image"
	"image/color"
	"math"
	"sort"

	"github.com/devcell-sh/go-textshot/internal/imgutil"
)

// GroupElements clusters elements into spatial groups: elements that share
// the same background region and are close together belong to the same
// group. Each element's Group field is set to a 0-based index; groups are
// numbered in reading order (top-to-bottom, left-to-right by the group's
// topmost element).
//
// The clustering uses two signals from the pixels alone (no model):
//   - Background color: the dominant color in a ring around each element's
//     box. Elements whose backgrounds differ by more than colorTol in any
//     channel are in different groups.
//   - Spatial proximity: elements whose boxes are within gapTol pixels
//     vertically and whose horizontal extents overlap (or are close) are
//     neighbors. gapTol defaults to 2x the median line height when zero.
func GroupElements(elements []Element, img image.Image) {
	boxes := make([]image.Rectangle, len(elements))
	for i, e := range elements {
		boxes[i] = e.Box
	}
	groups := GroupBoxes(boxes, img)
	for i := range elements {
		elements[i].Group = groups[i]
	}
}

// GroupBoxes clusters bounding boxes into spatial groups by background
// color and proximity. Returns a slice parallel to boxes where each value
// is a 0-based group index in reading order.
func GroupBoxes(boxes []image.Rectangle, img image.Image) []int {
	n := len(boxes)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return []int{0}
	}

	src := imgutil.ToRGBA(img)
	bgs := make([]color.RGBA, n)
	for i, b := range boxes {
		bgs[i] = boxBackground(src, b)
	}

	heights := make([]int, 0, n)
	for _, b := range boxes {
		if h := b.Dy(); h > 0 {
			heights = append(heights, h)
		}
	}
	sort.Ints(heights)
	median := 20
	if len(heights) > 0 {
		median = heights[len(heights)/2]
	}
	gapTol := median * 2

	// Union-find.
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if !colorClose(bgs[i], bgs[j], 12) {
				continue
			}
			if !spatiallyClose(boxes[i], boxes[j], gapTol) {
				continue
			}
			union(i, j)
		}
	}

	// Collect components and assign IDs in reading order.
	comps := map[int][]int{}
	for i := range boxes {
		r := find(i)
		comps[r] = append(comps[r], i)
	}
	type grp struct {
		topY, leftX int
		members     []int
	}
	groups := make([]grp, 0, len(comps))
	for _, members := range comps {
		topY, leftX := math.MaxInt32, math.MaxInt32
		for _, m := range members {
			if boxes[m].Min.Y < topY {
				topY = boxes[m].Min.Y
			}
			if boxes[m].Min.X < leftX {
				leftX = boxes[m].Min.X
			}
		}
		groups = append(groups, grp{topY, leftX, members})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].topY != groups[j].topY {
			return groups[i].topY < groups[j].topY
		}
		return groups[i].leftX < groups[j].leftX
	})
	result := make([]int, n)
	for gid, g := range groups {
		for _, m := range g.members {
			result[m] = gid
		}
	}
	return result
}

func boxBackground(img *image.RGBA, box image.Rectangle) color.RGBA {
	ring := box.Inset(-4).Intersect(img.Rect)
	if ring.Empty() {
		return color.RGBA{255, 255, 255, 255}
	}
	counts := map[color.RGBA]int{}
	for y := ring.Min.Y; y < ring.Max.Y; y++ {
		for x := ring.Min.X; x < ring.Max.X; x++ {
			if (image.Point{x, y}).In(box.Inset(-1)) {
				continue
			}
			r := img.Pix[(y*img.Stride)+(x*4):]
			c := color.RGBA{r[0] & 0xFC, r[1] & 0xFC, r[2] & 0xFC, 255}
			counts[c]++
		}
	}
	best := color.RGBA{255, 255, 255, 255}
	bestN := 0
	for c, n := range counts {
		if n > bestN {
			bestN = n
			best = c
		}
	}
	return best
}

func colorClose(a, b color.RGBA, tol int) bool {
	return absDiffU8(a.R, b.R) < tol && absDiffU8(a.G, b.G) < tol && absDiffU8(a.B, b.B) < tol
}

func absDiffU8(a, b uint8) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

func spatiallyClose(a, b image.Rectangle, gapTol int) bool {
	vgap := 0
	if a.Max.Y <= b.Min.Y {
		vgap = b.Min.Y - a.Max.Y
	} else if b.Max.Y <= a.Min.Y {
		vgap = a.Min.Y - b.Max.Y
	}
	if vgap >= gapTol {
		return false
	}
	hgap := 0
	if a.Max.X <= b.Min.X {
		hgap = b.Min.X - a.Max.X
	} else if b.Max.X <= a.Min.X {
		hgap = a.Min.X - b.Max.X
	}
	return hgap < gapTol
}
