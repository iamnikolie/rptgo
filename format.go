package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Crystal's defaults on a US-English Windows box: dates M/d/yy, numbers
// and currency #,##0.00 with a leading minus.
const (
	defaultDate   = "M/d/yy"
	defaultNumber = "#,##0.00"
	defaultTime   = "h:mm:sstt"
)

// formatValue applies a .NET custom format string, the kind the .rpt parser
// writes: "#,##0.00", "0", "\"$\"#,##0.00\" \"", "M'/'d'/'yy". Text in
// single or double quotes is literal.
func formatValue(v any, pattern string) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return toText(v)
	case time.Time:
		if pattern == "" {
			pattern = defaultDate
		}
		return formatDate(v, pattern)
	case float64:
		if pattern == "" {
			pattern = defaultNumber
		}
		return formatNumberPattern(v, pattern)
	}
	return fmt.Sprint(v)
}

// formatNumberPattern: literal text before the digits is a prefix, after them
// a suffix; the digit part says the decimals and whether to group thousands.
// A negative number puts its minus before the prefix, as .NET does.
func formatNumberPattern(x float64, pattern string) string {
	var prefix, digits, suffix strings.Builder
	seenDigits := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\'' || c == '"' {
			j := strings.IndexByte(pattern[i+1:], c)
			if j < 0 {
				j = len(pattern) - i - 1
			}
			lit := pattern[i+1 : i+1+j]
			if seenDigits {
				suffix.WriteString(lit)
			} else {
				prefix.WriteString(lit)
			}
			i += j + 1
			continue
		}
		if strings.IndexByte("#0,.", c) >= 0 {
			seenDigits = true
			digits.WriteByte(c)
			continue
		}
		if seenDigits {
			suffix.WriteByte(c)
		} else {
			prefix.WriteByte(c)
		}
	}
	d := digits.String()
	decimals := 0
	if i := strings.IndexByte(d, '.'); i >= 0 {
		decimals = len(d) - i - 1
	}
	s := formatNumber(math.Abs(x), decimals, strings.Contains(d, ","))
	if x < 0 && strings.Trim(s, "0.,") != "" {
		return "-" + prefix.String() + s + suffix.String()
	}
	return prefix.String() + s + suffix.String()
}

func formatNumber(x float64, decimals int, thousands bool) string {
	x = roundHalfAway(x, decimals)
	neg := x < 0
	s := fmt.Sprintf("%.*f", decimals, math.Abs(x))
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	if thousands {
		var b strings.Builder
		for i, c := range intPart {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteRune(c)
		}
		intPart = b.String()
	}
	if neg && strings.Trim(intPart+frac, "0.,") != "" {
		return "-" + intPart + frac
	}
	return intPart + frac
}

// formatDate formats the .NET date tokens yyyy yy MMMM MMM MM M dddd ddd dd d
// HH H hh h mm ss tt; quoted text is literal.
func formatDate(t time.Time, p string) string {
	hour12 := t.Hour() % 12
	if hour12 == 0 {
		hour12 = 12
	}
	ampm := "AM"
	if t.Hour() >= 12 {
		ampm = "PM"
	}
	tokens := []struct {
		tok string
		val func() string
	}{
		{"yyyy", func() string { return fmt.Sprintf("%04d", t.Year()) }},
		{"yy", func() string { return fmt.Sprintf("%02d", t.Year()%100) }},
		{"MMMM", func() string { return t.Month().String() }},
		{"MMM", func() string { return t.Month().String()[:3] }},
		{"MM", func() string { return fmt.Sprintf("%02d", int(t.Month())) }},
		{"M", func() string { return fmt.Sprint(int(t.Month())) }},
		{"dddd", func() string { return t.Weekday().String() }},
		{"ddd", func() string { return t.Weekday().String()[:3] }},
		{"dd", func() string { return fmt.Sprintf("%02d", t.Day()) }},
		{"d", func() string { return fmt.Sprint(t.Day()) }},
		{"HH", func() string { return fmt.Sprintf("%02d", t.Hour()) }},
		{"H", func() string { return fmt.Sprint(t.Hour()) }},
		{"hh", func() string { return fmt.Sprintf("%02d", hour12) }},
		{"h", func() string { return fmt.Sprint(hour12) }},
		{"mm", func() string { return fmt.Sprintf("%02d", t.Minute()) }},
		{"ss", func() string { return fmt.Sprintf("%02d", t.Second()) }},
		{"tt", func() string { return ampm }},
	}
	var b strings.Builder
	for i := 0; i < len(p); {
		if c := p[i]; c == '\'' || c == '"' {
			j := strings.IndexByte(p[i+1:], c)
			if j < 0 {
				j = len(p) - i - 1
			}
			b.WriteString(p[i+1 : i+1+j])
			i += j + 2
			continue
		}
		matched := false
		for _, tk := range tokens {
			if strings.HasPrefix(p[i:], tk.tok) {
				b.WriteString(tk.val())
				i += len(tk.tok)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(p[i])
			i++
		}
	}
	return b.String()
}
