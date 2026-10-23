#!/usr/bin/env bash
# diffref.sh [name-filter] -- parse every .rpt under $CORPUS with the Go
# parser and compare its JSON with the C# reference reader's
# (majorsilence.crystal), both normalized with jq -S. $REF mirrors $CORPUS:
# $CORPUS/a/b.rpt is compared with $REF/a/b.json. Prints one line per file
# and a summary; for a file that differs, the first differing JSON path.
#
# Known, intended departures from the reference are masked before comparing
# (and counted on their own line):
#   - RunningTotalField.Function: the C# reader reads every total as a Sum;
#     the Go one decodes the summary operation.
#   - RunningTotalField.ResetCondition / EvaluateCondition, which the C#
#     reader leaves null; the Go one decodes "Group:<level>" and
#     "Formula:<text>".
#   - ObjectFormat.Suppress (the object's own Suppress box),
#     SuppressIfZero and NegativeFormat, which the C# reader does not
#     decode; written only when set.
#   - The currency symbol in FormatString: the C# reader shows it by the
#     negative-format byte; the Go one by the currency-symbol byte.
set -uo pipefail
cd "$(dirname "$0")/.."
CORPUS=${CORPUS:?set CORPUS to a directory of .rpt files}
REF=${REF:?set REF to the reference JSON directory}
OUT=${OUT:-${TMPDIR:-/tmp}/rptgo-json}
filter=${1:-}
go build -o "$OUT.rpt2json" ./cmd/rpt2json || exit 1
mkdir -p "$OUT"
MASK='walk(if type == "object" and ."$type" == "RunningTotalField" then .Function = "*" | .ResetCondition = null | .EvaluateCondition = null elif type == "object" and has("CanGrow") then del(.Suppress, .SuppressIfZero, .NegativeFormat) | (if .FormatString then .FormatString |= gsub("\"[$]\"|\"[ \u00a0]\""; "") else . end) else . end)'
decoded=0; suppressed=0; conditions=0; zeros=0
same=0; raw=0; differ=0; both_fail=0; only_go_fail=0; only_ref_fail=0
while IFS= read -r -d '' f; do
    rel=${f#"$CORPUS"/}; rel=${rel%.*}; name=$(basename "$rel")
    mkdir -p "$OUT/$(dirname "$rel")"
    [[ -n "$filter" && "$name" != *$filter* ]] && continue
    ref="$REF/$rel.json"
    got="$OUT/$rel.json"
    if ! "$OUT.rpt2json" "$f" > "$got" 2>"$OUT/$rel.err"; then
      if [[ -f "$ref" ]]; then echo "GOFAIL $rel"; only_go_fail=$((only_go_fail+1)); else echo "BOTHFAIL $rel"; both_fail=$((both_fail+1)); fi
      continue
    fi
    if [[ ! -f "$ref" ]]; then echo "REFFAIL $rel"; only_ref_fail=$((only_ref_fail+1)); continue; fi
    if cmp -s <(jq -S "$MASK" "$ref") <(jq -S "$MASK" "$got"); then
      same=$((same+1))
      cmp -s "$ref" "$got" && raw=$((raw+1))
      decoded=$((decoded + $(jq '[.. | objects | select(."$type" == "RunningTotalField" and .Function != "Sum")] | length' "$got")))
      conditions=$((conditions + $(jq '[.. | objects | select(."$type" == "RunningTotalField") | .ResetCondition, .EvaluateCondition | select(. != null)] | length' "$got")))
      suppressed=$((suppressed + $(jq '[.. | objects | select(has("CanGrow") and .Suppress == true)] | length' "$got")))
      zeros=$((zeros + $(jq '[.. | objects | select(has("CanGrow") and .SuppressIfZero == true)] | length' "$got")))
    else
      differ=$((differ+1))
      path=$(python3 - <(jq "$MASK" "$ref") <(jq "$MASK" "$got") <<'PY'
import json, sys
a, b = json.load(open(sys.argv[1])), json.load(open(sys.argv[2]))
def walk(x, y, p):
    if type(x) != type(y): return p + f"  ref={str(x)[:80]!r} go={str(y)[:80]!r}"
    if isinstance(x, dict):
        for k in sorted(set(x) | set(y)):
            if k not in x or k not in y: return f"{p}.{k}  missing in {'go' if k in x else 'ref'}"
            r = walk(x[k], y[k], f"{p}.{k}")
            if r: return r
        return None
    if isinstance(x, list):
        for i, (u, v) in enumerate(zip(x, y)):
            r = walk(u, v, f"{p}[{i}]")
            if r: return r
        if len(x) != len(y): return f"{p}  len ref={len(x)} go={len(y)}"
        return None
    return None if x == y else p + f"  ref={str(x)[:80]!r} go={str(y)[:80]!r}"
print(walk(a, b, "$"))
PY
)
      echo "DIFF $rel  $path"
    fi
done < <(find "$CORPUS" -iname '*.rpt' -print0 | sort -z)
echo "masked: running totals other than Sum $decoded, total conditions $conditions, suppressed objects $suppressed, suppress-if-zero $zeros"
echo "identical=$same (byte-identical=$raw) differ=$differ go-fails=$only_go_fail ref-fails=$only_ref_fail both-fail=$both_fail"
