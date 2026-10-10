package ppocr

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

// dictBytes is the PP-OCRv6 character dictionary (ppocrv6_dict.txt,
// Apache-2.0, from PaddlePaddle/PaddleOCR): one character per line.
//
//go:embed ppocrv6_dict.txt
var dictBytes []byte

var (
	defaultDictOnce sync.Once
	defaultDict     *Dict
)

// DefaultDict returns the embedded PP-OCRv6 dictionary.
func DefaultDict() *Dict {
	defaultDictOnce.Do(func() {
		d, err := ParseDict(dictBytes)
		if err != nil {
			panic(fmt.Sprintf("ppocr: embedded dict: %v", err))
		}
		defaultDict = d
	})
	return defaultDict
}

// Dict maps CTC output classes to characters using the PaddleOCR layout:
// class 0 is the CTC blank, classes 1..Len() are dictionary characters in
// file order, and class Len()+1 is the space character (use_space_char).
type Dict struct {
	chars []rune
}

// ParseDict parses a dictionary with one character per line. Blank lines
// are skipped and only the first rune of each line is used.
func ParseDict(data []byte) (*Dict, error) {
	var chars []rune
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		chars = append(chars, []rune(line)[0])
	}
	if len(chars) == 0 {
		return nil, fmt.Errorf("ppocr: dict is empty")
	}
	return &Dict{chars: chars}, nil
}

// NewDict builds a dictionary from a character slice (tests, custom dicts).
func NewDict(chars []rune) *Dict {
	return &Dict{chars: append([]rune(nil), chars...)}
}

// Len is the number of dictionary characters, excluding blank and space.
func (d *Dict) Len() int { return len(d.chars) }

// ClassCount is the model output width: blank + Len() + space.
func (d *Dict) ClassCount() int { return len(d.chars) + 2 }

// SpaceClass is the output class of the space character.
func (d *Dict) SpaceClass() int { return len(d.chars) + 1 }

// At decodes an output class. It reports false for the blank class and for
// classes outside the layout.
func (d *Dict) At(class int) (rune, bool) {
	switch {
	case class <= 0:
		return 0, false
	case class <= len(d.chars):
		return d.chars[class-1], true
	case class == d.SpaceClass():
		return ' ', true
	default:
		return 0, false
	}
}

// Validate checks the dictionary against the model's output class count.
func (d *Dict) Validate(modelClasses int) error {
	if d.ClassCount() != modelClasses {
		return fmt.Errorf("ppocr: dict has %d chars (%d classes with blank and space) but model outputs %d classes",
			d.Len(), d.ClassCount(), modelClasses)
	}
	return nil
}
