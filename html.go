package main

import (
	"fmt"
	"html"
	"strings"
)

func pt(tw int) string { return fmt.Sprintf("%gpt", float64(tw)/20) }

// cssFamily is the font stack for a family other than the page's default sans.
var cssFamily = map[string]string{
	"Serif":      `"Times New Roman", "Liberation Serif", Tinos, serif`,
	"Mono":       `"Courier New", "Liberation Mono", Cousine, monospace`,
	"SansNarrow": `"Arial Narrow", "Liberation Sans Narrow", sans-serif`,
}

// Document lays the report out and writes it as one HTML page per report
// page, ready for a browser's print-to-PDF.
func (e *Engine) Document(title string) string {
	e.Run()
	w, h := float64(e.rpt.Page.WidthTwips)/1440, float64(e.rpt.Page.HeightTwips)/1440
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title><style>
@page { size: %gin %gin; margin: 0 }
body { margin: 0; font-family: Arial, "Liberation Sans", Arimo, Helvetica, sans-serif; color: #000 }
.page { position: relative; width: %gin; height: %gin; overflow: hidden; page-break-after: always }
.t { position: absolute; white-space: pre; overflow: hidden }
.b { position: absolute; box-sizing: border-box }
.ln { position: absolute; background: #000 }
.bx { position: absolute; border: 0.75pt solid #000; box-sizing: border-box }
</style></head><body>
`, html.EscapeString(title), w, h, w, h)
	for i, p := range e.pages {
		lines := make([]string, len(p.items))
		for j, it := range p.items {
			lines[j] = htmlItem(it, i+1, len(e.pages))
		}
		b.WriteString(`<div class="page">` + "\n" + strings.Join(lines, "\n") + "\n</div>\n")
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func htmlItem(it item, page, total int) string {
	switch it.kind {
	case itemFill:
		return fmt.Sprintf(`<div class="ln" style="left:%s;top:%s;width:%s;height:%s"></div>`,
			pt(it.x), pt(it.y), pt(it.w), pt(it.h))
	case itemBox:
		return fmt.Sprintf(`<div class="bx" style="left:%s;top:%s;width:%s;height:%s"></div>`,
			pt(it.x), pt(it.y), pt(it.w), pt(it.h))
	case itemBorder:
		b := it.border
		return fmt.Sprintf(`<div class="b" style="left:%s;top:%s;width:%s;height:%s%s%s%s%s"></div>`,
			pt(it.x), pt(it.y), pt(it.w), pt(it.h),
			b.css("top", b.top), b.css("bottom", b.bottom), b.css("left", b.left), b.css("right", b.right))
	}
	// A browser puts the first baseline half the leading plus the ascent below
	// the top; shift the box so it lands on Crystal's.
	f := it.font
	em, baseline, pitch := f.setting()
	hh := hheaUnits[fontFamily(f.name)]
	asc, desc := hh[0]/2048*em, hh[1]/2048*em
	shift := baseline - ((pitch-asc-desc)/2 + asc)
	style := fmt.Sprintf("left:%s;top:%.2fpt;width:%s;height:%.2fpt;font-size:%gpt;line-height:%gpt;text-align:%s",
		pt(it.x), float64(it.y)/20+shift, pt(it.w), float64(it.h)/20-shift, em, pitch, strings.ToLower(it.align))
	if fam, ok := cssFamily[fontFamily(f.name)]; ok {
		style += ";font-family:" + fam
	}
	if f.bold {
		style += ";font-weight:bold"
	}
	if f.italic {
		style += ";font-style:italic"
	}
	if f.underline {
		style += ";text-decoration:underline"
	}
	return fmt.Sprintf(`<div class="t" style="%s">%s</div>`, style, html.EscapeString(pageText(it.text, page, total)))
}

// hheaUnits is each family's ascender and descender in units of a 2048 em,
// the metrics a browser lays a line out with.
var hheaUnits = map[string][2]float64{"Sans": {1854, 434}, "Serif": {1825, 443}, "Mono": {1705, 615}, "SansNarrow": {1916, 434}}

func (b borders) css(side string, code int) string {
	style := map[int]string{1: "solid", 2: "double", 3: "dashed", 4: "dotted"}[code]
	if style == "" {
		return ""
	}
	return fmt.Sprintf(";border-%s:%gpt %s #000", side, b.edgeWidth(code), style)
}
