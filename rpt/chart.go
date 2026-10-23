package rpt

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Charts: the tag-180 object (ParseChartObject), its tag-289 definition
// (ChartDefinitionDecoder.cs) and its "CHART <n>l" drawing template stream
// (ChartTemplateReader.cs).

func newChartFont(name *string) *ChartFont { return &ChartFont{Name: name} }

func parseChartObject(records []Record, start int, report *reportBuilder) (ReportObject, int) {
	wrapper := records[start]
	bounds, name := ObjectBounds{}, ""
	if inner179 := child(wrapper, 179); inner179 != nil {
		if inner174 := child(*inner179, 174); inner174 != nil {
			bounds, name = extractObjectBounds(*inner174), extractObjectName(*inner174)
		}
	}

	title, fieldBoundCategory := "", ""
	var groupFields []string
	var groupConditions []GroupCondition
	seriesField := ""
	seriesFunction := AggSum
	haveQualified := false
	var def *chartDefinition
	dataSource, groupIndex := -1, 0

	next := start + 1
	for next < len(records) && records[next].Tag != tagChartObjectEnd {
		rec := records[next]
		switch {
		case rec.Tag == tagChartTypeRecord && len(rec.Data) > 3:
			dataSource, groupIndex = int(rec.Data[2]), int(rec.Data[3])
		case rec.Tag == tagChartSeriesField:
			for _, s := range scanStrings(rec) {
				if strings.Contains(s, " of ") {
					if fn, rest := parseSummaryPrefix(s); fn != nil {
						seriesFunction = *fn
						if dot := strings.IndexByte(rest, '.'); dot > 0 {
							seriesField = rest[dot+1:]
						} else {
							seriesField = rest
						}
						haveQualified = true
					}
					break
				}
			}
		case rec.Tag == tagChartDefinition:
			def = decodeChartDefinition(rec)
			strs := scanStrings(rec)
			if def != nil {
				title = def.title
			} else if len(strs) > 1 || (len(strs) > 0 && len(groupFields) == 0) {
				title = strs[0]
			}
			if len(strs) > 1 {
				fieldBoundCategory = strs[1]
			}
			if !haveQualified && len(strs) > 2 {
				if fn, rest := parseSummaryPrefix(strs[2]); fn != nil {
					seriesFunction, seriesField = *fn, rest
				}
			}
		case rec.Tag == tagGroupCondition:
			tf, nc, ok := rec.String(0)
			isAxis := false
			for _, s := range scanStrings(rec) {
				if strings.Contains(s, "Detail Value Grid") {
					isAxis = true
					break
				}
			}
			if isAxis && ok && tf != "" && strings.Contains(tf, ".") {
				dot := strings.IndexByte(tf, '.')
				field := tf[dot+1:]
				groupFields = append(groupFields, field)
				cond := GroupCondition("EachValue")
				if report != nil && isDateGroupField(report, tf[:dot], field) && nc+3 < len(rec.Data) {
					cond = dateInterval(int(rec.Data[nc+3]))
				}
				groupConditions = append(groupConditions, cond)
			}
		case rec.Tag == tagChartDetailFieldDef && !haveQualified:
			if ch126 := child(rec, 126); ch126 != nil {
				for _, s := range scanStrings(*ch126) {
					if strings.Contains(s, ".") || strings.HasPrefix(s, "@") {
						if strings.HasPrefix(s, "@") {
							seriesField = s[1:]
						} else if dot := strings.IndexByte(s, '.'); dot > 0 {
							seriesField = s[dot+1:]
						} else {
							seriesField = s
						}
						break
					}
				}
			}
		}
		next++
	}
	if next < len(records) {
		next++
	}

	// The category has to be one of the report's fields, not a typeface.
	if fieldBoundCategory != "" && !isKnownFieldName(fieldBoundCategory, report) {
		fieldBoundCategory = ""
	}
	categoryFields := groupFields
	fromGroups := len(groupFields) > 0
	if !fromGroups {
		categoryFields = nil
		if fieldBoundCategory != "" {
			categoryFields = []string{fieldBoundCategory}
		}
	}
	if len(categoryFields) == 0 && dataSource == 1 && report != nil && groupIndex < len(report.Groups) &&
		report.Groups[groupIndex].FieldName != "" {
		categoryFields = []string{report.Groups[groupIndex].FieldName}
		fromGroups = false
	}
	if len(categoryFields) == 0 || seriesField == "" {
		return nil, next
	}

	kind, stacking := ChartKind("Column"), ChartStacking("None")
	if def != nil {
		kind, stacking = mapChartStyle(def.styleType, def.subtype)
	}
	chartIndex := 0
	for _, r := range records[:start] {
		if r.Tag == tagChartObjectStart {
			chartIndex++
		}
	}
	c := &ChartObject{
		Title: title, Kind: kind, Stacking: stacking,
		CategoryFields: categoryFields, CategoryConditions: []GroupCondition{},
		SeriesField: seriesField, SeriesFunction: seriesFunction,
		ChartIndex:     chartIndex,
		LegendPosition: "Right",
		TitleFont:      newChartFont(nil), CategoryAxisTitleFont: newChartFont(nil), ValueAxisTitleFont: newChartFont(nil),
		LegendFont: newChartFont(nil), CategoryLabelFont: newChartFont(nil), ValueLabelFont: newChartFont(nil),
		Palette:    []string{},
		ObjectBase: ObjectBase{Name: name, Bounds: bounds, Format: newFormat()},
	}
	if fromGroups {
		c.CategoryConditions = groupConditions
	}
	if def != nil {
		c.CategoryAxisTitle, c.ValueAxisTitle = def.groupTitle, def.dataTitle
		c.ShowLegend, c.LegendPosition = def.showLegend, def.legendPosition
		c.CategoryAxisGridLines, c.ValueAxisGridLines = def.groupGrid, def.dataGrid
		c.TitleFont, c.CategoryAxisTitleFont, c.ValueAxisTitleFont = def.titleFont, def.groupTitleFont, def.dataTitleFont
		c.LegendFont, c.CategoryLabelFont, c.ValueLabelFont = def.legendFont, def.groupLabelFont, def.dataLabelFont
	}
	return c, next
}

