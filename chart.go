package main

// Charts: the numbers (a summary of the series field per category, and per
// series when a second category field splits them) over the records of the
// group the chart sits in, or the whole report; and a drawing of them as
// columns, lines, areas or a pie, laid out as primitives that pdf.go and
// html.go both draw.

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"

	"github.com/signintech/gopdf"
)

// ChartData is what a chart shows.
type ChartData struct {
	Title      string      `json:"title,omitempty"`
	Kind       string      `json:"kind"`
	Stacking   string      `json:"stacking,omitempty"`
	Category   string      `json:"category"`
	Series     string      `json:"series,omitempty"`
	Value      string      `json:"value"`
	Categories []string    `json:"categories"`
	SeriesKeys []string    `json:"seriesNames,omitempty"`
	Values     [][]float64 `json:"values"` // [category][series]
	legend     bool
	palette    []string
}

// chartData computes o's numbers for the current record's group (the
// group of section s), or for the report.
func (e *Engine) chartData(o Object, s Section) *ChartData {
	if len(o.CategoryFields) == 0 || len(e.rows) == 0 {
		return nil
	}
	level := -1
	if s.Type == "GroupHeader" || s.Type == "GroupFooter" {
		level = s.GroupLevel
	}
	lo, hi := e.spanOf(level)
	fn := o.SeriesFunction
	if fn == "" {
		fn = "Sum"
	}
	ref := e.rpt.refOf(o.SeriesField)
	cd := &ChartData{Title: o.Title, Kind: o.Kind, Stacking: o.Stacking, legend: o.ShowLegend, palette: o.Palette,
		Category: display(e.rpt, e.rpt.refOf(o.CategoryFields[0])), Value: fn + " of " + display(e.rpt, ref)}
	if cd.Stacking == "None" {
		cd.Stacking = ""
	}
	key := func(i, k int) (any, string) {
		cond := "EachValue"
		if k < len(o.CategoryConditions) {
			cond = o.CategoryConditions[k]
		}
		v, _ := e.atRow(i, e.rpt.refOf(o.CategoryFields[k]))
		v = groupValue(Group{FieldName: "v", Condition: cond}, Row{"v": v})
		return v, toText(v)
	}
	if len(o.CategoryFields) > 1 {
		cd.Series = display(e.rpt, e.rpt.refOf(o.CategoryFields[1]))
	}
	type cell struct{ cat, ser string }
	totals := map[cell]*runTotal{}
	cats, sers := map[string]any{}, map[string]any{}
	for i := lo; i < hi; i++ {
		cv, ct := key(i, 0)
		sv, st := any(nil), ""
		if len(o.CategoryFields) > 1 {
			sv, st = key(i, 1)
		}
		cats[ct], sers[st] = cv, sv
		c := cell{ct, st}
		if totals[c] == nil {
			totals[c] = &runTotal{fn: fn}
		}
		v, err := e.atRow(i, ref)
		if err != nil {
			e.errs[fmt.Sprintf("chart %s: %v", o.Name, err)]++
		}
		totals[c].add(v)
	}
	order := func(m map[string]any) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.SliceStable(keys, func(a, b int) bool { return compare(m[keys[a]], m[keys[b]]) < 0 })
		return keys
	}
	catKeys, serKeys := order(cats), order(sers)
	for _, c := range catKeys {
		row := make([]float64, len(serKeys))
		for j, sk := range serKeys {
			if t := totals[cell{c, sk}]; t != nil {
				row[j] = num(t.result())
			}
		}
		cd.Values = append(cd.Values, row)
	}
	for _, c := range catKeys {
		cd.Categories = append(cd.Categories, chartLabel(cats[c]))
	}
	if len(o.CategoryFields) > 1 {
		for _, s := range serKeys {
			cd.SeriesKeys = append(cd.SeriesKeys, chartLabel(sers[s]))
		}
	}
	return cd
}

func chartLabel(v any) string {
	if v == nil {
		return "(none)"
	}
	return formatValue(v, "")
}

