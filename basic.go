package main

// Crystal's second formula language, Basic syntax: line-based statements,
// the result is whatever was last assigned to "formula". The parser keeps
// no syntax flag, so a formula that assigns "formula = ..." on a line of its
// own is read as Basic. Statements: Dim, assignment, If/ElseIf/Else/End If
// (and the one-line If), Select Case/Case/Case Else/End Select; comments are
// Rem and '. Expressions are the Crystal-syntax ones.

import (
	"fmt"
	"regexp"
	"strings"
)

var basicFormula = regexp.MustCompile(`(?i)(^|\s)formula\s*=`)

func isBasic(src string) bool { return basicFormula.MatchString(src) }

type (
	// basicProgram runs body with "formula" set to the zero of the result
	// type, then yields "formula".
	basicProgram struct {
		body node
		zero any
	}
	// basicIf is a statement: no branch taken leaves everything as it was.
	basicIf struct {
		conds  []node
		bodies []node
		els    node
	}
	basicSelect struct {
		subject node
		cases   [][]node // values per Case; nil for Case Else
		bodies  []node
	}
)

func parseBasic(src string, resultType string) (node, error) {
	b := &basicParser{lines: basicLines(src)}
	body, err := b.block()
	if err != nil {
		return nil, err
	}
	if b.pos < len(b.lines) {
		return nil, fmt.Errorf("unexpected %q", b.lines[b.pos])
	}
	return basicProgram{body: body, zero: zeroOfType(resultType)}, nil
}

func zeroOfType(t string) any {
	switch strings.ToLower(t) {
	case "string":
		return ""
	case "boolean":
		return false
	case "date", "datetime", "time":
		return nil
	}
	return 0.0
}

