package rpt

// Parse reads a .rpt into a ReportDefinition: a port of RptParser.cs.
//
// The inflated Contents stream is a flat run of records. Area pairs (130/131
// report, 132/133 page, 134/135 detail, 136/137 group) wrap areas (138 ... 139),
// areas wrap sections (141..154, one wrapper tag per section kind, with a
// nested tag-140 header), and sections are followed by their objects, each a
// wrapper (159 field, 165 text, 170 line, ...) with a nested tag-158 size and
// name, a tag-190 placement right after it, then format records up to the
// object's end tag. Field definitions (115 database, 119 formula, 122
// parameter, 128 running total), groups (229) and sorts (41) are records of
// their own elsewhere in the stream.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ParseResult mirrors the C# ParseResult, minus its diagnostics.
type ParseResult struct {
	Report   *ReportDefinition
	Success  bool
	Warnings []string
	Errors   []string
}

const (
	tagDbFieldDef           = 115
	tagFormulaFieldDef      = 119
	tagParamFieldDef        = 122
	tagCustomFunctionDef    = 335
	tagRunningTotalFieldDef = 128

	tagReportAreaStart = 130
	tagPageAreaStart   = 132
	tagDetailAreaStart = 134
	tagGroupAreaStart  = 136

	tagAreaHeader         = 138
	tagAreaEnd            = 139
	tagAreaCode           = 156
	tagAreaPairCode       = 155
	tagSectionProperties  = 255
	tagSectionCode        = 157
	tagFont               = 8
	tagReportObjectHeader = 158
	tagObjectPlacement    = 190
	tagObjectBorder       = 237
	tagConditionalFormat  = 191
	tagPageSetup          = 398
	tagPrinterSettings    = 7
	tagPageMargins        = 102
	tagSqlExpression      = 129

	tagFieldObjectStart     = 159
	tagFieldObjectEnd       = 160
	tagTextObjectStart      = 165
	tagTextObjectEnd        = 166
	tagSubreportObjectStart = 163
	tagSubreportObjectEnd   = 164
	tagLineObjectStart      = 170
	tagBoxObjectStart       = 172
	tagCrossTabObjectStart  = 185
	tagCrossTabObjectEnd    = 186
	tagCrossTabCellStart    = 161
	tagCrossTabCellEnd      = 162
	tagPictureObjectStart   = 175
	tagPictureObjectEnd     = 176
	tagChartObjectStart     = 180
	tagChartObjectEnd       = 181
	tagChartTypeRecord      = 284
	tagChartSeriesField     = 287
	tagChartDefinition      = 289
	tagChartDetailFieldDef  = 127
	tagBlobFieldObjectStart = 177
	tagBlobFieldObjectEnd   = 178
	tagOleObjectRef         = 189
	tagTextParagraph        = 192
	tagTextStaticSection    = 194
	tagTextFieldSection     = 196
	tagFontColourProps      = 257
	tagFontColour           = 256
	tagGroupCondition       = 229
	tagSortField            = 41
	tagCommonFormat         = 241
	tagCommonFormatInner    = 240
	tagDateFormat           = 243
	tagDateFormatInner      = 242
	tagNumericFormat        = 249
	tagNumericFormatInner   = 248
	tagObjectProps          = 253
	tagObjectPropsInner     = 252
)

// Crystal's per-object and per-section format formulas: they arrive like user
// formulas but drive the Format Editor rather than producing a value.
var objectFormatHookNames = map[string]bool{}

func init() {
	for _, n := range []string{
		"Tool_Tip_Text", "Hyperlink_Text", "Display_String",
		"Font_Style", "Font_Size", "Font_Color", "Font_Name",
		"Top_Line_Style", "Bottom_Line_Style",
		"Currency_Symbol", "Currency_Symbol_Type", "Negative_Type",
		"Date_Order", "Date_First_Separator", "Date_Second_Separator",
		"Suppress_If_Zero", "Suppress_Blank_Section", "Section_Back_Color",
		"New_Page_Before", "New_Page_After", "New_Page_After_N_Records",
		"Reset_Page_N_After",
		"Running Total Condition Formula", "Group Sort Order Formula",
	} {
		objectFormatHookNames[strings.ToLower(n)] = true
	}
}

