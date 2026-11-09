#!/usr/bin/env bash
set -euo pipefail
# Explicit release baseline in CI; before the first release, use the v1 fixture.
baseline_ref="${MIGRATION_BASELINE_REF:-}"
if [[ -z "$baseline_ref" ]]; then
  diff -u examples/api/testdata/baseline/00001_create_orders.sql examples/api/migrations/00001_create_orders.sql
  exit
fi
git rev-parse --verify "${baseline_ref}^{commit}" >/dev/null
while IFS= read -r migration; do
  [[ -f "$migration" ]] || { echo "missing published migration: $migration" >&2; exit 1; }
  git show "$baseline_ref:$migration" | cmp - "$migration"
done < <(git ls-tree -r --name-only "$baseline_ref" -- examples/api/migrations/)
