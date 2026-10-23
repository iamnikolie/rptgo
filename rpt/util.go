package rpt

import (
	"strings"
	"unicode"
	"unicode/utf16"
)

// Helpers that keep the port close to the C# it follows: .NET string lengths
// count UTF-16 units, its comparisons here are ordinal and case-insensitive,
// and its \w is Unicode's.

// ulen is a string's length as .NET counts it.
func ulen(s string) int { return len(utf16.Encode([]rune(s))) }

func eqFold(a, b string) bool { return strings.EqualFold(a, b) }

func strPtr(s string) *string { return &s }

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' ||
		unicode.Is(unicode.Pc, r)
}

func allASCIIPrintable(s string) bool {
	for _, c := range s {
		if c < 0x20 || c >= 0x7F {
			return false
		}
	}
	return true
}

// child is C#'s rec.ParseChildren().FirstOrDefault(c => c.Tag == tag).
func child(r Record, tag int) *Record {
	for _, c := range r.Children() {
		if c.Tag == tag {
			c := c
			return &c
		}
	}
	return nil
}

// childMin is child with a minimum data length, as the C# filters often add.
func childMin(r Record, tag, minLen int) *Record {
	for _, c := range r.Children() {
		if c.Tag == tag && len(c.Data) >= minLen {
			c := c
			return &c
		}
	}
	return nil
}

func firstRecord(records []Record, tag int) *Record {
	for i := range records {
		if records[i].Tag == tag {
			return &records[i]
		}
	}
	return nil
}

// str is ReadMutf8String: the string (nil for a null one) and the bytes consumed.
func (r Record) str(off int) (*string, int) {
	s, n, ok := r.String(off)
	if !ok {
		return nil, n
	}
	return &s, n
}

// strOr is `rec.ReadMutf8String(off, out _) ?? fallback`.
func (r Record) strOr(off int, fallback string) string {
	s, _, ok := r.String(off)
	if !ok {
		return fallback
	}
	return s
}

// scanStrings is ScanStrings: every MUTF-8 string at any offset, three or more
// printable ASCII characters long.
func scanStrings(r Record) []string {
	var out []string
	for off := 0; off+8 < len(r.Data); off++ {
		s, n, ok := r.String(off)
		if n > 0 && ok && s != "" && ulen(s) >= 3 && allASCIIPrintable(s) {
			out = append(out, s)
		}
	}
	return out
}

func trimEndChars(s, chars string) string { return strings.TrimRight(s, chars) }

// latin1 decodes bytes one to one into runes.
func latin1(b []byte) string {
	rs := make([]rune, len(b))
	for i, c := range b {
		rs[i] = rune(c)
	}
	return string(rs)
}