// Parse reads the file at path. Like the C# Parse, any failure is reported in
// the result rather than returned as an error.
func Parse(path string) *ParseResult {
	f, err := Open(path)
	if err != nil {
		return failed(fmt.Sprintf("Failed to parse '%s': %v", path, err))
	}
	res, err := parseFile(f)
	if err != nil {
		return failed(fmt.Sprintf("Failed to parse '%s': %v", path, err))
	}
	return res
}

func failed(msg string) *ParseResult {
	return &ParseResult{Success: false, Errors: []string{msg}}
}

// stream finds a stream by its OLE name; a leading control character (the
// \x05 of "\x05SummaryInformation") is optional, as readers differ on it.
func (f *File) stream(path string) ([]byte, bool) {
	if b, ok := f.Streams[path]; ok {
		return b, true
	}
	for name, b := range f.Streams {
		if strings.TrimLeft(name, "\x00\x01\x02\x03\x04\x05\x06") == strings.TrimLeft(path, "\x00\x01\x02\x03\x04\x05\x06") {
			return b, true
		}
	}
	return nil, false
}

func parseFile(f *File) (*ParseResult, error) {
	var warnings []string

	title, author, comments := "", "", ""
	if b, ok := f.stream("\x05SummaryInformation"); ok {
		var err error
		if title, author, comments, err = parseSummaryInfo(b); err != nil {
			return nil, err
		}
	}

	var graph *qeGraph
	if b, ok := f.stream("QESession"); ok {
		session := parseQeSession(b)
		switch {
		case !session.valid:
			warnings = append(warnings, "QESession stream missing or unrecognized QENG header.")
		case session.encrypted && !session.decrypted:
			warnings = append(warnings, "QESession stream could not be decrypted.")
		}
		graph = session.graph
	}

	contents, ok := f.stream("Contents")
	if !ok {
		return failed("No Contents stream found — not a valid Crystal Reports file."), nil
	}
	inflated, err := Decrypt(contents)
	if err != nil {
		return nil, err
	}
	records := Records(inflated)

	report := buildReport(records, &warnings)
	if graph != nil {
		applyDependencyGraph(report, graph, &warnings)
	}
	resolveEmbeddedImages(f, report, &warnings, "")
	applyChartTemplates(f, "", chartsOf(report), &warnings)
	resolveSubreports(f, report, &warnings, "", 0)
	if title != "" {
		report.ReportTitle = title
	}
	if author != "" {
		report.Author = author
	}
	if comments != "" {
		report.ReportComments = comments
	}
	return &ParseResult{Success: true, Report: report.toModel(), Warnings: warnings, Errors: []string{}}, nil
}

// ---- builder ----

type formulaHook struct {
	name    string
	ordinal int
}

type namedFormula struct {
	name, text string
}

type shapeEnd struct {
	shape        *ShapeObject
	sectionIndex int
	bottomTwips  int
}

type reportBuilder struct {
	ReportTitle            string
	Author                 string
	ReportComments         string
	Page                   PageLayout
	DataSources            []*DataSource
	TableLinks             []*TableLink
	Fields                 []ReportField
	Groups                 []*GroupDefinition
	SortFields             []*SortField
	Sections               []*sectionBuilder
	RecordSelectionFormula *string
	GroupSelectionFormula  *string

	// Every formula's text by name, case-insensitively (lower-cased keys),
	// including the internal ones that are not report fields.
	formulaTexts map[string]string
	// The same in file order, one entry per formula record (nil when unnamed),
	// so a section hook's ordinal can tell formulas of one name apart.
	formulasByOrdinal []*namedFormula
	numberLanguage    *string
	shapeEnds         []shapeEnd
}

func newReportBuilder() *reportBuilder {
	return &reportBuilder{
		// The runtime's default margin when no printer says otherwise: 240 twips.
		Page: PageLayout{
			WidthTwips: 12240, HeightTwips: 15840,
			TopMarginTwips: 240, BottomMarginTwips: 240, LeftMarginTwips: 240, RightMarginTwips: 240,
			Orientation: Portrait,
		},
		formulaTexts: map[string]string{},
	}
}

