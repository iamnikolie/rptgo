#!/usr/bin/env bash
# compare.sh <name> -- render a fixture report and compare its words, page by
# page, with a reference PDF that Crystal itself printed.
#
# $RPTGO_FIXTURES holds, per name:
#   <name>.args        line 1 the stored procedure, line 2 the print timestamp,
#                      then one Name=Value parameter per line
#   <name>.rpt         the report; a name with a dot (Sales.march) is a second
#                      printout of Sales.rpt
#   <name>.golden.pdf  the reference printout
# Output goes to out/<name>.pdf. Needs $RPTGO_DSN and Python with PyMuPDF ($PY).
set -euo pipefail
name=$1
fx=${RPTGO_FIXTURES:?set RPTGO_FIXTURES to the fixtures directory}
root=$(cd "$(dirname "$0")/.." && pwd)
mapfile -t args < "$fx/$name.args"
PRINTED=${args[1]} bash "$root/scripts/render.sh" "$fx/${name%%.*}.rpt" "$root/out/$name.pdf" "${args[0]}" "${args[@]:2}" 2>/dev/null
"${PY:-python3}" -W ignore "$root/scripts/cmp.py" "$fx/$name.golden.pdf" "$root/out/$name.pdf"