// isKnownFieldName: the bare column, "TableName ColumnName", a column-name
// suffix after a space, or a formula's name.
func isKnownFieldName(name string, report *reportBuilder) bool {
	if report == nil {
		return true
	}
	n := strings.TrimLeft(name, "@")
	if dot := strings.IndexByte(n, '.'); dot > 0 {
		n = n[dot+1:]
	}
	for _, f := range report.dbFields() {
		if f.ColumnName == "" {
			continue
		}
		if eqFold(f.ColumnName, n) {
			return true
		}
		if f.TableName != "" && eqFold(f.TableName+" "+f.ColumnName, n) {
			return true
		}
		if len(n) > len(f.ColumnName) && strings.EqualFold(n[len(n)-len(f.ColumnName):], f.ColumnName) &&
			n[len(n)-len(f.ColumnName)-1] == ' ' {
			return true
		}
	}
	for _, f := range report.formulaFields() {
		if eqFold(f.Name, n) {
			return true
		}
	}
	return false
}

// ---- tag-289 definition ----

type chartDefinition struct {
	styleType, subtype                                                   int
	title, groupTitle, dataTitle                                         string
	showLegend                                                           bool
	legendPosition                                                       ChartLegendPosition
	groupGrid, dataGrid                                                  bool
	titleFont, groupTitleFont, dataTitleFont, legendFont, groupLabelFont *ChartFont
	dataLabelFont                                                        *ChartFont
}

const (
	fontTitle, fontGroupTitle, fontDataTitle, fontLegend, fontGroupLabel = 0, 3, 4, 6, 7
)

var weightOffsets = [][3]int{
	{fontTitle, 0, 2},
	{fontGroupTitle, 15, 18},
	{fontDataTitle, 21, 23},
	{fontLegend, 31, 33},
	{fontGroupLabel, 36, 38},
}

