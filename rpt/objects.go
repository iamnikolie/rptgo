package rpt

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---- field objects ----

type borders struct {
	l, r, t, b byte
	shadow     bool
	backColor  *string
	widthTwips int
	color      *string
}

type numericFormat struct {
	decimals           int
	thousands, decimal string
	currency           string
	apply, showSymbol  bool
	suppressZero       bool
	negative           int
}

// parseFieldObject: tag-159 (wrapper with nested 158 and the field reference),
// format records, tag-160.
func parseFieldObject(records []Record, start int, report *reportBuilder) (ReportObject, int) {
	wrapper := records[start]
	bounds := extractObjectBounds(wrapper)
	objectName := extractObjectName(wrapper)
	name := objectName

	// The "Table.Column" reference in the wrapper's payload; a summary carries a
	// function prefix: "Sum of Table.Column".
	tableName, fieldRef := extractFieldRefFull(wrapper)
	var summaryFunction *AggregateFunction
	if tableName != nil {
		if fn, rest := parseSummaryPrefix(*tableName); fn != nil {
			summaryFunction = fn
			tableName = &rest
		}
	}
	if fieldRef != nil && *fieldRef != "" {
		name = *fieldRef
		if tableName != nil && *tableName != "" && report != nil {
			for _, f := range report.dbFields() {
				if eqFold(f.ColumnName, *fieldRef) {
					if f.TableName == "" {
						f.TableName = *tableName
					}
					break
				}
			}
		}
	}

	next := start + 1
	format := newFormat()
	var foreColor, dateFormat *string
	usesMachineFormats := false
	var numeric *numericFormat
	suppressZero := false
	var border *borders
	hAlign := AlignDefault
	canGrow, suppressed := false, false
	conditions := []ConditionalFormat{}
	for next < len(records) && records[next].Tag != tagFieldObjectEnd {
		r := records[next]
		if r.Tag == tagConditionalFormat {
			if cf := extractConditionalFormat(r); cf != nil {
				conditions = append(conditions, *cf)
			}
		}
		switch r.Tag {
		case tagFont:
			format = extractFontFormat(r)
		case tagFontColourProps:
			foreColor = extractForeColor(r)
		case tagObjectProps:
			hAlign, canGrow = extractObjectProps(r)
			suppressed = extractObjectSuppressed(r)
		case tagCommonFormat:
			usesMachineFormats = usesMachineFormats || extractUsesMachineFormats(r)
		case tagDateFormat:
			if dateFormat == nil {
				dateFormat = extractDateFormat(r)
			}
		case tagNumericFormat:
			// Last wins: an object carries two and the second is the effective one.
			if n := extractNumericFormat(r); n != nil {
				suppressZero = suppressZero || n.suppressZero
				numeric = n
			}
		case tagObjectBorder:
			if border == nil {
				border = extractBorders(r)
			}
		}
		next++
	}
	if next < len(records) {
		next++
	}

	// Every field object carries a date record; it means something only on a date field.
	isDateField := false
	if report != nil && name != "" {
		for _, f := range report.dbFields() {
			if eqFold(f.ColumnName, name) && f.DataType == "DateTime" {
				isDateField = true
				break
			}
		}
	}
	if !isDateField || usesMachineFormats {
		dateFormat = nil
	}

	// Likewise the numeric record: applied only to a field the report calls a number.
	isNumericField := report != nil && name != "" &&
		(isNumericColumn(report, name) ||
			(strings.HasPrefix(name, "@") && isNumericFormula(report, name[1:], nil)) ||
			(strings.HasPrefix(name, "#") && isNumericRunningTotal(report, name[1:])) ||
			(strings.HasPrefix(name, "%") && isNumericFormula(report, name, nil)))
	if report != nil && report.numberLanguage == nil && numeric != nil {
		report.numberLanguage = languageForSeparators(numeric.thousands, numeric.decimal)
	}
	var numberFormat *string
	if isNumericField && !isDateField && numeric != nil {
		numberFormat = buildNumericFormat(*numeric, usesMachineFormats)
	}
	if dateFormat == nil {
		dateFormat = numberFormat
	}
	if dateFormat == nil && summaryFunction != nil && *summaryFunction == AggPercentage {
		dateFormat = strPtr("0.00'%'")
	}

	if foreColor != nil || hAlign != AlignDefault || dateFormat != nil || border != nil || len(conditions) > 0 || canGrow {
		format = withExtras(format, foreColor, hAlign, border, canGrow, conditions)
		format.FormatString = dateFormat
	}
	format.Suppress = suppressed
	// Either numeric record may carry it (seen set on the first only, with
	// the zero left blank in Crystal's own export).
	format.SuppressIfZero = isNumericField && suppressZero
	if isNumericField && numeric != nil {
		format.NegativeFormat = numeric.negative
	}
	return &FieldObject{
		FieldName: name, SummaryFunction: summaryFunction,
		ObjectBase: ObjectBase{Name: objectName, Bounds: bounds, Format: format},
	}, next
}

