package main

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Engine runs the banded layout Crystal uses: report header, then for each
// page a page header and footer; records grouped by the group fields, each
// group opening with its header and closing with its footer; details per
// record; report footer at the end.
type Engine struct {
	rpt      *Report
	ov       *Overlay
	rows     []Row
	printed  time.Time
	formulas map[string]node
	rtots    []runTotal
	// params are the report's parameter values, {?Name}, by lower-case name.
	params map[string]any
	// conds holds the parsed section formulas (suppress, new page), by text.
	conds map[string]node
	// ext, when set, receives the bands instead of the page layout (extract).
	ext *extractor
	// aggs caches the summaries formulas take over the report or a group.
	aggs map[string]any

	pages     []*pageOut
	page      *pageOut
	done      []*span // multi-section lines already ended on this page
	y         int     // twips from the top of the page
	bottom    int     // last twip usable by bands above the page footer
	body      int     // where the page body starts, below the page header
	breakAt   bool    // a section asked for a new page after it
	titlePage bool    // the first page holds the report header alone
	row       Row
	prevRow   Row
	rowIdx    int // index of row in rows
	cache     map[string]any
	errs      map[string]int
}

type runTotal struct {
	name    string
	of      string // field reference summed
	fn      string // Sum, Count, DistinctCount, Average, Maximum, Minimum
	resetAt int    // group level whose change resets it; -1 = never
	evalAt  int    // add only on a change of this group level; -1 = every record
	evalIf  node   // add only where this formula holds; nil = always
	value   float64
	count   int             // non-null values seen
	seen    map[string]bool // DistinctCount: the values seen
	extreme any             // Maximum / Minimum so far
}

// add takes one record's value into the total.
func (rt *runTotal) add(v any) {
	if v == nil {
		return
	}
	rt.count++
	rt.value += num(v)
	switch rt.fn {
	case "DistinctCount":
		if rt.seen == nil {
			rt.seen = map[string]bool{}
		}
		rt.seen[fmt.Sprintf("%T:%v", v, v)] = true
	case "Maximum":
		if rt.extreme == nil || compare(v, rt.extreme) > 0 {
			rt.extreme = v
		}
	case "Minimum":
		if rt.extreme == nil || compare(v, rt.extreme) < 0 {
			rt.extreme = v
		}
	}
}

// result is what the total prints. A total that met only nulls is null
// itself and prints blank (a group with no detail rows that count).
func (rt *runTotal) result() any {
	if rt.count == 0 && rt.fn != "Count" && rt.fn != "DistinctCount" {
		return nil
	}
	switch rt.fn {
	case "Count":
		return float64(rt.count)
	case "DistinctCount":
		return float64(len(rt.seen))
	case "Average":
		if rt.count == 0 {
			return 0.0
		}
		return rt.value / float64(rt.count)
	case "Maximum", "Minimum":
		return rt.extreme
	case "Sum", "":
		return rt.value
	}
	return nil // StandardDeviation, Variance, Percentage, ...: not computed
}

// aggregates are the summary operations runTotal computes.
var aggregates = map[string]bool{"Sum": true, "Count": true, "DistinctCount": true, "Average": true, "Maximum": true, "Minimum": true, "": true}

type pageOut struct {
	items []item
	spans map[string]*span // open multi-section lines, by object
}

// span is a line that runs down past its own band: from the band's first
// instance on the page to the instance of the section it ends in (endType,
// endLevel, endBottom twips into it), or to the bottom of the page body when
// that section does not print on the page.
type span struct {
	x, width, thickness, top int
	endType                  string
	endLevel                 int
	endBottom                int
	end                      int  // set once the end section printed
	ended                    bool // end is set
	untilReportEnd           bool // inferred end: on the last page, stop where the report ends
}

