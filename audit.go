package main

// audit lists what of a report rptgo does not reproduce, or reproduces only
// in part, and what in it is dead: formulas the interpreter cannot parse or
// that call functions it lacks, references to fields the report does not
// have, objects that are parsed but not drawn, selection formulas rptgo
// leaves to the data's source, unused formulas, totals and parameters.

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one thing the audit found. Severity: "error" (rptgo's output
// will be wrong or missing there), "warning" (it may differ from Crystal's),
// "info" (worth knowing, output unaffected).
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Where    string `json:"where"`
	Message  string `json:"message"`
}

func (f Finding) line() string {
	return fmt.Sprintf("%s [%s] %s: %s", f.Severity, f.Code, f.Where, f.Message)
}

// knownNames are identifiers the engine evaluates outside a formula's own
// variables.
var knownNames = map[string]bool{
	"true": true, "false": true, "pagenumber": true, "printdate": true, "currentdate": true,
	"printtime": true, "currenttime": true, "datadate": true, "datatime": true, "recordnumber": true,
	"onfirstrecord": true, "onlastrecord": true, "formula": true, "crlf": true,
}

// Audit checks r; findings come errors first, then by place.
func Audit(r *Report, ov *Overlay) []Finding {
	var out []Finding
	add := func(sev, code, where, msg string, args ...any) {
		out = append(out, Finding{Severity: sev, Code: code, Where: where, Message: fmt.Sprintf(msg, args...)})
	}
	used := r.usage(ov)

	// Formulas: parse, functions, names, references.
	checkFormula := func(where, src, resultType string) {
		if strings.TrimSpace(src) == "" {
			return
		}
		n, err := parseAny(src, resultType)
		if err != nil {
			add("error", "formula-syntax", where, "rptgo cannot parse it (%v); it prints blank", err)
			return
		}
		w := walkFormula(n)
		for _, fn := range w.calls {
			if !builtinFuncs[fn] {
				add("error", "function-unsupported", where, "calls %s(), which rptgo does not implement", fn)
			}
		}
		for _, id := range w.idents {
			if !knownNames[strings.ToLower(id)] && !w.assigned[strings.ToLower(id)] {
				add("warning", "name-unknown", where, "uses %s, which rptgo evaluates as a column of the row", id)
			}
		}
		for _, ref := range w.refs {
			if !r.known(ref) {
				add("warning", "reference-unknown", where, "refers to {%s}, which the report does not define", ref)
			}
		}
	}
	for _, f := range r.Fields {
		switch f.Type {
		case "FormulaField":
			checkFormula("{@"+f.Name+"}", f.FormulaText, f.ResultType)
		case "RunningTotalField":
			for _, c := range []*string{f.ResetCondition, f.EvaluateCondition} {
				if c == nil {
					continue
				}
				if src, ok := strings.CutPrefix(*c, "Formula:"); ok {
					checkFormula("{#"+f.Name+"} condition", src, "Boolean")
				}
			}
			if !aggregates[f.Function] {
				add("error", "summary-function", "{#"+f.Name+"}", "%s is not computed; it prints blank", f.Function)
			}
			if f.SummarizedFieldName != "" && !r.known(strings.Trim(display(r, r.refOf(f.SummarizedFieldName)), "{}")) {
				add("warning", "reference-unknown", "{#"+f.Name+"}", "totals %s, which the report does not define", f.SummarizedFieldName)
			}
		}
	}

	// Sections.
	for _, i := range r.designOrder() {
		s := r.Sections[i]
		name := r.sectionName(i)
		checkFormula(name+" suppress", deref(s.SuppressFormula), "Boolean")
		checkFormula(name+" new page before", deref(s.NewPageBeforeFormula), "Boolean")
		checkFormula(name+" new page after", deref(s.NewPageAfterFormula), "Boolean")
		if s.Suppress {
			continue
		}
		for _, o := range s.Objects {
			if o.Format.Suppress {
				continue
			}
			where := name + " " + o.Name
			switch {
			case strings.Contains(o.Type, "Subreport"):
				add("error", "subreport", where, "subreport %q is not rendered or extracted", o.SubreportName)
			case strings.Contains(o.Type, "Chart"):
				add("error", "chart", where, "chart %q is not rendered", o.Title)
			case strings.Contains(o.Type, "CrossTab"):
				add("error", "crosstab", where, "cross-tab is not rendered")
			case strings.Contains(o.Type, "Image"):
				add("warning", "picture", where, "picture is not drawn")
			case isField(o):
				ref := r.binding(o, ov)
				if o.SummaryFunction != nil {
					if !aggregates[*o.SummaryFunction] {
						add("error", "summary-function", where, "%s is not computed; it prints blank", display(r, ref))
					}
					if s.Type != "GroupFooter" && s.Type != "ReportFooter" {
						add("error", "summary-placement", where, "%s in a %s needs the group's records ahead; rptgo computes summaries only in footers", display(r, ref), strings.ToLower(name))
					}
					continue
				}
				switch ref {
				case "PageNofM", "PageNumber", "PrintDate", "PrintTime", "RecordNumber", "DataDate", "DataTime":
					continue
				}
				if !r.known(ref) {
					if strings.Contains(o.FieldName, " ") {
						add("error", "special-field", where, "special field %q is not supported; it prints blank", o.FieldName)
					} else {
						add("warning", "reference-unknown", where, "prints %s, which the report does not define (taken from the row's column)", display(r, ref))
					}
				}
			}
			if len(o.Format.Conditions) > 0 {
				add("info", "conditional-format", where, "conditional colours are not applied")
			}
		}
	}

	// Data, groups and selection.
	if r.RecordSelectionFormula != nil && strings.TrimSpace(*r.RecordSelectionFormula) != "" {
		add("warning", "record-selection", "report", "the record selection formula is not applied; rptgo prints every row it is given")
	}
	if r.GroupSelectionFormula != nil && strings.TrimSpace(*r.GroupSelectionFormula) != "" {
		add("error", "group-selection", "report", "the group selection formula is not applied; groups it would drop still print")
	}
	for i, g := range r.Groups {
		where := fmt.Sprintf("group #%d", i+1)
		if g.TopN != nil {
			add("error", "group-topn", where, "Top N / sort by summary is not applied")
		}
		if g.SortOrder == "Specified" {
			add("warning", "group-specified-order", where, "specified order is not applied; groups keep the data's order")
		}
	}
	for _, s := range r.DataSources {
		if deref(s.SqlQuery) != "" || deref(s.ServerName) != "" || deref(s.OdbcDsn) != "" {
			add("info", "own-data", "data source "+s.Name, "the report reads its own data; rptgo needs those rows from a stored procedure or a file")
		}
	}

	// Dead parts.
	for _, f := range r.Fields {
		var ref string
		switch f.Type {
		case "FormulaField":
			if strings.TrimSpace(f.FormulaText) == "" {
				continue
			}
			ref = "{@" + f.Name + "}"
		case "RunningTotalField":
			ref = "{#" + f.Name + "}"
		case "ParameterField":
			ref = "{?" + f.Name + "}"
		default:
			continue
		}
		if len(used[ref]) == 0 {
			add("info", "unused", ref, "defined but not printed or used")
		}
	}

	rank := map[string]int{"error": 0, "warning": 1, "info": 2}
	sort.SliceStable(out, func(a, b int) bool { return rank[out[a].Severity] < rank[out[b].Severity] })
	return dedupe(out)
}

