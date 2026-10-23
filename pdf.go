package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signintech/gopdf"
)

// The PDF writer draws the display list straight into a PDF with gopdf: text
// in Liberation fonts (metric-compatible with Arial, Times New Roman and
// Courier New, so widths and line breaks match the Windows originals),
// embedded as subsets, lines and borders as filled rectangles.

// fontFiles is each family's TrueType file per style, in fontDir.
var fontFiles = map[string][4]string{ // regular, bold, italic, bold italic
	"Sans":       {"LiberationSans-Regular.ttf", "LiberationSans-Bold.ttf", "LiberationSans-Italic.ttf", "LiberationSans-BoldItalic.ttf"},
	"Serif":      {"LiberationSerif-Regular.ttf", "LiberationSerif-Bold.ttf", "LiberationSerif-Italic.ttf", "LiberationSerif-BoldItalic.ttf"},
	"Mono":       {"LiberationMono-Regular.ttf", "LiberationMono-Bold.ttf", "LiberationMono-Italic.ttf", "LiberationMono-BoldItalic.ttf"},
	"SansNarrow": {"LiberationSansNarrow-Regular.ttf", "LiberationSansNarrow-Bold.ttf", "LiberationSansNarrow-Italic.ttf", "LiberationSansNarrow-BoldItalic.ttf"},
}

var fontStyles = [4]int{gopdf.Regular, gopdf.Bold, gopdf.Italic, gopdf.Bold | gopdf.Italic}

type pdfWriter struct {
	pdf     *gopdf.GoPdf
	fontDir string
	loaded  map[string]bool // families added to the document
	pageW   float64
}