// withExtras is the C# `new ObjectFormat { font's fields, colour, alignment,
// borders, can grow, conditions }`.
func withExtras(font ObjectFormat, foreColor *string, hAlign HorizontalAlignment, b *borders,
	canGrow bool, conditions []ConditionalFormat) ObjectFormat {
	f := newFormat()
	f.FontName, f.FontSize, f.Bold, f.Italic, f.Underline = font.FontName, font.FontSize, font.Bold, font.Italic, font.Underline
	f.ForeColor = foreColor
	f.HAlign = hAlign
	if b != nil {
		f.BorderLeft, f.BorderRight, f.BorderTop, f.BorderBottom = b.l, b.r, b.t, b.b
		f.DropShadow, f.BackColor, f.BorderWidthTwips, f.BorderColor = b.shadow, b.backColor, b.widthTwips, b.color
	}
	f.CanGrow = canGrow
	f.Conditions = conditions
	return f
}

// extractFieldRefFull scans the wrapper's payload for a "Table.Field" string;
// failing that, the first readable string (a prefixed @/?/#/% reference however
// short, anything else from five characters).
func extractFieldRefFull(wrapper Record) (table, column *string) {
	var fallback *string
	for off := 16; off+8 < len(wrapper.Data); off++ {
		s, nc, ok := wrapper.String(off)
		if nc <= 0 || !ok || s == "" {
			continue
		}
		dot := strings.IndexByte(s, '.')
		if dot > 0 && dot < len(s)-1 && ulen(s) >= 5 {
			t, c := s[:dot], s[dot+1:]
			return &t, &c
		}
		prefixed := ulen(s) >= 2 && strings.ContainsRune("@?#%", rune(s[0]))
		if fallback == nil && (ulen(s) >= 5 || prefixed) && allASCIIPrintable(s) {
			v := s
			fallback = &v
		}
	}
	return nil, fallback
}

// ---- text objects ----

// parseTextObject: tag-165, then paragraphs (192), static text (194) and field
// embeds (196), tag-166. The paragraph's alignment wins over the object's.
func parseTextObject(records []Record, start int) (ReportObject, int) {
	wrapper := records[start]
	bounds := extractObjectBounds(wrapper)
	var text strings.Builder
	next := start + 1
	format := newFormat()
	var foreColor *string
	var border *borders
	hAlign := AlignDefault
	var paragraphAlign *HorizontalAlignment
	canGrow, suppressed := false, false
	conditions := []ConditionalFormat{}
	for next < len(records) && records[next].Tag != tagTextObjectEnd {
		r := records[next]
		if r.Tag == tagConditionalFormat {
			if cf := extractConditionalFormat(r); cf != nil {
				conditions = append(conditions, *cf)
			}
		}
		switch r.Tag {
		case tagFont:
			format = extractFontFormat(r)
		case tagFontColourProps:
			foreColor = extractForeColor(r)
		case tagObjectBorder:
			if border == nil {
				border = extractBorders(r)
			}
		case tagObjectProps:
			hAlign, canGrow = extractObjectProps(r)
			suppressed = extractObjectSuppressed(r)
		case tagTextParagraph:
			if paragraphAlign == nil {
				paragraphAlign = extractParagraphAlignment(r)
			}
			// Each paragraph after the first starts a new line.
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
		case tagTextStaticSection:
			if s, _, ok := r.String(0); ok && s != "" {
				text.WriteString(strings.TrimRight(s, "\t\n\r"))
			}
		case tagTextFieldSection:
			if s, _, ok := r.String(0); ok && s != "" {
				text.WriteString("{" + s + "}")
			}
		}
		next++
	}
	if next < len(records) {
		next++
	}
	if paragraphAlign != nil {
		hAlign = *paragraphAlign
	}
	if foreColor != nil || hAlign != AlignDefault || border != nil || len(conditions) > 0 || canGrow {
		format = withExtras(format, foreColor, hAlign, border, canGrow, conditions)
	}
	format.Suppress = suppressed
	// A label typed with a final Enter ends in an empty paragraph.
	t := strings.TrimRight(text.String(), "\n")
	name := extractObjectName(wrapper)
	if t == "" {
		t = name
	}
	return &TextObject{Text: t, ObjectBase: ObjectBase{Name: name, Bounds: bounds, Format: format}}, next
}