type sectionBuilder struct {
	kind                     SectionType
	groupLevel               int
	heightTwips              int
	suppress                 bool
	newPageBefore            bool
	newPageAfter             bool
	resetPageNumber          bool
	underlay                 bool
	repeatGroupHeader        bool
	name                     string
	suppressFormulaHook      *formulaHook
	newPageBeforeFormulaHook *formulaHook
	newPageAfterFormulaHook  *formulaHook
	backColorFormulaHook     *formulaHook
	backColor                *string
	objects                  []ReportObject
}

func (r *reportBuilder) toModel() *ReportDefinition {
	sections := make([]*Section, len(r.Sections))
	for i, s := range r.Sections {
		sections[i] = s.toModel(r.formulaTexts, r.formulasByOrdinal)
	}
	r.resolveShapeEnds(sections)
	return &ReportDefinition{
		ReportTitle:            r.ReportTitle,
		Author:                 r.Author,
		ReportComments:         r.ReportComments,
		Language:               r.numberLanguage,
		CrVersion:              0,
		Page:                   r.Page,
		DataSources:            r.DataSources,
		TableLinks:             r.TableLinks,
		Fields:                 r.Fields,
		Groups:                 r.Groups,
		SortFields:             r.SortFields,
		Sections:               sections,
		RecordSelectionFormula: r.RecordSelectionFormula,
		GroupSelectionFormula:  r.GroupSelectionFormula,
	}
}

// resolveShapeEnds: a shape's end counts sections in the order they print;
// the file holds them area by area. Only an end in a later section is kept.
func (r *reportBuilder) resolveShapeEnds(sections []*Section) {
	if len(r.shapeEnds) == 0 {
		return
	}
	rank := func(t SectionType) int {
		switch t {
		case ReportHeader:
			return 0
		case PageHeader:
			return 1
		case GroupHeader:
			return 2
		case Details:
			return 3
		case GroupFooter:
			return 4
		case ReportFooter:
			return 5
		case PageFooter:
			return 6
		}
		return 7
	}
	printOrder := append([]*Section{}, sections...)
	sort.SliceStable(printOrder, func(i, j int) bool {
		a, b := printOrder[i], printOrder[j]
		if rank(a.Type) != rank(b.Type) {
			return rank(a.Type) < rank(b.Type)
		}
		la, lb := a.GroupLevel, b.GroupLevel
		if a.Type == GroupFooter {
			la = -la
		}
		if b.Type == GroupFooter {
			lb = -lb
		}
		return la < lb
	})
	for _, e := range r.shapeEnds {
		own := -1
		for i, s := range printOrder {
			for _, o := range s.Objects {
				if shapeOf(o) == e.shape {
					own = i
				}
			}
			if own >= 0 {
				break
			}
		}
		if own < 0 || e.sectionIndex <= own || e.sectionIndex >= len(printOrder) {
			continue
		}
		e.shape.EndSection = printOrder[e.sectionIndex]
		e.shape.EndBottomTwips = e.bottomTwips
	}
}

func shapeOf(o ReportObject) *ShapeObject {
	switch o := o.(type) {
	case *LineObject:
		return &o.ShapeObject
	case *BoxObject:
		return &o.ShapeObject
	}
	return nil
}

func (s *sectionBuilder) toModel(formulaTexts map[string]string, byOrdinal []*namedFormula) *Section {
	objects := s.objects
	if objects == nil {
		objects = []ReportObject{}
	}
	return &Section{
		Type:                 s.kind,
		GroupLevel:           s.groupLevel,
		HeightTwips:          s.heightTwips,
		Suppress:             s.suppress,
		NewPageBefore:        s.newPageBefore,
		NewPageAfter:         s.newPageAfter,
		ResetPageNumber:      s.resetPageNumber,
		Underlay:             s.underlay,
		RepeatGroupHeader:    s.repeatGroupHeader,
		SuppressFormula:      resolveFormulaText(s.suppressFormulaHook, formulaTexts, byOrdinal),
		NewPageBeforeFormula: resolveFormulaText(s.newPageBeforeFormulaHook, formulaTexts, byOrdinal),
		NewPageAfterFormula:  resolveFormulaText(s.newPageAfterFormulaHook, formulaTexts, byOrdinal),
		BackColorFormula:     resolveFormulaText(s.backColorFormulaHook, formulaTexts, byOrdinal),
		BackColor:            s.backColor,
		Objects:              objects,
	}
}

