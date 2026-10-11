#!/usr/bin/env bash
# Runs unit and integration tests with coverage across every package, drops
# generated files (sqlc, oapi-codegen) from the profile, and fails when the
# total is below the threshold.
#
# Usage: scripts/coverage.sh [threshold]   (default 85)
# Output: coverage.out (filtered profile), coverage summary on stdout.
set -euo pipefail

threshold="${1:-85}"
raw="coverage.raw.out"
profile="coverage.out"
module="$(go list -m)"

# -count=1: cached results from packages that do not import a changed file
# still report its old block positions under -coverpkg.
go test -count=1 -race -tags=integration -covermode=atomic -coverpkg=./... -coverprofile="$raw" ./...

# Profile lines are "<module>/<path>:<range> ...". Exclude every file that
# carries the standard "Code generated ... DO NOT EDIT." header.
patterns="$(mktemp)"
trap 'rm -f "$patterns" "$raw"' EXIT
git ls-files --cached --others --exclude-standard -- '*.go' \
	| xargs grep -l -E '^// Code generated .* DO NOT EDIT\.$' 2>/dev/null \
	| sed "s|^|$module/|; s|$|:|" >"$patterns" || true

filtered="$(mktemp)"
trap 'rm -f "$patterns" "$raw" "$filtered"' EXIT
if [ -s "$patterns" ]; then
	grep -v -F -f "$patterns" "$raw" >"$filtered"
else
	cp "$raw" "$filtered"
fi

# With -coverpkg every test binary reports every package, so each block
# appears once per package, mostly with count 0 from binaries that never ran
# it. Merge duplicates by summing counts, or the total is understated.
awk 'NR == 1 { print; next }
	{ key = $1 " " $2; if (!(key in count)) order[++n] = key; count[key] += $3 }
	END { for (i = 1; i <= n; i++) print order[i], count[order[i]] }' "$filtered" >"$profile"

total="$(go tool cover -func="$profile" | awk '/^total:/ { sub(/%/, "", $3); print $3 }')"
echo "coverage: ${total}% of statements (generated code excluded, threshold ${threshold}%)"

if awk -v t="$total" -v min="$threshold" 'BEGIN { exit !(t < min) }'; then
	echo "coverage ${total}% is below the ${threshold}% threshold" >&2
	exit 1
fi
