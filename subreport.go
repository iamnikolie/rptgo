package main

// Subreports: each runs as a report of its own over its own rows, printed
// where its object sits in the main report (render) or nested in the
// band's data (extract). An application feeds a subreport a table of its
// own, so rptgo takes those rows by the subreport's name or its table's:
// -sub Name=procedure or -sub-data Name=file. A linked subreport's
// parameters ("Pm-Table.Field") take the main report's values of those
// fields for the record it prints with; the rest share the main report's
// parameter values.

import (
	"fmt"
	"strings"
)

// SetSubreportRows gives the subreports' rows, by subreport name or table
// name (any case).
func (e *Engine) SetSubreportRows(rows map[string][]Row) {
	e.subRows = map[string][]Row{}
	for k, v := range rows {
		e.subRows[strings.ToLower(k)] = v
	}
}

// subRowsFor finds o's rows: by its name, else by a table it reads.
func (e *Engine) subRowsFor(o Object) ([]Row, bool) {
	rows := e.root().subRows
	if r, ok := rows[strings.ToLower(o.SubreportName)]; ok {
		return r, true
	}
	if o.Report != nil {
		for _, s := range o.Report.DataSources {
			for _, t := range s.Tables {
				for _, name := range []string{t.Name, t.Alias} {
					if r, ok := rows[strings.ToLower(name)]; ok {
						return r, true
					}
				}
			}
		}
	}
	return nil, false
}

// subreport prepares o's report for the current record: its rows, the
// main report's parameter values and the linked fields' values. nil when
// no rows were given for it (a warning says so).
func (e *Engine) subreport(o Object) *Engine {
	rows, ok := e.subRowsFor(o)
	if !ok {
		e.root().errs[fmt.Sprintf("subreport %q not printed: give its rows with -sub %s=PROCEDURE or -sub-data %s=FILE",
			o.SubreportName, o.SubreportName, o.SubreportName)]++
		return nil
	}
	sub, err := NewEngine(o.Report, &Overlay{}, append([]Row(nil), rows...), e.printed)
	if err != nil {
		e.root().errs[fmt.Sprintf("subreport %q: %v", o.SubreportName, err)]++
		return nil
	}
	for k, n := range sub.errs { // the subreport's own warnings so far
		e.root().errs[fmt.Sprintf("subreport %q: %s", o.SubreportName, k)] += n
	}
	sub.host, sub.pager, sub.errs = e.root(), e.pager, e.root().errs
	for k, v := range e.params {
		sub.params[k] = v
	}
	for _, f := range o.Report.Fields {
		linked, ok := strings.CutPrefix(f.Name, "Pm-")
		if f.Type != "ParameterField" || !ok {
			continue
		}
		v, err := e.Lookup(linked)
		if err != nil {
			e.root().errs[fmt.Sprintf("subreport %q link %s: %v", o.SubreportName, linked, err)]++
		}
		sub.params[strings.ToLower(f.Name)] = v
	}
	return sub
}

// subreportModel is the report of the subreport named name, or reading a
// table named name.
func (r *Report) subreportModel(name string) *Report {
	for _, s := range r.Sections {
		for _, o := range s.Objects {
			if o.Report == nil {
				continue
			}
			if strings.EqualFold(o.SubreportName, name) {
				return o.Report
			}
			for _, ds := range o.Report.DataSources {
				for _, t := range ds.Tables {
					if strings.EqualFold(t.Name, name) || strings.EqualFold(t.Alias, name) {
						return o.Report
					}
				}
			}
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
