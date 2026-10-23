package main

// Reading a report's layout as a reader of the printout would: sections in
// design order with Crystal's names, objects in visual rows, the detail
// columns under their headings, and the totals printed under each column.
// describe and extract share it.

import (
	"fmt"
	"sort"
	"strings"
)

// sectionKinds is the design view's order of section kinds.
var sectionKinds = []string{"ReportHeader", "PageHeader", "GroupHeader", "Details", "GroupFooter", "ReportFooter", "PageFooter"}

// designOrder is the report's sections as Crystal's design view lists them,
// as indexes into r.Sections: group headers outermost first, group footers
// innermost first.
func (r *Report) designOrder() []int {
	var out []int
	add := func(typ string, level int) {
		for i, s := range r.Sections {
			if s.Type == typ && (level < 0 || s.GroupLevel == level) {
				out = append(out, i)
			}
		}
	}
	for _, k := range sectionKinds {
		switch k {
		case "GroupHeader":
			for l := 0; l < r.groupLevels(); l++ {
				add(k, l)
			}
		case "GroupFooter":
			for l := r.groupLevels() - 1; l >= 0; l-- {
				add(k, l)
			}
		default:
			add(k, -1)
		}
	}
	return out
}

// groupLevels is how many group levels the sections and groups use.
func (r *Report) groupLevels() int {
	n := len(r.Groups)
	for _, s := range r.Sections {
		if (s.Type == "GroupHeader" || s.Type == "GroupFooter") && s.GroupLevel+1 > n {
			n = s.GroupLevel + 1
		}
	}
	return n
}

// sectionName is Crystal's name for section i: "Group Header #1", and
// "Details a", "Details b" where a kind has more than one section.
func (r *Report) sectionName(i int) string {
	s := r.Sections[i]
	name := map[string]string{
		"ReportHeader": "Report Header", "PageHeader": "Page Header", "GroupHeader": "Group Header",
		"Details": "Details", "GroupFooter": "Group Footer", "ReportFooter": "Report Footer", "PageFooter": "Page Footer",
	}[s.Type]
	if name == "" {
		name = s.Type
	}
	if s.Type == "GroupHeader" || s.Type == "GroupFooter" {
		name += fmt.Sprintf(" #%d", s.GroupLevel+1)
	}
	var same []int
	for j, t := range r.Sections {
		if t.Type == s.Type && t.GroupLevel == s.GroupLevel {
			same = append(same, j)
		}
	}
	if len(same) > 1 {
		for k, j := range same {
			if j == i {
				name += " " + letters(k)
			}
		}
	}
	return name
}

func letters(k int) string {
	if k < 26 {
		return string(rune('a' + k))
	}
	return letters(k/26-1) + string(rune('a'+k%26))
}

// visualRows groups a section's printing objects into the rows a reader
// sees: objects whose vertical extents overlap share a row; rows top to
// bottom, objects left to right. Lines and boxes are left out.
func visualRows(objs []Object) [][]Object {
	var list []Object
	for _, o := range objs {
		if o.Format.Suppress || isShape(o) {
			continue
		}
		list = append(list, o)
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].Bounds.Top < list[b].Bounds.Top })
	var rows [][]Object
	var bottom int
	for _, o := range list {
		h := max(o.Bounds.Height, 1)
		n := len(rows)
		if n > 0 && o.Bounds.Top < bottom-min(h, rowHeight(rows[n-1]))/3 {
			rows[n-1] = append(rows[n-1], o)
			bottom = max(bottom, o.Bounds.Top+h)
			continue
		}
		rows = append(rows, []Object{o})
		bottom = o.Bounds.Top + h
	}
	for _, row := range rows {
		sort.SliceStable(row, func(a, b int) bool { return row[a].Bounds.Left < row[b].Bounds.Left })
	}
	return rows
}

func rowHeight(row []Object) int {
	h := 1
	for _, o := range row {
		h = max(h, o.Bounds.Height)
	}
	return h
}

func isShape(o Object) bool {
	return strings.Contains(o.Type, "Line") || strings.Contains(o.Type, "Box")
}
func isText(o Object) bool  { return strings.Contains(o.Type, "Text") }
func isField(o Object) bool { return strings.Contains(o.Type, "Field") }

// overlaps: two objects share enough of their horizontal span to sit in the
// same column, half the narrower one's width and a third of the wider one's
// (so a title centred over the page heads no column).
func overlaps(a, b Object) (int, bool) {
	l := max(a.Bounds.Left, b.Bounds.Left)
	r := min(a.Bounds.Left+a.Bounds.Width, b.Bounds.Left+b.Bounds.Width)
	ov := r - l
	lo, hi := min(a.Bounds.Width, b.Bounds.Width), max(a.Bounds.Width, b.Bounds.Width)
	return ov, ov > 0 && 2*ov >= lo && 3*ov >= hi
}

