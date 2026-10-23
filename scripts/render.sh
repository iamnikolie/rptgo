#!/usr/bin/env bash
# render.sh <report.rpt> <out.pdf|out.html> <proc> [Name=Value ...]
#
# Builds rptgo, runs the stored procedure with the parameters and renders the
# report. The database comes from $RPTGO_DSN, the print timestamp from
# $PRINTED ("2006-01-02 15:04:05", default now). With CHROME=1 an .html output
# is also printed to PDF by headless Chrome (docker), for comparison.
set -euo pipefail
rpt=$1; out=$2; proc=$3; shift 3
: "${RPTGO_DSN:?set RPTGO_DSN=sqlserver://user:pass@host:port?database=name}"
root=$(cd "$(dirname "$0")/.." && pwd)
(cd "$root" && go build -o rptgo .)
args=(); for p in "$@"; do args+=(-param "$p"); done
mkdir -p "$(dirname "$out")"
"$root/rptgo" -rpt "$rpt" -proc "$proc" "${args[@]}" ${PRINTED:+-printed "$PRINTED"} -out "$out"
if [[ -n "${CHROME:-}" && "$out" == *.html ]]; then
  dir=$(cd "$(dirname "$out")" && pwd); name=$(basename "$out" .html)
  docker run --rm --user "$(id -u):$(id -g)" -v "$dir:/out" \
    -v "${RPTGO_FONTS:-/usr/share/fonts/truetype/liberation}:/usr/share/fonts/liberation:ro" \
    zenika/alpine-chrome:latest --no-sandbox --disable-gpu --no-pdf-header-footer \
    --print-to-pdf="/out/$name.chrome.pdf" "file:///out/$name.html" >/dev/null 2>&1
fi
