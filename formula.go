package main

// A Crystal-syntax formula interpreter: the subset common business reports use.
// Values are float64 (numbers and currency), string, time.Time, bool or nil.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ---- lexer ----

type tokKind int

const (
	tEOF tokKind = iota
	tNum
	tStr
	tIdent
	tField // {Table.Field}, {@Formula}, {#RunTot}, {?Param}
	tOp
	tDate // #2024-03-31#
)

type token struct {
	kind tokKind
	text string
	num  float64
}

func lex(src string) ([]token, error) {
	var out []token
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '/' && i+1 < len(rs) && rs[i+1] == '/':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case c == '{':
			j := i + 1
			for j < len(rs) && rs[j] != '}' {
				j++
			}
			if j == len(rs) {
				return nil, fmt.Errorf("unclosed {")
			}
			out = append(out, token{kind: tField, text: string(rs[i+1 : j])})
			i = j + 1
		case c == '"' || c == '\'':
			j := i + 1
			var sb strings.Builder
			for j < len(rs) {
				if rs[j] == c {
					if j+1 < len(rs) && rs[j+1] == c { // doubled quote
						sb.WriteRune(c)
						j += 2
						continue
					}
					break
				}
				sb.WriteRune(rs[j])
				j++
			}
			out = append(out, token{kind: tStr, text: sb.String()})
			i = j + 1
		case c == '#':
			j := i + 1
			for j < len(rs) && rs[j] != '#' {
				j++
			}
			out = append(out, token{kind: tDate, text: string(rs[i+1 : j])})
			i = j + 1
		case unicode.IsDigit(c) || (c == '.' && i+1 < len(rs) && unicode.IsDigit(rs[i+1])):
			j := i
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.') {
				j++
			}
			n, err := strconv.ParseFloat(string(rs[i:j]), 64)
			if err != nil {
				return nil, err
			}
			out = append(out, token{kind: tNum, num: n, text: string(rs[i:j])})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_') {
				j++
			}
			out = append(out, token{kind: tIdent, text: string(rs[i:j])})
			i = j
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "<>", "<=", ">=", ":=":
				out = append(out, token{kind: tOp, text: two})
				i += 2
				continue
			}
			if strings.ContainsRune("+-*/^%&=<>(),;[]:", c) {
				out = append(out, token{kind: tOp, text: string(c)})
				i++
				continue
			}
			return nil, fmt.Errorf("unexpected %q", c)
		}
	}
	return append(out, token{kind: tEOF}), nil
}

// ---- AST ----

type node interface{}

type (
	numLit   struct{ v float64 }
	strLit   struct{ v string }
	dateLit  struct{ v time.Time }
	fieldRef struct{ ref string }
	ident    struct{ name string }
	unary    struct {
		op string
		x  node
	}
	binary struct {
		op   string
		l, r node
	}
	ifExpr struct{ cond, then, els node }
	call   struct {
		fn   string
		args []node
	}
	seq    struct{ stmts []node }
	assign struct {
		name string
		x    node
	}
	declare struct{ name string }
)

// ---- parser ----

type parser struct {
	toks []token
	pos  int
}

func parseFormula(src string) (node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.statements()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected %q", p.peek().text)
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) isKw(kw string) bool {
	t := p.peek()
	return t.kind == tIdent && strings.EqualFold(t.text, kw)
}

func (p *parser) isOp(op string) bool {
	t := p.peek()
	return t.kind == tOp && t.text == op
}

func (p *parser) expectOp(op string) error {
	if !p.isOp(op) {
		return fmt.Errorf("expected %q, got %q", op, p.peek().text)
	}
	p.next()
	return nil
}

func (p *parser) statements() (node, error) {
	var stmts []node
	for {
		for p.isOp(";") {
			p.next()
		}
		if p.peek().kind == tEOF {
			break
		}
		s, err := p.statement()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s)
		if !p.isOp(";") {
			break
		}
	}
	if len(stmts) == 1 {
		return stmts[0], nil
	}
	return seq{stmts}, nil
}

var varKinds = map[string]bool{"numbervar": true, "stringvar": true, "datevar": true,
	"currencyvar": true, "booleanvar": true, "datetimevar": true}

