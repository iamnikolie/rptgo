package rpt

// The report model: a Go port of Majorsilence.Crystal.Model. Field order in each
// struct is the order System.Text.Json writes the C# properties in (a derived
// type's own properties first, then its base type's), so the JSON this package
// writes lines up with the reference one property for property. Fields and
// objects are written with a leading "$type" naming their C# class.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
)

type ReportDefinition struct {
	ReportTitle            string
	Author                 string
	ReportComments         string
	CrVersion              int
	Language               *string
	Page                   PageLayout
	DataSources            []*DataSource
	TableLinks             []*TableLink
	Fields                 []ReportField
	Groups                 []*GroupDefinition
	SortFields             []*SortField
	Sections               []*Section
	RecordSelectionFormula *string
	GroupSelectionFormula  *string
}

type PageLayout struct {
	WidthTwips        int
	HeightTwips       int
	TopMarginTwips    int
	BottomMarginTwips int
	LeftMarginTwips   int
	RightMarginTwips  int
	Orientation       PageOrientation
	PaperFromPrinter  bool
	// DefaultMargins: the report keeps Crystal's "default margins" (all four
	// stored as 0x80000000), so the printer decides them. Not part of the
	// reference JSON.
	DefaultMargins bool `json:"-"`
}

type PageOrientation string

const (
	Portrait  PageOrientation = "Portrait"
	Landscape PageOrientation = "Landscape"
)

type DataSourceKind string

const (
	DataSourceOdbc    DataSourceKind = "Odbc"
	DataSourceOleDb   DataSourceKind = "OleDb"
	DataSourceNative  DataSourceKind = "Native"
	DataSourceDataset DataSourceKind = "Dataset"
	DataSourceUnknown DataSourceKind = "Unknown"
)

type DataSource struct {
	Name         string
	Kind         DataSourceKind
	ServerName   *string
	DatabaseName *string
	OdbcDsn      *string
	SqlQuery     *string
	Tables       []*TableDefinition
}

type TableDefinition struct {
	Name    string
	Alias   string
	Columns []*ColumnDefinition
}

type ColumnDefinition struct {
	Name     string
	DataType string
}

type TableJoinType string
type LinkOperator string

type TableLink struct {
	SourceTable  string
	SourceColumn string
	TargetTable  string
	TargetColumn string
	JoinType     TableJoinType
	Operator     LinkOperator
}

type SortDirection string

type SortField struct {
	FieldName string
	TableName string
	Direction SortDirection
}

type GroupSortOrder string
type GroupCondition string

type GroupDefinition struct {
	Level             int
	FieldName         string
	TableName         string
	SortOrder         GroupSortOrder
	Condition         GroupCondition
	RepeatGroupHeader bool
	TopN              *GroupTopN
}

type GroupTopN struct {
	Count         int
	Bottom        bool
	Function      AggregateFunction
	FieldName     string
	TableName     string
	DiscardOthers bool
	OthersName    string
}

type SectionType string

const (
	ReportHeader  SectionType = "ReportHeader"
	PageHeader    SectionType = "PageHeader"
	GroupHeader   SectionType = "GroupHeader"
	Details       SectionType = "Details"
	GroupFooter   SectionType = "GroupFooter"
	PageFooter    SectionType = "PageFooter"
	ReportFooter  SectionType = "ReportFooter"
	UnknownSecton SectionType = "Unknown"
)

type Section struct {
	Type                 SectionType
	GroupLevel           int
	HeightTwips          int
	Suppress             bool
	NewPageBefore        bool
	NewPageAfter         bool
	ResetPageNumber      bool
	Underlay             bool
	RepeatGroupHeader    bool
	SuppressFormula      *string
	NewPageBeforeFormula *string
	NewPageAfterFormula  *string
	BackColorFormula     *string
	BackColor            *string
	Objects              []ReportObject
}

// ---- fields ----

type AggregateFunction string

const (
	AggSum               AggregateFunction = "Sum"
	AggCount             AggregateFunction = "Count"
	AggAverage           AggregateFunction = "Average"
	AggMaximum           AggregateFunction = "Maximum"
	AggMinimum           AggregateFunction = "Minimum"
	AggDistinctCount     AggregateFunction = "DistinctCount"
	AggStandardDeviation AggregateFunction = "StandardDeviation"
	AggVariance          AggregateFunction = "Variance"
	AggPercentage        AggregateFunction = "Percentage"
)

// ReportField is DatabaseField, FormulaField, ParameterField or RunningTotalField.
type ReportField interface {
	base() *FieldBase
}