// resolveFormulaText: by ordinal, which picks the one formula among several of
// the same name, and by name only where the ordinal does not lead to a formula
// of that name (by name alone it is the last one read).
func resolveFormulaText(h *formulaHook, formulaTexts map[string]string, byOrdinal []*namedFormula) *string {
	if h == nil {
		return nil
	}
	if h.ordinal >= 0 && h.ordinal < len(byOrdinal) && byOrdinal[h.ordinal] != nil &&
		eqFold(byOrdinal[h.ordinal].name, h.name) {
		return strPtr(byOrdinal[h.ordinal].text)
	}
	if t, ok := formulaTexts[strings.ToLower(h.name)]; ok {
		return strPtr(t)
	}
	return nil
}

func buildReport(records []Record, warnings *[]string) *reportBuilder {
	report := newReportBuilder()

	extractPageSetup(records, report)
	extractFields(records, report)
	extractSqlExpressions(records, report)
	expandCustomFunctionCalls(records, report)
	backfillTableNamesFromFormulas(report)
	extractGroups(records, report)
	extractSortFields(records, report)

	for i := 0; i < len(records); {
		switch records[i].Tag {
		case tagReportAreaStart, tagPageAreaStart, tagDetailAreaStart, tagGroupAreaStart:
			i = parseAreaPair(records, i, report, warnings)
			continue
		}
		i++
	}
	return report
}

// ---- page setup ----

// extractPageSetup: record 398 holds the page the report was last laid out
// on, and byte 41 says whether that page is the template's own. Set, Crystal
// prints on exactly that page; clear, the report follows a printer, and record
// 7's paper (where it names one) replaces it. Record 7's orientation turns the
// page either way. Margins come from record 102.
func extractPageSetup(records []Record, report *reportBuilder) {
	printer := firstRecord(records, tagPrinterSettings)
	margins := firstRecord(records, tagPageMargins)
	if l, r, t, b, ok := resolveMargins(margins, printer); ok {
		report.Page.DefaultMargins = margins.Int32(3) == math.MinInt32
		report.Page.LeftMarginTwips = l
		report.Page.RightMarginTwips = r
		report.Page.TopMarginTwips = t
		report.Page.BottomMarginTwips = b
	}
	w, h, fromPrinter, ok := resolvePage(firstRecord(records, tagPageSetup), printer)
	if !ok {
		return
	}
	report.Page.PaperFromPrinter = fromPrinter
	report.Page.WidthTwips = w
	report.Page.HeightTwips = h
	if w > h {
		report.Page.Orientation = Landscape
	} else {
		report.Page.Orientation = Portrait
	}
}

// resolveMargins: four big-endian Int32s at bytes 3, 7, 11, 15 (left, right,
// top, bottom), or 0x80000000 in all four for the default margins, which the
// runtime made 360 twips on a portrait printer and 240 otherwise.
func resolveMargins(margins, printer *Record) (int, int, int, int, bool) {
	if margins == nil || len(margins.Data) < 19 {
		return 0, 0, 0, 0, false
	}
	l, r := margins.Int32(3), margins.Int32(7)
	t, b := margins.Int32(11), margins.Int32(15)
	if l != math.MinInt32 || r != math.MinInt32 || t != math.MinInt32 || b != math.MinInt32 {
		for _, m := range []int{l, r, t, b} {
			if m < 0 || m >= 14400 {
				return 0, 0, 0, 0, false
			}
		}
		return l, r, t, b, true
	}
	_, landscape, hasOrientation := readPrinterPaper(printer)
	byDefault := 240
	if hasOrientation && !landscape {
		byDefault = 360
	}
	return byDefault, byDefault, byDefault, byDefault, true
}

