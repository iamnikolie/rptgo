package rpt

import (
	"fmt"
	"strings"
)

// Cross-tabs (RptParser.CrossTab.cs). Between the tag-185 wrapper and its
// tag-186: column-axis levels (206 ... 207), row-axis levels (210 ... 211), and
// grid cells (215 ... 216, a tag-214 box and the 161/162 value cells). No file
// in the test corpus has one; this follows the C# without a check here.

const (
	tagCrossTabDefinition       = 184
	tagCrossTabColumnLevelStart = 206
	tagCrossTabColumnLevelEnd   = 207
	tagCrossTabRowLevelStart    = 210
	tagCrossTabRowLevelEnd      = 211
	tagCrossTabGridCellStart    = 215
	tagCrossTabGridCellEnd      = 216
	tagCrossTabGridCellBox      = 214
	tagCrossTabTextStart        = 167
	tagCrossTabTextEnd          = 168
	currencyTrailer             = "\" \""
)

func parseCrossTabObject(records []Record, start int, report *reportBuilder) (ReportObject, int) {
	wrapper := records[start]
	ct := &CrossTabObject{ObjectBase: ObjectBase{Name: extractObjectName(wrapper), Bounds: extractObjectBounds(wrapper), Format: newFormat()}}
	next := start + 1
	for next < len(records) && records[next].Tag != tagCrossTabObjectEnd {
		rec := records[next]
		if rec.Tag == tagGroupCondition {
			if tf, _, ok := rec.String(0); ok && tf != "" && strings.Contains(tf, ".") {
				column := tf[strings.IndexByte(tf, '.')+1:]
				var axis *string
				for _, s := range scanStrings(rec) {
					if strings.HasPrefix(s, "Row #") || strings.HasPrefix(s, "Column #") {
						s := s
						axis = &s
						break
					}
				}
				if axis != nil && strings.HasPrefix(*axis, "Row #") {
					ct.RowGroupFields = append(ct.RowGroupFields, column)
				} else if axis != nil {
					ct.ColumnGroupFields = append(ct.ColumnGroupFields, column)
				}
			}
		} else if rec.Tag == tagCrossTabCellStart {
			if ch159 := child(rec, tagFieldObjectStart); ch159 != nil {
				table, column := extractFieldRefFull(*ch159)
				if table != nil && column != nil {
					if fn, _ := parseSummaryPrefix(*table); fn != nil {
						cell := CrossTabCell{FieldName: *column, Function: *fn}
						dup := false
						for _, c := range ct.Cells {
							if c == cell {
								dup = true
							}
						}
						if !dup {
							ct.Cells = append(ct.Cells, cell)
						}
					}
				}
			}
			for next < len(records) && records[next].Tag != tagCrossTabCellEnd {
				next++
			}
		}
		next++
	}
	if next < len(records) {
		next++
	}
	readCrossTabLayout(ct, records, start, next, report)
	if len(ct.RowGroupFields) > 0 || len(ct.ColumnGroupFields) > 0 || len(ct.Cells) > 0 {
		return ct, next
	}
	return nil, next
}

func readCrossTabLayout(ct *CrossTabObject, records []Record, start, end int, report *reportBuilder) {
	readCrossTabSettings(ct, records[start])
	columnLevel, rowLevel := -1, -1
	inColumn, inRow := false, false
	var levelField *string
	var headerFormat, totalLabelFormat *ObjectFormat
	var headerBounds *ObjectBounds
	var totalLabel, totalBackColor *string
	var gridCell *CrossTabCellLayout

	for i := start + 1; i < end; i++ {
		rec := records[i]
		switch {
		case rec.Tag == tagCrossTabColumnLevelStart || rec.Tag == tagCrossTabRowLevelStart:
			inColumn = rec.Tag == tagCrossTabColumnLevelStart
			inRow = !inColumn
			if inColumn {
				columnLevel++
			} else {
				rowLevel++
			}
			levelField, headerFormat, headerBounds, totalLabel, totalLabelFormat = nil, nil, nil, nil, nil
			totalBackColor = readLevelTotalColour(rec)
		case rec.Tag == tagCrossTabColumnLevelEnd || rec.Tag == tagCrossTabRowLevelEnd:
			lvl := rowLevel
			if inColumn {
				lvl = columnLevel
			}
			level := &CrossTabAxisLevel{Level: lvl, HeaderFormat: headerFormat, HeaderBounds: headerBounds,
				TotalLabel: totalLabel, TotalLabelFormat: totalLabelFormat, TotalBackColor: totalBackColor}
			if inColumn {
				ct.ColumnLevels = append(ct.ColumnLevels, level)
			} else {
				ct.RowLevels = append(ct.RowLevels, level)
			}
			inColumn, inRow = false, false
		case rec.Tag == tagGroupCondition && (inColumn || inRow):
			if tf, _, ok := rec.String(0); ok && tf != "" {
				if strings.Contains(tf, ".") {
					tf = tf[strings.IndexByte(tf, '.')+1:]
				}
				levelField = &tf
			}
		case rec.Tag == tagCrossTabGridCellStart:
			gridCell = readGridCellBox(rec)
			if gridCell != nil {
				ct.CellLayouts = append(ct.CellLayouts, gridCell)
			}
		case rec.Tag == tagCrossTabGridCellEnd:
			gridCell = nil
		case rec.Tag == tagCrossTabCellStart:
			inner := child(rec, tagFieldObjectStart)
			size := ObjectBounds{}
			if inner != nil {
				size = extractObjectBounds(*inner)
			}
			placed := applyPlacement(records, i, size)
			if gridCell != nil {
				var column *string
				var fn *AggregateFunction
				if inner != nil {
					table, col := extractFieldRefFull(*inner)
					if table != nil {
						if f, _ := parseSummaryPrefix(*table); f != nil {
							column, fn = col, f
						}
					}
				}
				gridCell.SummaryFormats = append(gridCell.SummaryFormats, readCrossTabCellFormat(records, &i, column, fn, report))
			} else {
				headerBounds = &placed
				f := readCrossTabCellFormat(records, &i, levelField, nil, report)
				headerFormat = &f
			}
		case rec.Tag == tagCrossTabTextStart:
			text, format := readCrossTabText(records, &i)
			if (inColumn || inRow) && totalLabel == nil {
				totalLabel, totalLabelFormat = &text, &format
			}
		}
	}
}