// ---- pictures, blobs, subreports ----

// parsePictureObject: tag-175 -> 174 -> 158 gives size and name; a tag-189
// holds the index N of the "Embedding N" storage with the image bytes.
func parsePictureObject(records []Record, start int) (ReportObject, int) {
	wrapper := records[start]
	bounds, name := ObjectBounds{}, ""
	if inner := child(wrapper, 174); inner != nil {
		bounds, name = extractObjectBounds(*inner), extractObjectName(*inner)
	}
	embedding := 0
	next := start + 1
	for next < len(records) && records[next].Tag != tagPictureObjectEnd {
		if records[next].Tag == tagOleObjectRef && len(records[next].Data) >= 4 {
			embedding = records[next].Int32(0)
		}
		next++
	}
	if next < len(records) {
		next++
	}
	if embedding <= 0 {
		return nil, next
	}
	return &ImageObject{Source: "Embedded", EmbeddingIndex: embedding,
		ObjectBase: ObjectBase{Name: name, Bounds: bounds, Format: newFormat()}}, next
}

func parseBlobFieldObject(records []Record, start int) (ReportObject, int) {
	wrapper := records[start]
	bounds, name := extractObjectBounds(wrapper), extractObjectName(wrapper)
	_, fieldRef := extractFieldRefFull(wrapper)
	next := start + 1
	for next < len(records) && records[next].Tag != tagBlobFieldObjectEnd {
		next++
	}
	if next < len(records) {
		next++
	}
	if fieldRef == nil || *fieldRef == "" {
		return nil, next
	}
	return &ImageObject{Source: "Database", FieldName: *fieldRef,
		ObjectBase: ObjectBase{Name: name, Bounds: bounds, Format: newFormat()}}, next
}

// parseSubreportObject: the Int32 right after the nested tag-158 block is the
// index N of the "Subdocument N" storage holding the inner report.
func parseSubreportObject(records []Record, start int) (ReportObject, int) {
	wrapper := records[start]
	bounds, name := extractObjectBounds(wrapper), extractObjectName(wrapper)
	subdoc := 0
	if ch := child(wrapper, tagReportObjectHeader); ch != nil {
		subdoc = wrapper.Int32(8 + len(ch.Data))
	}
	var border *borders
	next := start + 1
	for next < len(records) && records[next].Tag != tagSubreportObjectEnd {
		if records[next].Tag == tagObjectBorder && border == nil {
			border = extractBorders(records[next])
		}
		next++
	}
	if next < len(records) {
		next++
	}
	if subdoc <= 0 {
		return nil, next
	}
	f := newFormat()
	if border != nil {
		f.BorderLeft, f.BorderRight, f.BorderTop, f.BorderBottom = border.l, border.r, border.t, border.b
		f.DropShadow, f.BackColor, f.BorderWidthTwips, f.BorderColor = border.shadow, border.backColor, border.widthTwips, border.color
	}
	subName := name
	if subName == "" {
		subName = fmt.Sprintf("Subreport%d", subdoc)
	}
	return &SubreportObject{SubreportName: subName, SubdocumentIndex: subdoc,
		ParameterValueOverrides: map[string]any{},
		ObjectBase:              ObjectBase{Name: name, Bounds: bounds, Format: f}}, next
}