func resolvePage(pageSetup, printer *Record) (w, h int, fromPrinter, ok bool) {
	if pageSetup == nil || len(pageSetup.Data) < 8 {
		return 0, 0, false, false
	}
	w, h = pageSetup.Int32(0), pageSetup.Int32(4)
	if w < 720 || h < 720 {
		return 0, 0, false, false
	}
	followsPrinter := len(pageSetup.Data) > 41 && pageSetup.Data[41] == 0
	if !followsPrinter {
		return w, h, false, true
	}
	paper, landscape, hasOrientation := readPrinterPaper(printer)
	sheetW, sheetH := w, h
	if paper != nil {
		sheetW, sheetH = paper[0], paper[1]
	}
	wide := w > h
	if hasOrientation {
		wide = landscape
	}
	if wide {
		w, h = max(sheetW, sheetH), min(sheetW, sheetH)
	} else {
		w, h = min(sheetW, sheetH), max(sheetW, sheetH)
	}
	return w, h, paper == nil, true
}

// readPrinterPaper: a 16-bit mask of which DEVMODE settings follow, then one
// 16-bit value per set bit: orientation (1 portrait, 2 landscape), paper code,
// paper length and width in tenths of a millimetre.
func readPrinterPaper(settings *Record) (paper *[2]int, landscape, hasOrientation bool) {
	if settings == nil || len(settings.Data) < 4 {
		return nil, false, false
	}
	mask := settings.Uint16(2)
	at := 4
	next := func(bit int) (int, bool) {
		if mask&bit == 0 {
			return 0, false
		}
		v := settings.Uint16(at)
		at += 2
		return v, true
	}
	orientation, hasO := next(0x1)
	code, hasCode := next(0x2)
	length, hasLen := next(0x4)
	width, hasWidth := next(0x8)

	if hasLen && hasWidth && length > 0 && width > 0 {
		paper = &[2]int{tenthsOfMmToTwips(width), tenthsOfMmToTwips(length)}
	} else if hasCode {
		paper = paperSizeTwips(code)
	}
	if hasO && orientation == 1 {
		return paper, false, true
	}
	if hasO && orientation == 2 {
		return paper, true, true
	}
	return paper, false, false
}

func tenthsOfMmToTwips(tenths int) int {
	return int(math.RoundToEven(float64(tenths) * 1440 / 254.0))
}

func paperSizeTwips(code int) *[2]int {
	switch code {
	case 1, 2:
		return &[2]int{12240, 15840}
	case 3, 4:
		return &[2]int{15840, 24480}
	case 5:
		return &[2]int{12240, 20160}
	case 8:
		return &[2]int{16838, 23811}
	case 9, 10:
		return &[2]int{11906, 16838}
	case 11:
		return &[2]int{8391, 11906}
	}
	return nil
}

// ---- areas and sections ----

func parseAreaPair(records []Record, start int, report *reportBuilder, warnings *[]string) int {
	endTag := records[start].Tag + 1
	groupLevel := 0
	i := start + 1
	for i < len(records) && records[i].Tag != endTag {
		rec := records[i]
		if rec.Tag == tagAreaPairCode {
			// int8u kind, int16u group level
			if len(rec.Data) >= 3 {
				groupLevel = rec.Uint16(1)
			}
			// A group's area pair opens with its options: Int16 repeat group header.
			if records[start].Tag == tagGroupAreaStart && len(records[start].Data) >= 2 {
				if g := groupAt(report, groupLevel); g != nil {
					g.RepeatGroupHeader = records[start].Uint16(0) != 0
				}
			}
			i++
			continue
		}
		if rec.Tag == tagAreaHeader {
			i = parseArea(records, i, report, warnings, groupLevel)
			continue
		}
		i++
	}
	return i + 1
}

func groupAt(report *reportBuilder, level int) *GroupDefinition {
	for _, g := range report.Groups {
		if g.Level == level {
			return g
		}
	}
	return nil
}

func parseArea(records []Record, start int, report *reportBuilder, warnings *[]string, groupLevel int) int {
	i := start + 1
	if i < len(records) && records[i].Tag == tagAreaCode {
		i++
	}
	// Area-level properties: hooks that fall back onto every section in the
	// area, and the area's own suppress / hide-for-drill-down.
	areaHooks := map[int]formulaHook{}
	areaHidden := false
	if i < len(records) && records[i].Tag == tagSectionProperties {
		areaHooks = extractFormulaHookEntries(records[i])
		areaHidden = areaIsHidden(records[i])
		i++
	}
	for i < len(records) && records[i].Tag != tagAreaEnd {
		if kind, ok := sectionKindFromTag(records[i].Tag); ok {
			i = parseSection(records, i, report, warnings, kind, groupLevel, areaHooks, areaHidden)
			continue
		}
		i++
	}
	return i + 1
}