// column is one value printed on every detail line, with the heading over it
// and the totals printed under it.
type column struct {
	Label   string // the heading, or the field's own name when it has none
	Labeled bool   // Label is a heading from the layout
	Ref     string // what prints: a reference as binding writes it
	Obj     Object
	Section int // index into r.Sections
	Totals  []columnTotal
}

type columnTotal struct {
	Section int
	Ref     string
	Obj     Object
}

// columns is the detail lines' values left to right, Details a before b.
func (r *Report) columns(ov *Overlay) []column {
	var cols []column
	lines, _ := r.lineSections()
	inLine := map[int]bool{}
	for _, i := range lines {
		inLine[i] = true
		s := r.Sections[i]
		for _, row := range visualRows(s.Objects) {
			for _, o := range row {
				if isField(o) {
					ref := r.binding(o, ov)
					cols = append(cols, column{Label: shortName(ref), Ref: ref, Obj: o, Section: i})
				}
			}
		}
	}
	r.labelColumns(cols)
	// A detail band laid out as a form labels its values in place.
	for c := range cols {
		if !cols[c].Labeled {
			if l := leftLabel(r.Sections[cols[c].Section], cols[c].Obj); l != "" {
				cols[c].Label, cols[c].Labeled = l, true
			}
		}
	}
	for _, i := range r.designOrder() {
		s := r.Sections[i]
		if (s.Type != "GroupFooter" && s.Type != "ReportFooter") || s.Suppress || inLine[i] {
			continue
		}
		for _, o := range s.Objects {
			if !isField(o) || o.Format.Suppress {
				continue
			}
			if c := bestColumn(cols, o); c >= 0 {
				cols[c].Totals = append(cols[c].Totals, columnTotal{Section: i, Ref: r.binding(o, ov), Obj: o})
			}
		}
	}
	return cols
}

// lineSections is the sections that print the report's lines: the visible
// detail sections that print values, or, where the details are hidden (a
// summary report), the innermost visible group footer that prints at least
// two values, one line per group; level is that footer's group level, -1
// for details.
func (r *Report) lineSections() (secs []int, level int) {
	for _, i := range r.designOrder() {
		s := r.Sections[i]
		if s.Type == "Details" && !s.Suppress && printsValues(s, 1) {
			secs = append(secs, i)
		}
	}
	if len(secs) > 0 {
		return secs, -1
	}
	for _, i := range r.designOrder() { // innermost footer first
		s := r.Sections[i]
		if s.Type != "GroupFooter" || s.Suppress || !printsValues(s, 2) {
			continue
		}
		if len(secs) > 0 && s.GroupLevel != level {
			break
		}
		secs, level = append(secs, i), s.GroupLevel
	}
	if len(secs) == 0 {
		return nil, -1
	}
	return secs, level
}

// printsValues: the section has at least n field objects that print.
func printsValues(s Section, n int) bool {
	k := 0
	for _, o := range s.Objects {
		if isField(o) && !o.Format.Suppress {
			k++
		}
	}
	return k >= n
}

// bestColumn is the column o sits under, or -1.
func bestColumn(cols []column, o Object) int {
	best, bestOv := -1, 0
	for c, col := range cols {
		if ov, ok := overlaps(col.Obj, o); ok && ov > bestOv {
			best, bestOv = c, ov
		}
	}
	return best
}

// labelColumns finds the heading row: the row of text objects, in a page or
// group header, that heads the most columns (at least two, or one when there
// is only one column); ties go to the row nearest the details. Each column
// takes the text over it in that row, and any text stacked right above it.
func (r *Report) labelColumns(cols []column) {
	if len(cols) == 0 {
		return
	}
	type headRow struct {
		sec   int
		rows  [][]Object
		index int
		score int
		rank  int
	}
	var best *headRow
	for rank, i := range r.designOrder() {
		s := r.Sections[i]
		if (s.Type != "PageHeader" && s.Type != "GroupHeader") || s.Suppress {
			continue
		}
		rows := visualRows(s.Objects)
		for k, row := range rows {
			score := 0
			for _, o := range row {
				if isText(o) && bestColumn(cols, o) >= 0 {
					score++
				}
			}
			need := 2
			if len(cols) == 1 {
				need = 1
			}
			if score < need {
				continue
			}
			if best == nil || score > best.score || (score == best.score && rank >= best.rank) {
				best = &headRow{sec: i, rows: rows, index: k, score: score, rank: rank}
			}
		}
	}
	if best == nil {
		return
	}
	for c := range cols {
		var parts []string
		top := -1
		for k := best.index; k >= 0; k-- {
			row := best.rows[k]
			if top >= 0 && rowBottom(row) < top-120 {
				break // not stacked right above
			}
			var hit *Object
			hitOv := 0
			for j := range row {
				o := row[j]
				if !isText(o) {
					continue
				}
				if ov, ok := overlaps(cols[c].Obj, o); ok && ov > hitOv && bestColumn(cols, o) == c {
					hit, hitOv = &row[j], ov
				} else if k < best.index && hit == nil && spans(o, cols) && covers(o, cols[c].Obj) {
					hit = &row[j] // a heading over several columns ("--- Overtime ---")
				}
			}
			if hit == nil {
				break
			}
			parts = append([]string{cleanLabel(hit.Text)}, parts...)
			top = hit.Bounds.Top
		}
		if label := strings.TrimSpace(strings.Join(parts, " ")); label != "" {
			cols[c].Label, cols[c].Labeled = label, true
		}
	}
}

