package main

// index catalogs a corpus of reports: one entry per report (title, data,
// groups, columns, sizes, audit counts) and the cross-references a
// migration needs: which reports use a table's field or a parameter, and
// which reports share the same logic under different layouts.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Index struct {
	Schema     string              `json:"schema"`
	Reports    []IndexEntry        `json:"reports"`
	Failed     int                 `json:"failed,omitempty"`
	Fields     map[string][]string `json:"fields"`               // {Table.Field} -> reports using it
	Parameters map[string][]string `json:"parameters,omitempty"` // parameter name -> reports
	SameLogic  [][]string          `json:"sameLogic,omitempty"`  // reports whose data and formulas match
}

type IndexEntry struct {
	Report     string      `json:"report"`
	Error      string      `json:"error,omitempty"`
	Title      string      `json:"title,omitempty"`
	Page       string      `json:"page,omitempty"`
	Sources    []string    `json:"sources,omitempty"`
	Fields     []string    `json:"fields,omitempty"`
	Parameters []string    `json:"parameters,omitempty"`
	Groups     []string    `json:"groups,omitempty"`
	Columns    []string    `json:"columns,omitempty"`
	Formulas   int         `json:"formulas"`
	Totals     int         `json:"runningTotals"`
	Subreports []string    `json:"subreports,omitempty"`
	Audit      AuditCounts `json:"audit"`
	Logic      string      `json:"logic,omitempty"` // hash of data, groups, formulas and totals
}

type AuditCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
}

func countFindings(fs []Finding) AuditCounts {
	var c AuditCounts
	for _, f := range fs {
		switch f.Severity {
		case "error":
			c.Errors++
		case "warning":
			c.Warnings++
		default:
			c.Info++
		}
	}
	return c
}

// reportFile is a .rpt to read, with the name it is listed under.
type reportFile struct{ path, name string }

// findReports expands files and directories (walked for .rpt files) into
// the reports to read; a directory's reports are named relative to it.
func findReports(args []string) ([]reportFile, error) {
	var out []reportFile
	for _, a := range args {
		st, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			out = append(out, reportFile{a, filepath.Base(a)})
			continue
		}
		err = filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".rpt") && !strings.HasPrefix(d.Name(), "._") {
				rel, _ := filepath.Rel(a, p)
				out = append(out, reportFile{p, filepath.ToSlash(rel)})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// BuildIndex reads every report and builds the catalog.
func BuildIndex(files []reportFile) *Index {
	ix := &Index{Schema: "rptgo.index/v1", Fields: map[string][]string{}, Parameters: map[string][]string{}}
	logic := map[string][]string{}
	for _, f := range files {
		e := IndexEntry{Report: f.name}
		r, err := loadReport(f.path)
		if err != nil {
			e.Error = err.Error()
			ix.Failed++
			ix.Reports = append(ix.Reports, e)
			continue
		}
		ov, err := loadOverlay(overlayPath(f.path))
		if err != nil {
			ov = &Overlay{}
		}
		d := Describe(r, ov, filepath.Base(f.path), DescribeOptions{})
		e.Title = d.Title
		e.Page = d.Page.Paper + " " + strings.ToLower(d.Page.Orientation)
		for _, s := range d.Data.Sources {
			src := s.Kind
			if s.Database != "" {
				src += " " + s.Database
			}
			if s.Server != "" {
				src += "@" + s.Server
			}
			e.Sources = append(e.Sources, src)
			for _, t := range s.Tables {
				for _, fl := range t.Fields {
					if fl.Used {
						ref := "{" + t.Alias + "." + fl.Name + "}"
						if t.Alias == "" {
							ref = "{" + t.Name + "." + fl.Name + "}"
						}
						e.Fields = append(e.Fields, ref)
						ix.Fields[ref] = append(ix.Fields[ref], f.name)
					}
				}
			}
		}
		for _, p := range d.Parameters {
			e.Parameters = append(e.Parameters, p.Name)
			ix.Parameters[p.Name] = append(ix.Parameters[p.Name], f.name)
		}
		for _, g := range d.Groups {
			e.Groups = append(e.Groups, g.Field)
		}
		for _, c := range d.Columns {
			e.Columns = append(e.Columns, c.Name)
		}
		e.Formulas, e.Totals = len(d.Formulas), len(d.Totals)
		for _, s := range d.Subreports {
			e.Subreports = append(e.Subreports, s.Name)
		}
		e.Audit = countFindings(Audit(r, ov))
		e.Logic = logicHash(d)
		logic[e.Logic] = append(logic[e.Logic], f.name)
		ix.Reports = append(ix.Reports, e)
	}
	for _, names := range logic {
		if len(names) > 1 {
			ix.SameLogic = append(ix.SameLogic, names)
		}
	}
	sort.Slice(ix.SameLogic, func(a, b int) bool { return ix.SameLogic[a][0] < ix.SameLogic[b][0] })
	return ix
}

// logicHash fingerprints what a report computes: its data, groups, sorts,
// formulas and running totals, not its layout or wording.
func logicHash(d *Description) string {
	var fields []string
	for _, s := range d.Data.Sources {
		for _, t := range s.Tables {
			for _, f := range t.Fields {
				if f.Used {
					fields = append(fields, strings.ToLower(t.Name+"."+f.Name))
				}
			}
		}
	}
	sort.Strings(fields)
	shape := struct {
		Fields   []string
		Groups   []GroupInfo
		Sorts    []SortInfo
		Formulas []FormulaInfo
		Totals   []TotalInfo
		Select   [2]string
	}{fields, d.Groups, d.Sorts, nil, nil, [2]string{d.Data.RecordSelection, d.Data.GroupSelection}}
	for _, f := range d.Formulas {
		shape.Formulas = append(shape.Formulas, FormulaInfo{Name: f.Name, Text: f.Text})
	}
	for _, t := range d.Totals {
		shape.Totals = append(shape.Totals, TotalInfo{Name: t.Name, Function: t.Function, Of: t.Of, Reset: t.Reset, Evaluate: t.Evaluate})
	}
	b, _ := json.Marshal(shape)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:6])
}