func (p *parser) statement() (node, error) {
	// Local/Global/Shared NumberVar x [:= expr]
	if p.isKw("local") || p.isKw("global") || p.isKw("shared") {
		p.next()
	}
	if t := p.peek(); t.kind == tIdent && varKinds[strings.ToLower(t.text)] {
		p.next()
		name := p.next().text
		if p.isOp(":=") {
			p.next()
			x, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			return assign{name, x}, nil
		}
		return declare{name}, nil
	}
	if t := p.peek(); t.kind == tIdent && p.toks[p.pos+1].kind == tOp && p.toks[p.pos+1].text == ":=" {
		p.next()
		p.next()
		x, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		return assign{t.text, x}, nil
	}
	return p.expr(0)
}

var prec = map[string]int{
	"or": 1, "and": 2,
	"=": 4, "<>": 4, "<": 4, ">": 4, "<=": 4, ">=": 4,
	"&": 5, "+": 6, "-": 6, "*": 7, "/": 7, "%": 7, "mod": 7, "^": 8,
}

func (p *parser) binop() (string, bool) {
	t := p.peek()
	if t.kind == tOp {
		_, ok := prec[t.text]
		return t.text, ok
	}
	if t.kind == tIdent {
		l := strings.ToLower(t.text)
		if l == "and" || l == "or" || l == "mod" {
			return l, true
		}
	}
	return "", false
}

func (p *parser) expr(minPrec int) (node, error) {
	l, err := p.unaryExpr()
	if err != nil {
		return nil, err
	}
	for {
		op, ok := p.binop()
		if !ok || prec[op] < minPrec {
			return l, nil
		}
		p.next()
		next := prec[op] + 1
		if op == "^" {
			next = prec[op]
		}
		r, err := p.expr(next)
		if err != nil {
			return nil, err
		}
		l = binary{op, l, r}
	}
}

func (p *parser) unaryExpr() (node, error) {
	if p.isOp("-") || p.isOp("+") {
		op := p.next().text
		x, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return unary{op, x}, nil
	}
	if p.isKw("not") {
		p.next()
		x, err := p.expr(3)
		if err != nil {
			return nil, err
		}
		return unary{"not", x}, nil
	}
	return p.primary()
}

func (p *parser) primary() (node, error) {
	t := p.next()
	switch t.kind {
	case tNum:
		return numLit{t.num}, nil
	case tStr:
		return strLit{t.text}, nil
	case tDate:
		d, err := parseLooseDate(t.text)
		if err != nil {
			return nil, err
		}
		return dateLit{d}, nil
	case tField:
		return fieldRef{t.text}, nil
	case tOp:
		if t.text == "(" {
			x, err := p.statements()
			if err != nil {
				return nil, err
			}
			return x, p.expectOp(")")
		}
	case tIdent:
		switch strings.ToLower(t.text) {
		case "if":
			cond, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			if !p.isKw("then") {
				return nil, fmt.Errorf("expected then")
			}
			p.next()
			th, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			var el node
			if p.isKw("else") {
				p.next()
				el, err = p.expr(0)
				if err != nil {
					return nil, err
				}
			}
			return ifExpr{cond, th, el}, nil
		case "select":
			return p.selectExpr()
		case "true":
			return ident{"true"}, nil
		case "false":
			return ident{"false"}, nil
		}
		if p.isOp("(") {
			p.next()
			var args []node
			for !p.isOp(")") {
				a, err := p.expr(0)
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if p.isOp(",") {
					p.next()
				}
			}
			p.next()
			return call{strings.ToLower(t.text), args}, nil
		}
		return ident{t.text}, nil
	}
	return nil, fmt.Errorf("unexpected %q", t.text)
}

// selectExpr: Select x Case v1, v2: expr Case ...: expr [Default: expr],
// read as a chain of ifs comparing x with each value.
func (p *parser) selectExpr() (node, error) {
	subject, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	type arm struct {
		values []node
		then   node
	}
	var arms []arm
	var def node
	for p.isKw("case") {
		p.next()
		var a arm
		for {
			v, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			a.values = append(a.values, v)
			if !p.isOp(",") {
				break
			}
			p.next()
		}
		if err := p.expectOp(":"); err != nil {
			return nil, err
		}
		if a.then, err = p.expr(0); err != nil {
			return nil, err
		}
		arms = append(arms, a)
	}
	if p.isKw("default") {
		p.next()
		if err := p.expectOp(":"); err != nil {
			return nil, err
		}
		if def, err = p.expr(0); err != nil {
			return nil, err
		}
	}
	if len(arms) == 0 {
		return nil, fmt.Errorf("select without case")
	}
	out := def
	for i := len(arms) - 1; i >= 0; i-- {
		var cond node
		for _, v := range arms[i].values {
			eq := binary{"=", subject, v}
			if cond == nil {
				cond = eq
			} else {
				cond = binary{"or", cond, eq}
			}
		}
		out = ifExpr{cond, arms[i].then, out}
	}
	return out, nil
}