func readCrossTabSettings(ct *CrossTabObject, wrapper Record) {
	children := wrapper.Children()
	if len(children) > 0 && children[0].Tag == tagCrossTabDefinition {
		def := children[0]
		dc := def.Children()
		if len(dc) > 0 && dc[0].Tag == tagReportObjectHeader {
			if len(dc[0].Data) >= 8 {
				ct.Bounds.Width = max(0, dc[0].Int32(0))
				ct.Bounds.Height = max(0, dc[0].Int32(4))
			}
			s := nestedRecordLength(def.Data, 0)
			if s > 0 && len(def.Data) >= s+16 {
				ct.ShowGrid = def.Uint16(s) != 0
				ct.ColumnHeaderHeightTwips = max(0, def.Int32(s+8))
				ct.RowHeaderWidthTwips = max(0, def.Int32(s+12))
			}
		}
	}
	t := nestedRecordLength(wrapper.Data, 0)
	if t > 0 && len(wrapper.Data) >= t+4 {
		ct.SuppressColumnGrandTotals = wrapper.Uint16(t) != 0
		ct.SuppressRowGrandTotals = wrapper.Uint16(t+2) != 0
	}
}

func readLevelTotalColour(level Record) *string {
	children := level.Children()
	if len(children) == 0 {
		return nil
	}
	inner := child(children[0], 203)
	if inner == nil || len(inner.Data) < 4 || inner.Data[0] != 0 {
		return nil
	}
	return strPtr(fmt.Sprintf("#%02X%02X%02X", inner.Data[3], inner.Data[2], inner.Data[1]))
}

// nestedRecordLength is the full length (header and data) of the record at pos.
func nestedRecordLength(data []byte, pos int) int {
	if pos+2 > len(data) {
		return 0
	}
	n2 := data[pos]
	sizeLen := [4]int{0, 1, 2, 4}[n2>>6&3]
	header := 2
	if n2&0x04 != 0 {
		header += 2
	}
	if n2&0x20 != 0 {
		header += 2
	}
	if pos+header+sizeLen > len(data) {
		return 0
	}
	size := 0
	for k := 0; k < sizeLen; k++ {
		size = size<<8 | int(data[pos+header+k])
	}
	return header + sizeLen + size
}

func readGridCellBox(cell Record) *CrossTabCellLayout {
	box := childMin(cell, tagCrossTabGridCellBox, 20)
	if box == nil {
		return nil
	}
	u16 := func(o int) int { return int(box.Data[o])<<8 | int(box.Data[o+1]) }
	return &CrossTabCellLayout{
		MarginLeftTwips: max(0, box.Int32(0)),
		MarginTopTwips:  max(0, box.Int32(4)),
		Box:             ObjectBounds{Left: u16(8), Top: u16(10), Width: u16(12), Height: u16(14)},
		RowLevel:        u16(16),
		ColumnLevel:     u16(18),
		SummaryFormats:  []ObjectFormat{},
	}
}