func dedupe(fs []Finding) []Finding {
	seen := map[Finding]bool{}
	var out []Finding
	for _, f := range fs {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// known: a reference names something the report defines, or a column a
// database field of the report has.
func (r *Report) known(ref string) bool {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "#summary:"):
		return true
	case strings.HasPrefix(ref, "@"), strings.HasPrefix(ref, "#"), strings.HasPrefix(ref, "?"):
		return r.fieldKind(ref[:1], ref[1:]) != nil
	}
	table, col := "", ref
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		table, col = ref[:i], ref[i+1:]
	}
	for _, f := range r.Fields {
		if f.Type == "DatabaseField" && strings.EqualFold(f.Name, col) && (table == "" || strings.EqualFold(f.TableName, table)) {
			return true
		}
	}
	for _, s := range r.DataSources {
		for _, t := range s.Tables {
			if table != "" && !strings.EqualFold(t.Alias, table) && !strings.EqualFold(t.Name, table) {
				continue
			}
			for _, c := range t.Columns {
				if strings.EqualFold(c.Name, col) {
					return true
				}
			}
		}
	}
	return false
}

// formulaWalk is what a parsed formula calls and names.
type formulaWalk struct {
	calls    []string
	idents   []string
	refs     []string
	assigned map[string]bool
}

func walkFormula(n node) formulaWalk {
	w := formulaWalk{assigned: map[string]bool{}}
	seen := map[string]bool{}
	note := func(list *[]string, kind, s string) {
		if !seen[kind+s] {
			seen[kind+s] = true
			*list = append(*list, s)
		}
	}
	var walk func(n node)
	walk = func(n node) {
		switch n := n.(type) {
		case fieldRef:
			note(&w.refs, "ref", n.ref)
		case ident:
			note(&w.idents, "id", n.name)
		case unary:
			walk(n.x)
		case binary:
			walk(n.l)
			walk(n.r)
		case ifExpr:
			walk(n.cond)
			walk(n.then)
			walk(n.els)
		case call:
			note(&w.calls, "fn", n.fn)
			for _, a := range n.args {
				walk(a)
			}
		case seq:
			for _, s := range n.stmts {
				walk(s)
			}
		case assign:
			w.assigned[strings.ToLower(n.name)] = true
			walk(n.x)
		case declare:
			w.assigned[strings.ToLower(n.name)] = true
		case basicProgram:
			walk(n.body)
		case basicIf:
			for _, c := range n.conds {
				walk(c)
			}
			for _, b := range n.bodies {
				walk(b)
			}
			walk(n.els)
		case basicSelect:
			walk(n.subject)
			for _, vs := range n.cases {
				for _, v := range vs {
					walk(v)
				}
			}
			for _, b := range n.bodies {
				walk(b)
			}
		}
	}
	walk(n)
	return w
}
