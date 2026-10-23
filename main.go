// rptgo reads Crystal Reports .rpt files without Crystal: it describes a
// report for a reader or a language model, audits what of it rptgo cannot
// reproduce, indexes a corpus of reports, extracts the data a report prints,
// and renders it to PDF or HTML.
//
//	rptgo describe Invoices.rpt                 # Markdown; -json for JSON
//	rptgo audit reports/                        # what rptgo does not reproduce
//	rptgo index reports/                        # catalog and cross-references
//	rptgo extract Invoices.rpt -proc spInvoices -param AsOf=2024-03-31
//	rptgo extract Invoices.rpt -data rows.json  # rows from a file
//	rptgo render Invoices.rpt -proc spInvoices -param AsOf=2024-03-31 -out invoices.pdf
//	rptgo skill                                 # the agent guide
//
// A stored procedure runs against $RPTGO_DSN (sqlserver://user:pass@host:port?database=name).
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

//go:embed SKILL.md
var skillDoc string

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

const usage = `rptgo reads Crystal Reports .rpt files and works with them without Crystal.

Usage:
  rptgo describe <report.rpt> [-json] [-layout]     what the report is: data, groups, columns, formulas, totals, layout
  rptgo audit <report.rpt|dir>... [-json] [-fail-on error|warning]
                                                    what rptgo does not reproduce, and what is unused
  rptgo index <dir|report.rpt>... [-json]           catalog of many reports with cross-references
  rptgo extract <report.rpt> (-proc P [-param N=V]... | -data rows.json|.jsonl|.csv) [-json] [-max-rows N]
                [-sub Name=proc]... [-sub-data Name=file]...
                                                    the data the report prints: header, groups, rows, totals,
                                                    charts, cross-tabs, subreports
  rptgo render <report.rpt> (-proc P [-param N=V]... | -data FILE) -out report.pdf|.html
                                                    the report as PDF or HTML
  rptgo skill                                       guide for agents using rptgo
  rptgo version

Run "rptgo <command> -h" for a command's flags. Markdown goes to stdout unless -json.
A stored procedure runs against $RPTGO_DSN (sqlserver://user:pass@host:port?database=name).
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := args[0]
	if strings.HasPrefix(cmd, "-") && cmd != "-h" && cmd != "-help" && cmd != "--help" {
		cmd, args = "render", append([]string{"render"}, args...) // the old flags-only form
	}
	var err error
	switch cmd {
	case "describe":
		err = cmdDescribe(args[1:])
	case "audit":
		err = cmdAudit(args[1:])
	case "index":
		err = cmdIndex(args[1:])
	case "extract":
		err = cmdExtract(args[1:])
	case "render":
		err = cmdRender(args[1:])
	case "skill":
		fmt.Print(skillDoc)
	case "version":
		fmt.Println("rptgo", version())
	case "help", "-h", "-help", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "rptgo: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		if code, ok := err.(exitCode); ok {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "rptgo:", err)
		os.Exit(1)
	}
}

// exitCode ends the program with a status and no message.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// parseArgs parses flags that may come before or after the positional
// arguments, and returns the positional ones.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return nil, exitCode(0)
			}
			return nil, exitCode(2)
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func newFlags(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: rptgo %s %s\n", name, synopsis)
		fs.PrintDefaults()
	}
	return fs
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func overlayPath(rpt string) string {
	return strings.TrimSuffix(rpt, filepath.Ext(rpt)) + ".overlay.json"
}

// openReport loads a report and its overlay (from -overlay, else next to it).
func openReport(path, ovPath string) (*Report, *Overlay, error) {
	r, err := loadReport(path)
	if err != nil {
		return nil, nil, err
	}
	if ovPath == "" {
		ovPath = overlayPath(path)
	}
	ov, err := loadOverlay(ovPath)
	if err != nil {
		return nil, nil, err
	}
	return r, ov, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func oneReport(pos []string, rptFlag string, fs *flag.FlagSet) (string, error) {
	if rptFlag != "" {
		pos = append([]string{rptFlag}, pos...)
	}
	if len(pos) != 1 {
		fs.Usage()
		return "", exitCode(2)
	}
	return pos[0], nil
}

func cmdDescribe(args []string) error {
	fs := newFlags("describe", "<report.rpt|model.json> [flags]")
	asJSON := fs.Bool("json", false, "write JSON (schema rptgo.describe/v1) instead of Markdown")
	layout := fs.Bool("layout", false, "add each object's position and size, and hidden sections' contents")
	ovPath := fs.String("overlay", "", "overlay (.overlay.json); default: next to the report")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	path, err := oneReport(pos, "", fs)
	if err != nil {
		return err
	}
	r, ov, err := openReport(path, *ovPath)
	if err != nil {
		return err
	}
	d := Describe(r, ov, filepath.Base(path), DescribeOptions{Layout: *layout})
	if *asJSON {
		return writeJSON(os.Stdout, d)
	}
	_, err = fmt.Print(d.Markdown(1))
	return err
}

type auditResult struct {
	Report   string      `json:"report"`
	Error    string      `json:"error,omitempty"`
	Counts   AuditCounts `json:"counts"`
	Findings []Finding   `json:"findings"`
}

func cmdAudit(args []string) error {
	fs := newFlags("audit", "<report.rpt|dir>... [flags]")
	asJSON := fs.Bool("json", false, "write JSON instead of Markdown")
	failOn := fs.String("fail-on", "", "exit 1 when a finding is at least this severe: error or warning")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 || (*failOn != "" && *failOn != "error" && *failOn != "warning") {
		fs.Usage()
		return exitCode(2)
	}
	files, err := findReports(pos)
	if err != nil {
		return err
	}
	var results []auditResult
	fail := false
	for _, f := range files {
		res := auditResult{Report: f.name, Findings: []Finding{}}
		r, ov, err := openReport(f.path, "")
		if err != nil {
			res.Error = err.Error()
			fail = fail || *failOn != ""
		} else {
			if fs := Audit(r, ov); fs != nil {
				res.Findings = fs
			}
			res.Counts = countFindings(res.Findings)
			fail = fail || (*failOn == "error" && res.Counts.Errors > 0) ||
				(*failOn == "warning" && res.Counts.Errors+res.Counts.Warnings > 0)
		}
		results = append(results, res)
	}
	if *asJSON {
		var out any = results
		if len(results) == 1 {
			out = results[0]
		}
		if err := writeJSON(os.Stdout, out); err != nil {
			return err
		}
	} else {
		fmt.Print(auditMarkdown(results))
	}
	if fail {
		return exitCode(1)
	}
	return nil
}

func auditMarkdown(results []auditResult) string {
	var b strings.Builder
	b.WriteString("# Audit\n\n")
	if len(results) > 1 {
		b.WriteString("| Report | Errors | Warnings | Info |\n| --- | --- | --- | --- |\n")
		for _, r := range results {
			if r.Error != "" {
				fmt.Fprintf(&b, "| %s | could not be read | | |\n", mdCell(r.Report))
				continue
			}
			fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", mdCell(r.Report), r.Counts.Errors, r.Counts.Warnings, r.Counts.Info)
		}
		b.WriteString("\n")
	}
	for _, r := range results {
		if len(results) > 1 && r.Error == "" && len(r.Findings) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", r.Report)
		if r.Error != "" {
			b.WriteString("Could not be read: " + r.Error + "\n\n")
			continue
		}
		if len(r.Findings) == 0 {
			b.WriteString("Nothing found: rptgo reproduces everything this report uses.\n\n")
			continue
		}
		for _, f := range r.Findings {
			b.WriteString("- " + f.line() + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func cmdIndex(args []string) error {
	fs := newFlags("index", "<dir|report.rpt>... [flags]")
	asJSON := fs.Bool("json", false, "write JSON (schema rptgo.index/v1) instead of Markdown")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fs.Usage()
		return exitCode(2)
	}
	files, err := findReports(pos)
	if err != nil {
		return err
	}
	ix := BuildIndex(files)
	if *asJSON {
		return writeJSON(os.Stdout, ix)
	}
	_, err = fmt.Print(ix.Markdown())
	return err
}

// dataFlags are the flags of the commands that run a report over rows.
type dataFlags struct {
	rpt, overlay, proc, data, printed, dumpRows *string
	params                                      multi
	subs, subData                               multi
}

func addDataFlags(fs *flag.FlagSet) *dataFlags {
	d := &dataFlags{
		rpt:      fs.String("rpt", "", "the report (or give it as the first argument): a .rpt, or its model saved as .json"),
		overlay:  fs.String("overlay", "", "overlay (.overlay.json); default: next to the report"),
		proc:     fs.String("proc", "", "stored procedure that returns the report's rows (needs $RPTGO_DSN)"),
		data:     fs.String("data", "", "the report's rows from a file instead: JSON array, JSON Lines (.jsonl) or CSV"),
		printed:  fs.String("printed", "", `print timestamp "2006-01-02 15:04:05" (default: now)`),
		dumpRows: fs.String("dump-rows", "", "also write the rows as JSON to this file (reuse them with -data)"),
	}
	fs.Var(&d.params, "param", "parameter Name=Value, for the procedure and the report's {?Name} (repeatable)")
	fs.Var(&d.subs, "sub", "a subreport's rows from a procedure, Name=procedure; it gets the -param values it declares (repeatable)")
	fs.Var(&d.subData, "sub-data", "a subreport's rows from a file, Name=file (repeatable); Name is the subreport's or its table's")
	return d
}

// engine loads the report and its rows and prepares the engine.
func (d *dataFlags) engine(path string) (*Engine, error) {
	r, ov, err := openReport(path, *d.overlay)
	if err != nil {
		return nil, err
	}
	var rows []Row
	switch {
	case *d.data != "":
		rows, err = readRows(*d.data, r)
	case *d.proc != "":
		dsn := os.Getenv("RPTGO_DSN")
		if dsn == "" {
			return nil, fmt.Errorf("RPTGO_DSN is not set")
		}
		rows, err = runProc(dsn, *d.proc, d.params)
	default:
		return nil, fmt.Errorf("give the rows: -proc (with $RPTGO_DSN) or -data")
	}
	if err != nil {
		return nil, err
	}
	if *d.dumpRows != "" {
		if err := dumpRows(*d.dumpRows, rows); err != nil {
			return nil, err
		}
	}
	subRows := map[string][]Row{}
	for _, spec := range append(append(multi{}, d.subs...), d.subData...) {
		name, src, ok := strings.Cut(spec, "=")
		if !ok {
			return nil, fmt.Errorf("subreport rows %q: want Name=procedure or Name=file", spec)
		}
		sub := r.subreportModel(name)
		if sub == nil {
			return nil, fmt.Errorf("the report has no subreport or subreport table %q", name)
		}
		var sr []Row
		if contains(d.subs, spec) {
			dsn := os.Getenv("RPTGO_DSN")
			if dsn == "" {
				return nil, fmt.Errorf("RPTGO_DSN is not set")
			}
			sr, err = runSubProc(dsn, src, d.params)
		} else {
			sr, err = readRows(src, sub)
		}
		if err != nil {
			return nil, fmt.Errorf("subreport %s: %w", name, err)
		}
		subRows[name] = sr
	}
	when := time.Now()
	if *d.printed != "" {
		if when, err = time.Parse("2006-01-02 15:04:05", *d.printed); err != nil {
			return nil, err
		}
	}
	eng, err := NewEngine(r, ov, rows, when)
	if err != nil {
		return nil, err
	}
	if err := eng.SetParams(d.params); err != nil {
		return nil, err
	}
	eng.SetSubreportRows(subRows)
	return eng, nil
}

func dumpRows(path string, rows []Row) error {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		m := map[string]any{}
		for k, v := range r {
			m[k] = jsonValue(v)
		}
		out[i] = m
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeJSON(f, out)
}

func cmdExtract(args []string) error {
	fs := newFlags("extract", "<report.rpt> (-proc P [-param N=V]... | -data FILE) [flags]")
	d := addDataFlags(fs)
	asJSON := fs.Bool("json", false, "write JSON (schema rptgo.extract/v1) instead of Markdown")
	maxRows := fs.Int("max-rows", -1, "keep at most this many detail rows; groups and totals stay complete (0: totals only, -1: all)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	path, err := oneReport(pos, *d.rpt, fs)
	if err != nil {
		return err
	}
	eng, err := d.engine(path)
	if err != nil {
		return err
	}
	x := eng.Extract(filepath.Base(path), d.params, *maxRows)
	if *asJSON {
		return writeJSON(os.Stdout, x)
	}
	_, err = fmt.Print(x.Markdown())
	return err
}

func cmdRender(args []string) error {
	fs := newFlags("render", "<report.rpt> (-proc P [-param N=V]... | -data FILE) -out FILE [flags]")
	d := addDataFlags(fs)
	out := fs.String("out", "", "output file (default: stdout)")
	format := fs.String("format", "", "pdf or html (default: from -out's extension, else html)")
	fontDir := fs.String("fonts", envOr("RPTGO_FONTS", "/usr/share/fonts/truetype/liberation"),
		"directory with the Liberation TrueType fonts, for -format pdf")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	path, err := oneReport(pos, *d.rpt, fs)
	if err != nil {
		return err
	}
	if *format == "" {
		*format = "html"
		if strings.EqualFold(filepath.Ext(*out), ".pdf") {
			*format = "pdf"
		}
	}
	if *format != "pdf" && *format != "html" {
		return fmt.Errorf("-format is pdf or html")
	}
	eng, err := d.engine(path)
	if err != nil {
		return err
	}
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var doc []byte
	if *format == "pdf" {
		if doc, err = eng.PDF(title, *fontDir); err != nil {
			return err
		}
	} else {
		doc = []byte(eng.Document(title))
	}
	fmt.Fprintf(os.Stderr, "%s: %d rows, %d pages\n", title, len(eng.rows), len(eng.pages))
	for _, k := range sortedKeys(eng.errs) {
		fmt.Fprintf(os.Stderr, "  warn: %s (x%d)\n", k, eng.errs[k])
	}
	if *out == "" {
		_, err = os.Stdout.Write(doc)
		return err
	}
	return os.WriteFile(*out, doc, 0o644)
}