// ---- format records ----

// extractFontFormat: tag-8 after the face name (nc bytes): [nc+4] size in
// points, [nc+5..8] style flags (1 underline, 0x10000 italic), [nc+9..12]
// weight, [nc+13..16] size in twips, used when within a point of the byte.
func extractFontFormat(r Record) ObjectFormat {
	if len(r.Data) < 14 {
		return newFormat()
	}
	name, nc := r.str(0)
	if nc <= 0 || len(r.Data) < nc+13 {
		return newFormat()
	}
	sizeByte := int(r.Data[nc+4])
	size := float64(sizeByte)
	if len(r.Data) >= nc+17 {
		precise := float64(r.Int32(nc+13)) / 20.0
		if precise > 0 && math.Abs(precise-float64(sizeByte)) < 1.0 {
			size = precise
		}
	}
	flags := 0
	if len(r.Data) >= nc+9 {
		flags = r.Int32(nc + 5)
	}
	weight := r.Int32(nc + 9)
	f := newFormat()
	f.FontName = name
	if size > 0 {
		f.FontSize = &size
	}
	f.Bold = weight >= 600
	f.Italic = flags&0x00010000 != 0
	f.Underline = flags&0x00000001 != 0
	return f
}

// extractForeColor: tag-257 -> tag-256, flag, B, G, R; black is no colour.
func extractForeColor(r Record) *string {
	ch := childMin(r, tagFontColour, 4)
	if ch == nil {
		return nil
	}
	b, g, rr := ch.Data[1], ch.Data[2], ch.Data[3]
	if rr == 0 && g == 0 && b == 0 {
		return nil
	}
	return strPtr(fmt.Sprintf("#%02X%02X%02X", rr, g, b))
}

// extractConditionalFormat: one Highlighting Expert rule: [0..3] operator LE
// (3 less than, 5 greater than), a length-prefixed ASCII threshold, padding, a
// second value, padding, then font and background colours as flag,B,G,R.
func extractConditionalFormat(r Record) *ConditionalFormat {
	d := r.Data
	if len(d) < 16 {
		return nil
	}
	var op ConditionalOperator
	switch int(d[0]) | int(d[1])<<8 | int(d[2])<<16 | int(d[3])<<24 {
	case 3:
		op = "LessThan"
	case 5:
		op = "GreaterThan"
	default:
		return nil
	}
	p := 4
	threshold, ok := readASCIINumber(d, &p)
	if !ok {
		return nil
	}
	p += 3
	if p >= len(d) {
		return nil
	}
	p += 1 + int(d[p])
	p++
	if p+8 > len(d) {
		return nil
	}
	colourAt := func(i int) *string {
		if d[i] != 0 {
			return nil
		}
		return strPtr(fmt.Sprintf("#%02X%02X%02X", d[i+3], d[i+2], d[i+1]))
	}
	font, back := colourAt(p), colourAt(p+4)
	if font == nil && back == nil {
		return nil
	}
	return &ConditionalFormat{Operator: op, Threshold: threshold, FontColor: font, BackColor: back}
}

func readASCIINumber(d []byte, pos *int) (float64, bool) {
	if *pos >= len(d) {
		return 0, false
	}
	n := int(d[*pos])
	if n == 0 || *pos+1+n > len(d) {
		return 0, false
	}
	text := strings.TrimRight(string(d[*pos+1:*pos+1+n]), "\x00")
	*pos += 1 + n
	v, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	return v, err == nil
}