// decodeChartDefinition: [0] style type, [1] subtype, from [2] eight strings
// (title, subtitle, footnote, two empty, group title, data title, series
// title), eight face names, a settings block ([1] show legend, [2] legend
// position, [8]/[10] gridlines on axis charts), then the weights block.
func decodeChartDefinition(rec Record) *chartDefinition {
	d := rec.Data
	if len(d) < 3 {
		return nil
	}
	off := 2
	var strs [8]*string
	for i := range strs {
		s, used := rec.str(off)
		if used == 0 {
			return nil
		}
		strs[i] = s
		off += used
	}
	if strs[0] == nil {
		return nil
	}
	var faces [8]*string
	for i := range faces {
		s, used := rec.str(off)
		if used == 0 || !isFaceName(s) {
			return nil
		}
		faces[i] = s
		off += used
	}
	settings := off
	styleType, subtype := int(d[0]), int(d[1])
	at := func(i int) int {
		if i < len(d) {
			return int(d[i])
		}
		return -1
	}
	legend := ChartLegendPosition("Right")
	switch at(settings + 2) {
	case 1:
		legend = "Left"
	case 2:
		legend = "Bottom"
	}
	axis := styleType == 0 || styleType == 1 || styleType == 2 || styleType == 5
	g := at(settings + 8)
	dg := at(settings + 10)
	fonts := make([]*ChartFont, 8)
	for i := range fonts {
		fonts[i] = newChartFont(faces[i])
	}
	readChartWeights(rec, settings, fonts)
	orEmpty := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return &chartDefinition{
		styleType: styleType, subtype: subtype,
		title: orEmpty(strs[0]), groupTitle: orEmpty(strs[5]), dataTitle: orEmpty(strs[6]),
		showLegend: at(settings+1) == 1, legendPosition: legend,
		groupGrid: axis && (g == 2 || g == 3), dataGrid: axis && (dg == 2 || dg == 3),
		titleFont: fonts[fontTitle], groupTitleFont: fonts[fontGroupTitle], dataTitleFont: fonts[fontDataTitle],
		legendFont: fonts[fontLegend], groupLabelFont: fonts[fontGroupLabel],
		dataLabelFont: newChartFont(faces[fontGroupLabel]),
	}
}

func mapChartStyle(styleType, subtype int) (ChartKind, ChartStacking) {
	barStacking := func() ChartStacking {
		switch subtype % 3 {
		case 1:
			return "Stacked"
		case 2:
			return "PercentStacked"
		}
		return "None"
	}
	switch styleType {
	case 0:
		return "Column", barStacking()
	case 1:
		return "Line", "None"
	case 2:
		return "Area", "None"
	case 3:
		return "Pie", "None"
	case 4:
		return "Doughnut", "None"
	}
	return "Column", "None"
}

func readChartWeights(rec Record, settings int, fonts []*ChartFont) {
	d := rec.Data
	for start := settings + 8; start+8 < len(d); start++ {
		first, used1 := rec.str(start)
		if used1 <= 5 || !isFaceName(first) {
			continue
		}
		second, used2 := rec.str(start + used1)
		if used2 <= 5 || !isFaceName(second) {
			continue
		}
		block := start + used1 + used2 + 20 + 4 + 2
		for _, w := range weightOffsets {
			style, weightAt, italicAt := w[0], w[1], w[2]
			if block+italicAt >= len(d) {
				return
			}
			weight := int(d[block+weightAt])<<8 | int(d[block+weightAt+1])
			if weight < 100 || weight > 900 || weight%100 != 0 {
				return
			}
			fonts[style].Bold = weight >= 600
			fonts[style].Italic = d[block+italicAt] == 1
		}
		return
	}
}

func isFaceName(s *string) bool {
	return s != nil && ulen(*s) > 0 && ulen(*s) <= 32 && allASCIIPrintable(*s)
}

// ---- drawing templates ----

const (
	recordFontStyle  = 0x2339
	recordFill       = 0x2329
	objectSeriesFill = 0x0854
	heightScale      = 32767

	titleText      = 0x07DF
	groupLabelText = 0x07E2
	dataLabelText  = 0x07E3
	legendText     = 0x07E4
	groupTitleText = 0x07E5
	dataTitleText  = 0x07E6
)

type chartTemplate struct {
	textHeights           map[int]int
	palette               []string
	paletteDescribesFills bool
}