// ---- evaluation ----

// Env supplies field values to a formula: the current row, other formulas,
// running totals, report variables.
type Env interface {
	Lookup(ref string) (any, error)
}

type evaluator struct {
	env  Env
	vars map[string]any
}

func eval(n node, env Env, vars map[string]any) (any, error) {
	e := &evaluator{env: env, vars: vars}
	return e.eval(n)
}

func (e *evaluator) eval(n node) (any, error) {
	switch n := n.(type) {
	case numLit:
		return n.v, nil
	case strLit:
		return n.v, nil
	case dateLit:
		return n.v, nil
	case fieldRef:
		return e.env.Lookup(n.ref)
	case ident:
		switch n.name {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		if v, ok := e.vars[strings.ToLower(n.name)]; ok {
			return v, nil
		}
		return e.env.Lookup(n.name)
	case declare:
		if _, ok := e.vars[strings.ToLower(n.name)]; !ok {
			e.vars[strings.ToLower(n.name)] = nil
		}
		return nil, nil
	case assign:
		v, err := e.eval(n.x)
		if err != nil {
			return nil, err
		}
		e.vars[strings.ToLower(n.name)] = v
		return v, nil
	case seq:
		var last any
		for _, s := range n.stmts {
			v, err := e.eval(s)
			if err != nil {
				return nil, err
			}
			last = v
		}
		return last, nil
	case unary:
		v, err := e.eval(n.x)
		if err != nil {
			return nil, err
		}
		switch n.op {
		case "-":
			return -num(v), nil
		case "+":
			return num(v), nil
		case "not":
			return !truthy(v), nil
		}
	case binary:
		return e.binary(n)
	case ifExpr:
		c, err := e.eval(n.cond)
		if err != nil {
			return nil, err
		}
		if truthy(c) {
			return e.eval(n.then)
		}
		if n.els != nil {
			return e.eval(n.els)
		}
		// Crystal: a missing else yields the default of the then-branch type.
		th, err := e.eval(n.then)
		if err != nil {
			return nil, err
		}
		return zeroOf(th), nil
	case call:
		return e.call(n)
	case constant, basicProgram, basicIf, basicSelect:
		return e.evalBasic(n)
	}
	return nil, fmt.Errorf("cannot evaluate %T", n)
}

func (e *evaluator) binary(n binary) (any, error) {
	l, err := e.eval(n.l)
	if err != nil {
		return nil, err
	}
	if n.op == "and" && !truthy(l) {
		return false, nil
	}
	if n.op == "or" && truthy(l) {
		return true, nil
	}
	r, err := e.eval(n.r)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "and", "or":
		return truthy(r), nil
	case "&":
		return toText(l) + toText(r), nil
	case "+":
		if ls, ok := l.(string); ok {
			return ls + toText(r), nil
		}
		if lt, ok := l.(time.Time); ok {
			return lt.AddDate(0, 0, int(num(r))), nil
		}
		return num(l) + num(r), nil
	case "-":
		if lt, ok := l.(time.Time); ok {
			if rt, ok := r.(time.Time); ok {
				return math.Round(dateOnly(lt).Sub(dateOnly(rt)).Hours() / 24), nil
			}
			return lt.AddDate(0, 0, -int(num(r))), nil
		}
		return num(l) - num(r), nil
	case "*":
		return num(l) * num(r), nil
	case "/":
		if num(r) == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return num(l) / num(r), nil
	case "%":
		return num(l) / 100 * num(r), nil
	case "mod":
		return math.Mod(num(l), num(r)), nil
	case "^":
		return math.Pow(num(l), num(r)), nil
	case "=", "<>", "<", ">", "<=", ">=":
		c := compare(l, r)
		switch n.op {
		case "=":
			return c == 0, nil
		case "<>":
			return c != 0, nil
		case "<":
			return c < 0, nil
		case ">":
			return c > 0, nil
		case "<=":
			return c <= 0, nil
		case ">=":
			return c >= 0, nil
		}
	}
	return nil, fmt.Errorf("operator %s", n.op)
}

