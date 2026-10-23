package main

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"
)

// run extracts the sales report after change edits its model.
func run(t *testing.T, change func(r *Report), subs map[string][]Row) (*Engine, *Extraction) {
	t.Helper()
	r, ov := salesReport(t)
	change(r)
	rows, err := readRows("testdata/sales.rows.json", r)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(r, ov, rows, time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	e.SetSubreportRows(subs)
	return e, e.Extract("sales", nil, -1)
}

func strp(s string) *string { return &s }

func section(r *Report, typ string) *Section {
	for i := range r.Sections {
		if r.Sections[i].Type == typ {
			return &r.Sections[i]
		}
	}
	return nil
}

func TestRecordSelection(t *testing.T) {
	_, x := run(t, func(r *Report) { r.RecordSelectionFormula = strp(`{Sales.Status} <> "Void"`) }, nil)
	north := x.Groups[1]
	if north.Footer.vals["Region total"] != 3.0 || north.Footer.vals["Amount"] != 2500.0 {
		t.Errorf("north footer %v", north.Footer.vals)
	}
}

func TestGroupSelection(t *testing.T) {
	_, x := run(t, func(r *Report) {
		r.GroupSelectionFormula = strp(`Sum({Sales.Amount}, {Sales.Region}) > 2600`)
	}, nil)
	if len(x.Groups) != 1 || x.Groups[0].Value != "North" || x.Footer.vals["Amount"] != 2800.0 {
		t.Errorf("groups %d, footer %v", len(x.Groups), x.Footer.vals)
	}
}

func TestTopNWithOthers(t *testing.T) {
	_, x := run(t, func(r *Report) {
		r.Groups[0].TopN = &struct {
			Count         int
			Bottom        bool
			Function      string
			FieldName     string
			DiscardOthers bool
			OthersName    string
		}{Count: 1, Function: "Sum", FieldName: "Amount", OthersName: "Rest"}
	}, nil)
	if len(x.Groups) != 2 || x.Groups[0].Value != "North" || x.Groups[1].Value != "Rest" {
		t.Fatalf("groups %v, %v", x.Groups[0].Value, x.Groups[len(x.Groups)-1].Value)
	}
	if x.Groups[1].Header.vals["Region"] != "Rest" || x.Groups[1].Footer.vals["Amount"] != 2500.0 {
		t.Errorf("others group header %v footer %v", x.Groups[1].Header.vals, x.Groups[1].Footer.vals)
	}
}

func TestSummariesReadAhead(t *testing.T) {
	_, x := run(t, func(r *Report) {
		gh, gf := section(r, "GroupHeader"), section(r, "GroupFooter")
		gh.Objects = append(gh.Objects, Object{Type: "FieldObject", Name: "HeadSum", FieldName: "Amount", SummaryFunction: strp("Sum"),
			Bounds: struct{ Left, Top, Width, Height int }{4000, 30, 1300, 240}})
		gf.Objects = append(gf.Objects, Object{Type: "FieldObject", Name: "Pct", FieldName: "Amount", SummaryFunction: strp("Percentage"),
			Bounds: struct{ Left, Top, Width, Height int }{8000, 60, 900, 240}})
	}, nil)
	north := x.Groups[1]
	if north.Header.vals["Sum of Amount"] != 2800.0 {
		t.Errorf("header %v", north.Header.vals)
	}
	if got := north.Footer.vals["Percentage of Amount"]; got != 52.830189 {
		t.Errorf("percentage %v", got)
	}
}

func TestCrossTab(t *testing.T) {
	e, x := run(t, func(r *Report) {
		rf := section(r, "ReportFooter")
		rf.Objects = append(rf.Objects, Object{Type: "CrossTabObject", Name: "CT", RowGroupFields: []string{"Region"},
			ColumnGroupFields: []string{"Status"}, Cells: []struct{ FieldName, Function string }{{"Amount", "Sum"}},
			Bounds: struct{ Left, Top, Width, Height int }{0, 800, 6000, 400}})
	}, nil)
	var ct *CrossTabData
	for _, o := range x.Objects {
		if o.Kind == "crosstab" {
			ct = o.CrossTab
		}
	}
	if ct == nil {
		t.Fatal("no cross-tab extracted")
	}
	if strings.Join(ct.Rows, ",") != "East,North" || strings.Join(ct.Columns, ",") != "Open,Paid,Void" {
		t.Fatalf("rows %v columns %v", ct.Rows, ct.Columns)
	}
	if ct.Cells[1][0][0] != 1700.0 || ct.GrandTotal[0] != 5300.0 || ct.Cells[0][1][0] != nil {
		t.Errorf("cells %v grand %v", ct.Cells, ct.GrandTotal)
	}
	if doc := e.Document("x"); !strings.Contains(doc, "Void") {
		t.Error("cross-tab not rendered")
	}
}

func TestSubreport(t *testing.T) {
	r, _ := salesReport(t)
	sub := *r
	rows, _ := readRows("testdata/sales.rows.json", r)
	change := func(m *Report) {
		rf := section(m, "ReportFooter")
		rf.Objects = append(rf.Objects, Object{Type: "SubreportObject", Name: "Sub1", SubreportName: "Detail", Report: &sub,
			Bounds: struct{ Left, Top, Width, Height int }{0, 820, 10000, 60}})
	}
	e, x := run(t, change, map[string][]Row{"Detail": rows[:2]})
	var got *Extraction
	for _, o := range x.Objects {
		if o.Kind == "subreport" {
			got = o.Subreport
		}
	}
	if got == nil || got.Footer.vals["Amount"] != 2000.0 {
		t.Fatalf("subreport extraction %+v", got)
	}
	doc := e.Document("x")
	if strings.Count(doc, "A-101") != 2 {
		t.Errorf("subreport rows not printed in the main report")
	}
	// Without rows it is left out, with a warning.
	e2, _ := run(t, change, nil)
	e2.Document("x")
	warned := false
	for k := range e2.errs {
		warned = warned || strings.Contains(k, `subreport "Detail" not printed`)
	}
	if !warned {
		t.Error("no warning for a subreport without rows")
	}
}

func TestPictureAndColours(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := bmp.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	e, _ := run(t, func(r *Report) {
		rh := section(r, "ReportHeader")
		rh.Objects = append(rh.Objects, Object{Type: "ImageObject", Name: "Logo", Source: "Embedded", ImageData: b.Bytes(),
			Bounds: struct{ Left, Top, Width, Height int }{0, 0, 720, 720}})
		for i, o := range section(r, "Details").Objects {
			if o.Name == "Margin1" {
				section(r, "Details").Objects[i].Format.Conditions = append(o.Format.Conditions, struct {
					Operator  string
					Threshold float64
					FontColor *string
					BackColor *string
				}{"LessThan", 0, strp("#FF0000"), nil})
			}
		}
	}, nil)
	doc := e.Document("x")
	if !strings.Contains(doc, "data:image/png;base64,") {
		t.Error("picture not drawn")
	}
	if strings.Count(doc, "color:#FF0000") != 1 {
		t.Errorf("negative margin not red: %d", strings.Count(doc, "color:#FF0000"))
	}
	if !strings.Contains(doc, "<svg") {
		t.Error("chart not drawn")
	}
}

func TestChartPDF(t *testing.T) {
	e, _ := run(t, func(*Report) {}, nil)
	dir := "/usr/share/fonts/truetype/liberation"
	if _, err := e.PDF("x", dir); err != nil && !strings.Contains(err.Error(), "no such file") {
		t.Fatal(err)
	}
}

func TestInOperator(t *testing.T) {
	for src, want := range map[string]bool{
		`{x} in ["Rent", "Attorney"]`: true,
		`{x} in ["Royalty"]`:          false,
		`{n} in 1 to 5`:               true,
		`{n} in [10, 2 to 4]`:         true,
		`{x} in "Rental income"`:      true,
	} {
		n, err := parseFormula(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		got, err := eval(n, mapEnv{"x": "Rent", "n": 3.0}, map[string]any{})
		if err != nil || got != want {
			t.Errorf("%s = %v, %v", src, got, err)
		}
	}
}

// Review regressions: ranges in arrays and Case arms, Others keeps inner
// groups together and real values, objects in a summary report's lines.
func TestRangeMatching(t *testing.T) {
	for src, want := range map[string]any{
		`{n} in [1 to 5]`:                             false,
		`{n} in [2 to 4, 10]`:                         false,
		`select {m} case 1 to 5: "in" default: "out"`: "in",
	} {
		n, err := parseFormula(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got, err := eval(n, mapEnv{"n": 0.0, "m": 3.0}, map[string]any{}); err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", src, got, err, want)
		}
	}
	n, _ := parseBasic("select case {m}\n case 1 to 5\n formula = \"in\"\nend select", "String")
	if got, _ := eval(n, mapEnv{"m": 3.0}, map[string]any{}); got != "in" {
		t.Errorf("Basic Case 1 To 5 = %v", got)
	}
}

func TestOthersKeepsInnerGroupsAndValues(t *testing.T) {
	_, x := run(t, func(r *Report) {
		r.Groups = append(r.Groups, Group{Level: 1, FieldName: "Status", TableName: "Sales", SortOrder: "Ascending", Condition: "EachValue"})
		r.Groups[0].TopN = &struct {
			Count         int
			Bottom        bool
			Function      string
			FieldName     string
			DiscardOthers bool
			OthersName    string
		}{Count: 0, Function: "Sum", FieldName: "Amount"}
		r.Groups[0].TopN.Count = 1
		r.Groups[0].FieldName, r.Groups[1].FieldName = "Rep", "Status"
	}, nil)
	others := x.Groups[len(x.Groups)-1]
	var inner []string
	for _, g := range others.Groups {
		inner = append(inner, g.Value.(string))
	}
	if strings.Join(inner, ",") != "Open,Paid,Void" {
		t.Errorf("inner groups of Others: %v", inner)
	}
	for _, g := range others.Groups {
		for _, l := range g.Lines {
			if l[2] == "Others" {
				t.Errorf("a detail row prints the Others name: %v", l)
			}
		}
	}
}

func TestSummaryReportObjects(t *testing.T) {
	_, x := run(t, func(r *Report) {
		section(r, "Details").Objects = nil
		gf := section(r, "GroupFooter")
		gf.Objects = append(gf.Objects, Object{Type: "CrossTabObject", Name: "CT", RowGroupFields: []string{"Rep"},
			ColumnGroupFields: []string{"Status"}, Cells: []struct{ FieldName, Function string }{{"Amount", "Sum"}},
			Bounds: struct{ Left, Top, Width, Height int }{0, 300, 6000, 100}})
	}, nil)
	n := 0
	for _, o := range x.Objects {
		if o.Kind == "crosstab" {
			n++
			if o.Row < 1 || o.Row > 2 {
				t.Errorf("cross-tab row %d", o.Row)
			}
		}
	}
	if n != 2 {
		t.Errorf("%d cross-tabs, want one per line", n)
	}
}