// spanOf is the records of the current record's group at level, or all.
func (e *Engine) spanOf(level int) (int, int) {
	lo, hi := 0, len(e.rows)
	if level < 0 || level >= len(e.rpt.Groups) || e.rowIdx >= len(e.rows) {
		return lo, hi
	}
	same := func(j int) bool {
		for l := 0; l <= level; l++ {
			if compare(e.groupKey(l, e.rows[j]), e.groupKey(l, e.rows[e.rowIdx])) != 0 {
				return false
			}
		}
		return true
	}
	lo, hi = e.rowIdx, e.rowIdx+1
	for lo > 0 && same(lo-1) {
		lo--
	}
	for hi < len(e.rows) && same(hi) {
		hi++
	}
	return lo, hi
}

// ---- drawing ----

// chartDraw is a chart laid out in its box, in twips from the box's corner.
type chartDraw struct {
	grid  []chartLine // drawn first, under the marks
	rects []chartRect
	lines []chartLine
	polys []chartPoly
	texts []chartText
}

type chartRect struct {
	x, y, w, h float64
	color      string
}

type chartLine struct {
	pts   [][2]float64
	color string
	width float64
}

type chartPoly struct {
	pts   [][2]float64
	color string
}

type chartText struct {
	x, y  float64 // the baseline's start, middle or end, by align
	text  string
	size  float64
	bold  bool
	align string
}

// chartPalette is Crystal's default series colours.
var chartPalette = []string{"#1F4E99", "#C0392B", "#2E8B57", "#E67E22", "#7D3C98", "#17A2B8", "#B7950B", "#5D6D7E"}

func (c *ChartData) color(i int) string {
	if len(c.palette) > 0 {
		return c.palette[i%len(c.palette)]
	}
	return chartPalette[i%len(chartPalette)]
}

