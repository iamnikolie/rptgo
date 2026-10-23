package rpt

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func mapAggregateFunction(code int) AggregateFunction {
	switch code {
	case 1:
		return AggSum
	case 2:
		return AggCount
	case 3:
		return AggDistinctCount
	case 4:
		return AggMinimum
	case 5:
		return AggMaximum
	case 6:
		return AggAverage
	case 7:
		return AggStandardDeviation
	case 8:
		return AggVariance
	}
	return AggSum
}

// mapSummaryOperation: CrSummaryOperationEnum.
func mapSummaryOperation(code byte) AggregateFunction {
	switch code {
	case 0:
		return AggSum
	case 1:
		return AggAverage
	case 3, 8:
		return AggStandardDeviation
	case 2, 7:
		return AggVariance
	case 4:
		return AggMaximum
	case 5:
		return AggMinimum
	case 6:
		return AggCount
	case 9:
		return AggDistinctCount
	}
	return AggregateFunction(fmt.Sprintf("Op%d", code))
}

// mapCrValueType: field value type codes, little-endian Int16 in tag-113.
func mapCrValueType(code int) string {
	switch code {
	case 1:
		return "Boolean"
	case 2:
		return "Int16"
	case 3:
		return "Int32"
	case 4:
		return "Float32"
	case 5:
		return "Float64"
	case 6:
		return "Currency"
	case 7:
		return "Float64"
	case 8:
		return "Boolean"
	case 9, 10:
		return "DateTime"
	case 11, 12, 13, 14:
		return "String"
	case 15:
		return "DateTime"
	}
	return "String"
}

// mapParamValueType: parameter type codes differ (6 is String, not Currency).
func mapParamValueType(code int) string {
	switch code {
	case 1:
		return "Boolean"
	case 3:
		return "Int32"
	case 6:
		return "String"
	case 7:
		return "Float64"
	case 9:
		return "DateTime"
	}
	return "String"
}

// mapQeValueType: the QueryEngine's numbering, used by the QESession graph and
// SQL expressions.
func mapQeValueType(code int) string {
	switch code {
	case 0, 1, 2, 3:
		return "Int16"
	case 4, 5:
		return "Int32"
	case 6:
		return "Float64"
	case 7:
		return "Currency"
	case 8:
		return "Boolean"
	case 9, 10, 15:
		return "DateTime"
	}
	return "String"
}

// mapFormulaValueType: a formula's result type, in the runtime's numbering.
func mapFormulaValueType(code int) *string {
	switch code {
	case 6:
		return strPtr("Float64")
	case 7:
		return strPtr("Currency")
	case 8:
		return strPtr("Boolean")
	case 9, 10, 15:
		return strPtr("DateTime")
	case 11, 12, 13:
		return strPtr("String")
	}
	return nil
}

func dataSourceKindFromDriver(driver *string) DataSourceKind {
	if driver == nil || *driver == "" {
		return DataSourceUnknown
	}
	d := strings.ToLower(*driver)
	switch {
	case strings.Contains(d, "odbc"):
		return DataSourceOdbc
	case strings.Contains(d, "adoplus") || strings.Contains(d, "dataset"):
		return DataSourceDataset
	case strings.Contains(d, "ado") || strings.Contains(d, "oledb"):
		return DataSourceOleDb
	}
	return DataSourceNative
}

// ---- typed views over report.Fields ----

func (r *reportBuilder) dbFields() []*DatabaseField {
	var out []*DatabaseField
	for _, f := range r.Fields {
		if d, ok := f.(*DatabaseField); ok {
			out = append(out, d)
		}
	}
	return out
}

func (r *reportBuilder) formulaFields() []*FormulaField {
	var out []*FormulaField
	for _, f := range r.Fields {
		if d, ok := f.(*FormulaField); ok {
			out = append(out, d)
		}
	}
	return out
}

func (r *reportBuilder) paramFields() []*ParameterField {
	var out []*ParameterField
	for _, f := range r.Fields {
		if d, ok := f.(*ParameterField); ok {
			out = append(out, d)
		}
	}
	return out
}

// ---- field definitions ----