// extractBorders: tag-237 -> tag-236: [0..3] edge styles left, right, top,
// bottom (0 none, 1 single, 2 double, ...), [9] drop shadow, [10..13] border
// colour, [14..17] background (flag, B, G, R), [18..21] width in twips.
func extractBorders(r Record) *borders {
	ch := childMin(r, tagObjectBorder-1, 22)
	if ch == nil {
		return nil
	}
	d := ch.Data
	var back, color *string
	if d[14] == 0 {
		back = strPtr(fmt.Sprintf("#%02X%02X%02X", d[17], d[16], d[15]))
	}
	if d[10] == 0 && (d[11]|d[12]|d[13]) != 0 {
		color = strPtr(fmt.Sprintf("#%02X%02X%02X", d[13], d[12], d[11]))
	}
	width := int(int32(uint32(d[18])<<24 | uint32(d[19])<<16 | uint32(d[20])<<8 | uint32(d[21])))
	if d[0] == 0 && d[1] == 0 && d[2] == 0 && d[3] == 0 && d[9] == 0 && back == nil {
		return nil
	}
	return &borders{d[0], d[1], d[2], d[3], d[9] != 0, back, width, color}
}

// extractObjectProps: tag-253 -> tag-252: [2] alignment, [9] Can Grow.
func extractObjectProps(r Record) (HorizontalAlignment, bool) {
	ch := childMin(r, tagObjectPropsInner, 3)
	if ch == nil {
		return AlignDefault, false
	}
	canGrow := len(ch.Data) >= 10 && ch.Data[9] != 0
	return alignmentFromCode(ch.Data[2]), canGrow
}

// extractObjectSuppressed: tag-253 -> tag-252 [1] is 1 on a printing object
// and 0 on one with "Suppress" ticked (a label Crystal's export never
// prints).
func extractObjectSuppressed(r Record) bool {
	ch := childMin(r, tagObjectPropsInner, 3)
	return ch != nil && ch.Data[1] == 0
}

func alignmentFromCode(code byte) HorizontalAlignment {
	switch code {
	case 1:
		return AlignLeft
	case 2:
		return AlignCenter
	case 3:
		return AlignRight
	case 4:
		return AlignJustify
	}
	return AlignDefault
}

func extractParagraphAlignment(r Record) *HorizontalAlignment {
	if len(r.Data) < 13 {
		return nil
	}
	a := alignmentFromCode(r.Data[12])
	return &a
}

// extractNumericFormat: tag-249 -> tag-248: [2] currency symbol shown, [4]
// symbol and grouping apply, [8] decimals; from [17] length-prefixed slots:
// thousands separator, decimal separator, currency symbol, format name.
func extractNumericFormat(r Record) *numericFormat {
	ch := childMin(r, tagNumericFormatInner, 20)
	if ch == nil {
		return nil
	}
	d := ch.Data
	var slots []string
	for pos := 17; pos < len(d) && len(slots) < 4; {
		n := int(d[pos])
		if n == 0 {
			pos++
			continue
		}
		if pos+1+n > len(d) {
			break
		}
		var sb []rune
		for i := 0; i < n; i++ {
			if c := d[pos+1+i]; c != 0 {
				sb = append(sb, rune(c))
			}
		}
		slots = append(slots, string(sb))
		pos += 1 + n
	}
	// [1] suppress if zero; [2] the negative format (1 leading minus, 3 in
	// brackets); [10] the currency symbol (0 none, 1 fixed, 2 floating). The
	// C# reader takes [2] for the symbol, which puts a "$" on numbers
	// Crystal's own export prints without one.
	nf := &numericFormat{decimals: int(d[8]), apply: d[4] != 0, showSymbol: d[10] != 0, suppressZero: d[1] != 0, negative: int(d[2])}
	if len(slots) > 0 {
		nf.thousands = slots[0]
	}
	if len(slots) > 1 {
		nf.decimal = slots[1]
	}
	// With no symbol the format's name slides into the third slot.
	if len(slots) > 2 && !strings.HasPrefix(slots[2], "<") {
		nf.currency = slots[2]
	}
	return nf
}