// NewEngine prepares a report for its rows. A formula the interpreter cannot
// parse is left out with a warning (in errs); what refers to it prints blank.
func NewEngine(r *Report, ov *Overlay, rows []Row, printed time.Time) (*Engine, error) {
	e := &Engine{rpt: r, ov: ov, rows: rows, printed: printed, formulas: map[string]node{},
		params: map[string]any{}, conds: map[string]node{}, aggs: map[string]any{}, errs: map[string]int{}}
	for name, src := range r.formulas() {
		n, err := parseAny(src, r.formulaField(name).ResultType)
		if err != nil {
			e.errs[fmt.Sprintf("formula @%s not evaluated: %v", name, err)]++
			continue
		}
		e.formulas[strings.ToLower(name)] = n
	}
	for _, f := range r.runningTotals() {
		rt := runTotal{name: f.Name, of: f.SummarizedFieldName, fn: f.Function, resetAt: e.resetLevel(f), evalAt: -1}
		if c := f.EvaluateCondition; c != nil {
			if lvl, ok := conditionGroup(*c); ok {
				rt.evalAt = lvl
			} else if src, ok := strings.CutPrefix(*c, "Formula:"); ok {
				var err error
				if rt.evalIf, err = parseFormula(src); err != nil {
					return nil, fmt.Errorf("running total %s condition: %w", f.Name, err)
				}
			}
		}
		if !aggregates[rt.fn] {
			e.errs[fmt.Sprintf("running total %s: %s not computed", f.Name, rt.fn)]++
		}
		e.rtots = append(e.rtots, rt)
	}
	// Summary fields in group and report footers total their field over the
	// group (or the report), as a running total reset with that group.
	for _, s := range r.Sections {
		if s.Type != "GroupFooter" && s.Type != "ReportFooter" {
			continue
		}
		for _, o := range s.Objects {
			if o.SummaryFunction == nil || o.FieldName == "" {
				continue
			}
			reset := -1
			if s.Type == "GroupFooter" {
				reset = s.GroupLevel
			}
			if !aggregates[*o.SummaryFunction] {
				e.errs[fmt.Sprintf("summary %s of %s not computed", *o.SummaryFunction, o.FieldName)]++
			}
			e.rtots = append(e.rtots, runTotal{name: summaryName(o), of: r.refOf(o.FieldName),
				fn: *o.SummaryFunction, resetAt: reset, evalAt: -1})
		}
	}
	return e, nil
}

// parseAny parses a formula in Basic or Crystal syntax.
func parseAny(src, resultType string) (node, error) {
	if isBasic(src) {
		return parseBasic(src, resultType)
	}
	return parseFormula(src)
}

// SetParams sets the report's parameter values, Name=Value as on the command
// line; a parameter not given takes its default.
// A value takes the parameter's declared type: "false" is false for a
// Boolean, "007" stays text for a String.
func (e *Engine) SetParams(params []string) error {
	for _, p := range params {
		name, val, ok := strings.Cut(p, "=")
		if !ok {
			return fmt.Errorf("param %q: want Name=Value", p)
		}
		name = strings.TrimPrefix(name, "@")
		e.params[strings.ToLower(name)] = e.paramValue(name, val)
	}
	return nil
}

// paramField is the report's parameter of that name; a stored procedure's
// parameters are named with an "@" ({?@AsOf}).
func (e *Engine) paramField(name string) *Field {
	name = strings.TrimPrefix(name, "@")
	if f := e.rpt.fieldKind("?", name); f != nil {
		return f
	}
	return e.rpt.fieldKind("?", "@"+name)
}

// paramValue converts a parameter's text to its declared type.
func (e *Engine) paramValue(name, val string) any {
	f := e.paramField(name)
	if f == nil || f.DataType == "" {
		v := paramValue(val)
		if n, ok := v.(int64); ok {
			return float64(n)
		}
		return v
	}
	return typedValue(val, f.DataType)
}

// cond evaluates a section formula (suppress, new page) for the current
// record; an empty or unparsable one is false.
func (e *Engine) cond(src *string) bool {
	if src == nil || strings.TrimSpace(*src) == "" {
		return false
	}
	n, ok := e.conds[*src]
	if !ok {
		var err error
		if n, err = parseAny(*src, "Boolean"); err != nil {
			e.errs[fmt.Sprintf("section formula %q not evaluated: %v", *src, err)]++
		}
		e.conds[*src] = n
	}
	if n == nil {
		return false
	}
	v, err := eval(n, e, map[string]any{})
	if err != nil {
		e.errs[fmt.Sprintf("section formula %q: %v", *src, err)]++
		return false
	}
	return truthy(v)
}

