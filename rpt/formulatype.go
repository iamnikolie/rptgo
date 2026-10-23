package rpt

import (
	"strings"
	"unicode"
)

// Whether a Crystal-syntax formula certainly returns a number, decided from its
// text (FormulaResultType.cs). The file does not always record a formula's
// result type, and a field object always carries a numeric format record, even
// one showing a string, so the format is applied only where the type is not in
// doubt: a number literal; a numeric column or formula; a call to a function
// that returns a number whatever it is given; arithmetic over numeric operands;
// a unary minus or parentheses around any of these. Anything else is "not
// known".

var numericFunctions = map[string]bool{
	// Conversions: the result is a number whatever the argument was.
	"ccur": true, "cdbl": true, "cint": true, "clng": true, "tonumber": true, "val": true,
	// Arithmetic.
	"round": true, "truncate": true, "abs": true, "int": true, "fix": true, "sgn": true, "sqr": true,
	"exp": true, "log": true, "remainder": true, "sin": true, "cos": true, "tan": true, "atn": true,
	// Summaries that are numbers whatever they summarise.
	"sum": true, "average": true, "count": true, "distinctcount": true, "stddev": true,
	"populationstddev": true, "variance": true, "populationvariance": true,
}

type ftKind int

const (
	ftNumber ftKind = iota
	ftField
	ftName
	ftOpen
	ftClose
	ftComma
	ftOperator
	ftMinus
	ftText
)

type ftToken struct {
	kind  ftKind
	value string
}

func formulaIsNumeric(text string, isNumericField, isNumericFormula func(string) bool) bool {
	tokens, ok := ftTokenize(text)
	if !ok || len(tokens) == 0 {
		return false
	}
	pos := 0
	return ftExpression(tokens, &pos, isNumericField, isNumericFormula) && pos == len(tokens)
}

func ftExpression(t []ftToken, pos *int, field, formula func(string) bool) bool {
	if !ftOperand(t, pos, field, formula) {
		return false
	}
	for *pos < len(t) && (t[*pos].kind == ftOperator || t[*pos].kind == ftMinus) {
		*pos++
		if !ftOperand(t, pos, field, formula) {
			return false
		}
	}
	return true
}

func ftOperand(t []ftToken, pos *int, field, formula func(string) bool) bool {
	if *pos >= len(t) {
		return false
	}
	tok := t[*pos]
	switch tok.kind {
	case ftMinus:
		*pos++
		return ftOperand(t, pos, field, formula)
	case ftNumber:
		*pos++
		return true
	case ftField:
		*pos++
		if strings.HasPrefix(tok.value, "@") {
			return formula(tok.value[1:])
		}
		return field(tok.value)
	case ftOpen:
		*pos++
		if !ftExpression(t, pos, field, formula) {
			return false
		}
		if *pos >= len(t) || t[*pos].kind != ftClose {
			return false
		}
		*pos++
		return true
	case ftName:
		// Only a call, and only to a function whose result is always a number. Its
		// arguments do not decide the type, so they are skipped, but must balance.
		if !numericFunctions[strings.ToLower(tok.value)] {
			return false
		}
		if *pos+1 >= len(t) || t[*pos+1].kind != ftOpen {
			return false
		}
		*pos += 2
		for depth := 1; *pos < len(t); *pos++ {
			if t[*pos].kind == ftOpen {
				depth++
			} else if t[*pos].kind == ftClose {
				depth--
				if depth == 0 {
					*pos++
					return true
				}
			}
		}
		return false
	}
	return false
}

// ftTokenize returns the formula's tokens, or false when it holds anything
// this does not model.
func ftTokenize(text string) ([]ftToken, bool) {
	var tokens []ftToken
	rs := []rune(text)
	for i := 0; i < len(rs); {
		c := rs[i]
		if unicode.IsSpace(c) {
			i++
			continue
		}
		if c == '{' {
			end := -1
			for j := i + 1; j < len(rs); j++ {
				if rs[j] == '}' {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, false
			}
			tokens = append(tokens, ftToken{ftField, string(rs[i+1 : end])})
			i = end + 1
			continue
		}
		if unicode.IsDigit(c) || (c == '.' && i+1 < len(rs) && unicode.IsDigit(rs[i+1])) {
			start := i
			for i < len(rs) && (unicode.IsDigit(rs[i]) || rs[i] == '.') {
				i++
			}
			tokens = append(tokens, ftToken{ftNumber, string(rs[start:i])})
			continue
		}
		if unicode.IsLetter(c) {
			start := i
			for i < len(rs) && (unicode.IsLetter(rs[i]) || unicode.IsDigit(rs[i]) || rs[i] == '_') {
				i++
			}
			word := string(rs[start:i])
			if strings.EqualFold(word, "mod") {
				tokens = append(tokens, ftToken{ftOperator, word})
			} else {
				tokens = append(tokens, ftToken{ftName, word})
			}
			continue
		}
		switch c {
		case '(':
			tokens = append(tokens, ftToken{ftOpen, "("})
		case ')':
			tokens = append(tokens, ftToken{ftClose, ")"})
		case ',':
			tokens = append(tokens, ftToken{ftComma, ","})
		case '-':
			tokens = append(tokens, ftToken{ftMinus, "-"})
		case '+', '*', '/', '^', '\\':
			// Two slashes open a comment, which could say anything.
			if c == '/' && i+1 < len(rs) && rs[i+1] == '/' {
				return nil, false
			}
			tokens = append(tokens, ftToken{ftOperator, string(c)})
		case '"', '\'':
			// A string outside a numeric function's arguments makes the formula not
			// known; inside them it is skipped like any argument.
			end := -1
			for j := i + 1; j < len(rs); j++ {
				if rs[j] == c {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, false
			}
			tokens = append(tokens, ftToken{ftText, string(rs[i : end+1])})
			i = end + 1
			continue
		default:
			return nil, false
		}
		i++
	}
	return tokens, true
}