func chartsOf(report *reportBuilder) []*ChartObject {
	var out []*ChartObject
	for _, s := range report.Sections {
		for _, o := range s.objects {
			if c, ok := o.(*ChartObject); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

// applyChartTemplates fills the text sizes and, where the template describes
// them, the series colours. Streams are numbered in chart order.
func applyChartTemplates(f *File, prefix string, charts []*ChartObject, warnings *[]string) {
	var list []*ChartObject
	for _, c := range charts {
		if c.ChartIndex >= 0 {
			list = append(list, c)
		}
	}
	if len(list) == 0 {
		return
	}
	templates := readChartTemplates(f, prefix)
	for _, c := range list {
		if c.ChartIndex >= len(templates) || templates[c.ChartIndex] == nil {
			continue
		}
		t := templates[c.ChartIndex]
		heightPt := float64(c.Bounds.Height) / 20.0
		size := func(font *ChartFont, id int) {
			if raw, ok := t.textHeights[id]; heightPt > 0 && ok && raw > 0 {
				v := math.RoundToEven(float64(raw)*heightPt/heightScale*10) / 10
				font.SizePt = &v
			}
		}
		size(c.TitleFont, titleText)
		size(c.CategoryAxisTitleFont, groupTitleText)
		size(c.ValueAxisTitleFont, dataTitleText)
		size(c.LegendFont, legendText)
		size(c.CategoryLabelFont, groupLabelText)
		size(c.ValueLabelFont, dataLabelText)
		if t.paletteDescribesFills && len(t.palette) > 0 {
			c.Palette = t.palette
			c.ColorByCategory = true
		}
	}
}

func readChartTemplates(f *File, prefix string) []*chartTemplate {
	type named struct {
		name   string
		number int
	}
	var names []named
	for path := range f.Streams {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		n := path[len(prefix):]
		if strings.Contains(n, "/") {
			continue
		}
		if num := chartStreamNumber(n); num >= 0 {
			names = append(names, named{n, num})
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.SliceStable(names, func(i, j int) bool { return names[i].number < names[j].number })
	contents, ok := f.Streams[prefix+"Contents"]
	if !ok {
		return nil
	}
	var out []*chartTemplate
	for _, n := range names {
		inflated, err := DecryptHeaderless(contents, f.Streams[prefix+n.name])
		if err != nil {
			out = append(out, nil)
			continue
		}
		out = append(out, parseChartTemplate(inflated))
	}
	return out
}

func chartStreamNumber(name string) int {
	if !strings.HasPrefix(name, "CHART ") || !strings.HasSuffix(name, "l") {
		return -1
	}
	n, err := strconv.Atoi(name[6 : len(name)-1])
	if err != nil {
		return -1
	}
	return n
}

// parseChartTemplate: "TIFFGRAPH", a 256-byte header, then little-endian
// records (id, length, payload). 0x2339 is a text style (object id at 2, height
// at 8 in 1/32767ths of the chart height); 0x2329 for object 0x0854 is a series
// colour (index at 0, R G B at 11, 13, 15).
func parseChartTemplate(data []byte) *chartTemplate {
	if len(data) < 0x104 || string(data[:9]) != "TIFFGRAPH" {
		return nil
	}
	heights := map[int]int{}
	fills := map[int]string{}
	flagged := false
	for off := 0x100; off+4 <= len(data); {
		id := int(binary.LittleEndian.Uint16(data[off:]))
		n := int(binary.LittleEndian.Uint16(data[off+2:]))
		if off+4+n > len(data) {
			break
		}
		p := data[off+4 : off+4+n]
		if id == recordFontStyle && n >= 10 && p[0] == 0xFF && p[1] == 0xFF {
			obj := int(binary.LittleEndian.Uint16(p[2:]))
			if _, ok := heights[obj]; !ok {
				heights[obj] = int(binary.LittleEndian.Uint16(p[8:]))
			}
		} else if id == recordFill && n == 16 && int(binary.LittleEndian.Uint16(p[2:])) == objectSeriesFill {
			index := int(binary.LittleEndian.Uint16(p))
			if _, ok := fills[index]; !ok {
				fills[index] = fmt.Sprintf("#%02X%02X%02X", p[11], p[13], p[15])
			}
			if p[4] != 0 {
				flagged = true
			}
		}
		off += 4 + n
	}
	idx := make([]int, 0, len(fills))
	for k := range fills {
		idx = append(idx, k)
	}
	sort.Ints(idx)
	palette := []string{}
	for _, k := range idx {
		palette = append(palette, fills[k])
	}
	return &chartTemplate{textHeights: heights, palette: palette, paletteDescribesFills: !flagged}
}
