# Sales by Region: data

Report `sales.json` · printed 2026-03-31 10:00:00 · 6 records · parameters MinAmount: 1000

**Header:** Flagged from: 1,000.00

## Region: East

| Invoice | Date | Rep | Amount | Margin | Margin % | Share % | Flag |
| --- | --- | --- | --- | --- | --- | --- | --- |
| B-201 | 3/3/26 | Sam Ortiz | 2,500.00 | 1,000.00 | 40.0 | 100 | * |
| B-202 | 3/6/26 | Sam Ortiz | 0.00 |  | 0.0 | 0 |  |

**Totals (East):** Region total: 2 · Amount: 2,500.00 · Margin: 1,000.00

## Region: North

| Invoice | Date | Rep | Amount | Margin | Margin % | Share % | Flag |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A-102 | 3/1/26 | ana diaz | 800.00 |  | 0.0 | 29 |  |
| A-101 | 3/2/26 | KIM LEE | 1,200.00 | 300.00 | 25.0 | 43 | * |
| A-103 | 3/5/26 | Kim Lee | 500.00 | -150.00 | -30.0 | 18 |  |

**Totals (North):** Region total: 4 · Amount: 2,800.00 · Margin: 350.00

**Report totals:** Amount: 5,300.00 · Maximum of Amount: 2,500.00

**Chart "Amount by region"** (Report Footer; Sum of {Sales.Amount} by {Sales.Region}):

| {Sales.Region} | Sum of {Sales.Amount} |
| --- | --- |
| East | 2,500.00 |
| North | 2,800.00 |

## Warnings

- formula @Broken not evaluated: unexpected end of formula (x1)