// FieldBase is ReportField's own properties; it is embedded last so its JSON
// comes after the derived type's, as System.Text.Json writes them.
type FieldBase struct {
	Name string
	Kind string
}

func (b *FieldBase) base() *FieldBase { return b }

type DatabaseField struct {
	TableName  string
	ColumnName string
	DataType   string
	FieldBase
}

type FormulaSyntax string

type FormulaField struct {
	FormulaText string
	Syntax      FormulaSyntax
	ResultType  *string
	FieldBase
}

// PickListEntry is a C# (string Value, string Label) tuple, which
// System.Text.Json writes by its field names.
type PickListEntry struct {
	Item1 string
	Item2 string
}

type ParameterField struct {
	PromptText          string
	DataType            string
	AllowMultipleValues bool
	DefaultValue        *string
	PickListValues      []PickListEntry
	FieldBase
}

type RunningTotalField struct {
	DataType            string
	SummarizedFieldName string
	Function            AggregateFunction
	EvaluateCondition   *string
	ResetCondition      *string
	FieldBase
}

// ---- objects ----

// ReportObject is any object placed in a section.
type ReportObject interface {
	object() *ObjectBase
}

type ObjectBase struct {
	Name             string
	Bounds           ObjectBounds
	Format           ObjectFormat
	SuppressOverride *bool
}

func (b *ObjectBase) object() *ObjectBase { return b }

type ObjectBounds struct {
	Left   int
	Top    int
	Width  int
	Height int
}

type HorizontalAlignment string

const (
	AlignDefault HorizontalAlignment = "Default"
	AlignLeft    HorizontalAlignment = "Left"
	AlignCenter  HorizontalAlignment = "Center"
	AlignRight   HorizontalAlignment = "Right"
	AlignJustify HorizontalAlignment = "Justify"
)

type ObjectFormat struct {
	FontName         *string
	FontSize         *float64
	Bold             bool
	Italic           bool
	Underline        bool
	ForeColor        *string
	BackColor        *string
	HAlign           HorizontalAlignment
	BorderLeft       byte
	BorderRight      byte
	BorderTop        byte
	BorderBottom     byte
	BorderWidthTwips int
	BorderColor      *string
	DropShadow       bool
	CanGrow          bool
	// Suppress: the object's own "Suppress" box, so it never prints. Not in
	// the C# reader; omitted when false.
	Suppress bool `json:",omitempty"`
	// SuppressIfZero: a number field prints blank at zero. Not in the C#
	// reader; omitted when false.
	SuppressIfZero bool `json:",omitempty"`
	// NegativeFormat: how a number field shows a negative value, from its
	// numeric format: 0 no sign, 1 leading minus, 3 in brackets. Not in the
	// C# reader; omitted when 0.
	NegativeFormat  int `json:",omitempty"`
	SuppressIfBlank bool
	FormatString    *string
	Conditions      []ConditionalFormat
}

// newFormat is C#'s `new ObjectFormat()`.
func newFormat() ObjectFormat {
	return ObjectFormat{HAlign: AlignDefault, Conditions: []ConditionalFormat{}}
}

type ConditionalOperator string

type ConditionalFormat struct {
	Operator  ConditionalOperator
	Threshold float64
	FontColor *string
	BackColor *string
}

type TextObject struct {
	Text string
	ObjectBase
}

type FieldObject struct {
	FieldName       string
	SummaryFunction *AggregateFunction
	ObjectBase
}

type ImageSourceKind string

type ImageObject struct {
	Source         ImageSourceKind
	FieldName      string
	EmbeddingIndex int
	ImageData      []byte
	MimeType       *string
	ObjectBase
}

// ShapeObject is a line or a box; EndSection is the section it runs down into.
type ShapeObject struct {
	EndSection     *Section
	EndBottomTwips int
	ObjectBase
}

type LineObject struct{ ShapeObject }
type BoxObject struct{ ShapeObject }

type SubreportObject struct {
	SubreportName           string
	ReportName              string
	SubdocumentIndex        int
	Report                  *ReportDefinition
	ParameterValueOverrides map[string]any
	ObjectBase
}

type ChartKind string
type ChartStacking string
type ChartLegendPosition string

type ChartFont struct {
	Name   *string
	SizePt *float64
	Bold   bool
	Italic bool
}

