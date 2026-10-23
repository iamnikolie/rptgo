package main

// describe turns a report's model into what a reader (or a language model)
// needs to understand it: where the data comes from, how it is grouped and
// sorted, the columns of the detail lines with their totals, every formula
// with what it uses and where it is used, the running totals, and the layout
// as rows of text and values per section. Markdown by default, the same
// content as JSON.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Description is a report described; its JSON is the stable schema
// "rptgo.describe/v1".
type Description struct {
	Schema     string          `json:"schema"`
	Report     string          `json:"report"`
	Title      string          `json:"title,omitempty"`
	Author     string          `json:"author,omitempty"`
	Comments   string          `json:"comments,omitempty"`
	Page       PageInfo        `json:"page"`
	Data       DataInfo        `json:"data"`
	Parameters []ParamInfo     `json:"parameters,omitempty"`
	Groups     []GroupInfo     `json:"groups,omitempty"`
	Sorts      []SortInfo      `json:"sorts,omitempty"`
	Columns    []ColumnInfo    `json:"columns,omitempty"`
	Formulas   []FormulaInfo   `json:"formulas,omitempty"`
	Totals     []TotalInfo     `json:"runningTotals,omitempty"`
	Sections   []SectionInfo   `json:"sections"`
	Subreports []SubreportInfo `json:"subreports,omitempty"`
	Caveats    []Finding       `json:"caveats,omitempty"`
}

type PageInfo struct {
	Paper       string     `json:"paper"`
	Orientation string     `json:"orientation"`
	Width       float64    `json:"widthInches"`
	Height      float64    `json:"heightInches"`
	Margins     [4]float64 `json:"marginsInches"` // top, right, bottom, left
}

type DataInfo struct {
	Sources         []SourceInfo `json:"sources,omitempty"`
	Joins           []string     `json:"joins,omitempty"`
	RecordSelection string       `json:"recordSelection,omitempty"`
	GroupSelection  string       `json:"groupSelection,omitempty"`
}

type SourceInfo struct {
	Name     string      `json:"name"`
	Kind     string      `json:"kind"`
	Server   string      `json:"server,omitempty"`
	Database string      `json:"database,omitempty"`
	Query    string      `json:"query,omitempty"`
	Tables   []TableInfo `json:"tables,omitempty"`
}

type TableInfo struct {
	Name   string      `json:"name"`
	Alias  string      `json:"alias,omitempty"`
	Fields []FieldInfo `json:"fields"`
}

type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Used bool   `json:"used"`
}