// buildNumericFormat: a .NET numeric format string for the record, or nil.
// Grouping and symbol only when the record applies them; a prefix symbol on
// the object's own formats ends in a quoted no-break space, as Crystal prints it.
func buildNumericFormat(n numericFormat, machineFormats bool) *string {
	if n.decimals < 0 || n.decimals > 9 {
		return nil
	}
	if n.currency == "%" {
		return nil
	}
	number := "0"
	if n.apply && n.thousands == "," {
		number = "#,##0"
	}
	if n.decimals > 0 {
		number += "." + strings.Repeat("0", n.decimals)
	}
	trailer := ""
	if n.showSymbol && !machineFormats && n.currency != "" && !strings.HasPrefix(n.currency, " ") {
		trailer = "\" \""
	}
	if !n.apply || !n.showSymbol || n.currency == "" {
		return strPtr(number + trailer)
	}
	symbol := "\"" + n.currency + "\""
	if strings.HasPrefix(n.currency, " ") {
		return strPtr(number + symbol)
	}
	return strPtr(symbol + number + trailer)
}

func languageForSeparators(thousands, decimal string) *string {
	switch {
	case thousands == "," && decimal == ".":
		return strPtr("en-US")
	case thousands == "." && decimal == ",":
		return strPtr("de-DE")
	case thousands == "" && decimal == ".":
		return strPtr("en-US")
	}
	return nil
}

// extractUsesMachineFormats: tag-241 -> tag-240, two Int16 flags; the second
// is "use the machine's own formats".
func extractUsesMachineFormats(r Record) bool {
	ch := childMin(r, tagCommonFormatInner, 4)
	return ch != nil && ch.Uint16(2) != 0
}

func extractDateFormat(r Record) *string {
	ch := childMin(r, tagDateFormatInner, 8)
	if ch == nil {
		return nil
	}
	return buildDateFormat(*ch)
}

// buildDateFormat: eight component bytes ([0] order, [1] year, [2] month, [3]
// day, [4] day of week) and strings: prefix, first separator, second separator,
// suffix, day-of-week separator, then the day of week's position byte.
func buildDateFormat(ch Record) *string {
	d := ch.Data
	if len(d) < 8 {
		return nil
	}
	const unreadable = "\x00"
	pick := func(b byte, opts ...string) string {
		if int(b) < len(opts) {
			return opts[b]
		}
		return unreadable
	}
	year := pick(d[1], "yy", "yyyy", "")
	month := pick(d[2], "M", "MM", "MMM", "MMMM", "")
	day := pick(d[3], "d", "dd", "")
	if year == unreadable || month == unreadable || day == unreadable {
		return nil
	}
	var ordered []string
	switch d[0] {
	case 0:
		ordered = []string{year, month, day}
	case 1:
		ordered = []string{day, month, year}
	case 2:
		ordered = []string{month, day, year}
	}
	var parts []string
	for _, p := range ordered {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nil
	}

	var strs []string
	afterStrings := 8
	for pos := 8; len(strs) < 5; {
		s, consumed, ok := ch.String(pos)
		if consumed <= 0 || !ok {
			break
		}
		strs = append(strs, s)
		pos += consumed
		afterStrings = pos
	}
	if len(strs) < 4 {
		return nil
	}
	for _, s := range strs {
		for _, c := range s {
			if c < ' ' || c > '~' || c == '\'' {
				return nil
			}
		}
	}
	prefix, first, second, suffix := strs[0], strs[1], strs[2], strs[3]
	quote := func(s string) string {
		if s == "" {
			return ""
		}
		return "'" + s + "'"
	}
	var date string
	switch len(parts) {
	case 3:
		date = parts[0] + quote(first) + parts[1] + quote(second) + parts[2]
	case 2:
		sep := first
		if ordered[0] == "" {
			sep = second
		}
		date = parts[0] + quote(sep) + parts[1]
	default:
		date = parts[0]
	}
	date = quote(prefix) + date + quote(suffix)

	dow := pick(d[4], "ddd", "dddd", "")
	if dow == unreadable {
		return nil
	}
	if dow != "" {
		if len(strs) < 5 || afterStrings >= len(d) {
			return nil
		}
		sep := quote(strs[4])
		switch d[afterStrings] {
		case 0:
			date = dow + sep + date
		case 1:
			date = date + sep + dow
		default:
			return nil
		}
	}
	return &date
}