// draw lays the chart out in a w x h twips box.
func (c *ChartData) draw(w, h int) *chartDraw {
	d := &chartDraw{}
	W, H := float64(w), float64(h)
	top, bottom, left, right := 120.0, H-120, 120.0, W-120
	if c.Title != "" {
		d.texts = append(d.texts, chartText{W / 2, 260, c.Title, 10, true, "Center"})
		top = 400
	}
	nser := 1
	if len(c.SeriesKeys) > 0 {
		nser = len(c.SeriesKeys)
	}
	if c.legend {
		names := c.SeriesKeys
		if c.Kind == "Pie" || c.Kind == "Doughnut" {
			names = c.Categories
		} else if len(names) == 0 {
			names = []string{c.Value}
		}
		lw := 1800.0
		right = W - lw - 120
		for i, n := range names {
			y := top + float64(i)*220
			if y+200 > H {
				break
			}
			d.rects = append(d.rects, chartRect{right + 200, y, 140, 140, c.color(i)})
			d.texts = append(d.texts, chartText{right + 400, y + 130, n, 7, false, "Left"})
		}
	}
	if c.Kind == "Pie" || c.Kind == "Doughnut" {
		c.drawPie(d, left, top, right, bottom)
		return d
	}
	// Value axis: from 0 (or the lowest negative) to a round maximum.
	lo, hi := 0.0, 0.0
	for _, row := range c.Values {
		pos, neg := 0.0, 0.0
		for _, v := range row {
			if c.Stacking != "" {
				if v > 0 {
					pos += v
				} else {
					neg += v
				}
			} else {
				hi, lo = math.Max(hi, v), math.Min(lo, v)
			}
		}
		if c.Stacking != "" {
			hi, lo = math.Max(hi, pos), math.Min(lo, neg)
		}
	}
	if c.Stacking == "PercentStacked" {
		lo, hi = 0, 100
	}
	step := niceStep((hi - lo) / 5)
	hi = math.Ceil(hi/step) * step
	lo = math.Floor(lo/step) * step
	if hi == lo {
		hi = lo + step
	}
	axisLeft, axisBottom := left+900, bottom-360
	y := func(v float64) float64 { return axisBottom - (v-lo)/(hi-lo)*(axisBottom-top) }
	for v := lo; v <= hi+step/2; v += step {
		d.grid = append(d.grid, chartLine{[][2]float64{{axisLeft, y(v)}, {right, y(v)}}, "#D0D0D0", 0.5})
		d.texts = append(d.texts, chartText{axisLeft - 60, y(v) + 50, formatNumber(v, decimalsFor(step), true), 7, false, "Right"})
	}
	d.lines = append(d.lines, chartLine{[][2]float64{{axisLeft, top}, {axisLeft, axisBottom}}, "", 0.75},
		chartLine{[][2]float64{{axisLeft, y(math.Max(lo, 0))}, {right, y(math.Max(lo, 0))}}, "", 0.75})
	n := len(c.Categories)
	if n == 0 {
		return d
	}
	slot := (right - axisLeft) / float64(n)
	every := int(math.Ceil(float64(n) * 700 / (right - axisLeft))) // labels at least ~0.5 in apart
	for i, cat := range c.Categories {
		if i%max(every, 1) == 0 {
			d.texts = append(d.texts, chartText{axisLeft + slot*(float64(i)+0.5), axisBottom + 200, cat, 7, false, "Center"})
		}
	}
	switch c.Kind {
	case "Line", "Area":
		for s := 0; s < nser; s++ {
			var pts [][2]float64
			for i, row := range c.Values {
				pts = append(pts, [2]float64{axisLeft + slot*(float64(i)+0.5), y(row[s])})
			}
			if c.Kind == "Area" {
				poly := append([][2]float64{{pts[0][0], y(math.Max(lo, 0))}}, pts...)
				poly = append(poly, [2]float64{pts[len(pts)-1][0], y(math.Max(lo, 0))})
				d.polys = append(d.polys, chartPoly{poly, c.color(s)})
			} else {
				d.lines = append(d.lines, chartLine{pts, c.color(s), 1.5})
			}
		}
	default: // Column
		for i, row := range c.Values {
			x0 := axisLeft + slot*float64(i) + slot*0.15
			bw := slot * 0.7
			total := 0.0
			for _, v := range row {
				total += math.Abs(v)
			}
			pos, neg := 0.0, 0.0
			for s, v := range row {
				if c.Stacking == "PercentStacked" && total != 0 {
					v = v / total * 100
				}
				if c.Stacking != "" {
					base := pos
					if v < 0 {
						base = neg
						neg += v
					} else {
						pos += v
					}
					d.rects = append(d.rects, bar(x0, bw, y(base), y(base+v), c.color(s)))
					continue
				}
				sw := bw / float64(len(row))
				d.rects = append(d.rects, bar(x0+sw*float64(s), sw, y(0), y(v), c.color(s)))
			}
		}
	}
	return d
}

func bar(x, w, y1, y2 float64, color string) chartRect {
	return chartRect{x, math.Min(y1, y2), w, math.Abs(y2 - y1), color}
}

func (c *ChartData) drawPie(d *chartDraw, left, top, right, bottom float64) {
	var sums []float64
	total := 0.0
	for _, row := range c.Values {
		s := 0.0
		for _, v := range row {
			s += math.Max(v, 0)
		}
		sums = append(sums, s)
		total += s
	}
	if total == 0 {
		return
	}
	cx, cy := (left+right)/2, (top+bottom)/2
	r := math.Min(right-left, bottom-top) / 2 * 0.9
	inner := 0.0
	if c.Kind == "Doughnut" {
		inner = r * 0.5
	}
	a := -math.Pi / 2
	for i, s := range sums {
		sweep := s / total * 2 * math.Pi
		var pts [][2]float64
		steps := max(int(sweep/(math.Pi/60)), 1)
		for k := 0; k <= steps; k++ {
			t := a + sweep*float64(k)/float64(steps)
			pts = append(pts, [2]float64{cx + r*math.Cos(t), cy + r*math.Sin(t)})
		}
		for k := steps; k >= 0; k-- {
			t := a + sweep*float64(k)/float64(steps)
			pts = append(pts, [2]float64{cx + inner*math.Cos(t), cy + inner*math.Sin(t)})
		}
		d.polys = append(d.polys, chartPoly{pts, c.color(i)})
		a += sweep
	}
}

