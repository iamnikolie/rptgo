package main

// extract runs a report over its rows as render does (groups, formulas,
// running totals, section suppression) but keeps what each band prints as
// data instead of laying out pages: the header values, the groups with
// their header values, detail rows and footer totals, the report totals.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Extraction is the data a report prints.
type Extraction struct {
	Schema     string          `json:"schema"`
	Report     string          `json:"report"`
	Title      string          `json:"title,omitempty"`
	Printed    string          `json:"printed"`
	Parameters *orderedMap     `json:"parameters,omitempty"`
	Records    int             `json:"records"`
	Columns    []extractColumn `json:"columns"`
	// Form: the detail band is laid out as a form (several rows of values),
	// so Markdown lists each record's values instead of a table.
	Form     bool            `json:"form,omitempty"`
	Header   *orderedMap     `json:"header,omitempty"`
	Groups   []*extractGroup `json:"groups,omitempty"`
	Lines    [][]any         `json:"rows,omitempty"`
	Texts    [][]string      `json:"-"`
	Omitted  int             `json:"omittedRows,omitempty"`
	Footer   *orderedMap     `json:"footer,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

type extractColumn struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type extractGroup struct {
	Level   int             `json:"level"`
	By      string          `json:"by"`
	Value   any             `json:"value"`
	Header  *orderedMap     `json:"header,omitempty"`
	Groups  []*extractGroup `json:"groups,omitempty"`
	Lines   [][]any         `json:"rows,omitempty"`
	Texts   [][]string      `json:"-"`
	Omitted int             `json:"omittedRows,omitempty"`
	Footer  *orderedMap     `json:"footer,omitempty"`
}

// orderedMap is a JSON object that keeps its keys in insertion order, with
// the printed text of each value alongside for Markdown.
type orderedMap struct {
	keys  []string
	vals  map[string]any
	texts map[string]string
}

func (m *orderedMap) set(k string, v any, text string) {
	if m.vals == nil {
		m.vals, m.texts = map[string]any{}, map[string]string{}
	}
	base := k
	for n := 2; ; n++ {
		if _, dup := m.vals[k]; !dup {
			break
		}
		k = fmt.Sprintf("%s (%d)", base, n)
	}
	m.keys = append(m.keys, k)
	m.vals[k], m.texts[k] = v, text
}

func (m *orderedMap) empty() bool { return m == nil || len(m.keys) == 0 }

func (m *orderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// extractor receives the bands from Engine.Run.
type extractor struct {
	cols    []column
	colIdx  map[string]int // detail object name -> column
	out     *Extraction
	stack   []*extractGroup
	lastRow int
	keep    bool // the current record's row is kept (maxRows)
	// lineLevel is the group level whose footer prints the lines (a summary
	// report), -1 when the details do; lineSeq counts those groups.
	lineLevel int
	lineSeq   int
	maxRows   int
	shown     int
}

// Extract runs the report over the engine's rows; maxRows >= 0 keeps only
// that many detail rows (groups and totals stay complete), -1 keeps all.
func (e *Engine) Extract(name string, params []string, maxRows int) *Extraction {
	cols := e.rpt.columns(e.ov)
	_, lineLevel := e.rpt.lineSections()
	x := &extractor{cols: cols, colIdx: map[string]int{}, lastRow: math.MinInt, maxRows: maxRows, lineLevel: lineLevel,
		out: &Extraction{Schema: "rptgo.extract/v1", Report: name, Title: e.rpt.title(),
			Printed: e.printed.Format("2006-01-02 15:04:05"), Records: len(e.rows)}}
	x.out.Form = e.rpt.detailRows() > 2
	for i, c := range cols {
		x.colIdx[c.Obj.Name] = i
		x.out.Columns = append(x.out.Columns, extractColumn{Name: c.Label, Value: display(e.rpt, c.Ref)})
	}
	if len(params) > 0 {
		x.out.Parameters = &orderedMap{}
		for _, p := range params {
			k, v, _ := strings.Cut(p, "=")
			x.out.Parameters.set(strings.TrimPrefix(k, "@"), jsonValue(e.paramValue(k, v)), v)
		}
	}
	e.ext = x
	e.Run()
	e.ext = nil
	for _, k := range sortedKeys(e.errs) {
		x.out.Warnings = append(x.out.Warnings, fmt.Sprintf("%s (x%d)", k, e.errs[k]))
	}
	return x.out
}

// start takes the report header and the first page header.
func (x *extractor) start(e *Engine) {
	for _, typ := range []string{"ReportHeader", "PageHeader"} {
		for _, s := range e.sections(typ, -1) {
			if !e.suppressed(s) {
				x.band(e, s)
			}
		}
	}
}

// open starts a group at level l for the current record.
func (x *extractor) open(e *Engine, l int) {
	g := e.rpt.Groups[l]
	grp := &extractGroup{Level: l + 1, By: display(e.rpt, e.rpt.refOf(g.FieldName)), Value: jsonValue(groupValue(g, e.row))}
	x.stack = x.stack[:min(l, len(x.stack))]
	if l == x.lineLevel {
		// One line of the parent's table, not a group of its own.
		x.lineSeq++
		x.stack = append(x.stack, grp)
		return
	}
	if len(x.stack) == 0 {
		x.out.Groups = append(x.out.Groups, grp)
	} else {
		parent := x.stack[len(x.stack)-1]
		parent.Groups = append(parent.Groups, grp)
	}
	x.stack = append(x.stack, grp)
}

func (x *extractor) band(e *Engine, s Section) {
	var cur *extractGroup
	if len(x.stack) > 0 {
		cur = x.stack[len(x.stack)-1]
	}
	if s.Type == "Details" {
		if x.lineLevel < 0 {
			x.detail(e, s, cur, e.rowIdx)
		}
		return
	}
	if s.Type == "GroupFooter" && s.GroupLevel == x.lineLevel {
		var parent *extractGroup
		if s.GroupLevel > 0 && s.GroupLevel <= len(x.stack) {
			parent = x.stack[s.GroupLevel-1]
		}
		x.detail(e, s, parent, -x.lineSeq) // negative: never a record's index
		return
	}
	if x.lineLevel >= 0 && s.Type == "GroupHeader" && s.GroupLevel >= x.lineLevel {
		return // a line's own header prints nothing a reader needs apart from the line
	}
	vals := x.values(e, s)
	if vals.empty() {
		return
	}
	var into **orderedMap
	switch s.Type {
	case "ReportHeader", "PageHeader":
		into = &x.out.Header
	case "ReportFooter":
		into = &x.out.Footer
	case "GroupHeader", "GroupFooter":
		lvl := s.GroupLevel
		if lvl >= len(x.stack) {
			return
		}
		g := x.stack[lvl]
		if s.Type == "GroupHeader" {
			into = &g.Header
		} else {
			into = &g.Footer
		}
	default:
		return
	}
	if *into == nil {
		*into = &orderedMap{}
	}
	for _, k := range vals.keys {
		(*into).set(k, vals.vals[k], vals.texts[k])
	}
}

// detail adds a line section's values to the current line; a record's
// Details a and Details b (or a group's footers a and b) fill one row.
// seq identifies the line: the record, or the group whose footer it is.
func (x *extractor) detail(e *Engine, s Section, cur *extractGroup, seq int) {
	lines, texts, omitted := &x.out.Lines, &x.out.Texts, &x.out.Omitted
	if cur != nil {
		lines, texts, omitted = &cur.Lines, &cur.Texts, &cur.Omitted
	}
	if seq != x.lastRow {
		x.lastRow = seq
		x.keep = x.maxRows < 0 || x.shown < x.maxRows
		if !x.keep {
			*omitted++
			return
		}
		x.shown++
		*lines = append(*lines, make([]any, len(x.cols)))
		*texts = append(*texts, make([]string, len(x.cols)))
	}
	if !x.keep {
		return
	}
	line, text := (*lines)[len(*lines)-1], (*texts)[len(*texts)-1]
	for _, o := range s.Objects {
		c, ok := x.colIdx[o.Name]
		if !ok || o.Format.Suppress {
			continue
		}
		v, t := e.display(x.cols[c].Ref, o)
		line[c], text[c] = jsonValue(v), t
	}
}

// values is a header or footer band's printed values by label; page numbers
// and print timestamps are left out.
func (x *extractor) values(e *Engine, s Section) *orderedMap {
	m := &orderedMap{}
	for _, row := range visualRows(s.Objects) {
		for _, o := range row {
			if !isField(o) {
				continue
			}
			ref := e.rpt.binding(o, e.ov)
			switch ref {
			case "PageNofM", "PageNumber", "PrintDate", "PrintTime", "DataDate", "DataTime":
				continue
			}
			v, t := e.display(ref, o)
			m.set(e.rpt.labelFor(s, o, x.cols, ref), jsonValue(v), t)
		}
	}
	return m
}

// display is a field's value and the text it prints, uncut by its box.
func (e *Engine) display(ref string, o Object) (any, string) {
	v := e.value(ref)
	format := e.match(e.ov.Formats, ref)
	if format == "" && o.Format.FormatString != nil {
		format = *o.Format.FormatString
	}
	if f, ok := v.(float64); ok && f == 0 && (o.Format.SuppressIfZero || e.matchAny(e.ov.SuppressZero, ref)) {
		return v, ""
	}
	return v, strings.Join(strings.Fields(formatValue(v, format)), " ")
}

// jsonValue makes a value JSON-friendly: dates as "2006-01-02" (with the
// time when there is one), whole numbers stay numbers, NaN and Inf null.
func jsonValue(v any) any {
	switch v := v.(type) {
	case time.Time:
		if v.Hour() == 0 && v.Minute() == 0 && v.Second() == 0 {
			return v.Format("2006-01-02")
		}
		return v.Format("2006-01-02T15:04:05")
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return math.Round(v*1e6) / 1e6
	case int64:
		return float64(v)
	}
	return v
}

// Markdown writes the extraction as headings per group and a table of the
// detail rows, values as the report prints them.
func (x *Extraction) Markdown() string {
	var b strings.Builder
	title := x.Title
	if title == "" {
		title = x.Report
	}
	fmt.Fprintf(&b, "# %s: data\n\n", mdText(title))
	meta := []string{"Report `" + x.Report + "`", "printed " + x.Printed, fmt.Sprintf("%d records", x.Records)}
	if !x.Parameters.empty() {
		meta = append(meta, "parameters "+inline(x.Parameters))
	}
	b.WriteString(strings.Join(meta, " · ") + "\n\n")
	if !x.Header.empty() {
		b.WriteString("**Header:** " + inline(x.Header) + "\n\n")
	}
	x.table(&b, x.Lines, x.Texts, x.Omitted)
	for _, g := range x.Groups {
		x.group(&b, g, 2)
	}
	if !x.Footer.empty() {
		b.WriteString("**Report totals:** " + inline(x.Footer) + "\n\n")
	}
	if len(x.Warnings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, w := range x.Warnings {
			b.WriteString("- " + w + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (x *Extraction) group(b *strings.Builder, g *extractGroup, depth int) {
	head := fmt.Sprintf("%s = %s", g.By, mdText(fmt.Sprint(nilText(g.Value))))
	if !g.Header.empty() {
		head = inline(g.Header)
	}
	if depth <= 6 {
		fmt.Fprintf(b, "%s %s\n\n", strings.Repeat("#", depth), head)
	} else {
		fmt.Fprintf(b, "**%s**\n\n", head)
	}
	x.table(b, g.Lines, g.Texts, g.Omitted)
	for _, sub := range g.Groups {
		x.group(b, sub, depth+1)
	}
	if !g.Footer.empty() {
		fmt.Fprintf(b, "**Totals (%s):** %s\n\n", mdText(fmt.Sprint(nilText(g.Value))), inline(g.Footer))
	}
}

func (x *Extraction) table(b *strings.Builder, lines [][]any, texts [][]string, omitted int) {
	if len(texts) > 0 && x.Form {
		for i, t := range texts {
			fmt.Fprintf(b, "Record %d:\n\n", i+1)
			for c, s := range t {
				if s != "" {
					fmt.Fprintf(b, "- %s: %s\n", mdText(x.Columns[c].Name), mdText(s))
				}
			}
			b.WriteString("\n")
		}
	} else if len(texts) > 0 {
		names := make([]string, len(x.Columns))
		for i, c := range x.Columns {
			names[i] = mdCell(c.Name)
		}
		b.WriteString("| " + strings.Join(names, " | ") + " |\n|" + strings.Repeat(" --- |", len(names)) + "\n")
		for _, t := range texts {
			cells := make([]string, len(t))
			for i, s := range t {
				cells[i] = mdCell(s)
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
		b.WriteString("\n")
	}
	if omitted > 0 {
		fmt.Fprintf(b, "_%d more rows not shown._\n\n", omitted)
	}
}

// inline lists a band's values; blank ones (a zero printed blank) are left
// out.
func inline(m *orderedMap) string {
	parts := make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		if t := m.texts[k]; t != "" {
			parts = append(parts, mdText(k)+": "+mdText(t))
		}
	}
	if len(parts) == 0 {
		return "(blank)"
	}
	return strings.Join(parts, " · ")
}

func nilText(v any) any {
	if v == nil {
		return "(null)"
	}
	return v
}