type ChartObject struct {
	Title                 string
	Kind                  ChartKind
	CategoryFields        []string
	CategoryConditions    []GroupCondition
	SeriesField           string
	SeriesFunction        AggregateFunction
	Stacking              ChartStacking
	CategoryAxisTitle     string
	ValueAxisTitle        string
	ShowLegend            bool
	LegendPosition        ChartLegendPosition
	ValueAxisGridLines    bool
	CategoryAxisGridLines bool
	TitleFont             *ChartFont
	CategoryAxisTitleFont *ChartFont
	ValueAxisTitleFont    *ChartFont
	LegendFont            *ChartFont
	CategoryLabelFont     *ChartFont
	ValueLabelFont        *ChartFont
	Palette               []string
	ColorByCategory       bool
	ChartIndex            int
	ObjectBase
}

type CrossTabCell struct {
	FieldName string
	Function  AggregateFunction
}

type CrossTabCellLayout struct {
	RowLevel        int
	ColumnLevel     int
	Box             ObjectBounds
	MarginLeftTwips int
	MarginTopTwips  int
	SummaryFormats  []ObjectFormat
}

type CrossTabAxisLevel struct {
	Level            int
	HeaderFormat     *ObjectFormat
	HeaderBounds     *ObjectBounds
	TotalLabel       *string
	TotalLabelFormat *ObjectFormat
	TotalBackColor   *string
}

type CrossTabObject struct {
	RowGroupFields            []string
	ColumnGroupFields         []string
	Cells                     []CrossTabCell
	ShowGrid                  bool
	SuppressRowGrandTotals    bool
	SuppressColumnGrandTotals bool
	ColumnHeaderHeightTwips   int
	RowHeaderWidthTwips       int
	CellLayouts               []*CrossTabCellLayout
	RowLevels                 []*CrossTabAxisLevel
	ColumnLevels              []*CrossTabAxisLevel
	ObjectBase
}

// ---- JSON ----

// ObjectBounds is a C# record with computed inch properties after the four ints.
func (b ObjectBounds) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Left, Top, Width, Height                         int
		LeftInches, TopInches, WidthInches, HeightInches float64
	}{b.Left, b.Top, b.Width, b.Height,
		float64(b.Left) / 1440.0, float64(b.Top) / 1440.0, float64(b.Width) / 1440.0, float64(b.Height) / 1440.0})
}