// basicLines: the statements, one per line, comments and blank lines gone.
func basicLines(src string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(src, "\r", ""), "\n") {
		l = strings.TrimSpace(stripBasicComment(l))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// stripBasicComment cuts a ' comment (outside a string) or a Rem line.
func stripBasicComment(l string) string {
	t := strings.TrimSpace(l)
	if strings.EqualFold(t, "rem") || len(t) > 3 && strings.EqualFold(t[:4], "rem ") {
		return ""
	}
	inStr := false
	for i, c := range l {
		switch {
		case c == '"':
			inStr = !inStr
		case c == '\'' && !inStr:
			return l[:i]
		}
	}
	return l
}

type basicParser struct {
	lines []string
	pos   int
}

// kw: the line starts with the keyword phrase (case-insensitive, whole words).
func kw(line, phrase string) bool {
	f, p := strings.Fields(strings.ToLower(line)), strings.Fields(phrase)
	if len(f) < len(p) {
		return false
	}
	for i := range p {
		if f[i] != p[i] {
			return false
		}
	}
	return true
}

// block reads statements up to a line that closes or continues the enclosing
// If or Select (left for the caller).
func (b *basicParser) block() (node, error) {
	var stmts []node
	for b.pos < len(b.lines) {
		l := b.lines[b.pos]
		if kw(l, "end if") || kw(l, "else") || kw(l, "elseif") || kw(l, "end select") || kw(l, "case") {
			break
		}
		s, err := b.statement()
		if err != nil {
			return nil, err
		}
		if s != nil {
			stmts = append(stmts, s)
		}
	}
	return seq{stmts}, nil
}

func (b *basicParser) statement() (node, error) {
	l := b.lines[b.pos]
	b.pos++
	switch {
	case kw(l, "dim"):
		name := strings.Fields(l)[1]
		typ := ""
		if f := strings.Fields(l); len(f) >= 4 && strings.EqualFold(f[2], "as") {
			typ = f[3]
		}
		return assign{name, constant{zeroOfType(typ)}}, nil
	case kw(l, "if"):
		return b.ifStatement(l)
	case kw(l, "select case"):
		return b.selectStatement(l)
	}
	if i := assignAt(l); i > 0 {
		x, err := basicExpr(l[i+1:])
		if err != nil {
			return nil, err
		}
		return assign{strings.TrimSpace(l[:i]), x}, nil
	}
	return basicExpr(l)
}

// assignAt: the index of "=" in "name = expr", or -1.
var assignment = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*\s*=`)

func assignAt(l string) int {
	if m := assignment.FindStringIndex(l); m != nil {
		return m[1] - 1
	}
	return -1
}

func (b *basicParser) ifStatement(l string) (node, error) {
	cond, rest, err := splitThen(l[2:])
	if err != nil {
		return nil, err
	}
	if rest != "" { // one-line If c Then stmt [Else stmt]
		thenSrc, elseSrc := rest, ""
		if i := indexWordOutsideStrings(rest, "else"); i >= 0 {
			thenSrc, elseSrc = rest[:i], rest[i+4:]
		}
		th, err := (&basicParser{lines: []string{strings.TrimSpace(thenSrc)}}).statement()
		if err != nil {
			return nil, err
		}
		n := basicIf{conds: []node{cond}, bodies: []node{th}}
		if strings.TrimSpace(elseSrc) != "" {
			if n.els, err = (&basicParser{lines: []string{strings.TrimSpace(elseSrc)}}).statement(); err != nil {
				return nil, err
			}
		}
		return n, nil
	}
	n := basicIf{conds: []node{cond}}
	for {
		body, err := b.block()
		if err != nil {
			return nil, err
		}
		if b.pos >= len(b.lines) {
			return nil, fmt.Errorf("If without End If")
		}
		l := b.lines[b.pos]
		b.pos++
		switch {
		case kw(l, "end if"):
			if len(n.bodies) < len(n.conds) {
				n.bodies = append(n.bodies, body)
			} else {
				n.els = body
			}
			return n, nil
		case kw(l, "elseif"):
			n.bodies = append(n.bodies, body)
			c, _, err := splitThen(strings.TrimSpace(l)[6:])
			if err != nil {
				return nil, err
			}
			n.conds = append(n.conds, c)
		case kw(l, "else"):
			n.bodies = append(n.bodies, body)
		default:
			return nil, fmt.Errorf("unexpected %q in If", l)
		}
	}
}

// splitThen parses "cond Then [rest]".
func splitThen(s string) (node, string, error) {
	i := indexWordOutsideStrings(s, "then")
	if i < 0 {
		return nil, "", fmt.Errorf("expected Then in %q", s)
	}
	cond, err := basicExpr(s[:i])
	return cond, strings.TrimSpace(s[i+4:]), err
}

func indexWordOutsideStrings(s, word string) int {
	inStr := false
	low := strings.ToLower(s)
	for i := 0; i+len(word) <= len(s); i++ {
		if s[i] == '"' {
			inStr = !inStr
		}
		if inStr || low[i:i+len(word)] != word {
			continue
		}
		before := i == 0 || !isWordChar(s[i-1])
		after := i+len(word) == len(s) || !isWordChar(s[i+len(word)])
		if before && after {
			return i
		}
	}
	return -1
}

func isWordChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func (b *basicParser) selectStatement(l string) (node, error) {
	subject, err := basicExpr(strings.TrimSpace(l)[len("select case"):])
	if err != nil {
		return nil, err
	}
	n := basicSelect{subject: subject}
	if _, err := b.block(); err != nil { // nothing belongs before the first Case
		return nil, err
	}
	for b.pos < len(b.lines) {
		l := b.lines[b.pos]
		b.pos++
		if kw(l, "end select") {
			return n, nil
		}
		if !kw(l, "case") {
			return nil, fmt.Errorf("unexpected %q in Select", l)
		}
		var values []node
		if !kw(l, "case else") {
			for _, v := range splitOutsideStrings(strings.TrimSpace(l)[4:], ',') {
				x, err := basicExpr(v)
				if err != nil {
					return nil, err
				}
				values = append(values, x)
			}
		}
		body, err := b.block()
		if err != nil {
			return nil, err
		}
		n.cases = append(n.cases, values)
		n.bodies = append(n.bodies, body)
	}
	return nil, fmt.Errorf("Select Case without End Select")
}

func splitOutsideStrings(s string, sep byte) []string {
	var out []string
	inStr, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			inStr = !inStr
		case s[i] == sep && !inStr:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// basicExpr parses one expression with the Crystal-syntax grammar; Basic's
// "=" compares there too.
func basicExpr(src string) (node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	x, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected %q in %q", p.peek().text, src)
	}
	return x, nil
}

type constant struct{ v any }

func (e *evaluator) evalBasic(n node) (any, error) {
	switch n := n.(type) {
	case constant:
		return n.v, nil
	case basicProgram:
		e.vars["formula"] = n.zero
		if _, err := e.eval(n.body); err != nil {
			return nil, err
		}
		return e.vars["formula"], nil
	case basicIf:
		for i, c := range n.conds {
			v, err := e.eval(c)
			if err != nil {
				return nil, err
			}
			if truthy(v) {
				return e.eval(n.bodies[i])
			}
		}
		if n.els != nil {
			return e.eval(n.els)
		}
		return nil, nil
	case basicSelect:
		subject, err := e.eval(n.subject)
		if err != nil {
			return nil, err
		}
		for i, values := range n.cases {
			match := values == nil
			for _, x := range values {
				v, err := e.eval(x)
				if err != nil {
					return nil, err
				}
				if compare(subject, v) == 0 {
					match = true
					break
				}
			}
			if match {
				return e.eval(n.bodies[i])
			}
		}
		return nil, nil
	}
	return nil, fmt.Errorf("cannot evaluate %T", n)
}
