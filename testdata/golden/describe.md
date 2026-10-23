# Sales by Region

Report `sales.json` · Letter portrait, margins 0.5 in · author Example

> Invoices by region, with margins and each invoice's share of its region.

## Data

- Source `DataSource1`: an ADO.NET dataset (`SalesData`) the application fills; rptgo takes these rows from a stored procedure (`-proc`) or a file (`-data`).
  - Table `Sales`, fields used: InvoiceNo String, InvoiceDate DateTime, Region String, Rep String, Amount Currency, Cost Currency, Status String, Notes String
- Group #1: {Sales.Region}, ascending
- Sorted within groups by {Sales.InvoiceDate} ascending

## Parameters

| Parameter | Type | Prompt | Default | Used by |
| --- | --- | --- | --- | --- |
| {?MinAmount} | Number | Flag invoices of at least | 1000 | {@Flag}, Report Header |

## Columns

Each detail line prints, left to right:

| Column | Value | Type | Format | Group Footer #1 | Report Footer |
| --- | --- | --- | --- | --- | --- |
| Invoice | {Sales.InvoiceNo} | String |  |  |  |
| Date | {Sales.InvoiceDate} | DateTime | `M'/'d'/'yy` | Count of {Sales.InvoiceNo} |  |
| Rep | {Sales.Rep} | String |  |  |  |
| Amount | {Sales.Amount} | Currency | `#,##0.00` | {#RegionAmt} | {#GrandAmt} |
| Margin | {@Margin} | Number | `#,##0.00` blank if 0 | {#RegionMargin} | Maximum of {Sales.Amount} |
| Margin % | {@MarginPct} | Number | `0.0` |  |  |
| Share % | {@Share} | Number | `0` |  |  |
| (Flag) | {@Flag} | String |  |  |  |

## Formulas

### {@Margin}

Number, Crystal syntax. Uses {Sales.Amount}, {Sales.Cost}. Used by {@MarginPct}, {#RegionMargin}, Details.

```
{Sales.Amount} - {Sales.Cost}
```

### {@MarginPct}

Number, Basic syntax. Uses {Sales.Amount}, {@Margin}. Used by Details.

```
if {Sales.Amount} <> 0 then
    formula = {@Margin} / {Sales.Amount} * 100
end if
```

### {@Share}

Number, Crystal syntax. Uses {Sales.Amount}, {Sales.Region}. Used by Details.

```
{Sales.Amount} / Sum({Sales.Amount}, {Sales.Region}) * 100
```

### {@Flag}

String, Crystal syntax. Uses {Sales.Amount}, {?MinAmount}. Used by Details.

```
if {Sales.Amount} >= {?MinAmount} then "*" else ""
```

### {@Legacy}

String, Crystal syntax. Uses {Sales.Rep}, {Sales.Notes}. Used by nothing (unused).

```
ProperCase({Sales.Rep}) & Chr(9) & {Sales.Notes}
```

### {@Broken}

Number, Crystal syntax. Uses {Sales.Amount}. Used by nothing (unused).

```
Round({Sales.Amount}, 2) +
```

## Running totals

| Total | Computes | Resets | Evaluates | Used by |
| --- | --- | --- | --- | --- |
| {#RegionAmt} | Sum of {Sales.Amount} | on change of group #1 ({Sales.Region}) | every record | Group Footer #1 |
| {#RegionMargin} | Sum of {@Margin} | on change of group #1 ({Sales.Region}) | every record | Group Footer #1 |
| {#GrandAmt} | Sum of {Sales.Amount} | never | every record | Report Footer |
| {#Big} | Count of {Sales.Amount} | never | every record | nothing (unused) |

## Layout

Sections in design order; each line is one printed row, left to right.

### Report Header

- "Sales by Region" (center, 14 pt bold)
- "Flagged from:" · {?MinAmount} (`#,##0.00`)

### Page Header

- "Invoice" (bold) · "Date" (bold) · "Rep" (bold) · "Amount" (right, bold) · "Margin" (right, bold) · "Margin %" (right, bold) · "Share %" (right, bold)

### Group Header #1: {Sales.Region}

- "Region:" (bold) · {Sales.Region} (bold)

### Details

_hidden when `{Sales.Status} = "Void"`; 1 hidden object not shown_

- {Sales.InvoiceNo} · {Sales.InvoiceDate} (`M'/'d'/'yy`) · {Sales.Rep} · {Sales.Amount} (`#,##0.00`) · {@Margin} (`#,##0.00`, blank if 0) · {@MarginPct} (`0.0`) · {@Share} (`0`) · {@Flag}

### Group Footer #1: {Sales.Region}

_new page after when `not OnLastRecord`_

- "Region total:" (bold) · Count of {Sales.InvoiceNo} (`0`) · {#RegionAmt} (`#,##0.00`) · {#RegionMargin} (`#,##0.00`)

### Report Footer

- "Grand total:" (bold) · {#GrandAmt} (`#,##0.00`) · Maximum of {Sales.Amount} (`#,##0.00`)
- [Bar chart "Amount by region": Sum of {Sales.Amount} by {Sales.Region}]

### Page Footer

- [Print Date] · [Page N of M] (right)

## Caveats

What rptgo does not reproduce of this report:

- error [formula-syntax] {@Broken}: rptgo cannot parse it (unexpected end of formula); it prints blank
- error [chart] Report Footer Graph1: chart "Amount by region" is not rendered