func extractFields(records []Record, report *reportBuilder) {
	var pending []pendingCondition
	for _, rec := range records {
		switch rec.Tag {
		case tagDbFieldDef:
			ch114 := child(rec, 114)
			if ch114 == nil {
				continue
			}
			ch113 := child(*ch114, 113)
			if ch113 == nil {
				continue
			}
			name, consumed := ch113.str(0)
			if name == nil || *name == "" {
				continue
			}
			report.Fields = append(report.Fields, &DatabaseField{
				ColumnName: *name, DataType: mapCrValueType(ch113.Uint16LE(consumed)),
				FieldBase: FieldBase{Name: *name},
			})

		case tagFormulaFieldDef:
			// Every record takes a place, read or not: the ordinal is a position.
			report.formulasByOrdinal = append(report.formulasByOrdinal, nil)
			ch118 := child(rec, 118)
			if ch118 == nil {
				continue
			}
			ch113 := child(*ch118, 113)
			if ch113 == nil {
				continue
			}
			displayName, nameConsumed := ch113.str(0)
			if displayName == nil || *displayName == "" {
				continue
			}
			resultType := mapFormulaValueType(ch113.Uint16LE(nameConsumed))

			// After the tag-113 block: a 2-byte count of dependencies, that many
			// strings each followed by 3 filler bytes, then the formula text.
			off := 8 + len(ch113.Data)
			depCount := ch118.Uint16(off)
			off += 2
			for i := 0; i < depCount; i++ {
				_, depConsumed := ch118.str(off)
				if depConsumed <= 0 {
					break
				}
				off += depConsumed + 3
			}
			// A saved body can genuinely be empty and is still declared.
			text := ch118.strOr(off, "")

			report.formulaTexts[strings.ToLower(*displayName)] = text
			report.formulasByOrdinal[len(report.formulasByOrdinal)-1] = &namedFormula{*displayName, text}

			if *displayName == "Record Selection" {
				report.RecordSelectionFormula = strPtr(text)
				continue
			}
			if *displayName == "Group Selection" {
				report.GroupSelectionFormula = strPtr(text)
				continue
			}
			// Internal formulas: section visibility, group sort keys, format hooks.
			if strings.Contains(*displayName, "_Visibility") || strings.HasPrefix(*displayName, "Group #") ||
				objectFormatHookNames[strings.ToLower(*displayName)] {
				continue
			}
			report.Fields = append(report.Fields, &FormulaField{
				FormulaText: text, Syntax: "Crystal", ResultType: resultType,
				FieldBase: FieldBase{Name: *displayName},
			})

		case tagParamFieldDef:
			ch113 := child(rec, 113)
			if ch113 == nil {
				continue
			}
			name, nc := ch113.str(0)
			if name == nil || *name == "" {
				continue
			}
			typeCode := ch113.Uint16LE(nc)
			prompt, pickList := extractParamPickList(rec.Data, *name)
			// A resaved report can carry two definitions of one parameter; keep the first.
			dup := false
			for _, p := range report.paramFields() {
				if eqFold(p.Name, *name) {
					dup = true
				}
			}
			if !dup {
				report.Fields = append(report.Fields, &ParameterField{
					DataType: mapParamValueType(typeCode), PromptText: prompt, PickListValues: pickList,
					FieldBase: FieldBase{Name: *name},
				})
			}

		case tagRunningTotalFieldDef:
			// tag-128 -> tag-126 -> tag-113 (name, type). After the 113 block in the
			// 126 payload: 4 bytes, the summarized "Table.Column", 6 bytes, Int16 LE
			// aggregate function.
			ch126 := child(rec, 126)
			if ch126 == nil {
				continue
			}
			ch113 := child(*ch126, 113)
			if ch113 == nil {
				continue
			}
			name, consumed := ch113.str(0)
			if name == nil || *name == "" {
				continue
			}
			typeCode := ch113.Uint16LE(consumed)
			after113 := ch113.Offset + 8 + len(ch113.Data)
			summarized := ""
			function := AggSum
			if after113 < len(ch126.Data) {
				// The byte after the name block is Crystal's summary operation
				// (crSOSum = 0 ... crSODistinctCount = 9). The C# reader looks
				// further on and reads every total as a Sum.
				function = mapSummaryOperation(ch126.Data[after113])
			}
			if after113+4 < len(ch126.Data) {
				full, _ := ch126.str(after113 + 4)
				if full != nil && *full != "" {
					if dot := strings.IndexByte(*full, '.'); dot >= 0 {
						summarized = (*full)[dot+1:]
					} else {
						summarized = *full
					}
				}
			}
			rt := &RunningTotalField{
				DataType: mapCrValueType(typeCode), SummarizedFieldName: summarized, Function: function,
				FieldBase: FieldBase{Name: *name},
			}
			// After the tag-126 record: the reset condition, then the evaluate
			// condition (see readTotalCondition).
			if ch126.End <= len(rec.Data) {
				tail := rec.Data[ch126.End:]
				reset, next := readTotalCondition(tail, 0)
				eval, _ := readTotalCondition(tail, next)
				pending = append(pending, pendingCondition{&rt.ResetCondition, reset}, pendingCondition{&rt.EvaluateCondition, eval})
			}
			report.Fields = append(report.Fields, rt)
		}
	}
	// A condition formula may come after the total that names it.
	for _, p := range pending {
		*p.target = p.cond.resolve(report.formulasByOrdinal)
	}
}

// totalCondition is a running total's reset or evaluate condition as stored:
// kind 0 none (reset: never; evaluate: every record), 2 on change of group,
// 3 a formula.
type totalCondition struct {
	kind    byte
	group   int // 1-based group number
	ordinal int // formula ordinal among the formula records
	name    string
}

type pendingCondition struct {
	target **string
	cond   totalCondition
}

