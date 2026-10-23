package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamnikolie/rptgo/rpt"
)

// Report is the report model in the JSON shape rptgo/rpt (and the C#
// majorsilence.crystal it ports) writes. A .rpt is parsed with rpt.Parse and
// read through that JSON, so a saved model works as input too.
type Report struct {
	// FromRpt: read from a .rpt, so running totals carry their decoded reset
	// and evaluate conditions (a saved JSON model from the C# reader does not).
	FromRpt     bool `json:"-"`
	ReportTitle string
	Page        Page
	Fields      []Field
	Groups      []Group
	SortFields  []SortField
	Sections    []Section

	// Read by describe, audit and index; the engine does not use them.
	Author                 string
	ReportComments         string
	DataSources            []DataSource
	TableLinks             []TableLink
	RecordSelectionFormula *string
	GroupSelectionFormula  *string
}

type DataSource struct {
	Name         string
	Kind         string
	ServerName   *string
	DatabaseName *string
	OdbcDsn      *string
	SqlQuery     *string
	Tables       []struct {
		Name    string
		Alias   string
		Columns []struct{ Name, DataType string }
	}
}

type TableLink struct {
	SourceTable, SourceColumn string
	TargetTable, TargetColumn string
	JoinType, Operator        string
}

type SortField struct {
	FieldName string
	TableName string
	Direction string
}

type Page struct {
	Orientation                       string
	WidthTwips, HeightTwips           int
	TopMarginTwips, BottomMarginTwips int
	LeftMarginTwips, RightMarginTwips int
}

type Field struct {
	Kind                string
	Name                string
	TableName           string
	ColumnName          string
	DataType            string
	FormulaText         string
	ResultType          string
	Function            string
	SummarizedFieldName string
	// Running totals: "Group:<level>" or "Formula:<text>"; nil is never
	// (reset) or every record (evaluate).
	ResetCondition    *string
	EvaluateCondition *string
	Syntax            string
	Type              string `json:"$type"`
	// Parameters.
	PromptText          string
	AllowMultipleValues bool
	DefaultValue        *string
	PickListValues      []struct{ Item1, Item2 string }
}

type Group struct {
	Level             int
	FieldName         string
	TableName         string
	SortOrder         string
	Condition         string
	RepeatGroupHeader bool
	TopN              *struct {
		Count         int
		Bottom        bool
		Function      string
		FieldName     string
		DiscardOthers bool
		OthersName    string
	}
}

type Section struct {
	Type        string
	GroupLevel  int
	HeightTwips int
	Suppress    bool
	Objects     []Object
	// Formulas that decide, per instance, whether the section prints and
	// whether a new page comes before or after it.
	SuppressFormula      *string
	NewPageBefore        bool
	NewPageAfter         bool
	NewPageBeforeFormula *string
	NewPageAfterFormula  *string
}

// SectionRef is where a line or box ends: a section's kind and group level.
type SectionRef struct {
	Type       string
	GroupLevel int
}

type Object struct {
	Type      string `json:"$type"`
	Name      string
	FieldName string
	Text      string
	Bounds    struct{ Left, Top, Width, Height int }
	Format    Format
	// Lines and boxes: the section they run down into, and how far into it.
	EndSection     *SectionRef
	EndBottomTwips int

	SummaryFunction *string // a summary field: FieldName summarized over its section's group

	// Subreports.
	SubreportName string
	Report        *Report // the subreport's own model

	// Charts.
	Title              string
	Kind               string // Column, Line, Area, Pie, Doughnut
	Stacking           string // None, Stacked, PercentStacked
	CategoryFields     []string
	CategoryConditions []string // per category field: EachValue or a date period
	SeriesField        string
	SeriesFunction     string
	ShowLegend         bool
	Palette            []string // series colours
	CategoryAxisTitle  string
	ValueAxisTitle     string

	// Cross-tabs.
	RowGroupFields    []string
	ColumnGroupFields []string
	Cells             []struct {
		FieldName, Function string
	}
	ShowGrid                  bool
	SuppressRowGrandTotals    bool
	SuppressColumnGrandTotals bool

	// Pictures.
	Source    string // Embedded or a field
	MimeType  *string
	ImageData []byte // the embedded file
}

type Format struct {
	FontName  string
	FontSize  float64
	Bold      bool
	Italic    bool
	Underline bool
	// Default (Crystal's "not set": numbers right, text and dates left), Left,
	// Center, Right, Justify.
	HAlign string
	// .NET custom format string for numbers and dates, nil when not set.
	FormatString *string
	// Edge styles: 0 none, 1 single, 2 double, 3 dashed, 4 dotted.
	BorderLeft, BorderRight, BorderTop, BorderBottom int
	BorderWidthTwips                                 int
	CanGrow                                          bool
	// Suppress: the object never prints.
	Suppress bool
	// SuppressIfZero: a number prints blank at zero.
	SuppressIfZero bool
	// NegativeFormat: 0 no sign, 1 leading minus, 3 in brackets.
	NegativeFormat  int
	SuppressIfBlank bool
	// Colours "#RRGGBB", nil for the default (black text, no fill).
	ForeColor  *string
	BackColor  *string
	Conditions []struct {
		Operator  string
		Threshold float64
		FontColor *string
		BackColor *string
	}
}