// niceStep rounds a step up to 1, 2 or 5 times a power of ten.
func niceStep(raw float64) float64 {
	if raw <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5, 10} {
		if raw <= m*p {
			return m * p
		}
	}
	return 10 * p
}

func decimalsFor(step float64) int {
	if step >= 1 {
		return 0
	}
	return int(math.Ceil(-math.Log10(step)))
}

// ---- writers ----

func (w *pdfWriter) chart(it item) error {
	d := it.chart
	if d == nil {
		return nil
	}
	ox, oy := tw(it.x), tw(it.y)
	p := func(x, y float64) (float64, float64) { return ox + x/20, oy + y/20 }
	stroke := func(lines []chartLine) {
		for _, l := range lines {
			r, g, b := rgb(l.color)
			w.pdf.SetStrokeColor(r, g, b)
			w.pdf.SetLineWidth(l.width)
			for k := 1; k < len(l.pts); k++ {
				x1, y1 := p(l.pts[k-1][0], l.pts[k-1][1])
				x2, y2 := p(l.pts[k][0], l.pts[k][1])
				w.pdf.Line(x1, y1, x2, y2)
			}
		}
	}
	stroke(d.grid)
	for _, r := range d.rects {
		w.color(r.color)
		x, y := p(r.x, r.y)
		w.fill(x, y, r.w/20, r.h/20)
	}
	for _, pl := range d.polys {
		w.color(pl.color)
		var pts []gopdf.Point
		for _, q := range pl.pts {
			x, y := p(q[0], q[1])
			pts = append(pts, gopdf.Point{X: x, Y: y})
		}
		w.pdf.Polygon(pts, "F")
	}
	stroke(d.lines)
	w.pdf.SetStrokeColor(0, 0, 0)
	w.color("")
	for _, t := range d.texts {
		f := font{name: "Arial", size: t.size, bold: t.bold}
		em, _, _ := f.setting()
		if err := w.setFont(f, em); err != nil {
			return err
		}
		width, err := w.pdf.MeasureTextWidth(t.text)
		if err != nil {
			return err
		}
		x, y := p(t.x, t.y)
		switch t.align {
		case "Center":
			x -= width / 2
		case "Right":
			x -= width
		}
		w.pdf.SetXY(x, y)
		if err := w.pdf.Text(t.text); err != nil {
			return err
		}
	}
	return nil
}

func htmlChart(it item) string {
	d := it.chart
	if d == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="b" style="left:%s;top:%s;width:%s;height:%s" viewBox="0 0 %d %d">`,
		pt(it.x), pt(it.y), pt(it.w), pt(it.h), it.w, it.h)
	fill := func(c string) string {
		if c == "" {
			return "#000"
		}
		return c
	}
	points := func(pts [][2]float64) string {
		s := make([]string, len(pts))
		for i, q := range pts {
			s[i] = fmt.Sprintf("%.0f,%.0f", q[0], q[1])
		}
		return strings.Join(s, " ")
	}
	for _, l := range d.grid {
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%.0f"/>`, points(l.pts), fill(l.color), l.width*20)
	}
	for _, r := range d.rects {
		fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="%s"/>`, r.x, r.y, r.w, r.h, fill(r.color))
	}
	for _, pl := range d.polys {
		fmt.Fprintf(&b, `<polygon points="%s" fill="%s"/>`, points(pl.pts), fill(pl.color))
	}
	for _, l := range d.lines {
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%.0f"/>`, points(l.pts), fill(l.color), l.width*20)
	}
	for _, t := range d.texts {
		anchor := map[string]string{"Center": "middle", "Right": "end"}[t.align]
		if anchor == "" {
			anchor = "start"
		}
		weight := ""
		if t.bold {
			weight = ` font-weight="bold"`
		}
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="%.0f" text-anchor="%s"%s>%s</text>`, t.x, t.y, t.size*20, anchor, weight, html.EscapeString(t.text))
	}
	b.WriteString("</svg>")
	return b.String()
}
