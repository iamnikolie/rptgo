package rpt

import "fmt"

// Things resolved after the record walk, against the OLE container: embedded
// pictures and subreports. storagePrefix is "" for the main report and
// "Subdocument N/" inside a subreport.

// resolveEmbeddedImages reads each embedded picture's "Embedding N/CONTENTS".
// A metafile (WMF/EMF) is not rasterized: the C# does that only on Windows, so
// on the reference's Linux run such an image is skipped too.
func resolveEmbeddedImages(f *File, report *reportBuilder, warnings *[]string, prefix string) {
	for _, s := range report.Sections {
		for _, o := range s.objects {
			img, ok := o.(*ImageObject)
			if !ok || img.Source != "Embedded" {
				continue
			}
			bytes, ok := f.Streams[fmt.Sprintf("%sEmbedding %d/CONTENTS", prefix, img.EmbeddingIndex)]
			if !ok {
				if _, pres := f.Streams[fmt.Sprintf("%sEmbedding %d/\x02OlePres000", prefix, img.EmbeddingIndex)]; !pres {
					*warnings = append(*warnings, fmt.Sprintf("Embedded object %d has no CONTENTS or presentation image stream — image skipped.", img.EmbeddingIndex))
				}
				continue
			}
			if mime := sniffImageMime(bytes); mime != "" {
				img.ImageData = bytes
				img.MimeType = strPtr(mime)
				continue
			}
			*warnings = append(*warnings, fmt.Sprintf("Embedded image %d is not a bitmap this reads (metafiles are rasterized only on Windows) — image skipped.", img.EmbeddingIndex))
		}
	}
}

func sniffImageMime(d []byte) string {
	switch {
	case len(d) >= 2 && d[0] == 0x42 && d[1] == 0x4D:
		return "image/bmp"
	case len(d) >= 4 && d[0] == 0x89 && d[1] == 0x50 && d[2] == 0x4E && d[3] == 0x47:
		return "image/png"
	case len(d) >= 3 && d[0] == 0xFF && d[1] == 0xD8 && d[2] == 0xFF:
		return "image/jpeg"
	case len(d) >= 4 && d[0] == 0x47 && d[1] == 0x49 && d[2] == 0x46 && d[3] == 0x38:
		return "image/gif"
	}
	return ""
}

// resolveSubreports parses each subreport's "Subdocument N/Contents" into a
// nested report, three levels deep at most.
func resolveSubreports(f *File, report *reportBuilder, warnings *[]string, prefix string, depth int) {
	for _, s := range report.Sections {
		for _, o := range s.objects {
			sub, ok := o.(*SubreportObject)
			if !ok {
				continue
			}
			if depth >= 3 {
				*warnings = append(*warnings, fmt.Sprintf("Subreport '%s': nesting deeper than 3 levels — skipped.", sub.SubreportName))
				continue
			}
			inner := fmt.Sprintf("%sSubdocument %d/", prefix, sub.SubdocumentIndex)
			contents, ok := f.Streams[inner+"Contents"]
			if !ok {
				*warnings = append(*warnings, fmt.Sprintf("Subreport '%s': failed to parse 'Subdocument %d' — no Contents stream", sub.SubreportName, sub.SubdocumentIndex))
				continue
			}
			inflated, err := Decrypt(contents)
			if err != nil {
				*warnings = append(*warnings, fmt.Sprintf("Subreport '%s': failed to parse 'Subdocument %d' — %v", sub.SubreportName, sub.SubdocumentIndex, err))
				continue
			}
			records := Records(inflated)
			builder := buildReport(records, warnings)
			// The subreport's own name: a string at offset 7 of its first record (tag 100).
			if len(records) > 0 && records[0].Tag == 100 {
				if name, _, ok := records[0].String(7); ok && name != "" {
					sub.ReportName = name
				}
			}
			if qe, ok := f.Streams[inner+"QESession"]; ok {
				if g := parseQeSession(qe).graph; g != nil {
					applyDependencyGraph(builder, g, warnings)
				}
			}
			resolveEmbeddedImages(f, builder, warnings, inner)
			applyChartTemplates(f, inner, chartsOf(builder), warnings)
			resolveSubreports(f, builder, warnings, inner, depth+1)
			if builder.ReportTitle == "" {
				builder.ReportTitle = sub.SubreportName
			}
			sub.Report = builder.toModel()
		}
	}
}
