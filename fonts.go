package main

import (
	"math"
	"strings"
)

// Crystal's PDF export does not set text at the point size the report names:
// it sets the size whose glyphs fill the GDI cell of that size, so an 8 pt
// Arial field prints at 7.15 pt. Measured on Crystal PDF exports:
//
//	Arial        8→7.15  9→8.1   10→8.95  11→9.85  12→10.75  14→12.6  16→14.3
//	Arial Bold   8→6.95  9→7.8   10→8.75
//	Times        8→7.05  10→8.95 12→10.65 14→12.5
//
// Other sizes scale by the family's ratio; the result is whole twips. A
// family not measured (Courier, Arial Narrow) scales by
// its cell height: 2048 font units over ascent plus descent.
var printedSizes = map[string]map[float64]float64{
	"arial":      {8: 7.15, 9: 8.1, 10: 8.95, 11: 9.85, 12: 10.75, 14: 12.6, 16: 14.3},
	"arial bold": {8: 6.95, 9: 7.8, 10: 8.75},
	"times":      {8: 7.05, 10: 8.95, 12: 10.65, 14: 12.5},
	"times bold": {8: 7.05},
}

var printedRatio = map[string]float64{
	"arial": 0.895, "arial bold": 0.87, "times": 0.885, "times bold": 0.88,
}

// printedSize is the em size Crystal sets a font at, in points. Both the
// HTML and the PDF writer take it through font.setting.
func printedSize(name string, size float64, bold bool) float64 {
	switch fontFamily(name) {
	case "Sans", "Serif":
		key := fontKey(Format{FontName: name, Bold: bold})
		if s, ok := printedSizes[key][size]; ok {
			return s
		}
		return math.Round(size*printedRatio[key]*20) / 20
	}
	ratio := 2048 / cellUnits[fontFamily(name)]
	if bold {
		ratio *= 0.972
	}
	return math.Round(size*ratio*20) / 20
}

// Advance widths of ASCII 32..126 in 1/1000 em, from Liberation Sans and
// Serif, which are metric-compatible with Arial and Times New Roman. Other
// characters count as an "n".
var glyphWidths = map[string][]int{
	"arial": {
		278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
		1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
		333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
		556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
	},
	"arial bold": {
		278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278,
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611,
		975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778,
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556,
		333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611,
		611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584,
	},
	"times": {
		250, 333, 408, 500, 500, 833, 778, 180, 333, 333, 500, 564, 250, 333, 250, 278,
		500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 278, 278, 564, 564, 564, 444,
		921, 722, 667, 667, 722, 611, 556, 722, 722, 333, 389, 722, 611, 889, 722, 722,
		556, 722, 667, 556, 611, 722, 722, 944, 722, 722, 611, 333, 278, 333, 469, 500,
		333, 444, 500, 444, 500, 444, 333, 500, 500, 278, 278, 500, 278, 778, 500, 500,
		500, 500, 333, 389, 278, 500, 500, 722, 500, 500, 444, 480, 200, 480, 541,
	},
	"times bold": {
		250, 333, 555, 500, 500, 1000, 833, 278, 333, 333, 500, 570, 250, 333, 250, 278,
		500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 333, 333, 570, 570, 570, 500,
		930, 722, 667, 722, 722, 667, 611, 778, 778, 389, 500, 778, 667, 944, 722, 778,
		611, 778, 722, 556, 667, 722, 722, 1000, 722, 722, 667, 333, 278, 333, 581, 500,
		333, 500, 556, 444, 556, 444, 333, 500, 556, 278, 333, 556, 278, 833, 556, 500,
		556, 556, 444, 389, 333, 556, 500, 722, 500, 500, 444, 394, 220, 394, 520,
	},
}

// fontKey names the family and weight the size and width tables use.
func fontKey(f Format) string {
	family := "arial"
	if strings.Contains(strings.ToLower(f.FontName), "times") {
		family = "times"
	}
	if f.Bold {
		family += " bold"
	}
	return family
}

// textWidth is the width Crystal lays s out at, in twips. Crystal measures
// narrower than it prints: on a measured export an 8 pt line prints at
// 7.15 pt and overhangs its box by 2 pt, yet one more letter (4 pt) is cut.
// Those clip points bound the layout scale to 0.86..0.879 of the nominal
// size; 0.87 sits inside.
func textWidth(s string, f Format) float64 {
	widths := glyphWidths[fontKey(f)]
	sum := 0
	for _, r := range s {
		if r >= 32 && r <= 126 {
			sum += widths[r-32]
		} else {
			sum += widths['n'-32]
		}
	}
	return float64(sum) * f.FontSize * layoutScale * 20 / 1000
}

const layoutScale = 0.87

// fitText cuts a one-line text to the characters that fit the object's width
// whole: Crystal prints no part of a character, where a browser clips one.
func fitText(s string, f Format, width int) string {
	s = strings.TrimRight(s, " ")
	for s != "" && textWidth(s, f) > float64(width) {
		r := []rune(s)
		s = string(r[:len(r)-1])
	}
	return s
}
