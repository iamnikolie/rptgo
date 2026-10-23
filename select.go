package main

// Which records and groups a report prints, and in what order beyond the
// record sort: the record selection formula, groups sorted by a summary
// (Top N, Bottom N, with or without an "Others" group), and the group
// selection formula. Crystal applies them in that order, before the
// running totals see a record.

import (
	"fmt"
	"sort"
	"strings"
)

// othersKey marks a record Top N moved into a level's "Others" group; its
// value is the name that group prints.
func othersKey(l int) string { return fmt.Sprintf("\x00others%d", l) }

// groupKey is what group level l compares for a record.
func (e *Engine) groupKey(l int, r Row) any {
	if v, ok := r[othersKey(l)]; ok {
		return v
	}
	return groupValue(e.rpt.Groups[l], r)
}

// selectRecords keeps the records the record selection formula holds for.
func (e *Engine) selectRecords() {
	src := deref(e.rpt.RecordSelectionFormula)
	if src == "" || len(e.rows) == 0 {
		return
	}
	n, err := parseAny(src, "Boolean")
	if err != nil {
		e.errs[fmt.Sprintf("record selection not applied: %v", err)]++
		return
	}
	all := e.rows
	var kept []Row
	for i, r := range all {
		e.setRow(r)
		e.rowIdx = i
		v, err := eval(n, e, map[string]any{})
		if err != nil {
			e.errs[fmt.Sprintf("record selection: %v", err)]++
			kept = append(kept, r) // a record the formula cannot judge stays
			continue
		}
		if truthy(v) {
			kept = append(kept, r)
		}
	}
	e.rows = kept
	e.setRow(nil)
	e.aggs = map[string]any{}
}

// span is a run of records [lo, hi).
type rowSpan struct{ lo, hi int }

// instances splits records [lo, hi) into the runs that share group level
// l's value (the records are sorted, so each group is one run).
func (e *Engine) instances(l, lo, hi int) []rowSpan {
	var out []rowSpan
	for i := lo; i < hi; {
		j := i + 1
		for j < hi && compare(e.groupKey(l, e.rows[j]), e.groupKey(l, e.rows[i])) == 0 {
			j++
		}
		out = append(out, rowSpan{i, j})
		i = j
	}
	return out
}

// blocks is the runs of records that share every group level above l.
func (e *Engine) blocks(l int) []rowSpan {
	spans := []rowSpan{{0, len(e.rows)}}
	for k := 0; k < l; k++ {
		var next []rowSpan
		for _, s := range spans {
			next = append(next, e.instances(k, s.lo, s.hi)...)
		}
		spans = next
	}
	return spans
}

// orderGroups applies each level's sort by summary: the groups in a block
// ordered by their summary, the first N kept, the rest dropped or gathered
// into one "Others" group.
func (e *Engine) orderGroups() {
	for l, g := range e.rpt.Groups {
		t := g.TopN
		if t == nil {
			continue
		}
		fn := strings.ToLower(t.Function)
		if fn == "" {
			fn = "sum"
		}
		ref := e.rpt.refOf(t.FieldName)
		var rows []Row
		for _, b := range e.blocks(l) {
			type inst struct {
				rows  []Row
				value any
			}
			var list []inst
			for _, s := range e.instances(l, b.lo, b.hi) {
				v, err := e.aggregateSpan(fn, ref, s.lo, s.hi)
				if err != nil {
					e.errs[fmt.Sprintf("group #%d sort by summary: %v", l+1, err)]++
				}
				list = append(list, inst{e.rows[s.lo:s.hi], v})
			}
			sort.SliceStable(list, func(a, c int) bool {
				if t.Bottom {
					return compare(list[a].value, list[c].value) < 0
				}
				return compare(list[a].value, list[c].value) > 0
			})
			var others []Row
			for k, in := range list {
				if t.Count <= 0 || k < t.Count {
					rows = append(rows, in.rows...)
					continue
				}
				if t.DiscardOthers {
					continue
				}
				name := t.OthersName
				if name == "" {
					name = "Others"
				}
				for _, r := range in.rows {
					c := make(Row, len(r)+1)
					for key, v := range r {
						c[key] = v
					}
					c[othersKey(l)] = name
					others = append(others, c)
				}
			}
			// The groups inside "Others" come from several groups; put
			// them back in their own order so each prints once.
			sort.SliceStable(others, func(a, b int) bool { return e.innerLess(l, others[a], others[b]) })
			rows = append(rows, others...)
			others = nil
		}
		e.rows = rows
		e.aggs = map[string]any{}
	}
}

// innerLess orders two records by the group levels below l, each in its
// own sort order.
func (e *Engine) innerLess(l int, a, b Row) bool {
	for k := l + 1; k < len(e.rpt.Groups); k++ {
		g := e.rpt.Groups[k]
		if g.SortOrder == "OriginalOrder" || g.SortOrder == "Specified" {
			continue
		}
		if c := compare(e.groupKey(k, a), e.groupKey(k, b)); c != 0 {
			return (c < 0) != (g.SortOrder == "Descending")
		}
	}
	return false
}

// selectGroups keeps the innermost groups the group selection formula
// holds for, judged on each group's last record with the group's summaries.
func (e *Engine) selectGroups() {
	src := deref(e.rpt.GroupSelectionFormula)
	if src == "" || len(e.rpt.Groups) == 0 || len(e.rows) == 0 {
		return
	}
	n, err := parseAny(src, "Boolean")
	if err != nil {
		e.errs[fmt.Sprintf("group selection not applied: %v", err)]++
		return
	}
	inner := len(e.rpt.Groups) - 1
	var kept []Row
	for _, b := range e.blocks(inner) {
		for _, s := range e.instances(inner, b.lo, b.hi) {
			e.setRow(e.rows[s.hi-1])
			e.rowIdx = s.hi - 1
			v, err := eval(n, e, map[string]any{})
			if err != nil {
				e.errs[fmt.Sprintf("group selection: %v", err)]++
			}
			if err != nil || truthy(v) {
				kept = append(kept, e.rows[s.lo:s.hi]...)
			}
		}
	}
	e.rows = kept
	e.setRow(nil)
	e.aggs = map[string]any{}
}