// readTotalCondition reads one condition at d[i]: a kind byte; for a group a
// UInt16 1-based group number; for a formula an Int32-length name ("@...",
// with its NUL), a flag byte and the formula's Int16 ordinal.
func readTotalCondition(d []byte, i int) (totalCondition, int) {
	if i >= len(d) {
		return totalCondition{}, i
	}
	c := totalCondition{kind: d[i]}
	i++
	switch c.kind {
	case 2:
		if i+2 <= len(d) {
			c.group = int(d[i])<<8 | int(d[i+1])
		}
		i += 2
	case 3:
		if i+4 > len(d) {
			return c, len(d)
		}
		n := int(binary.BigEndian.Uint32(d[i:]))
		i += 4
		if n < 0 || i+n+3 > len(d) {
			return c, len(d)
		}
		c.name = strings.TrimLeft(strings.TrimRight(string(d[i:i+n]), "\x00"), "@")
		i += n
		c.ordinal = int(int16(uint16(d[i+1])<<8 | uint16(d[i+2])))
		i += 3
	}
	return c, i
}

// resolve writes the condition as the model keeps it: "Group:<level>" with
// the 0-based group level, or "Formula:<text>"; nil for none.
func (c totalCondition) resolve(byOrdinal []*namedFormula) *string {
	switch c.kind {
	case 2:
		if c.group > 0 {
			return strPtr(fmt.Sprintf("Group:%d", c.group-1))
		}
	case 3:
		if c.ordinal >= 0 && c.ordinal < len(byOrdinal) && byOrdinal[c.ordinal] != nil &&
			eqFold(byOrdinal[c.ordinal].name, c.name) {
			return strPtr("Formula:" + byOrdinal[c.ordinal].text)
		}
	}
	return nil
}