// typed writes v's JSON with a leading "$type".
func typed(name string, v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	head := []byte(`{"$type":` + quote(name))
	if len(b) > 2 {
		head = append(head, ',')
	}
	return append(head, b[1:]...), nil
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (f *DatabaseField) MarshalJSON() ([]byte, error) {
	type plain DatabaseField
	return typed("DatabaseField", (*plain)(f))
}

func (f *FormulaField) MarshalJSON() ([]byte, error) {
	type plain FormulaField
	return typed("FormulaField", (*plain)(f))
}

func (f *ParameterField) MarshalJSON() ([]byte, error) {
	type plain ParameterField
	c := *(*plain)(f)
	if c.PickListValues == nil {
		c.PickListValues = []PickListEntry{}
	}
	return typed("ParameterField", &c)
}

func (f *RunningTotalField) MarshalJSON() ([]byte, error) {
	type plain RunningTotalField
	return typed("RunningTotalField", (*plain)(f))
}

func (o *TextObject) MarshalJSON() ([]byte, error) {
	type plain TextObject
	return typed("TextObject", (*plain)(o))
}

func (o *FieldObject) MarshalJSON() ([]byte, error) {
	type plain FieldObject
	return typed("FieldObject", (*plain)(o))
}

func (o *ImageObject) MarshalJSON() ([]byte, error) {
	type plain ImageObject
	return typed("ImageObject", (*plain)(o))
}

func (o *LineObject) MarshalJSON() ([]byte, error) {
	type plain ShapeObject
	return typed("LineObject", (*plain)(&o.ShapeObject))
}

func (o *BoxObject) MarshalJSON() ([]byte, error) {
	type plain ShapeObject
	return typed("BoxObject", (*plain)(&o.ShapeObject))
}

func (o *SubreportObject) MarshalJSON() ([]byte, error) {
	type plain SubreportObject
	c := *(*plain)(o)
	if c.ParameterValueOverrides == nil {
		c.ParameterValueOverrides = map[string]any{}
	}
	return typed("SubreportObject", &c)
}

func (o *ChartObject) MarshalJSON() ([]byte, error) {
	type plain ChartObject
	c := *(*plain)(o)
	if c.CategoryFields == nil {
		c.CategoryFields = []string{}
	}
	if c.CategoryConditions == nil {
		c.CategoryConditions = []GroupCondition{}
	}
	if c.Palette == nil {
		c.Palette = []string{}
	}
	return typed("ChartObject", &c)
}

func (o *CrossTabObject) MarshalJSON() ([]byte, error) {
	type plain CrossTabObject
	c := *(*plain)(o)
	if c.RowGroupFields == nil {
		c.RowGroupFields = []string{}
	}
	if c.ColumnGroupFields == nil {
		c.ColumnGroupFields = []string{}
	}
	if c.Cells == nil {
		c.Cells = []CrossTabCell{}
	}
	if c.CellLayouts == nil {
		c.CellLayouts = []*CrossTabCellLayout{}
	}
	if c.RowLevels == nil {
		c.RowLevels = []*CrossTabAxisLevel{}
	}
	if c.ColumnLevels == nil {
		c.ColumnLevels = []*CrossTabAxisLevel{}
	}
	return typed("CrossTabObject", &c)
}

func (f ObjectFormat) MarshalJSON() ([]byte, error) {
	type plain ObjectFormat
	if f.Conditions == nil {
		f.Conditions = []ConditionalFormat{}
	}
	if f.HAlign == "" {
		f.HAlign = AlignDefault
	}
	return json.Marshal(plain(f))
}

func (l CrossTabCellLayout) MarshalJSON() ([]byte, error) {
	type plain CrossTabCellLayout
	if l.SummaryFormats == nil {
		l.SummaryFormats = []ObjectFormat{}
	}
	return json.Marshal(plain(l))
}

func (s *Section) MarshalJSON() ([]byte, error) {
	type plain Section
	c := *(*plain)(s)
	if c.Objects == nil {
		c.Objects = []ReportObject{}
	}
	return json.Marshal(&c)
}

func (r *ReportDefinition) MarshalJSON() ([]byte, error) {
	type plain ReportDefinition
	c := *(*plain)(r)
	if c.DataSources == nil {
		c.DataSources = []*DataSource{}
	}
	if c.TableLinks == nil {
		c.TableLinks = []*TableLink{}
	}
	if c.Fields == nil {
		c.Fields = []ReportField{}
	}
	if c.Groups == nil {
		c.Groups = []*GroupDefinition{}
	}
	if c.SortFields == nil {
		c.SortFields = []*SortField{}
	}
	if c.Sections == nil {
		c.Sections = []*Section{}
	}
	return json.Marshal(&c)
}

func (d *DataSource) MarshalJSON() ([]byte, error) {
	type plain DataSource
	c := *(*plain)(d)
	if c.Tables == nil {
		c.Tables = []*TableDefinition{}
	}
	return json.Marshal(&c)
}

func (t *TableDefinition) MarshalJSON() ([]byte, error) {
	type plain TableDefinition
	c := *(*plain)(t)
	if c.Columns == nil {
		c.Columns = []*ColumnDefinition{}
	}
	return json.Marshal(&c)
}

// MarshalIndent writes the report the way the reference tool does: indented
// two spaces, strings escaped as System.Text.Json's default encoder escapes
// them, so the output is byte-identical to the reference.
func MarshalIndent(r *ReportDefinition) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return reescapeStrings(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// reescapeStrings rewrites every JSON string literal with stjEscape.
func reescapeStrings(b []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(b); i++ {
		if b[i] != '"' {
			out.WriteByte(b[i])
			continue
		}
		j := i + 1
		for j < len(b) && b[j] != '"' {
			if b[j] == '\\' {
				j++
			}
			j++
		}
		var s string
		if err := json.Unmarshal(b[i:j+1], &s); err != nil {
			out.Write(b[i : j+1])
		} else {
			out.WriteString(stjEscape(s))
		}
		i = j
	}
	return out.Bytes()
}

// stjEscape quotes s as JavaScriptEncoder.Default does: printable ASCII is
// kept except the HTML-sensitive " & ' + < > ` (written \uXXXX, upper-case
// hex); backslash and the usual control characters get their short escapes;
// everything else, non-ASCII included, is \uXXXX (UTF-16 units).
func stjEscape(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, u := range utf16.Encode([]rune(s)) {
		switch {
		case u == '\\':
			b.WriteString(`\\`)
		case u == '\n':
			b.WriteString(`\n`)
		case u == '\r':
			b.WriteString(`\r`)
		case u == '\t':
			b.WriteString(`\t`)
		case u == '\b':
			b.WriteString(`\b`)
		case u == '\f':
			b.WriteString(`\f`)
		case u >= 0x20 && u < 0x7F && !strings.ContainsRune("\"&'+<>`", rune(u)):
			b.WriteByte(byte(u))
		default:
			fmt.Fprintf(&b, `\u%04X`, u)
		}
	}
	b.WriteByte('"')
	return b.String()
}
