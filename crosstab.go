package main

// Cross-tabs: a summary of each cell field per row value and column value
// (nested when there are several row or column fields), with row, column
// and grand totals, over the records of the group the cross-tab sits in or
// the whole report; drawn as a grid that grows down from its box.

import (
	"fmt"
	"sort"
	"strings"
)

// CrossTabData is what a cross-tab shows.
type CrossTabData struct {
	RowFields    []string  `json:"rowFields"`
	ColumnFields []string  `json:"columnFields"`
	Summaries    []string  `json:"summaries"`
	Rows         []string  `json:"rows"`    // each row's values, joined with " / "
	Columns      []string  `json:"columns"` // each column's values, joined with " / "
	Cells        [][][]any `json:"cells"`   // [row][column][summary]
	RowTotals    [][]any   `json:"rowTotals,omitempty"`
	ColumnTotals [][]any   `json:"columnTotals,omitempty"`
	GrandTotal   []any     `json:"grandTotal,omitempty"`
	formats      []string
}

func (e *Engine) crossTabData(o Object, s Section) *CrossTabData {
	if len(o.Cells) == 0 || len(e.rows) == 0 {
		return nil
	}
	level := -1
	if s.Type == "GroupHeader" || s.Type == "GroupFooter" {
		level = s.GroupLevel
	}
	lo, hi := e.spanOf(level)
	ct := &CrossTabData{}
	for _, f := range o.RowGroupFields {
		ct.RowFields = append(ct.RowFields, display(e.rpt, e.rpt.refOf(f)))
	}
	for _, f := range o.ColumnGroupFields {
		ct.ColumnFields = append(ct.ColumnFields, display(e.rpt, e.rpt.refOf(f)))
	}
	for _, c := range o.Cells {
		fn := c.Function
		if fn == "" {
			fn = "Sum"
		}
		ct.Summaries = append(ct.Summaries, fn+" of "+display(e.rpt, e.rpt.refOf(c.FieldName)))
	}
	// keyOf is a record's values of fields, and their text.
	keyOf := func(i int, fields []string) ([]any, string) {
		var vals []any
		var parts []string
		for _, f := range fields {
			v, _ := e.atRow(i, e.rpt.refOf(f))
			vals = append(vals, v)
			parts = append(parts, chartLabel(v))
		}
		return vals, strings.Join(parts, " / ")
	}
	type cell struct{ r, c string }
	totals := map[cell][]*runTotal{}
	rowVals, colVals := map[string][]any{}, map[string][]any{}
	add := func(k cell, i int) {
		if totals[k] == nil {
			for _, c := range o.Cells {
				fn := c.Function
				if fn == "" {
					fn = "Sum"
				}
				totals[k] = append(totals[k], &runTotal{fn: fn})
			}
		}
		for j, c := range o.Cells {
			v, err := e.atRow(i, e.rpt.refOf(c.FieldName))
			if err != nil {
				e.errs[fmt.Sprintf("cross-tab %s: %v", o.Name, err)]++
			}
			totals[k][j].add(v)
		}
	}
	const all = "\x00all"
	for i := lo; i < hi; i++ {
		rv, rk := keyOf(i, o.RowGroupFields)
		cv, ck := keyOf(i, o.ColumnGroupFields)
		rowVals[rk], colVals[ck] = rv, cv
		for _, k := range []cell{{rk, ck}, {rk, all}, {all, ck}, {all, all}} {
			add(k, i)
		}
	}
	order := func(m map[string][]any) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.SliceStable(keys, func(a, b int) bool {
			va, vb := m[keys[a]], m[keys[b]]
			for i := range va {
				if c := compare(va[i], vb[i]); c != 0 {
					return c < 0
				}
			}
			return false
		})
		return keys
	}
	results := func(k cell) []any {
		var out []any
		for _, t := range totals[k] {
			out = append(out, jsonValue(t.result()))
		}
		return out
	}
	ct.Rows, ct.Columns = order(rowVals), order(colVals)
	for _, r := range ct.Rows {
		var row [][]any
		for _, c := range ct.Columns {
			if totals[cell{r, c}] == nil {
				row = append(row, make([]any, len(o.Cells)))
				continue
			}
			row = append(row, results(cell{r, c}))
		}
		ct.Cells = append(ct.Cells, row)
		if !o.SuppressRowGrandTotals {
			ct.RowTotals = append(ct.RowTotals, results(cell{r, all}))
		}
	}
	if !o.SuppressColumnGrandTotals {
		for _, c := range ct.Columns {
			ct.ColumnTotals = append(ct.ColumnTotals, results(cell{all, c}))
		}
	}
	if !o.SuppressRowGrandTotals && !o.SuppressColumnGrandTotals {
		ct.GrandTotal = results(cell{all, all})
	}
	ct.formats = make([]string, len(o.Cells))
	if f := o.Format.FormatString; f != nil {
		for i := range ct.formats {
			ct.formats[i] = *f
		}
	}
	return ct
}