// extractParamPickList scans a tag-122 payload for the prompt (a string ending
// in ':') and the longest run of back-to-back strings, the static pick list,
// paired as value+label when the first half are prefixes of the second.
func extractParamPickList(data []byte, paramName string) (string, []PickListEntry) {
	type found struct {
		start, end int
		text       string
	}
	var all []found
	for i := 0; i < len(data)-6; i++ {
		n := int(int32(binary.BigEndian.Uint32(data[i:])))
		if n < 2 || n > 200 || i+4+n > len(data) {
			continue
		}
		ok := true
		for j := i + 4; j < i+4+n-1; j++ {
			if data[j] < 0x20 {
				ok = false
				break
			}
		}
		if !ok || data[i+4+n-1] != 0 {
			continue
		}
		s := decodeUTF8Lossy(data[i+4 : i+4+n-1])
		hasLetter := false
		for _, c := range s {
			if unicode.IsLetter(c) {
				hasLetter = true
				break
			}
		}
		if ulen(s) >= 2 && hasLetter {
			all = append(all, found{i, i + 4 + n, s})
		}
	}

	prompt := ""
	for _, f := range all {
		if strings.HasSuffix(f.text, ":") && prompt == "" {
			prompt = strings.TrimSpace(f.text[:len(f.text)-1])
		}
	}

	nameCore := strings.TrimSpace(strings.Trim(paramName, "@$[]"))
	var best, cur []string
	prevEnd := -1
	for _, f := range all {
		if f.text == paramName || f.text == nameCore || strings.HasPrefix(f.text, "crobj://") ||
			strings.HasSuffix(f.text, ":") || (strings.Contains(f.text, ".") && !strings.Contains(f.text, " ")) {
			prevEnd = -1
			cur = nil
			continue
		}
		if f.start == prevEnd {
			cur = append(cur, f.text)
		} else {
			cur = []string{f.text}
		}
		prevEnd = f.end
		if len(cur) > len(best) {
			best = append([]string{}, cur...)
		}
	}
	if len(best) < 2 {
		return prompt, []PickListEntry{}
	}
	half := len(best) / 2
	paired := len(best)%2 == 0 && half >= 2
	if paired {
		for i := 0; i < half; i++ {
			if !(hasPrefixFold(best[i+half], best[i]) && ulen(best[i+half]) > ulen(best[i])) {
				paired = false
				break
			}
		}
	}
	var out []PickListEntry
	if paired {
		for i := 0; i < half; i++ {
			out = append(out, PickListEntry{best[i], best[i+half]})
		}
	} else {
		for _, v := range best {
			out = append(out, PickListEntry{v, v})
		}
	}
	return prompt, out
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// decodeUTF8Lossy decodes like .NET's Encoding.UTF8: invalid bytes become U+FFFD.
func decodeUTF8Lossy(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// ---- SQL expressions (tag 129) ----

// extractSqlExpressions: bytes 4..7 are the length of a block at 8, XOR 0x71: a
// name length, the name, a little-endian Int16 value type; then a plain length
// and the SQL text, kept as the Crystal formula it amounts to.
func extractSqlExpressions(records []Record, report *reportBuilder) {
	for _, rec := range records {
		if rec.Tag != tagSqlExpression {
			continue
		}
		d := rec.Data
		if len(d) < 16 {
			continue
		}
		blockLength := int(int32(binary.BigEndian.Uint32(d[4:])))
		if blockLength < 6 || 8+blockLength+4 > len(d) {
			continue
		}
		x := func(i int) int { return int(d[i] ^ 0x71) }
		nameLength := int(int32(uint32(x(8))<<24 | uint32(x(9))<<16 | uint32(x(10))<<8 | uint32(x(11))))
		if nameLength < 1 || 12+nameLength > 8+blockLength {
			continue
		}
		nb := make([]rune, nameLength)
		for i := range nb {
			nb[i] = rune(x(12 + i))
		}
		name := strings.TrimRight(string(nb), "\x00")
		typeAt := 12 + nameLength
		typeCode := -1
		if typeAt+1 < 8+blockLength {
			typeCode = x(typeAt) | x(typeAt+1)<<8
		}
		textAt := 8 + blockLength
		textLength := int(int32(binary.BigEndian.Uint32(d[textAt:])))
		if name == "" || textLength < 1 || textAt+4+textLength > len(d) {
			continue
		}
		sql := strings.TrimRight(decodeUTF8Lossy(d[textAt+4:textAt+4+textLength]), "\x00")
		formula := sqlColumnRef.ReplaceAllStringFunc(sql, func(m string) string {
			g := sqlColumnRef.FindStringSubmatch(m)
			table, column := "", ""
			for k := 1; k+1 < len(g); k += 2 {
				if g[k] != "" || g[k+1] != "" {
					table, column = g[k], g[k+1]
					break
				}
			}
			return "{" + table + "." + column + "}"
		})
		t := mapQeValueType(typeCode)
		if t == "Int16" || t == "Int32" {
			t = "Float64"
		}
		report.Fields = append(report.Fields, &FormulaField{
			FormulaText: formula, Syntax: "Crystal", ResultType: strPtr(t),
			FieldBase: FieldBase{Name: "%" + name},
		})
	}
}

// `Table`.`Column` or "table"."column" (each name in its own quote kind):
// ([`"])([^`"]+)\1\.([`"])([^`"]+)\3 without backreferences.
var sqlColumnRef = regexp.MustCompile("`([^`\"]+)`\\.`([^`\"]+)`|`([^`\"]+)`\\.\"([^`\"]+)\"|\"([^`\"]+)\"\\.`([^`\"]+)`|\"([^`\"]+)\"\\.\"([^`\"]+)\"")

// ---- custom functions (tag 335) ----

var (
	customFnSignature = regexp.MustCompile(`(?is)^\s*Function\s*\((?P<params>[^)]*)\)\s*(?P<body>.*)$`)
	customFnParam     = regexp.MustCompile(`(?i)(\w+)\s*$`)
)

type customFn struct {
	params []fnParam
	body   string
}

type fnParam struct {
	name string
	def  *string
}

// expandCustomFunctionCalls: a report's custom function lives in a tag-335
// record, its source XOR 0x76. Calls are inlined into every formula: the body
// in parentheses, arguments substituted for parameter names.
func expandCustomFunctionCalls(records []Record, report *reportBuilder) {
	type entry struct {
		name string
		fn   customFn
	}
	var functions []entry
	set := func(name string, fn customFn) {
		for i := range functions {
			if eqFold(functions[i].name, name) {
				functions[i].fn = fn
				return
			}
		}
		functions = append(functions, entry{name, fn})
	}
	needle := []byte("Function")
	for i := range needle {
		needle[i] ^= 0x76
	}
	for _, rec := range records {
		if rec.Tag != tagCustomFunctionDef {
			continue
		}
		var name *string
		if ch118 := child(rec, 118); ch118 != nil {
			if ch113 := child(*ch118, 113); ch113 != nil {
				name, _ = ch113.str(0)
			}
		}
		if name == nil || *name == "" {
			continue
		}
		d := rec.Data
		start := -1
		for i := 0; i+len(needle) <= len(d) && start < 0; i++ {
			hit := true
			for k := range needle {
				if d[i+k] != needle[k] {
					hit = false
					break
				}
			}
			if hit {
				start = i
			}
		}
		if start < 0 {
			continue
		}
		var sb []rune
		for i := start; i < len(d) && d[i] != 0x76; i++ {
			sb = append(sb, rune(d[i]^0x76))
		}
		m := customFnSignature.FindStringSubmatch(string(sb))
		if m == nil {
			continue
		}
		var params []fnParam
		for _, p := range strings.Split(m[1], ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			var def *string
			decl := p
			if k := strings.Index(p, ":="); k >= 0 {
				def = strPtr(strings.TrimSpace(p[k+2:]))
				decl = strings.TrimSpace(p[:k])
			}
			pm := customFnParam.FindStringSubmatch(decl)
			if pm == nil || pm[1] == "" {
				continue
			}
			params = append(params, fnParam{pm[1], def})
		}
		set(*name, customFn{params, strings.TrimSpace(m[2])})
	}
	if len(functions) == 0 {
		return
	}
	for _, formula := range report.formulaFields() {
		if formula.FormulaText == "" {
			continue
		}
		text := formula.FormulaText
		for pass := 0; pass < 10; pass++ {
			next := text
			for _, f := range functions {
				next = expandOnce(next, f.name, f.fn)
			}
			if next == text {
				break
			}
			text = next
		}
		formula.FormulaText = text
	}
}

func expandOnce(text, name string, fn customFn) string {
	idx := 0
	for {
		k := indexFold(text, name, idx)
		if k < 0 {
			return text
		}
		idx = k
		leftOK := idx == 0
		if !leftOK {
			prev, _ := utf8.DecodeLastRuneInString(text[:idx])
			leftOK = !(unicode.IsLetter(prev) || unicode.IsDigit(prev)) && prev != '_'
		}
		p := idx + len(name)
		for p < len(text) && unicode.IsSpace(rune(text[p])) {
			p++
		}
		if !leftOK || p >= len(text) || text[p] != '(' {
			idx += len(name)
			continue
		}
		depth, argStart, end := 0, p+1, -1
		var args []string
		inStr, q := false, byte('"')
		for i := p; i < len(text); i++ {
			c := text[i]
			if inStr {
				if c == q {
					inStr = false
				}
				continue
			}
			if c == '"' || c == '\'' {
				inStr, q = true, c
				continue
			}
			if c == '(' {
				depth++
			} else if c == ')' {
				depth--
				if depth == 0 {
					args = append(args, text[argStart:i])
					end = i
					break
				}
			} else if c == ',' && depth == 1 {
				args = append(args, text[argStart:i])
				argStart = i + 1
			}
		}
		if end < 0 || len(args) > len(fn.params) {
			idx += len(name)
			continue
		}
		defaultsOK := true
		for a := len(args); a < len(fn.params); a++ {
			if fn.params[a].def == nil {
				defaultsOK = false
				break
			}
			args = append(args, *fn.params[a].def)
		}
		if !defaultsOK {
			idx += len(name)
			continue
		}
		// A body that assigns is a procedure; degrade the call to its first argument.
		var body string
		if strings.Contains(fn.body, ":=") {
			if len(args) > 0 {
				body = strings.TrimSpace(args[0])
			} else {
				body = `""`
			}
		} else {
			body = fn.body
			for a := range fn.params {
				body = replaceWholeWordFold(body, fn.params[a].name, "("+strings.TrimSpace(args[a])+")")
			}
		}
		text = text[:idx] + "(" + body + ")" + text[end+1:]
		idx += len(body) + 2
	}
}

// indexFold is IndexOf(value, start, OrdinalIgnoreCase).
func indexFold(s, sub string, start int) int {
	if start > len(s) {
		return -1
	}
	ls, lsub := strings.ToLower(s[start:]), strings.ToLower(sub)
	if len(ls) != len(s)-start || len(lsub) != len(sub) {
		// Lower-casing changed byte lengths: fall back to a rune-by-rune scan.
		for i := start; i+len(sub) <= len(s); i++ {
			if strings.EqualFold(s[i:i+len(sub)], sub) {
				return i
			}
		}
		return -1
	}
	k := strings.Index(ls, lsub)
	if k < 0 {
		return -1
	}
	return start + k
}

// replaceWholeWordFold is Regex.Replace(s, (?<![\w])(word)(?![\w]), repl, IgnoreCase).
func replaceWholeWordFold(s, word, repl string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if i+len(word) <= len(s) && strings.EqualFold(s[i:i+len(word)], word) {
			before := i == 0
			if !before {
				r, _ := utf8.DecodeLastRuneInString(s[:i])
				before = !isWordRune(r)
			}
			after := i+len(word) == len(s)
			if !after {
				r, _ := utf8.DecodeRuneInString(s[i+len(word):])
				after = !isWordRune(r)
			}
			if before && after {
				b.WriteString(repl)
				i += len(word)
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

// ---- table names from formula text ----

var (
	bracedTableColumn  = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_ ]*(?:;\d+)?)\.([A-Za-z_][A-Za-z0-9_ ]*)\}`)
	bracedParameterRef = regexp.MustCompile(`\{\?([^}\r\n]+)\}`)
)

func isIdentStart(c byte) bool { return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || (c >= '0' && c <= '9') }

// bareTableColumns is the matches of
// (?<![{@#?.\w])([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)(?!\w).
func bareTableColumns(text string) [][2]string {
	var out [][2]string
	for i := 0; i < len(text); {
		if !isIdentStart(text[i]) {
			i++
			continue
		}
		if i > 0 {
			prev, _ := utf8.DecodeLastRuneInString(text[:i])
			if strings.ContainsRune("{@#?.", prev) || isWordRune(prev) {
				i++
				continue
			}
		}
		j := i + 1
		for j < len(text) && isIdentPart(text[j]) {
			j++
		}
		if j+1 >= len(text) || text[j] != '.' || !isIdentStart(text[j+1]) {
			i++
			continue
		}
		k := j + 2
		for k < len(text) && isIdentPart(text[k]) {
			k++
		}
		if k < len(text) {
			r, _ := utf8.DecodeRuneInString(text[k:])
			if isWordRune(r) {
				i++
				continue
			}
		}
		out = append(out, [2]string{text[i:j], text[j+1 : k]})
		i = k
	}
	return out
}

// backfillTableNamesFromFormulas: a column reached only through a formula never
// gets its table from a placed field object, so formula bodies (and the
// selection formulas) name it; a braced reference to a column the dictionary
// lacks is synthesized as a String column. Synthesized columns and parameters
// used arithmetically become Float64; parameters used as flags, Boolean.
func backfillTableNamesFromFormulas(report *reportBuilder) {
	dbFields := report.dbFields()
	allColumns := map[string]bool{}
	for _, f := range dbFields {
		allColumns[strings.ToLower(f.ColumnName)] = true
	}
	var synthesized []*DatabaseField

	backfill := func(table, column string, canSynthesize bool) {
		for _, f := range dbFields {
			if eqFold(f.ColumnName, column) {
				if f.TableName == "" {
					f.TableName = table
				}
				return
			}
		}
		if !canSynthesize || allColumns[strings.ToLower(column)] {
			return
		}
		s := &DatabaseField{ColumnName: column, TableName: table, DataType: "String", FieldBase: FieldBase{Name: column}}
		report.Fields = append(report.Fields, s)
		dbFields = append(dbFields, s)
		allColumns[strings.ToLower(column)] = true
		synthesized = append(synthesized, s)
	}

	var texts []string
	for _, f := range report.formulaFields() {
		texts = append(texts, f.FormulaText)
	}
	texts = append(texts, deref(report.RecordSelectionFormula), deref(report.GroupSelectionFormula))
	for _, text := range texts {
		if text == "" {
			continue
		}
		for _, m := range bracedTableColumn.FindAllStringSubmatch(text, -1) {
			backfill(m[1], m[2], true)
		}
		for _, m := range bareTableColumns(text) {
			backfill(m[0], m[1], false)
		}
	}

	paramNames := map[string]bool{}
	for _, p := range report.paramFields() {
		paramNames[strings.ToLower(p.Name)] = true
	}
	for _, formula := range report.formulaFields() {
		if formula.FormulaText == "" {
			continue
		}
		for _, m := range bracedParameterRef.FindAllStringSubmatch(formula.FormulaText, -1) {
			pname := strings.TrimSpace(m[1])
			if paramNames[strings.ToLower(pname)] {
				continue
			}
			report.Fields = append(report.Fields, &ParameterField{DataType: "String", FieldBase: FieldBase{Name: pname}})
			paramNames[strings.ToLower(pname)] = true
		}
	}

	// Every formula text, hooks included, in a stable order.
	keys := make([]string, 0, len(report.formulaTexts))
	for k := range report.formulaTexts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	usedNumerically := func(ref string) bool {
		strict := regexp.MustCompile(`(?i)[-*/]\s*\(*\s*` + ref + `|` + ref + `\s*\)*\s*[-*/]`)
		plus := regexp.MustCompile(`(?i)\+\s*\(*\s*` + ref + `|` + ref + `\s*\)*\s*\+`)
		for _, k := range keys {
			t := report.formulaTexts[k]
			if t == "" {
				continue
			}
			if strict.MatchString(t) {
				return true
			}
			if plus.MatchString(t) && !strings.ContainsAny(t, `"'&`) {
				return true
			}
		}
		return false
	}
	for _, f := range synthesized {
		if usedNumerically(regexp.QuoteMeta("{" + f.TableName + "." + f.ColumnName + "}")) {
			f.DataType = "Float64"
		}
	}
	for _, p := range report.paramFields() {
		if p.DataType == "String" || p.DataType == "" {
			if usedNumerically(regexp.QuoteMeta("{?" + p.Name + "}")) {
				p.DataType = "Float64"
			}
		}
	}
	usedAsBoolean := func(ref string) bool {
		re := regexp.MustCompile(`(?i)\bNot\s*\(*\s*` + ref + `|` + ref + `\s*\)*\s*(?:And|Or)\b|\b(?:And|Or)\s*\(*\s*` + ref + `|` + ref + `\s*=\s*(?:True|False)\b`)
		for _, k := range keys {
			if t := report.formulaTexts[k]; t != "" && re.MatchString(t) {
				return true
			}
		}
		return false
	}
	for _, p := range report.paramFields() {
		if p.DataType == "String" || p.DataType == "" {
			if usedAsBoolean(regexp.QuoteMeta("{?" + p.Name + "}")) {
				p.DataType = "Boolean"
			}
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---- groups (tag 229) and sorts (tag 41) ----

func extractGroups(records []Record, report *reportBuilder) {
	level := 0
	for _, rec := range records {
		if rec.Tag != tagGroupCondition {
			continue
		}
		// Shared with cross-tab and chart axes; only "@Group #N Order" is a report group.
		isGroup := false
		for _, s := range scanStrings(rec) {
			if strings.HasPrefix(s, "@Group #") {
				isGroup = true
				break
			}
		}
		if !isGroup {
			continue
		}
		// "Table.Field", then two bytes not read, the date interval (nc+3), the
		// sort direction (nc+4, nc+5).
		tableField, nc := rec.str(0)
		if tableField == nil || *tableField == "" {
			continue
		}
		fieldName, groupTable := *tableField, ""
		if dot := strings.IndexByte(*tableField, '.'); dot >= 0 {
			fieldName = (*tableField)[dot+1:]
			if dot > 0 {
				groupTable = (*tableField)[:dot]
			}
		}
		if fieldName == "" {
			continue
		}
		dateCode, sortCode := 0, 0
		if nc+3 < len(rec.Data) {
			dateCode = int(rec.Data[nc+3])
		}
		if nc+4 < len(rec.Data) {
			sortCode = int(rec.Data[nc+4])
		}
		sortOrder := GroupSortOrder("Ascending")
		switch sortCode {
		case 1:
			sortOrder = "Descending"
		case 2:
			sortOrder = "OriginalOrder"
		case 3:
			sortOrder = "Specified"
		}
		condition := GroupCondition("EachValue")
		if isDateGroupField(report, groupTable, fieldName) {
			condition = dateInterval(dateCode)
		}
		// After the tail: the "Others" string, a UInt16 N for a top/bottom N, a
		// UInt16 discard flag, and the name the others are gathered under.
		var topN *GroupTopN
		othersAt := nc + 6
		if othersAt+4 <= len(rec.Data) {
			if s, used := rec.str(othersAt); s != nil {
				p := othersAt + used
				if p+4 <= len(rec.Data) {
					n := int(rec.Data[p])<<8 | int(rec.Data[p+1])
					discard := (int(rec.Data[p+2])<<8 | int(rec.Data[p+3])) != 0
					othersName := rec.strOr(p+4, "Others")
					if n > 0 {
						topN = &GroupTopN{Count: n, DiscardOthers: discard, OthersName: othersName, Function: AggSum}
					}
				}
			}
		}
		report.Groups = append(report.Groups, &GroupDefinition{
			Level: level, FieldName: fieldName, TableName: groupTable,
			SortOrder: sortOrder, Condition: condition, TopN: topN,
		})
		level++
	}
}

func dateInterval(code int) GroupCondition {
	names := []GroupCondition{"Daily", "Weekly", "Biweekly", "Semimonthly", "Monthly", "Quarterly",
		"Semiannually", "Annually", "BySecond", "ByMinute", "ByHour", "ByAmPm"}
	if code >= 1 && code < len(names) {
		return names[code]
	}
	return "Daily"
}

func isDateGroupField(report *reportBuilder, table, field string) bool {
	if strings.HasPrefix(field, "@") {
		formula := field[1:]
		for _, f := range report.formulaFields() {
			if eqFold(strings.TrimLeft(f.Name, "@"), formula) && f.ResultType != nil && *f.ResultType == "DateTime" {
				return true
			}
		}
		return false
	}
	for _, f := range report.dbFields() {
		if eqFold(f.ColumnName, field) &&
			(table == "" || f.TableName == "" || eqFold(f.TableName, table)) &&
			f.DataType == "DateTime" {
			return true
		}
	}
	return false
}

// extractSortFields: a tag-41 record is the sorted field's name, then [0] kind
// (0 database field, 1 formula, 2 summary = a group's top or bottom N), [1]
// zero, [2] unknown, [3] direction (0 ascending, 1 descending). A record sort
// on a group's own field is folded into the group, as Crystal does.
func extractSortFields(records []Record, report *reportBuilder) {
	var awaiting []*GroupDefinition
	for _, g := range report.Groups {
		if g.TopN != nil {
			awaiting = append(awaiting, g)
		}
	}
	for _, rec := range records {
		if rec.Tag != tagSortField {
			continue
		}
		name, used := rec.str(0)
		if name == nil || *name == "" || used+4 > len(rec.Data) {
			continue
		}
		kind := rec.Data[used]
		direction := rec.Data[used+3]
		if kind == 2 {
			if len(awaiting) == 0 {
				continue
			}
			group := awaiting[0]
			awaiting = awaiting[1:]
			dot := strings.IndexByte(*name, '.')
			var fn *AggregateFunction
			summaryTable := ""
			if dot > 0 {
				fn, summaryTable = parseSummaryPrefix((*name)[:dot])
			}
			if fn == nil || dot <= 0 {
				group.TopN = nil
				continue
			}
			group.TopN.Function = *fn
			group.TopN.TableName = summaryTable
			group.TopN.FieldName = (*name)[dot+1:]
			group.TopN.Bottom = direction == 0
			continue
		}
		if kind > 2 {
			continue
		}
		table, field := "", *name
		if kind == 0 {
			if dot := strings.IndexByte(*name, '.'); dot > 0 {
				table, field = (*name)[:dot], (*name)[dot+1:]
			}
		} else if !strings.HasPrefix(field, "@") {
			field = "@" + field
		}
		grouped := false
		for _, g := range report.Groups {
			if eqFold(g.FieldName, field) && eqFold(g.TableName, table) {
				grouped = true
			}
		}
		if grouped {
			continue
		}
		dir := SortDirection("Ascending")
		if direction == 1 {
			dir = "Descending"
		}
		report.SortFields = append(report.SortFields, &SortField{FieldName: field, TableName: table, Direction: dir})
	}
	for _, g := range awaiting {
		g.TopN = nil
	}
}

// parseSummaryPrefix splits "<Function> of Table" (the part before the first
// dot of a summary reference); "Percentage of <Function> of ..." is compound.
func parseSummaryPrefix(tablePart string) (*AggregateFunction, string) {
	const percentPrefix = "Percentage of "
	if strings.HasPrefix(tablePart, percentPrefix) {
		_, remainder := parseSummaryPrefix(tablePart[len(percentPrefix):])
		p := AggPercentage
		return &p, remainder
	}
	sep := strings.Index(tablePart, " of ")
	if sep <= 0 {
		return nil, tablePart
	}
	var fn AggregateFunction
	switch tablePart[:sep] {
	case "Sum":
		fn = AggSum
	case "Count":
		fn = AggCount
	case "DistinctCount", "Distinct Count":
		fn = AggDistinctCount
	case "Average", "Avg":
		fn = AggAverage
	case "Max", "Maximum":
		fn = AggMaximum
	case "Min", "Minimum":
		fn = AggMinimum
	case "StdDev":
		fn = AggStandardDeviation
	case "Variance":
		fn = AggVariance
	default:
		return nil, tablePart
	}
	return &fn, tablePart[sep+4:]
}

// ---- numeric-ness ----

func isNumericDataType(t string) bool {
	switch t {
	case "Int16", "Int32", "Float32", "Float64", "Currency":
		return true
	}
	return false
}

func isNumericRunningTotal(report *reportBuilder, name string) bool {
	var total *RunningTotalField
	for _, f := range report.Fields {
		if rt, ok := f.(*RunningTotalField); ok && eqFold(strings.TrimLeft(rt.Name, "#"), name) {
			total = rt
			break
		}
	}
	if total == nil {
		return false
	}
	if total.Function == AggCount || total.Function == AggDistinctCount {
		return true
	}
	if isNumericDataType(total.DataType) {
		return true
	}
	src := total.SummarizedFieldName
	return src != "" && (isNumericColumn(report, src) || (strings.HasPrefix(src, "@") && isNumericFormula(report, src[1:], nil)))
}

func isNumericColumn(report *reportBuilder, ref string) bool {
	matches := func(column string) bool {
		for _, f := range report.dbFields() {
			if eqFold(f.ColumnName, column) && isNumericDataType(f.DataType) {
				return true
			}
		}
		return false
	}
	if matches(ref) {
		return true
	}
	dot := strings.IndexByte(ref, '.')
	return dot > 0 && matches(ref[dot+1:])
}

func isNumericFormula(report *reportBuilder, formulaName string, visiting map[string]bool) bool {
	var formula *FormulaField
	for _, f := range report.formulaFields() {
		if eqFold(f.Name, formulaName) {
			formula = f
			break
		}
	}
	if formula == nil {
		return false
	}
	if formula.ResultType != nil {
		return *formula.ResultType == "Float64" || *formula.ResultType == "Currency"
	}
	if formula.Syntax != "Crystal" {
		return false
	}
	if visiting == nil {
		visiting = map[string]bool{}
	}
	key := strings.ToLower(formulaName)
	if visiting[key] {
		return false
	}
	visiting[key] = true
	return formulaIsNumeric(formula.FormulaText,
		func(ref string) bool { return isNumericColumn(report, ref) },
		func(name string) bool { return isNumericFormula(report, name, visiting) })
}

// ---- QESession dependency graph ----

func applyDependencyGraph(report *reportBuilder, graph *qeGraph, warnings *[]string) {
	if len(graph.tables) == 0 {
		return
	}
	var tables []*TableDefinition
	for _, t := range graph.tables {
		td := &TableDefinition{Name: t.name, Alias: t.alias, Columns: []*ColumnDefinition{}}
		for _, f := range t.fields {
			td.Columns = append(td.Columns, &ColumnDefinition{Name: f.name, DataType: mapQeValueType(f.valueType)})
		}
		tables = append(tables, td)
	}
	report.DataSources = append(report.DataSources, &DataSource{
		Name: "DataSource1", Kind: dataSourceKindFromDriver(graph.driverName),
		DatabaseName: graph.databaseName, Tables: tables,
	})

	type tc struct{ table, column string }
	byID := map[int]tc{}
	for _, t := range graph.tables {
		for _, f := range t.fields {
			byID[f.id] = tc{t.alias, f.name}
		}
	}
	for _, l := range graph.links {
		src, ok1 := byID[l.sourceFieldID]
		dst, ok2 := byID[l.targetFieldID]
		if !ok1 || !ok2 {
			*warnings = append(*warnings, fmt.Sprintf("QESession: link %d refers to a field the graph does not list; skipped.", l.id))
			continue
		}
		join := TableJoinType("Inner")
		switch l.joinTypeCode {
		case 2:
			join = "LeftOuter"
		case 3:
			join = "RightOuter"
		case 4:
			join = "FullOuter"
		}
		op := LinkOperator("Unknown")
		if l.operatorCode == 4 {
			op = "Equal"
		}
		report.TableLinks = append(report.TableLinks, &TableLink{
			SourceTable: src.table, SourceColumn: src.column, TargetTable: dst.table, TargetColumn: dst.column,
			JoinType: join, Operator: op,
		})
	}

	// A column named without its table takes the one table that has it.
	for _, f := range report.dbFields() {
		if f.TableName != "" {
			continue
		}
		var owners []string
		for _, t := range graph.tables {
			has := false
			for _, gf := range t.fields {
				if eqFold(gf.name, f.ColumnName) {
					has = true
				}
			}
			if !has {
				continue
			}
			dup := false
			for _, o := range owners {
				if eqFold(o, t.alias) {
					dup = true
				}
			}
			if !dup {
				owners = append(owners, t.alias)
			}
		}
		if len(owners) == 1 {
			f.TableName = owners[0]
		}
	}
}
