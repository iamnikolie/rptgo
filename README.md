# rptgo

A pure-Go reader for Crystal Reports `.rpt` files, and a banded report engine
that renders them to PDF, with no Crystal runtime, no .NET and no Windows.

```bash
go install github.com/iamnikolie/rptgo@latest

export RPTGO_DSN='sqlserver://user:pass@host:1433?database=sales'
rptgo -rpt Invoices.rpt -proc spInvoices -param Company=1 -param AsOf=2024-03-31 -out invoices.pdf
```

The report's data comes from a SQL Server stored procedure (via
[go-mssqldb](https://github.com/microsoft/go-mssqldb)), the way many
applications fed Crystal: the engine runs the procedure, then lays the rows out
by the report's own sections, groups, formulas, running totals and formats.

## What is in it

| Package / command | What it does |
|---|---|
| `rpt` | Opens the OLE container, decrypts and inflates the `Contents` stream, walks the TSLV records and builds the report model: fields, formulas, running totals, groups, sorts, sections, objects with fonts, borders and number/date formats, lines and boxes, pictures, subreports, charts, cross-tabs, data sources |
| `rptgo` (root) | The engine: Crystal- and Basic-syntax formulas, banded layout (report/page/group headers and footers, details), running totals, pagination with `Page N of M`, lines that span sections and pages; writes PDF natively or HTML |
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
go test ./...                                   # unit tests; no reports needed

RPTGO_FIXTURES=~/reports go test ./rpt/         # parse your own .rpt files
CORPUS=~/reports REF=~/reports-json bash scripts/diffref.sh   # compare with the C# reader's JSON
RPTGO_FIXTURES=~/reports bash scripts/compare.sh Invoices     # render and compare with a Crystal printout
```

`rpt` tests read optional files next to each fixture: `<name>.ref.json` (the C#
reader's JSON) and `<name>.expect.json` (facts such as the fields that suppress
a zero; see `rpt/rpt_test.go`). `scripts/compare.sh` describes its
`<name>.args` and `<name>.golden.pdf`.

## Limits

- Data comes only from a SQL Server stored procedure. There is no
  connection-string or command-table handling from the report itself.
- Subreports, cross-tabs, charts and pictures are parsed, not rendered.
  Cross-tabs are not checked against the C# reader either: the corpus has none.
- WMF/EMF pictures are not rasterized: the C# reader does that only on Windows.
- The formula interpreter covers the functions common business reports use, not
  all of Crystal's.

## License

MIT, see `LICENSE`. The `rpt` package is a port of MIT-licensed code; its notice is in `NOTICE`.

## Notices

Crystal Reports is a trademark of SAP SE. This project is not affiliated with,
endorsed by or sponsored by SAP.