// Overlay carries what the .rpt model does not hold, one JSON file per report.
// The parser now decodes zero suppression, object suppression and running
// total conditions too, so reports rarely need one; the keys remain to
// override the parsed model when a report does.
type Overlay struct {
	// Format by field reference ("InvoiceDate", "@Balance", "#Total");
	// overrides the model's FormatString.
	Formats map[string]string `json:"formats"`
	// Field references whose zero value prints blank.
	SuppressZero []string `json:"suppressZero"`
	// Border by field reference: "top", "top-double" (overrides the model's).
	Borders map[string]string `json:"borders"`
	// Running total reset by name: "never" or "group:<level>".
	Resets map[string]string `json:"resets"`
	// Object bindings the parser lost: object name -> field reference.
	Bind map[string]string `json:"bind"`
	// HAlign override by field reference.
	Align map[string]string `json:"align"`
	// Text object content the parser flattened (lost line breaks).
	Text map[string]string `json:"text"`
}

// loadReport reads a .rpt (through rptgo/rpt) or a saved JSON model.
func loadReport(path string) (*Report, error) {
	var b []byte
	var err error
	defaultMargins := false
	if strings.EqualFold(filepath.Ext(path), ".rpt") {
		res := rpt.Parse(path)
		if !res.Success || res.Report == nil {
			return nil, fmt.Errorf("%s: %s", path, strings.Join(res.Errors, "; "))
		}
		defaultMargins = res.Report.Page.DefaultMargins
		b, err = rpt.MarshalIndent(res.Report)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.FromRpt = strings.EqualFold(filepath.Ext(path), ".rpt")
	if defaultMargins {
		// "Default margins" are the printer's: measured exports print a
		// quarter inch on every side.
		r.Page.TopMarginTwips, r.Page.BottomMarginTwips = printerMargin, printerMargin
		r.Page.LeftMarginTwips, r.Page.RightMarginTwips = printerMargin, printerMargin
	}
	return &r, nil
}

const printerMargin = 360

func loadOverlay(path string) (*Overlay, error) {
	o := &Overlay{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, o); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return o, nil
}

func (r *Report) field(name string) *Field {
	for i := range r.Fields {
		if r.Fields[i].Name == name {
			return &r.Fields[i]
		}
	}
	return nil
}

func (r *Report) formulaField(name string) *Field { return r.fieldKind("@", name) }

func (r *Report) formulas() map[string]string {
	m := map[string]string{}
	for _, f := range r.Fields {
		if f.FormulaText != "" {
			m[f.Name] = f.FormulaText
		}
	}
	return m
}

func (r *Report) runningTotals() []Field {
	var out []Field
	for _, f := range r.Fields {
		if f.SummarizedFieldName != "" && strings.Contains(f.Type, "RunningTotal") {
			out = append(out, f)
		}
	}
	return out
}

// binding resolves what a field object prints, as a reference: "InvoiceNo"
// (database field), "@Balance" (formula), "#Total" (running total), or a
// special field ("PageNofM", "PrintDate", "PrintTime"). Crystal names an
// object after its field plus a counter ("Due301" for @Due30), which recovers
// bindings the parser lost.
func (r *Report) binding(o Object, ov *Overlay) string {
	if b, ok := ov.Bind[o.Name]; ok {
		return b
	}
	if o.SummaryFunction != nil {
		return "#" + summaryName(o)
	}
	switch o.FieldName {
	case "Page N of M":
		return "PageNofM"
	case "Print Date":
		return "PrintDate"
	case "Print Time":
		return "PrintTime"
	case "Page Number":
		return "PageNumber"
	case "Record Number":
		return "RecordNumber"
	case "Data Date":
		return "DataDate"
	case "Data Time":
		return "DataTime"
	}
	// "Group #2 Name" (or "Group #2") prints the value the group breaks on.
	var n int
	if _, err := fmt.Sscanf(o.FieldName, "Group #%d", &n); err == nil && n >= 1 && n <= len(r.Groups) {
		return r.refOf(r.Groups[n-1].FieldName)
	}
	ref := o.FieldName
	if r.resolves(ref) {
		return ref
	}
	for base := o.Name; len(base) > 0 && strings.ContainsRune("0123456789", rune(base[len(base)-1])); {
		base = base[:len(base)-1]
		for _, cand := range []string{"@" + base, "#" + base, base} {
			if r.resolves(cand) {
				return cand
			}
		}
	}
	return ref
}

// summaryName is the running total a summary field object computes.
func summaryName(o Object) string { return "summary:" + o.Name }

// refOf turns a field name as the model writes it ("Amt", "@Due", "#Total")
// into a reference, trying a formula and a running total for a bare name.
func (r *Report) refOf(name string) string {
	if r.resolves(name) {
		return name
	}
	for _, cand := range []string{"@" + name, "#" + name} {
		if r.resolves(cand) {
			return cand
		}
	}
	return name
}

func (r *Report) resolves(ref string) bool {
	switch {
	case strings.HasPrefix(ref, "@"):
		f := r.fieldKind("@", ref[1:])
		return f != nil && f.FormulaText != ""
	case strings.HasPrefix(ref, "#"):
		f := r.fieldKind("#", ref[1:])
		return f != nil && f.SummarizedFieldName != ""
	default:
		f := r.fieldKind("", ref)
		return f != nil && f.FormulaText == "" && f.SummarizedFieldName == ""
	}
}

// fieldKind finds a field by name (any case) and kind, as a reference's
// prefix gives it: "@" formula, "#" running total, "?" parameter, "" a
// database field. A formula and a running total may share a name.
func (r *Report) fieldKind(prefix, name string) *Field {
	kind := map[string]string{"@": "FormulaField", "#": "RunningTotalField", "?": "ParameterField", "": "DatabaseField"}[prefix]
	var loose *Field
	for i := range r.Fields {
		f := &r.Fields[i]
		if !strings.EqualFold(f.Name, name) {
			continue
		}
		if f.Type == kind {
			return f
		}
		if f.Type == "" && loose == nil { // a model without $type
			loose = f
		}
	}
	return loose
}