func (e *evaluator) call(n call) (any, error) {
	args := make([]any, len(n.args))
	for i, a := range n.args {
		if n.fn == "iif" && i > 0 {
			continue // evaluated lazily below
		}
		v, err := e.eval(a)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	switch n.fn {
	case "iif":
		if truthy(args[0]) {
			return e.eval(n.args[1])
		}
		return e.eval(n.args[2])
	case "isnull":
		return arg(0) == nil, nil
	case "cstr", "totext":
		if len(args) >= 2 {
			if s, ok := args[1].(string); ok {
				return formatValue(args[0], s), nil
			}
			return formatNumber(num(args[0]), int(num(args[1])), true), nil
		}
		return toText(arg(0)), nil
	case "tonumber", "val", "cdbl":
		return num(arg(0)), nil
	case "length", "len":
		return float64(len([]rune(toText(arg(0))))), nil
	case "trim":
		return strings.TrimSpace(toText(arg(0))), nil
	case "uppercase", "ucase":
		return strings.ToUpper(toText(arg(0))), nil
	case "lowercase", "lcase":
		return strings.ToLower(toText(arg(0))), nil
	case "left":
		s := []rune(toText(arg(0)))
		k := clamp(int(num(arg(1))), 0, len(s))
		return string(s[:k]), nil
	case "right":
		s := []rune(toText(arg(0)))
		k := clamp(int(num(arg(1))), 0, len(s))
		return string(s[len(s)-k:]), nil
	case "mid":
		s := []rune(toText(arg(0)))
		start := clamp(int(num(arg(1)))-1, 0, len(s))
		end := len(s)
		if len(args) > 2 {
			end = clamp(start+int(num(args[2])), start, len(s))
		}
		return string(s[start:end]), nil
	case "chrw", "chr":
		return string(rune(int(num(arg(0))))), nil
	case "round":
		d := 0
		if len(args) > 1 {
			d = int(num(args[1]))
		}
		return roundHalfAway(num(arg(0)), d), nil
	case "roundup":
		d := 0
		if len(args) > 1 {
			d = int(num(args[1]))
		}
		p := math.Pow(10, float64(d))
		return math.Ceil(num(arg(0))*p-1e-9) / p, nil
	case "truncate", "int", "fix":
		return math.Trunc(num(arg(0))), nil
	case "abs":
		return math.Abs(num(arg(0))), nil
	case "year":
		return float64(date(arg(0)).Year()), nil
	case "month":
		return float64(date(arg(0)).Month()), nil
	case "day":
		return float64(date(arg(0)).Day()), nil
	case "dayofweek", "weekday":
		return float64(date(arg(0)).Weekday()) + 1, nil
	case "monthname":
		m := int(num(arg(0)))
		if m < 1 || m > 12 {
			return "", nil
		}
		name := time.Month(m).String()
		if len(args) > 1 && truthy(args[1]) {
			name = name[:3]
		}
		return name, nil
	case "weekdayname":
		d := int(num(arg(0)))
		name := time.Weekday((d - 1 + 7) % 7).String()
		if len(args) > 1 && truthy(args[1]) {
			name = name[:3]
		}
		return name, nil
	case "date", "cdate", "datevalue":
		if len(args) == 3 {
			return time.Date(int(num(args[0])), time.Month(int(num(args[1]))), int(num(args[2])), 0, 0, 0, 0, time.UTC), nil
		}
		if s, ok := arg(0).(string); ok {
			return parseLooseDate(s)
		}
		return dateOnly(date(arg(0))), nil
	case "datediff":
		return dateDiff(toText(arg(0)), date(arg(1)), date(arg(2))), nil
	case "dateadd":
		return dateAdd(toText(arg(0)), num(arg(1)), date(arg(2))), nil
	case "datepart":
		return datePart(toText(arg(0)), date(arg(1))), nil
	case "towords":
		return toWords(num(arg(0))), nil
	case "switch":
		for i := 0; i+1 < len(args); i += 2 {
			if truthy(args[i]) {
				return args[i+1], nil
			}
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unsupported function %s()", n.fn)
}

// ---- value helpers ----

func num(v any) float64 {
	switch v := v.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(v, ",", "")), 64)
		return f
	}
	return 0
}

func truthy(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return false
}

