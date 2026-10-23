package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// golden compares got with testdata/golden/<name>, or writes it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file; run go test -update and review the diff\n--- got ---\n%s", name, got)
	}
}

func toJSON(t *testing.T, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writeJSON(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func salesReport(t *testing.T) (*Report, *Overlay) {
	t.Helper()
	r, ov, err := openReport("testdata/sales.json", "")
	if err != nil {
		t.Fatal(err)
	}
	return r, ov
}

func salesEngine(t *testing.T, rowsFile string) *Engine {
	t.Helper()
	r, ov := salesReport(t)
	rows, err := readRows(rowsFile, r)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(r, ov, rows, time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	e.SetParams([]string{"MinAmount=1000"})
	return e
}

func TestDescribe(t *testing.T) {
	r, ov := salesReport(t)
	d := Describe(r, ov, "sales.json", DescribeOptions{})
	golden(t, "describe.md", []byte(d.Markdown(1)))
	golden(t, "describe.json", toJSON(t, d))
}

func TestExtract(t *testing.T) {
	x := salesEngine(t, "testdata/sales.rows.json").Extract("sales.json", []string{"MinAmount=1000"}, -1)
	golden(t, "extract.md", []byte(x.Markdown()))
	golden(t, "extract.json", toJSON(t, x))

	// The same rows from CSV, cut to two detail rows: groups and totals stay.
	y := salesEngine(t, "testdata/sales.rows.csv").Extract("sales.json", nil, 2)
	if y.Groups[1].Omitted != 3 || y.Footer.vals["Amount"] != 5300.0 {
		t.Errorf("max-rows: omitted %d, total %v", y.Groups[1].Omitted, y.Footer.vals["Amount"])
	}
}

func TestExtractSemantics(t *testing.T) {
	x := salesEngine(t, "testdata/sales.rows.json").Extract("sales.json", nil, -1)
	if len(x.Groups) != 2 || x.Groups[0].Value != "East" || x.Groups[1].Value != "North" {
		t.Fatalf("groups: %+v", x.Groups)
	}
	north := x.Groups[1]
	// Sorted by date within the group; the void invoice's details are
	// suppressed by the section formula but it still counts in the totals.
	var order []string
	for _, l := range north.Lines {
		order = append(order, l[0].(string))
	}
	if strings.Join(order, ",") != "A-102,A-101,A-103" {
		t.Errorf("north rows %v", order)
	}
	if north.Footer.vals["Amount"] != 2800.0 || north.Footer.vals["Region total"] != 4.0 {
		t.Errorf("north footer %v", north.Footer.vals)
	}
	// Share: Sum({Sales.Amount}, {Sales.Region}) over the whole group.
	if got := north.Lines[1][6]; got != 42.857143 {
		t.Errorf("share of A-101 = %v", got)
	}
	// {?MinAmount} reaches formulas.
	if north.Lines[1][7] != "*" || north.Lines[0][7] != "" {
		t.Errorf("flags %v %v", north.Lines[1][7], north.Lines[0][7])
	}
}

func TestRenderNewPageAfterGroup(t *testing.T) {
	e := salesEngine(t, "testdata/sales.rows.json")
	e.Document("sales")
	if len(e.pages) != 2 {
		t.Errorf("%d pages, want one per region", len(e.pages))
	}
}

func TestAudit(t *testing.T) {
	r, ov := salesReport(t)
	golden(t, "audit.json", toJSON(t, Audit(r, ov)))
}

func TestIndex(t *testing.T) {
	ix := BuildIndex([]reportFile{{"testdata/sales.json", "sales.json"}})
	golden(t, "index.md", []byte(ix.Markdown()))
}

func TestReadRowsTypes(t *testing.T) {
	r, _ := salesReport(t)
	rows, err := readRows("testdata/sales.rows.csv", r)
	if err != nil {
		t.Fatal(err)
	}
	row := rows[0]
	if _, ok := row["invoicedate"].(time.Time); !ok {
		t.Errorf("date column read as %T", row["invoicedate"])
	}
	if row["amount"] != 1200.0 || row["invoiceno"] != "A-101" {
		t.Errorf("row %v", row)
	}
}

// builtinFuncs must list exactly the functions call and recordCall handle.
func TestBuiltinFuncsMatchImplementation(t *testing.T) {
	src, err := os.ReadFile("formula.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	var impl []string
	for _, fn := range []string{"func (e *evaluator) recordCall(", "func (e *evaluator) call("} {
		body := text[strings.Index(text, fn):]
		body = body[:strings.Index(body, "\n}\n")]
		for _, m := range regexp.MustCompile(`(?m)^\tcase ("[a-z]+"(?:, "[a-z]+")*):`).FindAllStringSubmatch(body, -1) {
			for _, q := range strings.Split(m[1], ", ") {
				impl = append(impl, strings.Trim(q, `"`))
			}
		}
	}
	var listed []string
	for k := range builtinFuncs {
		listed = append(listed, k)
	}
	sort.Strings(impl)
	sort.Strings(listed)
	if strings.Join(impl, " ") != strings.Join(listed, " ") {
		t.Errorf("builtinFuncs %v\nimplemented %v", listed, impl)
	}
}

// The JSON schemas name themselves.
func TestSchemas(t *testing.T) {
	r, ov := salesReport(t)
	var d map[string]any
	if err := json.Unmarshal(toJSON(t, Describe(r, ov, "x", DescribeOptions{})), &d); err != nil || d["schema"] != "rptgo.describe/v1" {
		t.Errorf("describe schema %v %v", d["schema"], err)
	}
}

// Parameters take their declared type, and a stored procedure's "@Name"
// parameter answers {?@Name} and -param Name=... alike.
func TestParamTypes(t *testing.T) {
	r, ov := salesReport(t)
	r.Fields = append(r.Fields,
		Field{Type: "ParameterField", Name: "@Show", DataType: "Boolean"},
		Field{Type: "ParameterField", Name: "Code", DataType: "String"})
	e, err := NewEngine(r, ov, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetParams([]string{"Show=false", "Code=007", "@MinAmount=500"}); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]any{"?@Show": false, "?Show": false, "?Code": "007", "?MinAmount": 500.0} {
		if v, err := e.Lookup(ref); err != nil || v != want {
			t.Errorf("%s = %#v, %v; want %#v", ref, v, err, want)
		}
	}
	if err := e.SetParams([]string{"NoValue"}); err == nil {
		t.Error("a parameter without a value is accepted")
	}
}

// A summary report: details visible but empty, lines printed by the group
// footer. extract makes each footer a line of the table.
func TestExtractFooterLines(t *testing.T) {
	r, ov := salesReport(t)
	for i, s := range r.Sections {
		if s.Type == "Details" {
			r.Sections[i].Objects = nil
		}
	}
	rows, _ := readRows("testdata/sales.rows.json", r)
	e, err := NewEngine(r, ov, rows, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	x := e.Extract("sales", nil, -1)
	if len(x.Lines) != 2 || len(x.Groups) != 0 {
		t.Fatalf("lines %v, groups %d", x.Lines, len(x.Groups))
	}
	if x.Columns[1].Name != "Amount" || x.Lines[1][1] != 2800.0 {
		t.Errorf("columns %v, line %v", x.Columns, x.Lines[1])
	}
}

func TestTitlePage(t *testing.T) {
	e := salesEngine(t, "testdata/sales.rows.json")
	for i, s := range e.rpt.Sections {
		if s.Type == "ReportHeader" {
			e.rpt.Sections[i].NewPageAfter = true
		}
	}
	e.Document("sales")
	if len(e.pages) != 3 {
		t.Errorf("%d pages, want a title page and one per region", len(e.pages))
	}
}

func TestUnsupportedSummary(t *testing.T) {
	r, ov := salesReport(t)
	sd := "StandardDeviation"
	r.object("Amount2").SummaryFunction = &sd
	found := false
	for _, f := range Audit(r, ov) {
		found = found || f.Code == "summary-function"
	}
	if !found {
		t.Error("audit does not flag a summary it does not compute")
	}
	rows, _ := readRows("testdata/sales.rows.json", r)
	e, _ := NewEngine(r, ov, rows, time.Now())
	x := e.Extract("sales", nil, 0)
	if v, ok := x.Footer.vals["Maximum of Amount"]; ok && v != nil {
		t.Errorf("standard deviation printed %v", v)
	}
}
