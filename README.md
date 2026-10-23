# rptgo

A pure-Go reader for Crystal Reports `.rpt` files, and a banded report engine
that runs them, with no Crystal runtime, no .NET and no Windows. It describes a
report for a reader or a language model, audits what of it can be reproduced,
indexes a folder of reports, extracts the data a report prints, and renders it
to PDF.

```bash
go install github.com/iamnikolie/rptgo@latest

rptgo describe Invoices.rpt            # what the report is, as Markdown (-json for JSON)
rptgo audit reports/                   # what rptgo does not reproduce, what is unused
rptgo index reports/                   # catalog of a folder, with cross-references
rptgo extract Invoices.rpt -data rows.json            # the data it prints: groups, rows, totals

export RPTGO_DSN='sqlserver://user:pass@host:1433?database=sales'
rptgo extract Invoices.rpt -proc spInvoices -param Company=1 -param AsOf=2024-03-31
rptgo render Invoices.rpt -proc spInvoices -param Company=1 -param AsOf=2024-03-31 -out invoices.pdf
```

## For agents

The commands write Markdown meant to go straight into a model's context. With
`-json` they write the same content under a versioned schema
(`rptgo.describe/v1`, `rptgo.extract/v1`, `rptgo.index/v1`). `rptgo skill`
prints a guide for an agent ([SKILL.md](SKILL.md)).

- **describe** reads a report the way a person reading the printout would:
  - where the data comes from;
  - the groups and sorts;
  - the **columns** of the detail line: the heading over each value, its
    format, and the totals printed under it in each footer;
  - every formula, with what it uses and what uses it;
  - the running totals;
  - the layout, as rows of text and values per section, with hidden sections
    and their conditions.
- **audit** lists, per report:
  - what rptgo does not reproduce: subreports, charts, group selection,
    unsupported functions, formulas it cannot parse;
  - what may differ from Crystal: record selection, pictures;
  - what nothing uses: dead formulas, totals and parameters.

  `-fail-on error` turns it into a CI gate.
- **index** catalogs a folder of reports:
  - title, data, groups, columns and audit counts per report;
  - which reports use each field and parameter;
  - which reports share the same logic under different layouts.
- **extract** runs the report over its rows and returns what it prints as data:
  - header values;
  - nested groups with their header values, detail rows and footer totals,
    named after the column headings;
  - report totals.

  Rows come from the stored procedure or from a JSON, JSON Lines or CSV file.
  `-max-rows` trims detail rows and keeps every total.

## What is in it

| Package / command | What it does |
|---|---|
| `rpt` | Opens the OLE container, decrypts and inflates the `Contents` stream, walks the TSLV records and builds the report model: fields, formulas, running totals, groups, sorts, sections, objects with fonts, borders and number/date formats, lines and boxes, pictures, subreports, charts, cross-tabs, data sources |
| `rptgo` (root) | The commands (`describe`, `audit`, `index`, `extract`, `render`) and the engine: Crystal- and Basic-syntax formulas, banded layout (report/page/group headers and footers, details), running totals and summary fields, section suppress and new-page conditions, pagination with `Page N of M`, lines that span sections and pages; writes PDF natively or HTML, or the printed data as JSON |
| `cmd/rpt2json` | Writes the parsed model as JSON |
| `cmd/rpttree` | Prints the record tree of a `.rpt`, with `-hex <tag>` to dump a record's bytes |

### The reader

`rpt` is a Go port of the reader in
[majorsilence.crystal](https://github.com/majorsilence/majorsilence.crystal) by
Peter Gill, which did the hard part of the format work (see `NOTICE`). On a
corpus of 324 production reports the port writes the same JSON model as the C#
reader, byte for byte, once the fields below are masked (`scripts/diffref.sh`).

It also decodes what the C# reader does not:

- **Suppress if zero**: byte 1 of the numeric format record (tag 248), set in
  either of an object's two numeric records.
- **Negative format**: byte 2 of the same record (none, leading minus,
  brackets); the C# reader takes it for the currency symbol.
- **Currency symbol**: byte 10 (none, fixed, floating).
- **The object's own Suppress box**: byte 1 of the object properties (tag 252).
- **Running-total operation**: Sum, Count, DistinctCount, Average, Maximum,
  Minimum, ... (the C# reader reads every total as a Sum).
- **Running-total reset and evaluate conditions**: on a group change or on a
  formula.
- **Default margins**: a quarter inch, where the report leaves them to the
  printer.

### The renderer

The engine follows what Crystal's own PDF export does, measured against
printouts Crystal made from the same data, where words and positions were
compared:

- **Point sizes.** Crystal sets text at the size whose glyphs fill the GDI cell:
  an 8 pt Arial field prints at 7.15 pt, bold at 6.95 pt.
- **Clipping.** A one-line field is cut to the whole characters that fit;
  Crystal never prints half a glyph.
- **Placement.** Borders sit outside the object box; lines are stroked with
  square caps.
- **Numbers.** Numbers align right and text and dates left, unless the report
  says otherwise. A left-aligned number whose negatives take a minus keeps a
  space for it.

PDF output uses [gopdf](https://github.com/signintech/gopdf) with the
[Liberation](https://github.com/liberationfonts/liberation-fonts) fonts (Sans,
Serif, Mono), the metric twins of Arial, Times New Roman and Courier New,
embedded as subsets. The engine looks for them in
`/usr/share/fonts/truetype/liberation`; `-fonts` or `$RPTGO_FONTS` points
elsewhere. `-format html` writes HTML for a browser's print-to-PDF instead.

## Checking it

```bash
go test ./...                                   # unit tests and goldens on a synthetic report; no .rpt needed

RPTGO_FIXTURES=~/reports go test ./rpt/         # parse your own .rpt files
CORPUS=~/reports REF=~/reports-json bash scripts/diffref.sh   # compare with the C# reader's JSON
RPTGO_FIXTURES=~/reports bash scripts/compare.sh Invoices     # render and compare with a Crystal printout
```

`rpt` tests read optional files next to each fixture: `<name>.ref.json` (the C#
reader's JSON) and `<name>.expect.json` (facts such as the fields that suppress
a zero; see `rpt/rpt_test.go`). `scripts/compare.sh` describes its
`<name>.args` and `<name>.golden.pdf`. `go test -update` rewrites the goldens in
`testdata/golden` after an intended change.

## Limits

`rptgo audit` reports each of these where a report uses it.

- Data comes from a SQL Server stored procedure or a rows file. A report's
  own connection, SQL command or tables are described but not queried.
- Record and group selection formulas are not applied: the rows are taken as
  they come, as when an application feeds Crystal a filtered dataset.
- Subreports, cross-tabs and charts are parsed and described, not rendered or
  extracted. Pictures are not drawn.
- Summary fields print only in group and report footers (not headers, which
  would need the group's records ahead).
- WMF/EMF pictures are not rasterized: the C# reader does that only on Windows.
- The formula interpreter covers the functions common business reports use:
  string, number and date functions, `Previous`/`Next`, `Sum`/`Count`/`Average`/
  `Maximum`/`Minimum`/`DistinctCount` over the report or a group. It does not
  cover all of Crystal's.

## License

MIT, see `LICENSE`. The `rpt` package is a port of MIT-licensed code; its notice is in `NOTICE`.

## Notices

Crystal Reports is a trademark of SAP SE. This project is not affiliated with,
endorsed by or sponsored by SAP.