type ParamInfo struct {
	Name     string   `json:"name"`
	Type     string   `json:"type,omitempty"`
	Prompt   string   `json:"prompt,omitempty"`
	Default  string   `json:"default,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	UsedBy   []string `json:"usedBy,omitempty"`
}

type GroupInfo struct {
	Level        int    `json:"level"`
	Field        string `json:"field"`
	Sort         string `json:"sort"`
	By           string `json:"by"` // EachValue, Daily, Weekly, Monthly, ...
	TopN         string `json:"topN,omitempty"`
	RepeatHeader bool   `json:"repeatHeader,omitempty"`
}

type SortInfo struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type ColumnInfo struct {
	Name        string    `json:"name"`
	Heading     bool      `json:"heading"` // Name is the heading printed over it
	Value       string    `json:"value"`
	Type        string    `json:"type,omitempty"`
	Format      string    `json:"format,omitempty"`
	Align       string    `json:"align,omitempty"`
	BlankIfZero bool      `json:"blankIfZero,omitempty"`
	Section     string    `json:"section"`
	Totals      []TotalAt `json:"totals,omitempty"`
}

type TotalAt struct {
	Section string `json:"section"`
	Value   string `json:"value"`
}

type FormulaInfo struct {
	Name   string   `json:"name"`
	Syntax string   `json:"syntax"`
	Type   string   `json:"type,omitempty"`
	Text   string   `json:"text"`
	Uses   []string `json:"uses,omitempty"`
	UsedBy []string `json:"usedBy,omitempty"`
}

type TotalInfo struct {
	Name     string   `json:"name"`
	Function string   `json:"function"`
	Of       string   `json:"of"`
	Reset    string   `json:"reset"`
	Evaluate string   `json:"evaluate"`
	UsedBy   []string `json:"usedBy,omitempty"`
}

type SectionInfo struct {
	Name          string       `json:"name"`
	Kind          string       `json:"kind"`
	Group         string       `json:"group,omitempty"`
	Height        float64      `json:"heightInches"`
	Hidden        bool         `json:"hidden,omitempty"`
	HiddenWhen    string       `json:"hiddenWhen,omitempty"`
	NewPageBefore string       `json:"newPageBefore,omitempty"`
	NewPageAfter  string       `json:"newPageAfter,omitempty"`
	Rows          [][]ItemInfo `json:"rows,omitempty"`
	HiddenObjects int          `json:"hiddenObjects,omitempty"`
}

type ItemInfo struct {
	Text        string      `json:"text,omitempty"`
	Value       string      `json:"value,omitempty"`
	Object      string      `json:"object,omitempty"`
	Format      string      `json:"format,omitempty"`
	Align       string      `json:"align,omitempty"`
	Style       string      `json:"style,omitempty"`
	BlankIfZero bool        `json:"blankIfZero,omitempty"`
	Box         *[4]float64 `json:"boxInches,omitempty"` // left, top, width, height
}

type SubreportInfo struct {
	Name    string       `json:"name"`
	Section string       `json:"section"`
	Report  *Description `json:"report,omitempty"`
}

// DescribeOptions: Layout adds each object's box to the sections.
type DescribeOptions struct {
	Layout bool
}

// Describe builds the description of r; name is the report's file name.
func Describe(r *Report, ov *Overlay, name string, opt DescribeOptions) *Description {
	d := &Description{Schema: "rptgo.describe/v1", Report: name, Title: r.title(),
		Author: strings.TrimSpace(r.Author), Comments: strings.TrimSpace(r.ReportComments)}
	used := r.usage(ov)
	d.Page = r.pageInfo()
	d.Data = r.dataInfo(used)
	for _, f := range r.Fields {
		if f.Type != "ParameterField" {
			continue
		}
		p := ParamInfo{Name: f.Name, Type: f.DataType, Prompt: strings.TrimSpace(f.PromptText),
			Multiple: f.AllowMultipleValues, UsedBy: used["{?"+f.Name+"}"]}
		if f.DefaultValue != nil {
			p.Default = *f.DefaultValue
		}
		for _, c := range f.PickListValues {
			if c.Item2 != "" && c.Item2 != c.Item1 {
				p.Choices = append(p.Choices, c.Item1+" ("+c.Item2+")")
			} else {
				p.Choices = append(p.Choices, c.Item1)
			}
		}
		d.Parameters = append(d.Parameters, p)
	}
	for i, g := range r.Groups {
		gi := GroupInfo{Level: i + 1, Field: display(r, r.groupRef(g)), Sort: g.SortOrder, By: g.Condition, RepeatHeader: g.RepeatGroupHeader}
		if t := g.TopN; t != nil {
			side := "top"
			if t.Bottom {
				side = "bottom"
			}
			gi.TopN = fmt.Sprintf("%s %d by %s of %s", side, t.Count, t.Function, display(r, r.refOf(t.FieldName)))
			if t.DiscardOthers {
				gi.TopN += ", others dropped"
			}
		}
		d.Groups = append(d.Groups, gi)
	}
	for _, s := range r.SortFields {
		d.Sorts = append(d.Sorts, SortInfo{Field: display(r, r.sortRef(s)), Direction: s.Direction})
	}
	cols := r.columns(ov)
	for _, c := range cols {
		ci := ColumnInfo{Name: c.Label, Heading: c.Labeled, Value: display(r, c.Ref), Type: r.typeOf(c.Ref),
			Format: formatOf(c.Obj), Align: alignOf(c.Obj), BlankIfZero: c.Obj.Format.SuppressIfZero, Section: r.sectionName(c.Section)}
		for _, t := range c.Totals {
			ci.Totals = append(ci.Totals, TotalAt{Section: r.sectionName(t.Section), Value: display(r, t.Ref)})
		}
		d.Columns = append(d.Columns, ci)
	}
	for _, f := range r.Fields {
		if f.Type != "FormulaField" || strings.TrimSpace(f.FormulaText) == "" {
			continue
		}
		syntax := f.Syntax
		if syntax == "" {
			syntax = "Crystal"
			if isBasic(f.FormulaText) {
				syntax = "Basic"
			}
		}
		d.Formulas = append(d.Formulas, FormulaInfo{Name: "{@" + f.Name + "}", Syntax: syntax, Type: f.ResultType,
			Text: strings.TrimSpace(strings.ReplaceAll(f.FormulaText, "\r\n", "\n")), Uses: r.refsIn(f.FormulaText),
			UsedBy: used["{@"+f.Name+"}"]})
	}
	for _, f := range r.runningTotals() {
		d.Totals = append(d.Totals, TotalInfo{Name: "{#" + f.Name + "}", Function: f.Function,
			Of: display(r, r.refOf(f.SummarizedFieldName)), Reset: r.condition(f.ResetCondition, "never"),
			Evaluate: r.condition(f.EvaluateCondition, "every record"), UsedBy: used["{#"+f.Name+"}"]})
	}
	style := r.defaultFont()
	for _, i := range r.designOrder() {
		s := r.Sections[i]
		si := SectionInfo{Name: r.sectionName(i), Kind: s.Type, Height: inches(s.HeightTwips), Hidden: s.Suppress,
			HiddenWhen: formulaText(s.SuppressFormula), NewPageBefore: pageBreak(s.NewPageBefore, s.NewPageBeforeFormula),
			NewPageAfter: pageBreak(s.NewPageAfter, s.NewPageAfterFormula)}
		if (s.Type == "GroupHeader" || s.Type == "GroupFooter") && s.GroupLevel < len(r.Groups) {
			si.Group = display(r, r.groupRef(r.Groups[s.GroupLevel]))
		}
		for _, o := range s.Objects {
			if o.Format.Suppress && !isShape(o) {
				si.HiddenObjects++
			}
		}
		if !s.Suppress || opt.Layout {
			for _, row := range visualRows(s.Objects) {
				var items []ItemInfo
				for _, o := range row {
					items = append(items, r.item(o, ov, style, opt))
				}
				si.Rows = append(si.Rows, items)
			}
		}
		d.Sections = append(d.Sections, si)
		for _, o := range s.Objects {
			if strings.Contains(o.Type, "Subreport") {
				sub := SubreportInfo{Name: o.SubreportName, Section: si.Name}
				if o.Report != nil {
					sub.Report = Describe(o.Report, &Overlay{}, o.SubreportName, opt)
				}
				d.Subreports = append(d.Subreports, sub)
			}
		}
	}
	for _, f := range Audit(r, ov) {
		if f.Severity != "info" {
			d.Caveats = append(d.Caveats, f)
		}
	}
	return d
}

// item describes one object of a row.
func (r *Report) item(o Object, ov *Overlay, def font, opt DescribeOptions) ItemInfo {
	var it ItemInfo
	switch {
	case isText(o):
		it.Text = strings.TrimSpace(strings.ReplaceAll(o.Text, "\r\n", "\n"))
		if t, ok := ov.Text[o.Text]; ok {
			it.Text = t
		}
	case isField(o):
		ref := r.binding(o, ov)
		it.Value = display(r, ref)
		it.Format = formatOf(o)
		it.BlankIfZero = o.Format.SuppressIfZero
	case strings.Contains(o.Type, "Subreport"):
		it.Object = fmt.Sprintf("subreport %q", o.SubreportName)
	case strings.Contains(o.Type, "Chart"):
		var cats []string
		for _, c := range o.CategoryFields {
			cats = append(cats, display(r, r.refOf(c)))
		}
		it.Object = fmt.Sprintf("%s chart %q: %s of %s by %s", o.Kind, o.Title, o.SeriesFunction,
			display(r, r.refOf(o.SeriesField)), strings.Join(cats, ", "))
	case strings.Contains(o.Type, "CrossTab"):
		var rows, cols, cells []string
		for _, f := range o.RowGroupFields {
			rows = append(rows, display(r, r.refOf(f)))
		}
		for _, f := range o.ColumnGroupFields {
			cols = append(cols, display(r, r.refOf(f)))
		}
		for _, c := range o.Cells {
			cells = append(cells, c.Function+" of "+display(r, r.refOf(c.FieldName)))
		}
		it.Object = fmt.Sprintf("cross-tab: rows %s; columns %s; cells %s",
			strings.Join(rows, ", "), strings.Join(cols, ", "), strings.Join(cells, ", "))
	case strings.Contains(o.Type, "Image"):
		it.Object = "picture"
		if o.FieldName != "" {
			it.Object += " from " + display(r, r.refOf(o.FieldName))
		}
	default:
		it.Object = strings.TrimSuffix(o.Type, "Object")
	}
	if a := alignOf(o); a != "" && (it.Text != "" || it.Value != "") {
		it.Align = a
	}
	if it.Text != "" || it.Value != "" {
		it.Style = styleOf(o, def)
	}
	if opt.Layout {
		it.Box = &[4]float64{inches(o.Bounds.Left), inches(o.Bounds.Top), inches(o.Bounds.Width), inches(o.Bounds.Height)}
	}
	return it
}

// ---- references ----

var fieldRefRe = regexp.MustCompile(`\{([^{}]+)\}`)

// refsIn is the fields a formula refers to, as display writes them, in order
// of first use.
func (r *Report) refsIn(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range fieldRefRe.FindAllStringSubmatch(text, -1) {
		d := display(r, strings.TrimSpace(m[1]))
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// display writes a reference the way Crystal's formulas do: {Table.Field},
// {@Formula}, {#RunningTotal}, {?Parameter}; special fields in brackets;
// a summary field as "Sum of {Table.Field}".
func display(r *Report, ref string) string {
	switch ref {
	case "":
		return ""
	case "PageNofM":
		return "[Page N of M]"
	case "PageNumber":
		return "[Page Number]"
	case "PrintDate":
		return "[Print Date]"
	case "PrintTime":
		return "[Print Time]"
	case "RecordNumber":
		return "[Record Number]"
	case "DataDate":
		return "[Data Date]"
	case "DataTime":
		return "[Data Time]"
	}
	if name, ok := strings.CutPrefix(ref, "#summary:"); ok {
		if o := r.object(name); o != nil && o.SummaryFunction != nil {
			return *o.SummaryFunction + " of " + display(r, r.refOf(o.FieldName))
		}
	}
	for _, p := range []string{"@", "#", "?"} {
		if name, ok := strings.CutPrefix(ref, p); ok {
			if f := r.fieldKind(p, name); f != nil {
				name = f.Name
			}
			return "{" + p + name + "}"
		}
	}
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		table, col := ref[:i], ref[i+1:]
		for _, f := range r.Fields {
			if f.Type == "DatabaseField" && strings.EqualFold(f.Name, col) && strings.EqualFold(f.TableName, table) {
				return "{" + f.TableName + "." + f.Name + "}"
			}
		}
		return "{" + ref + "}"
	}
	if f := r.fieldKind("", ref); f != nil && f.TableName != "" {
		return "{" + f.TableName + "." + f.Name + "}"
	}
	if strings.ContainsAny(ref, " ") {
		return "[" + ref + "]"
	}
	return "{" + ref + "}"
}

func (r *Report) object(name string) *Object {
	for i := range r.Sections {
		for j := range r.Sections[i].Objects {
			if r.Sections[i].Objects[j].Name == name {
				return &r.Sections[i].Objects[j]
			}
		}
	}
	return nil
}

func (r *Report) groupRef(g Group) string {
	if g.TableName != "" && !strings.ContainsAny(g.FieldName, "@#?") {
		return g.TableName + "." + g.FieldName
	}
	return r.refOf(g.FieldName)
}

func (r *Report) sortRef(s SortField) string {
	if s.TableName != "" && !strings.ContainsAny(s.FieldName, "@#?") {
		return s.TableName + "." + s.FieldName
	}
	return r.refOf(s.FieldName)
}

// typeOf is the data type a reference prints.
func (r *Report) typeOf(ref string) string {
	if name, ok := strings.CutPrefix(ref, "#summary:"); ok {
		if o := r.object(name); o != nil && o.SummaryFunction != nil {
			switch *o.SummaryFunction {
			case "Count", "DistinctCount":
				return "Number"
			}
			return r.typeOf(r.refOf(o.FieldName))
		}
		return ""
	}
	prefix, name := "", ref
	if ref != "" && strings.ContainsRune("@#?", rune(ref[0])) {
		prefix, name = ref[:1], ref[1:]
	} else if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	f := r.fieldKind(prefix, name)
	if f == nil {
		return ""
	}
	if f.ResultType != "" {
		return f.ResultType
	}
	return f.DataType
}

// condition reads a running total's reset or evaluate condition.
func (r *Report) condition(c *string, none string) string {
	if c == nil {
		return none
	}
	if lvl, ok := conditionGroup(*c); ok {
		g := fmt.Sprintf("group #%d", lvl+1)
		if lvl < len(r.Groups) {
			g += " (" + display(r, r.groupRef(r.Groups[lvl])) + ")"
		}
		return "on change of " + g
	}
	if src, ok := strings.CutPrefix(*c, "Formula:"); ok {
		return "when " + strings.TrimSpace(src)
	}
	return *c
}

// usage is, per reference as display writes it, where the report uses it.
func (r *Report) usage(ov *Overlay) map[string][]string {
	used := map[string][]string{}
	add := func(ref, where string) {
		for _, w := range used[ref] {
			if w == where {
				return
			}
		}
		used[ref] = append(used[ref], where)
	}
	addText := func(text, where string) {
		for _, ref := range r.refsIn(text) {
			add(ref, where)
		}
	}
	for _, f := range r.Fields {
		switch f.Type {
		case "FormulaField":
			addText(f.FormulaText, "{@"+f.Name+"}")
		case "RunningTotalField":
			if f.SummarizedFieldName != "" {
				add(display(r, r.refOf(f.SummarizedFieldName)), "{#"+f.Name+"}")
			}
			for _, c := range []*string{f.ResetCondition, f.EvaluateCondition} {
				if c != nil {
					addText(*c, "{#"+f.Name+"}")
				}
			}
		}
	}
	for i, g := range r.Groups {
		add(display(r, r.groupRef(g)), fmt.Sprintf("group #%d", i+1))
	}
	for _, s := range r.SortFields {
		add(display(r, r.sortRef(s)), "sort")
	}
	if r.RecordSelectionFormula != nil {
		addText(*r.RecordSelectionFormula, "record selection")
	}
	if r.GroupSelectionFormula != nil {
		addText(*r.GroupSelectionFormula, "group selection")
	}
	for i, s := range r.Sections {
		name := r.sectionName(i)
		for _, c := range []*string{s.SuppressFormula, s.NewPageBeforeFormula, s.NewPageAfterFormula} {
			if c != nil {
				addText(*c, name+" (condition)")
			}
		}
		for _, o := range s.Objects {
			if o.Format.Suppress {
				continue
			}
			var fields []string // what a chart or cross-tab reads
			fields = append(fields, o.CategoryFields...)
			fields = append(fields, o.RowGroupFields...)
			fields = append(fields, o.ColumnGroupFields...)
			if o.SeriesField != "" {
				fields = append(fields, o.SeriesField)
			}
			for _, c := range o.Cells {
				fields = append(fields, c.FieldName)
			}
			for _, f := range fields {
				add(display(r, r.refOf(f)), name)
			}
			if !isField(o) {
				continue
			}
			if o.SummaryFunction != nil {
				add(display(r, r.refOf(o.FieldName)), name)
				continue
			}
			add(display(r, r.binding(o, ov)), name)
		}
	}
	return used
}

// ---- report-level facts ----

// title is the report's title: its own, else the largest text in the report
// or page header (bold first, then the topmost).
func (r *Report) title() string {
	if t := strings.TrimSpace(r.ReportTitle); t != "" {
		return t
	}
	def := r.defaultFont()
	var best *Object
	for _, i := range r.designOrder() {
		s := r.Sections[i]
		if (s.Type != "ReportHeader" && s.Type != "PageHeader") || s.Suppress {
			continue
		}
		for j := range s.Objects {
			o := &s.Objects[j]
			if !isText(*o) || o.Format.Suppress || strings.TrimSpace(o.Text) == "" {
				continue
			}
			if best == nil || o.Format.FontSize > best.Format.FontSize ||
				(o.Format.FontSize == best.Format.FontSize && o.Format.Bold && !best.Format.Bold) {
				best = o
			}
		}
	}
	if best == nil || (best.Format.FontSize <= def.size && !best.Format.Bold) {
		return ""
	}
	return cleanLabel(best.Text)
}

// defaultFont is the report's most common font and size.
func (r *Report) defaultFont() font {
	count := map[font]int{}
	for _, s := range r.Sections {
		for _, o := range s.Objects {
			if isText(o) || isField(o) {
				count[font{name: o.Format.FontName, size: o.Format.FontSize}]++
			}
		}
	}
	var best font
	n := -1
	for f, c := range count {
		if c > n || (c == n && (f.name < best.name || (f.name == best.name && f.size < best.size))) {
			best, n = f, c
		}
	}
	return best
}

func styleOf(o Object, def font) string {
	var parts []string
	f := o.Format
	if f.FontName != "" && f.FontName != def.name {
		parts = append(parts, f.FontName)
	}
	if f.FontSize > 0 && f.FontSize != def.size {
		parts = append(parts, fmt.Sprintf("%g pt", f.FontSize))
	}
	for _, p := range []struct {
		on   bool
		name string
	}{{f.Bold, "bold"}, {f.Italic, "italic"}, {f.Underline, "underline"}} {
		if p.on {
			parts = append(parts, p.name)
		}
	}
	return strings.Join(parts, " ")
}

func formatOf(o Object) string {
	if o.Format.FormatString != nil {
		return *o.Format.FormatString
	}
	return ""
}

func alignOf(o Object) string {
	switch o.Format.HAlign {
	case "Center", "Right", "Justify":
		return strings.ToLower(o.Format.HAlign)
	}
	return ""
}

func formulaText(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(*s, "\r\n", "\n"))
}

func pageBreak(always bool, f *string) string {
	if t := formulaText(f); t != "" {
		return "when " + t
	}
	if always {
		return "always"
	}
	return ""
}

func inches(tw int) float64 { return float64(int(float64(tw)/1440*100+0.5)) / 100 }

func (r *Report) pageInfo() PageInfo {
	p := r.Page
	w, h := min(p.WidthTwips, p.HeightTwips), max(p.WidthTwips, p.HeightTwips)
	paper := fmt.Sprintf("%g x %g in", inches(w), inches(h))
	near := func(a, b int) bool { return a-b < 40 && b-a < 40 }
	for _, s := range []struct {
		name string
		w, h int
	}{{"Letter", 12240, 15840}, {"Legal", 12240, 20160}, {"A4", 11906, 16838}, {"Tabloid", 15840, 24480}, {"A3", 16838, 23811}} {
		if near(w, s.w) && near(h, s.h) {
			paper = s.name
		}
	}
	orient := p.Orientation
	if orient == "" {
		orient = "Portrait"
		if p.WidthTwips > p.HeightTwips {
			orient = "Landscape"
		}
	}
	return PageInfo{Paper: paper, Orientation: orient, Width: inches(p.WidthTwips), Height: inches(p.HeightTwips),
		Margins: [4]float64{inches(p.TopMarginTwips), inches(p.RightMarginTwips), inches(p.BottomMarginTwips), inches(p.LeftMarginTwips)}}
}

func (r *Report) dataInfo(used map[string][]string) DataInfo {
	var d DataInfo
	seenTable := map[string]bool{}
	tableInfo := func(name, alias string, cols []struct{ Name, DataType string }) TableInfo {
		t := TableInfo{Name: name}
		if alias != name {
			t.Alias = alias
		}
		seen := map[string]bool{}
		addField := func(col, typ string) {
			key := strings.ToLower(col)
			if seen[key] {
				return
			}
			seen[key] = true
			ref := "{" + alias + "." + col + "}"
			if f := r.fieldKind("", col); f != nil && strings.EqualFold(f.TableName, alias) {
				typ = f.DataType
				ref = display(r, col)
			}
			t.Fields = append(t.Fields, FieldInfo{Name: col, Type: typ, Used: len(used[ref]) > 0})
		}
		for _, f := range r.Fields {
			if f.Type == "DatabaseField" && (strings.EqualFold(f.TableName, alias) || strings.EqualFold(f.TableName, name)) {
				addField(f.Name, f.DataType)
			}
		}
		for _, c := range cols {
			addField(c.Name, c.DataType)
		}
		return t
	}
	for _, s := range r.DataSources {
		si := SourceInfo{Name: s.Name, Kind: s.Kind, Server: deref(s.ServerName), Database: deref(s.DatabaseName), Query: deref(s.SqlQuery)}
		if si.Server == "" {
			si.Server = deref(s.OdbcDsn)
		}
		for _, t := range s.Tables {
			seenTable[strings.ToLower(t.Alias)], seenTable[strings.ToLower(t.Name)] = true, true
			si.Tables = append(si.Tables, tableInfo(t.Name, t.Alias, t.Columns))
		}
		d.Sources = append(d.Sources, si)
	}
	// Database fields of tables no data source lists.
	var orphan []string
	for _, f := range r.Fields {
		if f.Type == "DatabaseField" && f.TableName != "" && !seenTable[strings.ToLower(f.TableName)] {
			seenTable[strings.ToLower(f.TableName)] = true
			orphan = append(orphan, f.TableName)
		}
	}
	if len(orphan) > 0 {
		si := SourceInfo{Name: "(fields)", Kind: "Unknown"}
		for _, t := range orphan {
			si.Tables = append(si.Tables, tableInfo(t, t, nil))
		}
		d.Sources = append(d.Sources, si)
	}
	for _, l := range r.TableLinks {
		op := l.Operator
		if op == "" || op == "Equal" {
			op = "="
		}
		d.Joins = append(d.Joins, fmt.Sprintf("{%s.%s} %s {%s.%s} (%s)", l.SourceTable, l.SourceColumn, op, l.TargetTable, l.TargetColumn, l.JoinType))
	}
	d.RecordSelection = formulaText(r.RecordSelectionFormula)
	d.GroupSelection = formulaText(r.GroupSelectionFormula)
	return d
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// ---- Markdown ----

// Markdown writes the description for a reader; level is the heading level
// of its title (1, deeper for a subreport).
func (d *Description) Markdown(level int) string {
	var b strings.Builder
	h := func(n int) string { return strings.Repeat("#", min(level+n, 6)) + " " }
	title := d.Title
	if title == "" {
		title = strings.TrimSuffix(d.Report, filepath.Ext(d.Report))
	}
	fmt.Fprintf(&b, "%s%s\n\n", h(0), mdText(title))
	var meta []string
	meta = append(meta, "Report `"+d.Report+"`")
	meta = append(meta, fmt.Sprintf("%s %s, margins %s in", d.Page.Paper, strings.ToLower(d.Page.Orientation), margins(d.Page.Margins)))
	if d.Author != "" {
		meta = append(meta, "author "+mdText(d.Author))
	}
	b.WriteString(strings.Join(meta, " · ") + "\n\n")
	if d.Comments != "" {
		b.WriteString("> " + mdText(d.Comments) + "\n\n")
	}

	// What the report is made of, in one paragraph.
	b.WriteString(h(1) + "Data\n\n")
	for _, s := range d.Data.Sources {
		switch s.Kind {
		case "Dataset":
			fmt.Fprintf(&b, "- Source `%s`: an ADO.NET dataset (`%s`) the application fills; rptgo takes these rows from a stored procedure (`-proc`) or a file (`-data`).\n", s.Name, s.Database)
		case "Unknown":
			b.WriteString("- Tables the fields come from:\n")
		default:
			where := []string{}
			if s.Database != "" {
				where = append(where, "database `"+s.Database+"`")
			}
			if s.Server != "" {
				where = append(where, "server `"+s.Server+"`")
			}
			fmt.Fprintf(&b, "- Source `%s` (%s) %s.\n", s.Name, s.Kind, strings.Join(where, ", "))
			if s.Query != "" {
				fmt.Fprintf(&b, "  Query:\n\n  ```sql\n  %s\n  ```\n", strings.ReplaceAll(strings.TrimSpace(s.Query), "\n", "\n  "))
			}
		}
		for _, t := range s.Tables {
			var usedF []string
			unused := 0
			for _, f := range t.Fields {
				if f.Used {
					usedF = append(usedF, f.Name+" "+f.Type)
				} else {
					unused++
				}
			}
			name := "`" + t.Name + "`"
			if t.Alias != "" {
				name += " as `" + t.Alias + "`"
			}
			line := fmt.Sprintf("  - Table %s, fields used: %s", name, orNone(strings.Join(usedF, ", ")))
			if unused > 0 {
				line += fmt.Sprintf("; %d more not used", unused)
			}
			b.WriteString(line + "\n")
		}
	}
	for _, j := range d.Data.Joins {
		b.WriteString("- Join " + j + "\n")
	}
	if d.Data.RecordSelection != "" {
		b.WriteString("- Record selection:\n\n" + codeBlock(d.Data.RecordSelection, "  ") + "\n")
	}
	if d.Data.GroupSelection != "" {
		b.WriteString("- Group selection:\n\n" + codeBlock(d.Data.GroupSelection, "  ") + "\n")
	}
	for _, g := range d.Groups {
		line := fmt.Sprintf("- Group #%d: %s, %s", g.Level, g.Field, strings.ToLower(g.Sort))
		if g.By != "" && g.By != "EachValue" {
			line += ", " + strings.ToLower(g.By)
		}
		if g.TopN != "" {
			line += ", " + g.TopN
		}
		b.WriteString(line + "\n")
	}
	if len(d.Sorts) > 0 {
		var s []string
		for _, x := range d.Sorts {
			s = append(s, x.Field+" "+strings.ToLower(x.Direction))
		}
		b.WriteString("- Sorted within groups by " + strings.Join(s, ", ") + "\n")
	}
	b.WriteString("\n")

	if len(d.Parameters) > 0 {
		b.WriteString(h(1) + "Parameters\n\n| Parameter | Type | Prompt | Default | Used by |\n| --- | --- | --- | --- | --- |\n")
		for _, p := range d.Parameters {
			typ := p.Type
			if p.Multiple {
				typ += ", several values"
			}
			def := p.Default
			if len(p.Choices) > 0 {
				def += " (choices: " + strings.Join(p.Choices, ", ") + ")"
			}
			fmt.Fprintf(&b, "| {?%s} | %s | %s | %s | %s |\n", mdCell(p.Name), mdCell(typ), mdCell(p.Prompt), mdCell(def), mdCell(orNone(strings.Join(p.UsedBy, ", "))))
		}
		b.WriteString("\n")
	}

	if len(d.Columns) > 0 {
		var totalSecs []string
		seen := map[string]bool{}
		for _, sec := range d.Sections {
			for _, c := range d.Columns {
				for _, t := range c.Totals {
					if t.Section == sec.Name && !seen[sec.Name] {
						seen[sec.Name] = true
						totalSecs = append(totalSecs, sec.Name)
					}
				}
			}
		}
		line := "Each detail line prints"
		if s := d.Columns[0].Section; !strings.HasPrefix(s, "Details") {
			line = "The details are hidden; each " + s + " prints one line"
		}
		b.WriteString(h(1) + "Columns\n\n" + line + ", left to right:\n\n")
		head := []string{"Column", "Value", "Type", "Format"}
		head = append(head, totalSecs...)
		b.WriteString("| " + strings.Join(head, " | ") + " |\n|" + strings.Repeat(" --- |", len(head)) + "\n")
		for _, c := range d.Columns {
			name := c.Name
			if !c.Heading {
				name = "(" + name + ")"
			}
			format := c.Format
			if format != "" {
				format = "`" + format + "`"
			}
			if c.BlankIfZero {
				format = strings.TrimSpace(format + " blank if 0")
			}
			cells := []string{mdCell(name), mdCell(c.Value), mdCell(c.Type), mdCell(format)}
			for _, sec := range totalSecs {
				var v []string
				for _, t := range c.Totals {
					if t.Section == sec {
						v = append(v, t.Value)
					}
				}
				cells = append(cells, mdCell(strings.Join(v, ", ")))
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
		b.WriteString("\n")
	}

	if len(d.Formulas) > 0 {
		b.WriteString(h(1) + "Formulas\n\n")
		for _, f := range d.Formulas {
			typ := f.Type
			if typ == "" {
				typ = "untyped"
			}
			fmt.Fprintf(&b, "%s%s\n\n%s, %s syntax. Uses %s. Used by %s.\n\n%s\n", h(2), f.Name, typ, f.Syntax,
				orNone(strings.Join(f.Uses, ", ")), orUnused(strings.Join(f.UsedBy, ", ")), codeBlock(f.Text, ""))
		}
	}

	if len(d.Totals) > 0 {
		b.WriteString(h(1) + "Running totals\n\n| Total | Computes | Resets | Evaluates | Used by |\n| --- | --- | --- | --- | --- |\n")
		for _, t := range d.Totals {
			fmt.Fprintf(&b, "| %s | %s of %s | %s | %s | %s |\n", t.Name, t.Function, mdCell(t.Of), mdCell(t.Reset), mdCell(t.Evaluate), mdCell(orUnused(strings.Join(t.UsedBy, ", "))))
		}
		b.WriteString("\n")
	}

	b.WriteString(h(1) + "Layout\n\nSections in design order; each line is one printed row, left to right.\n\n")
	for _, s := range d.Sections {
		if len(s.Rows) == 0 && !s.Hidden && s.HiddenWhen == "" {
			continue
		}
		name := s.Name
		if s.Group != "" {
			name += ": " + s.Group
		}
		var notes []string
		if s.Hidden {
			notes = append(notes, "hidden")
		}
		if s.HiddenWhen != "" {
			notes = append(notes, "hidden when `"+oneLine(s.HiddenWhen)+"`")
		}
		if s.NewPageBefore != "" {
			notes = append(notes, "new page before "+codeWhen(s.NewPageBefore))
		}
		if s.NewPageAfter != "" {
			notes = append(notes, "new page after "+codeWhen(s.NewPageAfter))
		}
		if s.HiddenObjects > 0 {
			notes = append(notes, plural(s.HiddenObjects, "hidden object")+" not shown")
		}
		fmt.Fprintf(&b, "%s%s\n\n", h(2), name)
		if len(notes) > 0 {
			b.WriteString("_" + strings.Join(notes, "; ") + "_\n\n")
		}
		for _, row := range s.Rows {
			parts := make([]string, len(row))
			for i, it := range row {
				parts[i] = it.markdown()
			}
			b.WriteString("- " + strings.Join(parts, " · ") + "\n")
		}
		if len(s.Rows) > 0 {
			b.WriteString("\n")
		}
	}

	for _, sub := range d.Subreports {
		fmt.Fprintf(&b, "%sSubreport %q (in %s)\n\n", h(1), sub.Name, sub.Section)
		if sub.Report != nil {
			b.WriteString(sub.Report.Markdown(min(level+2, 6)))
		}
	}

	if len(d.Caveats) > 0 {
		b.WriteString(h(1) + "Caveats\n\nWhat rptgo does not reproduce of this report:\n\n")
		for _, f := range d.Caveats {
			b.WriteString("- " + f.line() + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (it ItemInfo) markdown() string {
	var s string
	switch {
	case it.Text != "":
		s = `"` + oneLine(it.Text) + `"`
	case it.Value != "":
		s = it.Value
	default:
		s = "[" + it.Object + "]"
	}
	var attrs []string
	if it.Format != "" {
		attrs = append(attrs, "`"+it.Format+"`")
	}
	if it.BlankIfZero {
		attrs = append(attrs, "blank if 0")
	}
	if it.Align != "" {
		attrs = append(attrs, it.Align)
	}
	if it.Style != "" {
		attrs = append(attrs, it.Style)
	}
	if it.Box != nil {
		attrs = append(attrs, fmt.Sprintf("at %g,%g in, %gx%g in", it.Box[0], it.Box[1], it.Box[2], it.Box[3]))
	}
	if len(attrs) > 0 {
		s += " (" + strings.Join(attrs, ", ") + ")"
	}
	return s
}

func margins(m [4]float64) string {
	if m[0] == m[1] && m[1] == m[2] && m[2] == m[3] {
		return fmt.Sprintf("%g", m[0])
	}
	return fmt.Sprintf("%g/%g/%g/%g", m[0], m[1], m[2], m[3])
}

func codeWhen(s string) string {
	if t, ok := strings.CutPrefix(s, "when "); ok {
		return "when `" + oneLine(t) + "`"
	}
	return s
}

func codeBlock(text, indent string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return indent + fence + "\n" + strings.Join(lines, "\n") + "\n" + indent + fence + "\n"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// mdText is inline text with the Markdown that would break a line escaped.
func mdText(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

// mdCell is a table cell: one line, pipes escaped.
func mdCell(s string) string { return strings.ReplaceAll(oneLine(s), "|", `\|`) }

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

func orUnused(s string) string {
	if s == "" {
		return "nothing (unused)"
	}
	return s
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