// suppressed: the section does not print for the current record.
func (e *Engine) suppressed(s Section) bool { return s.Suppress || e.cond(s.SuppressFormula) }

// conditionGroup reads "Group:<level>".
func conditionGroup(c string) (int, bool) {
	var lvl int
	if _, err := fmt.Sscanf(c, "Group:%d", &lvl); err != nil {
		return 0, false
	}
	return lvl, true
}

// resetLevel: the overlay decides, then the reset condition read from the
// .rpt. A model without one (a JSON from the C# reader) infers it: a running
// total printed in a group footer resets with that group, one printed only
// in the report footer never resets.
func (e *Engine) resetLevel(f Field) int {
	name := f.Name
	if e.rpt.FromRpt && e.ov.Resets[name] == "" {
		if f.ResetCondition == nil {
			return -1
		}
		if lvl, ok := conditionGroup(*f.ResetCondition); ok {
			return lvl
		}
	}
	if v, ok := e.ov.Resets[name]; ok {
		if v == "never" {
			return -1
		}
		var lvl int
		fmt.Sscanf(v, "group:%d", &lvl)
		return lvl
	}
	level := -1
	for _, s := range e.rpt.Sections {
		if s.Type != "GroupFooter" {
			continue
		}
		for _, o := range s.Objects {
			if e.rpt.binding(o, e.ov) == "#"+name && s.GroupLevel > level {
				level = s.GroupLevel
			}
		}
	}
	return level
}

func (e *Engine) sections(typ string, level int) []Section {
	var out []Section
	for _, s := range e.rpt.Sections {
		if s.Type == typ && (level < 0 || s.GroupLevel == level) {
			out = append(out, s)
		}
	}
	return out
}

// ---- Env for formulas ----

func (e *Engine) Lookup(ref string) (any, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "@"):
		return e.formula(ref[1:])
	case strings.HasPrefix(ref, "#"):
		for _, rt := range e.rtots {
			if strings.EqualFold(rt.name, ref[1:]) {
				return rt.result(), nil
			}
		}
		return nil, fmt.Errorf("unknown running total %s", ref)
	}
	if name, ok := strings.CutPrefix(ref, "?"); ok {
		name = strings.TrimPrefix(name, "@")
		if v, ok := e.params[strings.ToLower(name)]; ok {
			return v, nil
		}
		if f := e.paramField(name); f != nil && f.DefaultValue != nil {
			return e.paramValue(name, *f.DefaultValue), nil
		}
		return nil, fmt.Errorf("no value for parameter %s", ref)
	}
	switch strings.ToLower(ref) {
	case "pagenumber":
		return float64(len(e.pages)), nil
	case "printdate", "currentdate", "datadate":
		return dateOnly(e.printed), nil
	case "printtime", "currenttime", "datatime":
		return e.printed, nil
	case "recordnumber":
		return float64(e.rowIdx + 1), nil
	case "onfirstrecord":
		return e.rowIdx == 0, nil
	case "onlastrecord":
		return e.rowIdx == len(e.rows)-1, nil
	case "crlf":
		return "\n", nil
	}
	col := ref
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		col = ref[i+1:]
	}
	if e.row == nil {
		return nil, nil
	}
	v, ok := e.row[strings.ToLower(col)]
	if !ok {
		return nil, fmt.Errorf("no column %s", col)
	}
	return v, nil
}

// atRow looks ref up as it is on record i (nil past either end).
func (e *Engine) atRow(i int, ref string) (any, error) {
	if i < 0 || i >= len(e.rows) {
		return nil, nil
	}
	row, prev, idx, cache := e.row, e.prevRow, e.rowIdx, e.cache
	defer func() { e.row, e.prevRow, e.rowIdx, e.cache = row, prev, idx, cache }()
	e.row, e.rowIdx, e.cache = e.rows[i], i, map[string]any{}
	e.prevRow = nil
	if i > 0 {
		e.prevRow = e.rows[i-1]
	}
	return e.Lookup(ref)
}

func (e *Engine) atOffset(ref string, offset int) (any, error) { return e.atRow(e.rowIdx+offset, ref) }

