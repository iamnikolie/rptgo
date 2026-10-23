---
name: rptgo
description: Read Crystal Reports .rpt files without Crystal. Describe a report (data, groups, columns, formulas, running totals, layout) as Markdown or JSON for an agent to reason over; audit what rptgo does not reproduce; index a folder of reports with cross-references; extract the data a report prints (header, groups, rows, totals) from a SQL Server procedure or a rows file; render it to PDF or HTML.
---

# rptgo

`rptgo` turns Crystal Reports `.rpt` files into context an agent can work with.
It needs no Crystal runtime, .NET or Windows. Every command writes Markdown to
stdout; add `-json` for the same content as JSON with a stable schema
(`rptgo.describe/v1`, `rptgo.extract/v1`, `rptgo.index/v1`).

## Which command

| You want to | Run |
| --- | --- |
| understand one report: what data it reads, how it groups, what each column and formula is | `rptgo describe report.rpt` |
| know whether rptgo can reproduce a report, or what in it is dead | `rptgo audit report.rpt` (or a folder) |
| survey many reports: titles, tables and fields used, parameters, duplicates | `rptgo index folder/` |
| get the numbers a report prints | `rptgo extract report.rpt -proc spName -param Name=Value` or `-data rows.json` |
| produce the printout | `rptgo render report.rpt ... -out report.pdf` |

## describe

```bash
rptgo describe Invoices.rpt            # Markdown
rptgo describe Invoices.rpt -json      # same content as JSON
rptgo describe Invoices.rpt -layout    # adds each object's box in inches, and hidden sections' contents
```

Sections of the Markdown, in order:

- **Data**: the data source and tables, the fields the report uses (with types),
  joins, record and group selection formulas, groups, sorts.
- **Parameters**: name, type, prompt, default, choices, and where each is used.
- **Columns**: the detail line left to right. Each column has its heading (or
  `(name)` when the layout has none), its value, type, format, and the totals
  printed under it in each group and report footer. Start here to understand
  a tabular report.
- **Formulas**: each formula with type, syntax (Crystal or Basic), what it uses,
  what uses it ("nothing (unused)" marks dead ones), and its source.
- **Running totals**: what each computes, when it resets and evaluates, where it prints.
- **Layout**: every section in design order (Report Header, Page Header,
  Group Header #1..., Details, Group Footer ...#1, Report Footer, Page Footer), each
  printed row as `"text" · {value} (format, alignment, style)`. Hidden sections
  and their conditions ("hidden when ...", "new page before when ...") are noted.
- **Subreports**: each described in full, nested.
- **Caveats**: the audit's errors and warnings.

Reference notation, as in Crystal formulas: `{Table.Field}` database field,
`{@Name}` formula, `{#Name}` running total, `{?Name}` parameter,
`[Page N of M]` special field, `Sum of {Table.Field}` summary field.
Formats are .NET custom format strings (`#,##0.00`, `M'/'d'/'yy`).

## audit

```bash
rptgo audit Invoices.rpt
rptgo audit reports/ -json
rptgo audit reports/ -fail-on error    # exit 1 if any report has an error (for CI)
```

Each finding is `severity [code] where: message`:

- **error**: rptgo's output is wrong or missing there. Codes: `formula-syntax`,
  `function-unsupported`, `subreport`, `chart`, `crosstab`, `special-field`,
  `summary-placement`, `group-selection`, `group-topn`.
- **warning**: output may differ from Crystal's. Codes: `record-selection`
  (rptgo prints every row it is given), `reference-unknown`, `name-unknown`,
  `picture`, `group-specified-order`.
- **info**: no effect on output. Codes: `unused` (formula, total or parameter
  nothing uses), `own-data`, `conditional-format`.

A report with no errors or warnings renders and extracts as Crystal would.

## index

```bash
rptgo index reports/               # Markdown catalog
rptgo index reports/ -json         # everything, including field -> reports
```

The Markdown has one row per report (title, data, groups, columns, counts of
formulas and totals, audit counts), then **Tables** (each field and how many
reports use it), **Parameters** (which reports take each) and **Same logic**
(reports that compute the same thing with a different layout). The JSON
`fields` map answers "which reports use `{Sales.Amount}`".

## extract

Rows come from a SQL Server stored procedure or from a file:

```bash
export RPTGO_DSN='sqlserver://user:pass@host:1433?database=sales'
rptgo extract Invoices.rpt -proc spInvoices -param Company=1 -param AsOf=2024-03-31
rptgo extract Invoices.rpt -data rows.json        # JSON array of objects, .jsonl, or .csv
rptgo extract Invoices.rpt -proc spInvoices -param AsOf=2024-03-31 -dump-rows rows.json   # keep the rows for reuse
rptgo extract Invoices.rpt -data rows.json -max-rows 50 -json
```

- `-param Name=Value` goes to the procedure and to the report's `{?Name}`.
  A date is `2024-03-31`.
- In a rows file, keys are column names (`Amount` or `Sales.Amount`). Strings are converted
  to the report's field types (dates, numbers, booleans).
- `-max-rows N` keeps N detail rows and leaves every group and total complete.
  Use it to keep context small. `-max-rows 0` gives the groups and totals only.
  Omitted rows are counted (`omittedRows`).
- `-printed "2024-04-01 09:00:00"` fixes the print timestamp.

Output: the header values, then each group (its header values, its detail rows
as a table, its footer totals by column name, nested groups), then the report
totals, then warnings. In JSON, `columns` names the detail values and every
`rows` entry is an array in that order with raw values: numbers, dates as
`YYYY-MM-DD`, strings. In Markdown, values appear as the report prints them.
A report laid out as a form (`"form": true`) lists each record's values
instead of a table.

The engine applies groups, sorts, formulas (Crystal and Basic syntax),
running totals, summary fields in footers, `Sum`/`Count`/... over a group,
`Previous`/`Next`, and section suppress formulas, as Crystal does. Check
`rptgo audit` for what it does not apply.

## render

```bash
rptgo render Invoices.rpt -proc spInvoices -param AsOf=2024-03-31 -out invoices.pdf
rptgo render Invoices.rpt -data rows.json -out invoices.html
```

The PDF needs the Liberation fonts (`-fonts` or `$RPTGO_FONTS`, default
`/usr/share/fonts/truetype/liberation`).

## Recipes

- **Explain a report**: `describe`, then read Columns and Formulas.
- **Migrate a report to SQL and HTML**: `describe -json` gives the data
  fields, groups, formulas with dependencies, and the columns with their totals.
  `audit` lists what needs hand work. `extract -json` on real rows is the
  expected output to test the new version against.
- **Answer a question from a report's numbers**: `extract -max-rows 0` gives
  the totals only. Add rows only when the question needs them.
- **Survey a legacy report folder**: `index`, then `audit folder/ -json`
  for the full list. `Same logic` groups are candidates to merge.
