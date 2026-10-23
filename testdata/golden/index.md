# Report index

1 report. Audit counts are errors/warnings/info (`rptgo audit <report>` lists them).

| Report | Title | Data | Groups | Columns | Formulas | Totals | Audit |
| --- | --- | --- | --- | --- | --- | --- | --- |
| sales.json | Sales by Region | Dataset SalesData; params MinAmount | {Sales.Region} | Invoice, Date, Rep, Amount, Margin, Margin %, Share %, Flag | 6 | 4 | 1/0/3 |

## Tables

Fields the reports print or compute with, and how many reports use each (`rptgo index -json` lists the reports).

- `Sales`, 1 report: Amount (1), Cost (1), InvoiceDate (1), InvoiceNo (1), Notes (1), Region (1), Rep (1), Status (1)

## Parameters

- {?MinAmount}: sales.json

