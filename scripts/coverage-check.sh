#!/usr/bin/env bash
# Enforces a statement-coverage floor per core package. The profile comes
# from -coverpkg=./internal/... so cross-package coverage counts; blocks
# then appear once per test binary and are de-duplicated here.
set -euo pipefail
PROFILE=${1:-cov.out}
MIN=${2:-80}
MODULE=github.com/mmedum/google-drive-mcp
fail=0
for pkg in internal/config internal/credentials internal/auth internal/gapi internal/ref internal/model internal/render internal/service internal/tools internal/server; do
  pct=$(awk -v p="$MODULE/$pkg/" 'NR>1 && index($1, p)==1 {
      if (!($1 in stmts)) stmts[$1]=$2;
      if ($3>0) hit[$1]=1 }
    END { for (k in stmts) { total+=stmts[k]; if (k in hit) cov+=stmts[k] }
          if (total) printf "%.1f", 100*cov/total; else print "0" }' "$PROFILE")
  printf '%-24s %6s%%\n' "$pkg" "$pct"
  if awk -v a="$pct" -v b="$MIN" 'BEGIN {exit !(a < b)}'; then fail=1; fi
done
[ $fail = 0 ] || { echo "coverage below ${MIN}% in at least one core package"; exit 1; }