// PDF lays the report out and writes it as a PDF. fontDir holds the
// Liberation TrueType files.
func (e *Engine) PDF(title, fontDir string) ([]byte, error) {
	e.Run()
	w := &pdfWriter{pdf: &gopdf.GoPdf{}, fontDir: fontDir, loaded: map[string]bool{}}
	pw, ph := float64(e.rpt.Page.WidthTwips)/20, float64(e.rpt.Page.HeightTwips)/20
	w.pageW = pw
	w.pdf.Start(gopdf.Config{Unit: gopdf.UnitPT, PageSize: gopdf.Rect{W: pw, H: ph}})
	w.pdf.SetInfo(gopdf.PdfInfo{Title: title, Creator: "rptgo", Producer: "rptgo"})
	for i, p := range e.pages {
		w.pdf.AddPage()
		w.pdf.SetFillColor(0, 0, 0)
		w.pdf.SetStrokeColor(0, 0, 0)
		for _, it := range p.items {
			if err := w.item(it, i+1, len(e.pages)); err != nil {
				return nil, err
			}
		}
	}
	var b bytes.Buffer
	if _, err := w.pdf.WriteTo(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func tw(v int) float64 { return float64(v) / 20 }

func (w *pdfWriter) item(it item, page, total int) error {
	switch it.kind {
	case itemFill:
		w.fill(tw(it.x), tw(it.y), tw(it.w), tw(it.h))
	case itemBox:
		// A 0.75pt frame inside the bounds, as the HTML's border-box draws it.
		const lw = 0.75
		x, y, bw, bh := tw(it.x), tw(it.y), tw(it.w), tw(it.h)
		w.fill(x, y, bw, lw)
		w.fill(x, y+bh-lw, bw, lw)
		w.fill(x, y, lw, bh)
		w.fill(x+bw-lw, y, lw, bh)
	case itemBorder:
		w.border(it)
	case itemText:
		return w.text(it, pageText(it.text, page, total))
	}
	return nil
}

func (w *pdfWriter) fill(x, y, width, height float64) {
	if width <= 0 || height <= 0 {
		return
	}
	w.pdf.RectFromUpperLeftWithStyle(x, y, width, height, "F")
}

// border draws each edge inside the item's bounds: a single edge one line
// wide, a double one two lines with a line's width between them. Each line
// runs half its width past both ends, as Crystal's square-capped strokes do.
func (w *pdfWriter) border(it item) {
	b := it.border
	x, y, bw, bh := tw(it.x), tw(it.y), tw(it.w), tw(it.h)
	edge := func(code int, rect func(off, width float64)) {
		lw := b.width
		switch code {
		case 0:
		case 2:
			rect(0, lw)
			rect(2*lw, lw)
		case 3, 4:
			w.pdf.SetLineType(map[int]string{3: "dashed", 4: "dotted"}[code])
			rect(-1, lw) // stroked, see below
			w.pdf.SetLineType("solid")
		default:
			rect(0, lw)
		}
	}
	stroke := func(x1, y1, x2, y2, lw float64) {
		w.pdf.SetLineWidth(lw)
		w.pdf.Line(x1, y1, x2, y2)
	}
	edge(b.top, func(off, lw float64) {
		if off < 0 {
			stroke(x, y+lw/2, x+bw, y+lw/2, lw)
			return
		}
		w.fill(x-lw/2, y+off, bw+lw, lw)
	})
	edge(b.bottom, func(off, lw float64) {
		if off < 0 {
			stroke(x, y+bh-lw/2, x+bw, y+bh-lw/2, lw)
			return
		}
		w.fill(x-lw/2, y+bh-off-lw, bw+lw, lw)
	})
	edge(b.left, func(off, lw float64) {
		if off < 0 {
			stroke(x+lw/2, y, x+lw/2, y+bh, lw)
			return
		}
		w.fill(x+off, y-lw/2, lw, bh+lw)
	})
	edge(b.right, func(off, lw float64) {
		if off < 0 {
			stroke(x+bw-lw/2, y, x+bw-lw/2, y+bh, lw)
			return
		}
		w.fill(x+bw-off-lw, y-lw/2, lw, bh+lw)
	})
}

// text sets each line of the item where Crystal does (font.setting),
// aligned in its box and clipped to its height. Crystal prints whole
// characters only: a field's text comes already cut to what fits (fitText),
// and its last glyph may overhang the box, as in Crystal's own exports, so
// nothing is clipped sideways. Glyphs starting past the box's right edge
// are left out, so the PDF's text holds what the page shows.
func (w *pdfWriter) text(it item, s string) error {
	f := it.font
	em, baseline, pitch := f.setting()
	if err := w.setFont(f, em); err != nil {
		return err
	}
	x, y, bw, bh := tw(it.x), tw(it.y), tw(it.w), tw(it.h)
	w.pdf.SaveGraphicsState()
	w.pdf.ClipPolygon([]gopdf.Point{{X: 0, Y: y}, {X: w.pageW, Y: y}, {X: w.pageW, Y: y + bh}, {X: 0, Y: y + bh}})
	for i, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		by := y + baseline + float64(i)*pitch
		if by-em >= y+bh {
			break
		}
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		width, err := w.pdf.MeasureTextWidth(line)
		if err != nil {
			return err
		}
		lx := x
		if width <= bw { // an overflowing line starts at the left, as in CSS
			switch it.align {
			case "Right":
				lx = x + bw - width
			case "Center":
				lx = x + (bw-width)/2
			}
		} else {
			line, width, err = w.fit(line, bw)
			if err != nil {
				return err
			}
		}
		w.pdf.SetXY(lx, by)
		if err := w.pdf.Text(line); err != nil {
			return err
		}
		if f.underline {
			w.fill(lx, by+0.1*em, width, em/14)
		}
	}
	w.pdf.RestoreGraphicsState()
	return nil
}

// fit keeps the glyphs that start inside width; the last one may overhang.
func (w *pdfWriter) fit(line string, width float64) (string, float64, error) {
	runes := []rune(line)
	for n := len(runes); n > 0; n-- {
		start, err := w.pdf.MeasureTextWidth(string(runes[:n-1]))
		if err != nil {
			return "", 0, err
		}
		if start < width {
			full, err := w.pdf.MeasureTextWidth(string(runes[:n]))
			return string(runes[:n]), full, err
		}
	}
	return "", 0, nil
}

// setFont selects the item's font, adding its family to the document first.
func (w *pdfWriter) setFont(f font, size float64) error {
	fam := fontFamily(f.name)
	if !w.loaded[fam] {
		for i, file := range fontFiles[fam] {
			data, err := os.ReadFile(filepath.Join(w.fontDir, file))
			if err != nil {
				return fmt.Errorf("font %s: %w", fam, err)
			}
			if err := w.pdf.AddTTFFontDataWithOption(fam, data, gopdf.TtfOption{Style: fontStyles[i], UseKerning: true}); err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
		}
		w.loaded[fam] = true
	}
	style := gopdf.Regular
	if f.bold {
		style |= gopdf.Bold
	}
	if f.italic {
		style |= gopdf.Italic
	}
	return w.pdf.SetFontWithStyle(fam, style, size)
}
