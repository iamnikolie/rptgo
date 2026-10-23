package main

import (
	"fmt"
	"strings"
)

// The engine lays pages out into a display list; html.go and pdf.go write it.
// Every position is in twips from the page's top-left corner.

type itemKind int

const (
	itemText   itemKind = iota // text in its object's box, clipped to it
	itemFill                   // a filled black rectangle: a line object
	itemBox                    // a box object: a 0.75pt frame inside its bounds
	itemBorder                 // an object's borders, drawn inside x, y, w, h
	itemImage                  // a picture scaled to x, y, w, h
	itemChart                  // a chart drawn in x, y, w, h
)

type item struct {
	kind       itemKind
	x, y, w, h int
	text       string // raw text; pageSlot and totalSlot stand for page numbers
	font       font
	align      string // Left, Center, Right
	border     borders
	color      string // "#RRGGBB" for text and fills; "" is black
	image      *picture
	chart      *chartDraw
}

type font struct {
	name                    string // Crystal's font name, "Arial"
	size                    float64
	bold, italic, underline bool
}

// Page-number slots in text, filled in once the page count is known.
const (
	pageSlot  = "\x00PAGE\x00"
	totalSlot = "\x00TOTAL\x00"
)

func pageText(s string, page, total int) string {
	if !strings.Contains(s, "\x00") {
		return s
	}
	s = strings.ReplaceAll(s, pageSlot, fmt.Sprint(page))
	return strings.ReplaceAll(s, totalSlot, fmt.Sprint(total))
}

func (p *pageOut) add(it item) { p.items = append(p.items, it) }

// fontFamily maps a Crystal font name to the metric-compatible family that
// stands in for it: Liberation Sans for Arial, Serif for Times New Roman,
// Mono for Courier.
func fontFamily(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "courier") || strings.Contains(n, "mono") || strings.Contains(n, "consol"):
		return "Mono"
	case strings.Contains(n, "times") || strings.Contains(n, "serif") && !strings.Contains(n, "sans") ||
		strings.Contains(n, "georgia") || strings.Contains(n, "garamond"):
		return "Serif"
	case strings.Contains(n, "narrow"):
		return "SansNarrow"
	}
	return "Sans"
}

// cellUnits is each family's cell height, Windows ascent plus descent, in
// font units of a 2048 em (from the Liberation files, which copy Arial's,
// Times New Roman's and Courier New's metrics).
var cellUnits = map[string]float64{"Sans": 2288, "Serif": 2268, "Mono": 2320, "SansNarrow": 2319}

// setting is where Crystal puts a font's glyphs, in points: the em size it
// draws them at (printedSize), the first baseline below the object's top,
// and the distance between lines.
//
// The baseline sits just below the em, a bold one lower: measured on a
// Crystal PDF export (5,000 words, sizes 8 to 12, regular and bold),
// baselines land within 0.05pt.
func (f font) setting() (em, baseline, pitch float64) {
	size := f.size
	if size <= 0 {
		size = 10
	}
	em = printedSize(f.name, size, f.bold)
	baseline = em * 1.0028
	if f.bold {
		baseline = em * 1.0667
	}
	return em, baseline, 1.2 * size
}