func date(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	if s, ok := v.(string); ok {
		t, _ := parseLooseDate(s)
		return t
	}
	return time.Time{}
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func zeroOf(v any) any {
	switch v.(type) {
	case string:
		return ""
	case bool:
		return false
	case time.Time:
		return nil
	}
	return 0.0
}

func compare(l, r any) int {
	switch lv := l.(type) {
	case string:
		rs := toText(r)
		return strings.Compare(lv, rs)
	case time.Time:
		rt := date(r)
		switch {
		case lv.Before(rt):
			return -1
		case lv.After(rt):
			return 1
		}
		return 0
	case nil:
		if r == nil {
			return 0
		}
		if rs, ok := r.(string); ok {
			return strings.Compare("", rs)
		}
		return compare(0.0, r)
	}
	a, b := num(l), num(r)
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func toText(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return formatNumber(v, 2, true)
	case bool:
		if v {
			return "True"
		}
		return "False"
	case time.Time:
		return v.Format("1/2/2006")
	}
	return fmt.Sprint(v)
}

func clamp(x, lo, hi int) int {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func roundHalfAway(x float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(x*p) / p
}

func parseLooseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range []string{"2006-01-02", "2006-01-02 15:04:05", "1/2/2006", "01/02/2006", "1/2/06", "2006/01/02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad date %q", s)
}

// Crystal DateDiff counts interval boundaries crossed, like VB.
func dateDiff(interval string, a, b time.Time) float64 {
	switch strings.ToLower(interval) {
	case "yyyy":
		return float64(b.Year() - a.Year())
	case "q":
		return float64((b.Year()-a.Year())*4 + (int(b.Month())-1)/3 - (int(a.Month())-1)/3)
	case "m":
		return float64((b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month()))
	case "d", "y":
		return math.Round(dateOnly(b).Sub(dateOnly(a)).Hours() / 24)
	case "w":
		return math.Trunc(math.Round(dateOnly(b).Sub(dateOnly(a)).Hours()/24) / 7)
	case "ww": // Sundays crossed
		sa := dateOnly(a).AddDate(0, 0, -int(a.Weekday()))
		sb := dateOnly(b).AddDate(0, 0, -int(b.Weekday()))
		return math.Round(sb.Sub(sa).Hours() / 24 / 7)
	case "h":
		return math.Trunc(b.Sub(a).Hours())
	case "n":
		return math.Trunc(b.Sub(a).Minutes())
	case "s":
		return math.Trunc(b.Sub(a).Seconds())
	}
	return 0
}

func dateAdd(interval string, n float64, t time.Time) time.Time {
	k := int(n)
	switch strings.ToLower(interval) {
	case "yyyy":
		return t.AddDate(k, 0, 0)
	case "q":
		return t.AddDate(0, 3*k, 0)
	case "m":
		return t.AddDate(0, k, 0)
	case "d", "y", "w":
		return t.AddDate(0, 0, k)
	case "ww":
		return t.AddDate(0, 0, 7*k)
	case "h":
		return t.Add(time.Duration(k) * time.Hour)
	case "n":
		return t.Add(time.Duration(k) * time.Minute)
	case "s":
		return t.Add(time.Duration(k) * time.Second)
	}
	return t
}

func datePart(interval string, t time.Time) float64 {
	switch strings.ToLower(interval) {
	case "yyyy":
		return float64(t.Year())
	case "q":
		return float64((int(t.Month())-1)/3 + 1)
	case "m":
		return float64(t.Month())
	case "d":
		return float64(t.Day())
	case "y":
		return float64(t.YearDay())
	case "w":
		return float64(t.Weekday()) + 1
	case "ww":
		_, w := t.ISOWeek()
		return float64(w)
	case "h":
		return float64(t.Hour())
	case "n":
		return float64(t.Minute())
	case "s":
		return float64(t.Second())
	}
	return 0
}

var ones = []string{"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
var tens = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

// toWords spells a check amount the way Crystal ToWords does: "one hundred
// twenty-three and 45 / 100".
func toWords(x float64) string {
	whole := int64(math.Floor(math.Abs(x)))
	cents := int64(math.Round((math.Abs(x) - float64(whole)) * 100))
	var spell func(n int64) string
	spell = func(n int64) string {
		switch {
		case n == 0:
			return "zero"
		case n < 20:
			return ones[n]
		case n < 100:
			s := tens[n/10]
			if n%10 != 0 {
				s += "-" + ones[n%10]
			}
			return s
		case n < 1000:
			s := ones[n/100] + " hundred"
			if n%100 != 0 {
				s += " " + spell(n%100)
			}
			return s
		}
		for _, u := range []struct {
			v    int64
			name string
		}{{1e9, "billion"}, {1e6, "million"}, {1e3, "thousand"}} {
			if n >= u.v {
				s := spell(n/u.v) + " " + u.name
				if n%u.v != 0 {
					s += " " + spell(n%u.v)
				}
				return s
			}
		}
		return ""
	}
	return fmt.Sprintf("%s and %02d / 100", spell(whole), cents)
}
