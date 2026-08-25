#!/usr/bin/env bash
# Per-module coverage gate. Usage: scripts/coverage.sh [threshold]
set -euo pipefail
THRESHOLD="${1:-80}"
fail=0
for mod in $(find . -name go.mod -not -path '*/node_modules/*' -exec dirname {} \; | sort); do
  (cd "$mod" && go test ./... -coverprofile=cover.out -covermode=atomic > /dev/null)
  grep -v -e '/internal/gen/' -e '/cmd/' -e '/libs/go/contract/' "$mod/cover.out" > "$mod/cover.filtered" || [ $? -eq 1 ]
  # /cmd/ exclusion: entrypoint wiring is validated by smoke/e2e, not unit tests.
  # /libs/go/contract/ exclusion: fully oapi-codegen output at the module
  # root (no internal/gen/ wrapper to catch it via the pattern above).
  # A module whose only statements are generated has nothing to gate:
  # the filtered profile is header-only and counts as a vacuous pass.
  if [ "$(wc -l < "$mod/cover.filtered")" -le 1 ]; then
    printf '%-40s %6s\n' "$mod" "n/a (generated only)"
    continue
  fi
  pct=$(cd "$mod" && go tool cover -func=cover.filtered | tail -1 | awk '{print $3}' | tr -d '%')
  printf '%-40s %6s%%\n' "$mod" "$pct"
  awk -v p="$pct" -v t="$THRESHOLD" 'BEGIN{exit !(p+0 < t)}' && { echo "  FAIL: below ${THRESHOLD}%"; fail=1; }
done
exit $fail