// Markdown writes the catalog table and the cross-references.
func (ix *Index) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Report index\n\n%s", plural(len(ix.Reports), "report"))
	if ix.Failed > 0 {
		fmt.Fprintf(&b, ", %d could not be read", ix.Failed)
	}
	b.WriteString(". Audit counts are errors/warnings/info (`rptgo audit <report>` lists them).\n\n")
	b.WriteString("| Report | Title | Data | Groups | Columns | Formulas | Totals | Audit |\n| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, e := range ix.Reports {
		if e.Error != "" {
			fmt.Fprintf(&b, "| %s | could not be read: %s | | | | | | |\n", mdCell(e.Report), mdCell(e.Error))
			continue
		}
		data := strings.Join(e.Sources, ", ")
		if len(e.Parameters) > 0 {
			data += "; params " + strings.Join(e.Parameters, ", ")
		}
		cols := strings.Join(e.Columns[:min(len(e.Columns), 12)], ", ")
		if len(e.Columns) > 12 {
			cols += fmt.Sprintf(" +%d more", len(e.Columns)-12)
		}
		if len(e.Subreports) > 0 {
			cols += "; subreports " + strings.Join(e.Subreports, ", ")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %d | %d/%d/%d |\n", mdCell(e.Report), mdCell(e.Title), mdCell(data),
			mdCell(strings.Join(e.Groups, ", ")), mdCell(cols), e.Formulas, e.Totals, e.Audit.Errors, e.Audit.Warnings, e.Audit.Info)
	}
	b.WriteString("\n")

	// Tables, with how many reports use each field.
	tables := map[string]map[string]int{}
	tableReports := map[string]map[string]bool{}
	for ref, reports := range ix.Fields {
		inner := strings.Trim(ref, "{}")
		i := strings.LastIndexByte(inner, '.')
		if i < 0 {
			continue
		}
		t, f := inner[:i], inner[i+1:]
		if tables[t] == nil {
			tables[t], tableReports[t] = map[string]int{}, map[string]bool{}
		}
		tables[t][f] = len(reports)
		for _, r := range reports {
			tableReports[t][r] = true
		}
	}
	if len(tables) > 0 {
		b.WriteString("## Tables\n\nFields the reports print or compute with, and how many reports use each (`rptgo index -json` lists the reports).\n\n")
		names := make([]string, 0, len(tables))
		for t := range tables {
			names = append(names, t)
		}
		sort.Slice(names, func(a, c int) bool {
			if len(tableReports[names[a]]) != len(tableReports[names[c]]) {
				return len(tableReports[names[a]]) > len(tableReports[names[c]])
			}
			return names[a] < names[c]
		})
		for _, t := range names {
			fs := make([]string, 0, len(tables[t]))
			for f := range tables[t] {
				fs = append(fs, f)
			}
			sort.Slice(fs, func(a, c int) bool {
				if tables[t][fs[a]] != tables[t][fs[c]] {
					return tables[t][fs[a]] > tables[t][fs[c]]
				}
				return strings.ToLower(fs[a]) < strings.ToLower(fs[c])
			})
			for i, f := range fs {
				fs[i] = fmt.Sprintf("%s (%d)", f, tables[t][f])
			}
			fmt.Fprintf(&b, "- `%s`, %s: %s\n", t, plural(len(tableReports[t]), "report"), strings.Join(fs, ", "))
		}
		b.WriteString("\n")
	}

	if len(ix.Parameters) > 0 {
		b.WriteString("## Parameters\n\n")
		names := make([]string, 0, len(ix.Parameters))
		for p := range ix.Parameters {
			names = append(names, p)
		}
		sort.Strings(names)
		for _, p := range names {
			fmt.Fprintf(&b, "- {?%s}: %s\n", p, strings.Join(ix.Parameters[p], ", "))
		}
		b.WriteString("\n")
	}

	if len(ix.SameLogic) > 0 {
		b.WriteString("## Same logic\n\nReports that read the same fields and compute the same groups, formulas and totals; they differ only in layout or wording.\n\n")
		for _, g := range ix.SameLogic {
			b.WriteString("- " + strings.Join(g, ", ") + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
