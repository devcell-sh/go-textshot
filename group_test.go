package textshot

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestGroupElements_TwoZones(t *testing.T) {
	// Simulate a window with a dark sidebar (left) and a white content area (right).
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	// Sidebar: dark grey background, x 0..200
	draw.Draw(img, image.Rect(0, 0, 200, 600), image.NewUniform(color.RGBA{50, 50, 55, 255}), image.Point{}, draw.Src)
	// Content: white background, x 200..800
	draw.Draw(img, image.Rect(200, 0, 800, 600), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)

	elements := []Element{
		{ID: 0, Kind: KindText, Box: image.Rect(20, 50, 180, 70), Text: "Favorites"},
		{ID: 1, Kind: KindText, Box: image.Rect(20, 80, 180, 100), Text: "Desktop"},
		{ID: 2, Kind: KindText, Box: image.Rect(20, 110, 180, 130), Text: "Documents"},
		{ID: 3, Kind: KindText, Box: image.Rect(220, 50, 600, 70), Text: "file1.txt"},
		{ID: 4, Kind: KindText, Box: image.Rect(220, 80, 600, 100), Text: "file2.txt"},
		{ID: 5, Kind: KindText, Box: image.Rect(220, 110, 600, 130), Text: "file3.txt"},
	}

	GroupElements(elements, img)

	// Sidebar items (0,1,2) should share one group; content items (3,4,5) another.
	sidebarGroup := elements[0].Group
	contentGroup := elements[3].Group
	if sidebarGroup == contentGroup {
		t.Fatalf("sidebar and content got same group %d", sidebarGroup)
	}
	for _, i := range []int{0, 1, 2} {
		if elements[i].Group != sidebarGroup {
			t.Errorf("element %d (%s) group=%d, want %d (sidebar)", i, elements[i].Text, elements[i].Group, sidebarGroup)
		}
	}
	for _, i := range []int{3, 4, 5} {
		if elements[i].Group != contentGroup {
			t.Errorf("element %d (%s) group=%d, want %d (content)", i, elements[i].Text, elements[i].Group, contentGroup)
		}
	}
	t.Logf("sidebar=group %d, content=group %d", sidebarGroup, contentGroup)
}

func TestGroupElements_MenuBarSameGroup(t *testing.T) {
	// All menu items on the same white background should cluster together.
	img := image.NewRGBA(image.Rect(0, 0, 1000, 40))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{245, 245, 245, 255}), image.Point{}, draw.Src)

	elements := []Element{
		{ID: 0, Kind: KindText, Box: image.Rect(10, 5, 100, 35), Text: "File"},
		{ID: 1, Kind: KindText, Box: image.Rect(120, 5, 200, 35), Text: "Edit"},
		{ID: 2, Kind: KindText, Box: image.Rect(220, 5, 310, 35), Text: "View"},
		{ID: 3, Kind: KindText, Box: image.Rect(330, 5, 450, 35), Text: "Window"},
		{ID: 4, Kind: KindText, Box: image.Rect(470, 5, 550, 35), Text: "Help"},
	}

	GroupElements(elements, img)

	g := elements[0].Group
	for _, e := range elements[1:] {
		if e.Group != g {
			t.Errorf("%s group=%d, want %d (same as File)", e.Text, e.Group, g)
		}
	}
}

func TestGroupElements_ThreeZones(t *testing.T) {
	// Header (white, top), sidebar (dark, left), content (light grey, right).
	img := image.NewRGBA(image.Rect(0, 0, 1000, 800))
	// Header bar
	draw.Draw(img, image.Rect(0, 0, 1000, 50), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
	// Sidebar
	draw.Draw(img, image.Rect(0, 50, 200, 800), image.NewUniform(color.RGBA{40, 40, 45, 255}), image.Point{}, draw.Src)
	// Content
	draw.Draw(img, image.Rect(200, 50, 1000, 800), image.NewUniform(color.RGBA{240, 240, 240, 255}), image.Point{}, draw.Src)

	elements := []Element{
		// Header
		{ID: 0, Kind: KindText, Box: image.Rect(10, 10, 100, 30), Text: "File"},
		{ID: 1, Kind: KindText, Box: image.Rect(120, 10, 200, 30), Text: "Edit"},
		// Sidebar
		{ID: 2, Kind: KindText, Box: image.Rect(20, 70, 180, 90), Text: "Favorites"},
		{ID: 3, Kind: KindText, Box: image.Rect(20, 100, 180, 120), Text: "Desktop"},
		// Content
		{ID: 4, Kind: KindText, Box: image.Rect(220, 70, 600, 90), Text: "file1.txt"},
		{ID: 5, Kind: KindText, Box: image.Rect(220, 100, 600, 120), Text: "file2.txt"},
	}

	GroupElements(elements, img)

	groups := map[string]int{
		"header":  elements[0].Group,
		"sidebar": elements[2].Group,
		"content": elements[4].Group,
	}
	// All three zones should be different.
	if groups["header"] == groups["sidebar"] {
		t.Error("header and sidebar got same group")
	}
	if groups["header"] == groups["content"] {
		t.Error("header and content got same group")
	}
	if groups["sidebar"] == groups["content"] {
		t.Error("sidebar and content got same group")
	}
	// Items within each zone agree.
	if elements[1].Group != groups["header"] {
		t.Error("Edit not in header group")
	}
	if elements[3].Group != groups["sidebar"] {
		t.Error("Desktop not in sidebar group")
	}
	if elements[5].Group != groups["content"] {
		t.Error("file2 not in content group")
	}
	t.Logf("header=%d sidebar=%d content=%d", groups["header"], groups["sidebar"], groups["content"])
}