// covers: o's span holds the middle of col.
func covers(o, col Object) bool {
	mid := col.Bounds.Left + col.Bounds.Width/2
	return mid >= o.Bounds.Left && mid <= o.Bounds.Left+o.Bounds.Width
}

// spans: a heading over at least two columns.
func spans(o Object, cols []column) bool {
	n := 0
	for _, c := range cols {
		if covers(o, c.Obj) {
			n++
		}
	}
	return n >= 2
}

func rowBottom(row []Object) int {
	b := 0
	for _, o := range row {
		b = max(b, o.Bounds.Top+o.Bounds.Height)
	}
	return b
}

// cleanLabel makes a heading one line: "Invoice\nDate:" -> "Invoice Date",
// without the rules around a spanning heading: "--- Overtime ---".
func cleanLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimSpace(strings.TrimSuffix(s, ":"))
	return strings.Trim(s, "-=_~* ")
}

// shortName is a reference's bare name: "Sales.Amount" -> "Amount", "@Due" -> "Due".
func shortName(ref string) string {
	ref = strings.TrimLeft(ref, "@#?")
	ref = strings.TrimPrefix(ref, "summary:")
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		ref = ref[i+1:]
	}
	return ref
}

// labelFor is what a value in a header or footer is called: the heading of
// the column it totals (in footers), else the text right before it on its
// row ("As Of:"), else what it computes ("Count of InvoiceNo"), else its
// field's name.
func (r *Report) labelFor(s Section, o Object, cols []column, ref string) string {
	if s.Type == "GroupFooter" || s.Type == "ReportFooter" || s.Type == "Details" {
		if c := bestColumn(cols, o); c >= 0 && cols[c].Labeled && r.totals(ref, cols[c].Ref) {
			return cols[c].Label
		}
	}
	if l := leftLabel(s, o); l != "" {
		return l
	}
	if o.SummaryFunction != nil {
		return *o.SummaryFunction + " of " + shortName(o.FieldName)
	}
	return shortName(ref)
}

// totals: ref prints col's value or a total of it (a running total or a
// summary of the same field).
func (r *Report) totals(ref, col string) bool {
	same := func(a, b string) bool { return strings.EqualFold(display(r, a), display(r, b)) }
	if same(ref, col) {
		return true
	}
	return same(r.totalled(ref), col) || same(r.totalled(ref), r.totalled(col))
}

// totalled is the field a running total or summary field adds up, or ref
// itself.
func (r *Report) totalled(ref string) string {
	if name, ok := strings.CutPrefix(ref, "#summary:"); ok {
		if o := r.object(name); o != nil {
			return r.refOf(o.FieldName)
		}
	}
	if name, ok := strings.CutPrefix(ref, "#"); ok {
		if f := r.fieldKind("#", name); f != nil && f.SummarizedFieldName != "" {
			return r.refOf(f.SummarizedFieldName)
		}
	}
	return ref
}

// leftLabel is the text right before o on its row ("As Of:" before a date),
// or "" when there is none within an inch.
func leftLabel(s Section, o Object) string {
	for _, row := range visualRows(s.Objects) {
		var prev *Object
		for j := range row {
			x := row[j]
			if x.Name == o.Name {
				if prev != nil && o.Bounds.Left-(prev.Bounds.Left+prev.Bounds.Width) < 1440 {
					return cleanLabel(prev.Text)
				}
				return ""
			}
			if isText(x) && x.Bounds.Left+x.Bounds.Width <= o.Bounds.Left+60 {
				prev = &row[j]
			} else {
				prev = nil
			}
		}
	}
	return ""
}

// detailRows is how many visual rows of values the tallest line section has.
func (r *Report) detailRows() int {
	n := 0
	secs, _ := r.lineSections()
	for _, i := range secs {
		s := r.Sections[i]
		k := 0
		for _, row := range visualRows(s.Objects) {
			for _, o := range row {
				if isField(o) {
					k++
					break
				}
			}
		}
		n = max(n, k)
	}
	return n
}