// aggregate is Sum, Count, Average, Maximum, Minimum or DistinctCount of
// ref over the report, or over the current record's group whose field is
// group. Count counts the values that are not null.
func (e *Engine) aggregate(fn, ref, group string) (any, error) {
	lo, hi := 0, len(e.rows)
	if group != "" {
		level := -1
		for l, g := range e.rpt.Groups {
			if strings.EqualFold(display(e.rpt, e.rpt.groupRef(g)), display(e.rpt, strings.TrimSpace(group))) {
				level = l
			}
		}
		if level < 0 {
			return nil, fmt.Errorf("%s(): {%s} is not a group of the report", fn, group)
		}
		same := func(j int) bool {
			for l := 0; l <= level; l++ {
				g := e.rpt.Groups[l]
				if compare(groupValue(g, e.rows[j]), groupValue(g, e.rows[e.rowIdx])) != 0 {
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
	}
	key := fmt.Sprintf("agg:%s:%s:%d:%d", fn, strings.ToLower(ref), lo, hi)
	if v, ok := e.aggs[key]; ok {
		return v, nil
	}
	rt := runTotal{fn: map[string]string{"sum": "Sum", "count": "Count", "average": "Average",
		"maximum": "Maximum", "minimum": "Minimum", "distinctcount": "DistinctCount"}[fn]}
	for i := lo; i < hi; i++ {
		v, err := e.atRow(i, ref)
		if err != nil {
			return nil, err
		}
		rt.add(v)
	}
	v := rt.result()
	if v == nil && (rt.fn == "Sum" || rt.fn == "Average") {
		v = 0.0
	}
	e.aggs[key] = v
	return v, nil
}

func (e *Engine) formula(name string) (any, error) {
	key := strings.ToLower(name)
	if v, ok := e.cache[key]; ok {
		return v, nil
	}
	n, ok := e.formulas[key]
	if !ok {
		return nil, fmt.Errorf("unknown formula @%s", name)
	}
	v, err := eval(n, e, map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("@%s: %w", name, err)
	}
	e.cache[key] = v
	return v, nil
}

func (e *Engine) value(ref string) any {
	v, err := e.Lookup(ref)
	if err != nil {
		e.errs[err.Error()]++
		return nil
	}
	return v
}

// ---- record loop ----

func (e *Engine) Run() {
	groups := e.rpt.Groups
	e.sortRows()
	e.aggs = map[string]any{} // a sort formula may have summarized the unsorted rows

	e.setRow(firstOrNil(e.rows))
	if e.ext != nil {
		e.ext.start(e)
	} else {
		e.newPage(true)
	}

	for i, r := range e.rows {
		changed := len(groups) // first level whose value changes
		if i > 0 {
			for l, g := range groups {
				if compare(groupValue(g, e.rows[i-1]), groupValue(g, r)) != 0 {
					changed = l
					break
				}
			}
		} else {
			changed = 0
		}
		if i > 0 {
			for l := len(groups) - 1; l >= changed; l-- {
				e.bands("GroupFooter", l)
			}
		}
		for l := changed; l < len(groups); l++ {
			e.reset(l)
		}
		e.setRow(r)
		e.rowIdx = i
		e.accumulate(changed)
		for l := changed; l < len(groups); l++ {
			if e.ext != nil {
				e.ext.open(e, l)
			}
			e.bands("GroupHeader", l)
		}
		e.bands("Details", -1)
	}
	if len(e.rows) > 0 {
		for l := len(groups) - 1; l >= 0; l-- {
			e.bands("GroupFooter", l)
		}
	}
	e.bands("ReportFooter", -1)
	if e.ext != nil {
		return
	}
	e.closePage(true)
}

// groupValue is what a group compares: the field, or for a date grouped by
// a period ("Daily", "Monthly", ...) the start of that period.
func groupValue(g Group, r Row) any {
	v := r[strings.ToLower(g.FieldName)]
	t, ok := v.(time.Time)
	if !ok {
		return v
	}
	switch g.Condition {
	case "Daily":
		return dateOnly(t)
	case "Weekly": // weeks start on Sunday
		d := dateOnly(t)
		return d.AddDate(0, 0, -int(d.Weekday()))
	case "Monthly":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case "Quarterly":
		return time.Date(t.Year(), (t.Month()-1)/3*3+1, 1, 0, 0, 0, 0, t.Location())
	case "Annually":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	}
	return v
}

// sortRows orders the records as Crystal does: by each group field, then by
// the report's record sorts; ties keep the procedure's order.
func (e *Engine) sortRows() {
	type key struct {
		value func(Row) any
		desc  bool
	}
	var keys []key
	for _, g := range e.rpt.Groups {
		if g.SortOrder == "OriginalOrder" || g.SortOrder == "Specified" {
			continue
		}
		col := strings.ToLower(g.FieldName)
		keys = append(keys, key{func(r Row) any { return r[col] }, g.SortOrder == "Descending"})
	}
	for _, sf := range e.rpt.SortFields {
		desc := sf.Direction == "Descending"
		if strings.HasPrefix(sf.FieldName, "@") {
			name := sf.FieldName[1:]
			keys = append(keys, key{func(r Row) any {
				e.setRow(r)
				v, _ := e.formula(name)
				return v
			}, desc})
			continue
		}
		col := strings.ToLower(sf.FieldName)
		keys = append(keys, key{func(r Row) any { return r[col] }, desc})
	}
	if len(keys) == 0 {
		return
	}
	vals := make([][]any, len(e.rows))
	for i, r := range e.rows {
		vals[i] = make([]any, len(keys))
		for k, kf := range keys {
			vals[i][k] = kf.value(r)
		}
	}
	idx := make([]int, len(e.rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		for k, kf := range keys {
			c := compare(vals[idx[a]][k], vals[idx[b]][k])
			if c != 0 {
				if kf.desc {
					return c > 0
				}
				return c < 0
			}
		}
		return false
	})
	sorted := make([]Row, len(e.rows))
	for i, j := range idx {
		sorted[i] = e.rows[j]
	}
	e.rows = sorted
	e.setRow(nil)
}

func firstOrNil(rows []Row) Row {
	if len(rows) > 0 {
		return rows[0]
	}
	return nil
}

func (e *Engine) setRow(r Row) {
	e.prevRow, e.row = e.row, r
	e.cache = map[string]any{}
}

func (e *Engine) reset(level int) {
	for i := range e.rtots {
		if e.rtots[i].resetAt == level {
			rt := e.rtots[i]
			e.rtots[i] = runTotal{name: rt.name, of: rt.of, fn: rt.fn, resetAt: rt.resetAt, evalAt: rt.evalAt, evalIf: rt.evalIf}
		}
	}
}

// accumulate adds the current record to the running totals that evaluate
// on it; changed is the outermost group level the record starts anew.
func (e *Engine) accumulate(changed int) {
	for i := range e.rtots {
		rt := &e.rtots[i]
		if rt.evalAt >= 0 && changed > rt.evalAt {
			continue
		}
		if rt.evalIf != nil {
			ok, err := eval(rt.evalIf, e, map[string]any{})
			if err != nil {
				e.errs[fmt.Sprintf("running total %s condition: %v", rt.name, err)]++
				continue
			}
			if !truthy(ok) {
				continue
			}
		}
		rt.add(e.value(rt.of))
	}
	e.cache = map[string]any{}
}

// ---- pages and bands ----

func (e *Engine) pageFooterHeight() int {
	h := 0
	for _, s := range e.sections("PageFooter", -1) {
		if !e.suppressed(s) {
			h += s.HeightTwips
		}
	}
	return h
}

func (e *Engine) newPage(first bool) {
	e.page = &pageOut{spans: map[string]*span{}}
	e.done = nil
	e.pages = append(e.pages, e.page)
	e.y = e.rpt.Page.TopMarginTwips
	e.bottom = e.rpt.Page.HeightTwips - e.rpt.Page.BottomMarginTwips - e.pageFooterHeight()
	if first {
		// The report header prints above the first page header.
		for _, s := range e.sections("ReportHeader", -1) {
			if !e.suppressed(s) {
				e.place(s)
				e.breakAt = e.breakAt || s.NewPageAfter || e.cond(s.NewPageAfterFormula)
			}
		}
		// A report header that asks for a new page after it is a title page.
		e.titlePage = e.breakAt
	}
	for _, s := range e.sections("PageHeader", -1) {
		e.place(s)
	}
	e.body = e.y
}

func (e *Engine) finishPage() { e.closePage(false) }

// closePage draws the page footer and the multi-section lines: those whose
// end section printed stop there, the rest run to the bottom of the page body.
func (e *Engine) closePage(last bool) {
	if last {
		for _, key := range e.spanKeys() {
			sp := e.page.spans[key]
			if sp.untilReportEnd {
				sp.end, sp.ended = e.y, true
				e.done = append(e.done, sp)
				delete(e.page.spans, key)
			}
		}
	}
	y := e.bottom
	for _, s := range e.sections("PageFooter", -1) {
		if e.suppressed(s) {
			continue
		}
		e.endSpans(s, y)
		e.draw(s, y)
		y += s.HeightTwips
	}
	for _, key := range e.spanKeys() {
		sp := e.page.spans[key]
		if !sp.ended {
			sp.end = e.bottom
			if last && sp.untilReportEnd {
				sp.end = e.y
			}
		}
		e.done = append(e.done, sp)
	}
	for _, sp := range e.done {
		e.page.add(item{kind: itemFill, x: sp.x, y: sp.top, w: sp.thickness, h: sp.end - sp.top})
	}
}

// spanKeys is the open lines' keys in a fixed order, so output is reproducible.
func (e *Engine) spanKeys() []string {
	keys := make([]string, 0, len(e.page.spans))
	for k := range e.page.spans {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// endSpans closes the open lines that end in the section printing at y.
func (e *Engine) endSpans(s Section, y int) {
	for _, key := range e.spanKeys() {
		sp := e.page.spans[key]
		if !sp.ended && sp.endType == s.Type && sp.endLevel == s.GroupLevel {
			sp.end, sp.ended = y+sp.endBottom, true
			e.done = append(e.done, sp)
			delete(e.page.spans, key)
		}
	}
}

func (e *Engine) bands(typ string, level int) {
	for _, s := range e.sections(typ, level) {
		if e.suppressed(s) {
			continue
		}
		if e.ext != nil {
			e.ext.band(e, s)
			continue
		}
		// A new page before the section, or one the last section asked for
		// after it; neither leaves a page with nothing but its header.
		brk := e.breakAt || s.NewPageBefore || e.cond(s.NewPageBeforeFormula)
		e.breakAt = false
		if (brk && (e.y > e.body || e.titlePage)) || e.y+s.HeightTwips > e.bottom {
			e.titlePage = false
			e.finishPage()
			e.newPage(false)
		}
		e.place(s)
		e.breakAt = s.NewPageAfter || e.cond(s.NewPageAfterFormula)
	}
}

func (e *Engine) place(s Section) {
	if e.suppressed(s) {
		return
	}
	e.endSpans(s, e.y)
	e.draw(s, e.y)
	e.y += s.HeightTwips
}

func (e *Engine) draw(s Section, top int) {
	left := e.rpt.Page.LeftMarginTwips
	for _, o := range s.Objects {
		if o.Format.Suppress {
			continue
		}
		x, y := left+o.Bounds.Left, top+o.Bounds.Top
		switch {
		case strings.Contains(o.Type, "Line"):
			thickness := o.Format.BorderWidthTwips
			if thickness <= 0 {
				thickness = 15
			}
			endType, endLevel, endBottom, inferred := "", 0, 0, false
			if o.EndSection != nil {
				endType, endLevel, endBottom = o.EndSection.Type, o.EndSection.GroupLevel, o.EndBottomTwips
			} else if o.Bounds.Top+o.Bounds.Height > s.HeightTwips {
				// No end in the model (a JSON from the old dump): measure the
				// height down the design view's section order.
				endType, endBottom = e.lineEnd(s, o.Bounds.Top+o.Bounds.Height)
				endLevel, inferred = e.levelOf(endType, s), true
			}
			if endType != "" {
				// One line per run from this band down to its end section.
				key := fmt.Sprintf("%s/%d/%d/%d", s.Type, s.GroupLevel, o.Bounds.Left, o.Bounds.Top)
				if _, open := e.page.spans[key]; !open {
					e.page.spans[key] = &span{x: x, width: o.Bounds.Width, thickness: max(o.Bounds.Width, thickness),
						top: y, endType: endType, endLevel: endLevel, endBottom: endBottom, untilReportEnd: inferred}
				}
				continue
			}
			h := min(o.Bounds.Height, s.HeightTwips-o.Bounds.Top)
			e.page.add(item{kind: itemFill, x: x, y: y, w: max(o.Bounds.Width, thickness), h: max(h, thickness)})
		case strings.Contains(o.Type, "Box"):
			e.page.add(item{kind: itemBox, x: x, y: y, w: o.Bounds.Width, h: o.Bounds.Height})
		case strings.Contains(o.Type, "Text"):
			txt := o.Text
			if t, ok := e.ov.Text[txt]; ok {
				txt = t
			}
			e.text(o, x, y, txt, textAlign(o.Format.HAlign), e.borderOf(o, ""))
		case strings.Contains(o.Type, "Field"):
			ref := e.rpt.binding(o, e.ov)
			txt, align := e.fieldText(ref, o)
			e.text(o, x, y, txt, align, e.borderOf(o, ref))
		}
	}
}

// designOrder is the section order of Crystal's design view, which is where
// a line's height is measured when it runs past its own section.
func (e *Engine) designOrder() []Section {
	var out []Section
	add := func(typ string, level int) { out = append(out, e.sections(typ, level)...) }
	add("ReportHeader", -1)
	add("PageHeader", -1)
	for l := 0; l < len(e.rpt.Groups); l++ {
		add("GroupHeader", l)
	}
	add("Details", -1)
	for l := len(e.rpt.Groups) - 1; l >= 0; l-- {
		add("GroupFooter", l)
	}
	add("ReportFooter", -1)
	add("PageFooter", -1)
	return out
}

// levelOf: the group level of a section kind reached from s in design order
// (group footers are walked innermost first from a details band).
func (e *Engine) levelOf(typ string, from Section) int {
	if typ == "GroupHeader" || typ == "GroupFooter" {
		return max(len(e.rpt.Groups)-1, 0)
	}
	return 0
}

// lineEnd finds the section a line ends in, and how far into it.
func (e *Engine) lineEnd(from Section, bottom int) (string, int) {
	order := e.designOrder()
	i := 0
	for i < len(order) && !(order[i].Type == from.Type && order[i].GroupLevel == from.GroupLevel) {
		i++
	}
	for ; i < len(order); i++ {
		if bottom <= order[i].HeightTwips {
			return order[i].Type, bottom
		}
		bottom -= order[i].HeightTwips
	}
	return "", 0
}

// fieldText is what a field object prints and its alignment. The format is
// the overlay's, else the model's FormatString. Alignment "Default" is
// Crystal's unset one: numbers right, text and dates left.
func (e *Engine) fieldText(ref string, o Object) (string, string) {
	align := o.Format.HAlign
	if a := e.match(e.ov.Align, ref); a != "" {
		align = a
	}
	switch ref {
	case "PageNofM":
		return "Page " + pageSlot + " of " + totalSlot, textAlign(align)
	case "PageNumber":
		return pageSlot, textAlign(align)
	case "PrintDate":
		return formatDate(e.printed, defaultDate), textAlign(align)
	case "PrintTime", "DataTime":
		return formatDate(e.printed, defaultTime), textAlign(align)
	}
	format := e.match(e.ov.Formats, ref)
	if format == "" && o.Format.FormatString != nil {
		format = *o.Format.FormatString
	}
	v := e.value(ref)
	if f, ok := v.(float64); ok {
		if f == 0 && (o.Format.SuppressIfZero || e.matchAny(e.ov.SuppressZero, ref)) {
			return "", textAlign(align)
		}
		switch {
		case align == "Default" || align == "":
			align = "Right"
		case align == "Left" && o.Format.FormatString == nil && e.isAmount(ref) && e.match(e.ov.Align, ref) == "":
			// The old JSON dump read every alignment as Left; an amount there is
			// Crystal's default, right.
			align = "Right"
		}
	}
	txt := formatValue(v, format)
	if _, number := v.(float64); !number && !o.Format.CanGrow {
		txt = fitText(txt, o.Format, o.Bounds.Width)
	} else if number && align == "Left" && o.Format.NegativeFormat == 1 {
		// A number whose negatives take a leading minus keeps a space in
		// its place when left-aligned (2 pt at 8 pt Arial); one shown
		// without a sign does not.
		txt = " " + txt
	}
	return txt, textAlign(align)
}

func textAlign(a string) string {
	switch a {
	case "Center", "Right":
		return a
	}
	return "Left"
}

// borders is an object's edges: style codes (0 none, 1 single, 2 double,
// 3 dashed, 4 dotted) and the line width in points.
type borders struct {
	top, bottom, left, right int
	width                    float64
}

// borderOf is the object's borders: the overlay's for the reference, else the
// model's, at the model's line width.
func (e *Engine) borderOf(o Object, ref string) borders {
	f := o.Format
	b := borders{f.BorderTop, f.BorderBottom, f.BorderLeft, f.BorderRight, float64(f.BorderWidthTwips) / 20}
	if b.width <= 0 {
		b.width = 1
	}
	if ov := e.match(e.ov.Borders, ref); ov != "" && ref != "" {
		b.top, b.bottom, b.left, b.right = 0, 0, 0, 0
		switch ov {
		case "top":
			b.top = 1
		case "top-double":
			b.top = 2
		case "bottom":
			b.bottom = 1
		case "bottom-double":
			b.bottom = 2
		}
	}
	return b
}

// edgeWidth is how much room an edge's line takes: a double line is two
// lines with a gap of the same width.
func (b borders) edgeWidth(code int) float64 {
	switch code {
	case 0:
		return 0
	case 2:
		return 3 * b.width
	}
	return b.width
}

// borderBox draws the borders outside the object's box, a point clear of it,
// which is where Crystal's PDF has them: a total's single rule 1.3pt above
// its field, the report total's double rule 1.6pt and 3.6pt above.
func (b borders) borderBox(x, y, w, h int) (item, bool) {
	if b.top == 0 && b.bottom == 0 && b.left == 0 && b.right == 0 {
		return item{}, false
	}
	out := func(code int) int {
		if code == 0 {
			return 0
		}
		return int((b.edgeWidth(code) + 1) * 20)
	}
	t, bo, l, r := out(b.top), out(b.bottom), out(b.left), out(b.right)
	return item{kind: itemBorder, x: x - l, y: y - t, w: w + l + r, h: h + t + bo, border: b}, true
}

// isAmount: formulas, running totals and currency columns print right-aligned
// by default; plain numeric columns (ids, numbers) keep the layout's own.
func (e *Engine) isAmount(ref string) bool {
	if strings.HasPrefix(ref, "@") || strings.HasPrefix(ref, "#") {
		return true
	}
	f := e.rpt.field(ref)
	return f != nil && (f.DataType == "Currency" || f.DataType == "Number")
}

func (e *Engine) match(m map[string]string, ref string) string {
	if v, ok := m[ref]; ok {
		return v
	}
	for pat, v := range m {
		if ok, _ := path.Match(pat, ref); ok {
			return v
		}
	}
	return ""
}

func (e *Engine) matchAny(pats []string, ref string) bool {
	for _, p := range pats {
		if ok, _ := path.Match(p, ref); ok || p == ref {
			return true
		}
	}
	return false
}

func (e *Engine) text(o Object, x, y int, txt, align string, border borders) {
	if txt == "" {
		return
	}
	// The border is its own item, so it does not move the text.
	if box, ok := border.borderBox(x, y, o.Bounds.Width, o.Bounds.Height); ok {
		e.page.add(box)
	}
	f := o.Format
	e.page.add(item{kind: itemText, x: x, y: y, w: o.Bounds.Width, h: o.Bounds.Height, text: txt, align: align,
		font: font{name: f.FontName, size: f.FontSize, bold: f.Bold, italic: f.Italic, underline: f.Underline}})
}