func readCrossTabCellFormat(records []Record, i *int, column *string, fn *AggregateFunction, report *reportBuilder) ObjectFormat {
	font := newFormat()
	var foreColor, dateFormat *string
	machine := false
	var numeric *numericFormat
	suppressZero := false
	var border *borders
	var alignCode byte
	for *i++; *i < len(records) && records[*i].Tag != tagCrossTabCellEnd; *i++ {
		r := records[*i]
		switch r.Tag {
		case tagFont:
			font = extractFontFormat(r)
		case tagFontColourProps:
			foreColor = extractForeColor(r)
		case tagObjectProps:
			alignCode = 0
			if ch := childMin(r, tagObjectPropsInner, 3); ch != nil {
				alignCode = ch.Data[2]
			}
		case tagCommonFormat:
			machine = machine || extractUsesMachineFormats(r)
		case tagDateFormat:
			if dateFormat == nil {
				dateFormat = extractDateFormat(r)
			}
		case tagNumericFormat:
			if n := extractNumericFormat(r); n != nil {
				suppressZero = suppressZero || n.suppressZero
				numeric = n
			}
		case tagObjectBorder:
			if border == nil {
				border = extractBorders(r)
			}
		}
	}
	isCount := fn != nil && (*fn == AggCount || *fn == AggDistinctCount)
	isDate := false
	if !isCount && report != nil && column != nil {
		for _, f := range report.dbFields() {
			if eqFold(f.ColumnName, *column) && f.DataType == "DateTime" {
				isDate = true
			}
		}
	}
	isNumber := isCount || (fn != nil && *fn == AggPercentage) ||
		(report != nil && column != nil && (isNumericColumn(report, *column) ||
			(strings.HasPrefix(*column, "@") && isNumericFormula(report, (*column)[1:], nil))))
	if !isDate || machine {
		dateFormat = nil
	}
	if report != nil && report.numberLanguage == nil && numeric != nil {
		report.numberLanguage = languageForSeparators(numeric.thousands, numeric.decimal)
	}
	var format *string
	if isNumber && !isDate && numeric != nil {
		format = buildNumericFormat(*numeric, machine)
	}
	if format != nil && strings.HasSuffix(*format, currencyTrailer) {
		format = strPtr(strings.TrimSuffix(*format, currencyTrailer))
	}
	if format == nil {
		format = dateFormat
	}
	if format == nil && fn != nil && *fn == AggPercentage {
		format = strPtr("0.00'%'")
	}
	align := alignmentFromCode(alignCode)
	if alignCode == 0 {
		align = AlignDefault
		if fn != nil {
			align = AlignRight
		}
	}
	f := font
	f.ForeColor = foreColor
	f.HAlign = align
	f.FormatString = format
	// Either numeric record may carry it, as on a field object.
	f.SuppressIfZero = isNumber && suppressZero
	if border != nil {
		f.BorderLeft, f.BorderRight, f.BorderTop, f.BorderBottom = border.l, border.r, border.t, border.b
		f.BackColor, f.BorderWidthTwips = border.backColor, border.widthTwips
	} else {
		f.BorderLeft, f.BorderRight, f.BorderTop, f.BorderBottom = 0, 0, 0, 0
		f.BackColor, f.BorderWidthTwips = nil, 0
	}
	return f
}

func readCrossTabText(records []Record, i *int) (string, ObjectFormat) {
	var text strings.Builder
	font := newFormat()
	var foreColor *string
	var paragraphAlign *HorizontalAlignment
	var alignCode byte
	var border *borders
	for *i++; *i < len(records) && records[*i].Tag != tagCrossTabTextEnd; *i++ {
		r := records[*i]
		switch r.Tag {
		case tagFont:
			font = extractFontFormat(r)
		case tagFontColourProps:
			foreColor = extractForeColor(r)
		case tagObjectBorder:
			if border == nil {
				border = extractBorders(r)
			}
		case tagObjectProps:
			alignCode = 0
			if ch := childMin(r, tagObjectPropsInner, 3); ch != nil {
				alignCode = ch.Data[2]
			}
		case tagTextParagraph:
			if paragraphAlign == nil {
				paragraphAlign = extractParagraphAlignment(r)
			}
		case tagTextStaticSection:
			if s, _, ok := r.String(0); ok && s != "" {
				text.WriteString(strings.TrimRight(s, "\t\n\r"))
			}
		}
	}
	f := font
	f.ForeColor = foreColor
	f.HAlign = alignmentFromCode(alignCode)
	if paragraphAlign != nil {
		f.HAlign = *paragraphAlign
	}
	if border != nil {
		f.BorderLeft, f.BorderRight, f.BorderTop, f.BorderBottom = border.l, border.r, border.t, border.b
		f.BackColor, f.BorderWidthTwips = border.backColor, border.widthTwips
	} else {
		f.BorderLeft, f.BorderRight, f.BorderTop, f.BorderBottom = 0, 0, 0, 0
		f.BackColor, f.BorderWidthTwips = nil, 0
	}
	return text.String(), f
}