// sectionKindFromTag maps a section start tag; an end tag or anything else is false.
func sectionKindFromTag(tag int) (SectionType, bool) {
	switch tag {
	case 141:
		return ReportHeader, true
	case 143:
		return ReportFooter, true
	case 145:
		return PageHeader, true
	case 147:
		return PageFooter, true
	case 149:
		return Details, true
	case 151:
		return GroupHeader, true
	case 153:
		return GroupFooter, true
	}
	return "", false
}

func parseSection(records []Record, start int, report *reportBuilder, warnings *[]string,
	kind SectionType, groupLevel int, areaHooks map[int]formulaHook, areaHidden bool) int {
	wrapper := records[start]
	endTag := wrapper.Tag + 1

	// tag-140: int32 height, int16u hasRuler, int16u objectCount, string name
	height, objectCount, name := 0, 0, ""
	if h := child(wrapper, 140); h != nil {
		height = h.Int32(0)
		if height < 0 {
			height = -height
		}
		objectCount = h.Uint16(6)
		name = h.strOr(8, "")
	}
	section := &sectionBuilder{
		kind:        kind,
		groupLevel:  groupLevel,
		heightTwips: height,
		suppress:    areaHidden,
		name:        name,
	}
	if kind == GroupHeader {
		if g := groupAt(report, groupLevel); g != nil && g.RepeatGroupHeader {
			section.repeatGroupHeader = true
		}
	}

	i := start + 1
	for i < len(records) && (records[i].Tag == tagSectionCode || records[i].Tag == tagSectionProperties) {
		if records[i].Tag == tagSectionProperties {
			sup, npb, npa, rpn, underlay := extractSectionFlags(records[i])
			section.underlay = underlay
			section.backColor = extractSectionBackColor(records[i])
			// A section prints only when its area does too.
			section.suppress = sup || areaHidden
			section.newPageBefore = npb
			section.newPageAfter = npa
			section.resetPageNumber = rpn

			hooks := extractFormulaHookEntries(records[i])
			hook := func(entry int) *formulaHook {
				if h, ok := hooks[entry]; ok {
					return &h
				}
				if h, ok := areaHooks[entry]; ok {
					return &h
				}
				return nil
			}
			section.suppressFormulaHook = hook(0)
			section.newPageBeforeFormulaHook = hook(2)
			section.newPageAfterFormulaHook = hook(3)
			section.backColorFormulaHook = hook(9)
		}
		i++
	}

	// Objects carry no position of their own: it is the tag-190 after the wrapper.
	addPlaced := func(obj ReportObject, wrapperIndex int) {
		if obj == nil {
			return
		}
		b := obj.object()
		b.Bounds = applyPlacement(records, wrapperIndex, b.Bounds)
		section.objects = append(section.objects, obj)
	}

	parsed := 0
	for i < len(records) && records[i].Tag != endTag && parsed < objectCount+100 {
		rec := records[i]
		switch {
		case rec.Tag == tagFieldObjectStart:
			obj, next := parseFieldObject(records, i, report)
			addPlaced(obj, i)
			i = next
			parsed++
			continue
		case rec.Tag == tagTextObjectStart:
			obj, next := parseTextObject(records, i)
			addPlaced(obj, i)
			i = next
			parsed++
			continue
		case rec.Tag == tagPictureObjectStart:
			obj, next := parsePictureObject(records, i)
			addPlaced(obj, i)
			i = next
			parsed++
			continue
		case rec.Tag == tagSubreportObjectStart:
			obj, next := parseSubreportObject(records, i)
			addPlaced(obj, i)
			i = next
			parsed++
			continue
		case rec.Tag == tagCrossTabObjectStart:
			obj, next := parseCrossTabObject(records, i, report)
			addPlaced(obj, i)
			i = next
			parsed++
			continue
		case rec.Tag == tagChartObjectStart:
			obj, next := parseChartObject(records, i, report)
			addPlaced(obj, i)
			i = next
			parsed++
			continue
		case rec.Tag == tagLineObjectStart || rec.Tag == tagBoxObjectStart:
			// Shapes drawn from the far corner carry negative extents; magnitude is
			// what matters. A zero-extent shape draws nothing and is dropped.
			bounds := extractShapeBounds(rec)
			var shape *ShapeObject
			if bounds.Width > 0 || bounds.Height > 0 {
				var obj ReportObject
				base := ShapeObject{ObjectBase: ObjectBase{Name: extractObjectName(rec), Bounds: bounds, Format: newFormat()}}
				if rec.Tag == tagLineObjectStart {
					l := &LineObject{base}
					obj, shape = l, &l.ShapeObject
				} else {
					b := &BoxObject{base}
					obj, shape = b, &b.ShapeObject
				}
				addPlaced(obj, i)
				if idx, bottom, ok := extractShapeEnd(rec); ok {
					report.shapeEnds = append(report.shapeEnds, shapeEnd{shape, idx, bottom})
				}
			}
			shapeEndTag := rec.Tag + 1
			for i < len(records) && records[i].Tag != shapeEndTag && records[i].Tag != endTag {
				// Its colour, fill, line width and style: the border record a field carries.
				if shape != nil && records[i].Tag == tagObjectBorder {
					if b := extractBorders(records[i]); b != nil {
						f := newFormat()
						f.BorderLeft, f.BorderRight, f.BorderTop, f.BorderBottom = b.l, b.r, b.t, b.b
						f.DropShadow, f.BackColor, f.BorderWidthTwips, f.BorderColor = b.shadow, b.backColor, b.widthTwips, b.color
						shape.Format = f
					}
				}
				i++
			}
			if i < len(records) && records[i].Tag == shapeEndTag {
				i++
			}
			parsed++
			continue
		case rec.Tag == tagBlobFieldObjectStart:
			obj, next := parseBlobFieldObject(records, i)
			addPlaced(obj, i)
			i = next
			parsed++
			continue
		case rec.Tag >= 159 && rec.Tag <= 200 && rec.Tag%2 == 1:
			// Unknown object type: skip to its end tag.
			objEnd := rec.Tag + 1
			for i < len(records) && records[i].Tag != objEnd && records[i].Tag != endTag {
				i++
			}
			if i < len(records) && records[i].Tag == objEnd {
				i++
			}
			parsed++
			continue
		}
		i++
	}
	for i < len(records) && records[i].Tag != endTag {
		i++
	}
	if i < len(records) {
		i++
	}
	report.Sections = append(report.Sections, section)
	return i
}