// grid is the cross-tab as rows of cell texts, headings included.
func (ct *CrossTabData) grid() [][]string {
	cellText := func(vals []any) string {
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = formatValue(v, ct.formats[i])
		}
		return strings.Join(parts, "\n")
	}
	head := append([]string{strings.Join(ct.RowFields, " / ")}, ct.Columns...)
	if ct.RowTotals != nil {
		head = append(head, "Total")
	}
	out := [][]string{head}
	for i, r := range ct.Rows {
		line := []string{r}
		for _, c := range ct.Cells[i] {
			line = append(line, cellText(c))
		}
		if ct.RowTotals != nil {
			line = append(line, cellText(ct.RowTotals[i]))
		}
		out = append(out, line)
	}
	if ct.ColumnTotals != nil {
		line := []string{"Total"}
		for _, c := range ct.ColumnTotals {
			line = append(line, cellText(c))
		}
		if ct.GrandTotal != nil {
			line = append(line, cellText(ct.GrandTotal))
		}
		out = append(out, line)
	}
	return out
}

// drawCrossTab draws the grid from the object's corner and returns its
// height: the row headings as wide as the object allows for one column of
// values per column value, at least an inch each.
func (e *Engine) drawCrossTab(ct *CrossTabData, o Object, x, y int) int {
	grid := ct.grid()
	cols := len(grid[0])
	headW := max(o.Bounds.Width/max(cols, 1), 1440)
	cellW := headW
	lineH := 240 * max(len(ct.Summaries), 1)
	f := o.Format
	fnt := font{name: f.FontName, size: f.FontSize, bold: f.Bold}
	if fnt.name == "" {
		fnt = font{name: "Arial", size: 8}
	}
	for r, line := range grid {
		h := 240
		if r > 0 {
			h = lineH
		}
		cx := x
		for c, txt := range line {
			w := cellW
			if c == 0 {
				w = headW
			}
			align := "Right"
			if r == 0 || c == 0 {
				align = "Left"
			}
			cf := fnt
			cf.bold = r == 0 || c == 0 || (ct.RowTotals != nil && c == len(line)-1)
			e.page.add(item{kind: itemText, x: cx + 30, y: y, w: w - 60, h: h, text: txt, align: align, font: cf})
			e.page.add(item{kind: itemBorder, x: cx, y: y, w: w, h: h, border: borders{top: 1, left: 1, right: 1, bottom: 1, width: 0.5}})
			cx += w
		}
		y += h
	}
	return ct.height()
}

// height is the drawn grid's height in twips.
func (ct *CrossTabData) height() int {
	return 240 + 240*max(len(ct.Summaries), 1)*(len(ct.grid())-1)
}

// markdown writes the cross-tab as a table.
func (ct *CrossTabData) markdown() string {
	var b strings.Builder
	grid := ct.grid()
	for r, line := range grid {
		cells := make([]string, len(line))
		for i, c := range line {
			cells[i] = mdCell(strings.ReplaceAll(c, "\n", " / "))
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if r == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", len(line)) + "\n")
		}
	}
	return b.String()
}
