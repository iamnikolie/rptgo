// rptgo renders a Crystal Reports .rpt from its parsed model and the SQL
// Server stored procedure that feeds it, to PDF or to HTML ready for a
// browser's print-to-PDF.
//
//	rptgo -rpt Invoices.rpt -proc spInvoices \
//	  -param Company=1 -param AsOf=2024-03-31 -printed "2024-04-01 09:00:00" -out invoices.pdf
//
// The database comes from $RPTGO_DSN (sqlserver://user:pass@host:port?database=name).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	var params multi
	rptPath := flag.String("rpt", "", "report: a .rpt file, or its model saved as .json")
	ovPath := flag.String("overlay", "", "overlay (.overlay.json); default: next to -rpt")
	proc := flag.String("proc", "", "stored procedure that feeds the report")
	flag.Var(&params, "param", "procedure parameter Name=Value (repeatable)")
	printed := flag.String("printed", "", `print timestamp "2006-01-02 15:04:05" (default: now)`)
	out := flag.String("out", "", "output file (default: stdout)")
	format := flag.String("format", "", "pdf or html (default: from -out's extension, else html)")
	fontDir := flag.String("fonts", envOr("RPTGO_FONTS", "/usr/share/fonts/truetype/liberation"),
		"directory with the Liberation TrueType fonts, for -format pdf")
	flag.Parse()

	if *rptPath == "" || *proc == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *ovPath == "" {
		*ovPath = strings.TrimSuffix(*rptPath, filepath.Ext(*rptPath)) + ".overlay.json"
	}
	if *format == "" {
		*format = "html"
		if strings.EqualFold(filepath.Ext(*out), ".pdf") {
			*format = "pdf"
		}
	}
	if *format != "pdf" && *format != "html" {
		fmt.Fprintln(os.Stderr, "rptgo: -format is pdf or html")
		os.Exit(2)
	}
	if err := run(*rptPath, *ovPath, *proc, params, *printed, *out, *format, *fontDir); err != nil {
		fmt.Fprintln(os.Stderr, "rptgo:", err)
		os.Exit(1)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func run(rptPath, ovPath, proc string, params []string, printed, out, format, fontDir string) error {
	rpt, err := loadReport(rptPath)
	if err != nil {
		return err
	}
	ov, err := loadOverlay(ovPath)
	if err != nil {
		return err
	}
	dsn := os.Getenv("RPTGO_DSN")
	if dsn == "" {
		return fmt.Errorf("RPTGO_DSN is not set")
	}
	rows, err := runProc(dsn, proc, params)
	if err != nil {
		return err
	}
	when := time.Now()
	if printed != "" {
		if when, err = time.Parse("2006-01-02 15:04:05", printed); err != nil {
			return err
		}
	}
	eng, err := NewEngine(rpt, ov, rows, when)
	if err != nil {
		return err
	}
	title := strings.TrimSuffix(filepath.Base(rptPath), filepath.Ext(rptPath))
	var doc []byte
	if format == "pdf" {
		if doc, err = eng.PDF(title, fontDir); err != nil {
			return err
		}
	} else {
		doc = []byte(eng.Document(title))
	}
	fmt.Fprintf(os.Stderr, "%s: %d rows, %d pages\n", proc, len(rows), len(eng.pages))
	reportErrors(eng.errs)
	if out == "" {
		_, err = os.Stdout.Write(doc)
		return err
	}
	return os.WriteFile(out, doc, 0o644)
}

func reportErrors(errs map[string]int) {
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(os.Stderr, "  warn: %s (x%d)\n", k, errs[k])
	}
}