// applyPlacement: the tag-190 after an object wrapper holds UInt16 left and
// top, twips, relative to the section.
func applyPlacement(records []Record, wrapperIndex int, b ObjectBounds) ObjectBounds {
	if wrapperIndex+1 >= len(records) {
		return b
	}
	p := records[wrapperIndex+1]
	if p.Tag != tagObjectPlacement || len(p.Data) < 4 {
		return b
	}
	b.Left = int(p.Data[0])<<8 | int(p.Data[1])
	b.Top = int(p.Data[2])<<8 | int(p.Data[3])
	return b
}

// extractObjectBounds reads the nested tag-158: int32 width, int32 height, two
// zero int32s, then the object's name. Size only; position is tag-190's.
func extractObjectBounds(wrapper Record) ObjectBounds {
	h := child(wrapper, tagReportObjectHeader)
	if h == nil || len(h.Data) < 16 {
		return ObjectBounds{}
	}
	return ObjectBounds{Width: max(h.Int32(0), 0), Height: max(h.Int32(4), 0)}
}

// extractShapeEnd: the tag-169 drawing header ends with the Int16 index of the
// section the shape ends in (print order) and how far down it ends.
func extractShapeEnd(wrapper Record) (int, int, bool) {
	h := child(wrapper, 169)
	if h == nil || len(h.Data) < 8 {
		return 0, 0, false
	}
	n := len(h.Data)
	index := int(int16(uint16(h.Data[n-8])<<8 | uint16(h.Data[n-7])))
	bottom := int(int16(uint16(h.Data[n-4])<<8 | uint16(h.Data[n-3])))
	if index < 0 {
		return 0, 0, false
	}
	return index, bottom, true
}

// extractShapeBounds: shapes nest one level deeper (170/172 -> 169 -> 158) and
// keep the magnitude of negative extents.
func extractShapeBounds(wrapper Record) ObjectBounds {
	children := wrapper.Children()
	var h *Record
	for i := range children {
		if children[i].Tag == tagReportObjectHeader {
			h = &children[i]
			break
		}
	}
	if h == nil {
		for _, c := range children {
			if found := child(c, tagReportObjectHeader); found != nil {
				h = found
				break
			}
		}
	}
	if h == nil || len(h.Data) < 16 {
		return ObjectBounds{}
	}
	return ObjectBounds{Width: abs(h.Int32(0)), Height: abs(h.Int32(4))}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func extractObjectName(wrapper Record) string {
	h := child(wrapper, tagReportObjectHeader)
	if h == nil || len(h.Data) < 16 {
		return ""
	}
	return h.strOr(16, "")
}

// ---- section properties ----

// tag-255 SectionProperties holds a tag-254 child: [0] area pair kind, then
// Int16 BE flags: [3..4] section-level (else area-level), [5..6] !suppress,
// [7..8] !hide, [9..10] new page before, [11..12] new page after, [17..18]
// reset page number, [21..22] underlay; [23..26] back colour (flag, B, G, R).
func extractSectionBackColor(props Record) *string {
	ch := child(props, 254)
	if ch == nil || len(ch.Data) < 27 {
		return nil
	}
	d := ch.Data
	if d[3] == 0 && d[4] == 0 {
		return nil
	}
	if d[23] == 0 {
		return strPtr(fmt.Sprintf("#%02X%02X%02X", d[26], d[25], d[24]))
	}
	return nil
}

func areaIsHidden(props Record) bool {
	ch := child(props, 254)
	if ch == nil || len(ch.Data) < 9 {
		return false
	}
	d := ch.Data
	if d[3] != 0 || d[4] != 0 {
		return false
	}
	suppressed := d[5] == 0 && d[6] == 0
	hidden := d[7] == 0 && d[8] == 0
	return suppressed || hidden
}

func extractSectionFlags(props Record) (suppress, newPageBefore, newPageAfter, resetPageNumber, underlay bool) {
	ch := child(props, 254)
	if ch == nil || len(ch.Data) < 13 {
		return
	}
	d := ch.Data
	if d[3] == 0 && d[4] == 0 {
		return // an area-level record has no per-section flags
	}
	suppress = d[5] == 0 && d[6] == 0
	newPageBefore = d[9] != 0 || d[10] != 0
	newPageAfter = d[11] != 0 || d[12] != 0
	resetPageNumber = len(d) > 18 && (d[17] != 0 || d[18] != 0)
	underlay = len(d) > 22 && (d[21] != 0 || d[22] != 0)
	return
}

// extractFormulaHookEntries: after the tag-254 block, one entry per
// formula-drivable section property: a formula name (empty when none) and three
// trailer bytes, a flag and the formula's Int16 ordinal among the tag-119
// records. 0 suppress, 2 new page before, 3 new page after, 9 back colour.
func extractFormulaHookEntries(props Record) map[int]formulaHook {
	result := map[int]formulaHook{}
	ch := child(props, 254)
	if ch == nil {
		return result
	}
	pos := 8 + len(ch.Data)
	for entry := 0; ; entry++ {
		name, consumed := props.str(pos)
		if consumed <= 0 || name == nil {
			break
		}
		trailer := pos + consumed
		pos += consumed + 3
		if *name == "" {
			continue
		}
		ordinal := -1
		if trailer+2 < len(props.Data) {
			ordinal = int(int16(uint16(props.Data[trailer+1])<<8 | uint16(props.Data[trailer+2])))
		}
		result[entry] = formulaHook{name: strings.TrimLeft(*name, "@"), ordinal: ordinal}
	}
	return result
}
