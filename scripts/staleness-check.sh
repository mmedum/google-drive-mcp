#!/usr/bin/env bash
# Fails when the documentation drifts from the code:
#  - README's tool table must list exactly the registered tools;
#  - docs/configuration.md must mention every GDRIVE_* variable config.go defines;
#  - CHANGELOG.md must have content under [Unreleased] when source changed since the last tag;
#  - docs/architecture.md must not claim "no code yet" once code exists.
set -euo pipefail
BIN=${1:-./google-drive-mcp}
fail=0

tools=$("$BIN" --dump-schemas | python3 -c 'import json,sys; print("\n".join(t["name"] for t in json.load(sys.stdin)["tools"]))')
readme_tools=$(grep -oE '^\| `[a-z_]+` \|' README.md | tr -d '`| ' | sort)
if [ "$(echo "$tools" | sort)" != "$readme_tools" ]; then
  echo "README tool table differs from registered tools:"
  diff <(echo "$tools" | sort) <(echo "$readme_tools") || true
  fail=1
fi

# Every env var config.go defines has to be documented. The names come
# from the Define calls, so a new setting fails here until it is written up.
for var in $(grep -oE 'def\(&s\.[A-Za-z]+, "[a-z-]+", "[A-Z_]+"' internal/config/config.go | grep -oE '"[A-Z_]+"$' | tr -d '"'); do
  if ! grep -q "GDRIVE_$var" docs/configuration.md; then
    echo "docs/configuration.md does not document GDRIVE_$var"
    fail=1
  fi
done

if grep -qi "no code yet" docs/architecture.md; then
  echo "docs/architecture.md still says 'no code yet'"
  fail=1
fi

# Source changes have to be written up. Normally that means content under
# [Unreleased]; on a release commit those notes have just moved under the
# new version heading, which is only acceptable while that version has no
# tag yet.
last=$(git describe --tags --abbrev=0 2>/dev/null || true)
range=${last:+$last..HEAD}
if ! git diff --quiet ${range:-HEAD~1} -- '*.go' 2>/dev/null; then
  notes=$(awk '/^## \[Unreleased\]/{f=1;next} /^## \[/{f=0} f' CHANGELOG.md | grep -c '^- ' || true)
  where="[Unreleased]"
  if [ "$notes" -eq 0 ]; then
    pending=$(awk 'match($0, /^## \[([0-9]+\.[0-9]+\.[0-9]+)\]/, m) {print m[1]; exit}' CHANGELOG.md)
    if [ -n "$pending" ] && ! git rev-parse -q --verify "refs/tags/v$pending" >/dev/null; then
      notes=$(awk -v v="$pending" 'index($0, "## [" v "]")==1{f=1;next} /^## \[/{f=0} f' CHANGELOG.md | grep -c '^- ' || true)
      where="[$pending] (untagged, so this is a release commit)"
    fi
  fi
  if [ "$notes" -eq 0 ]; then
    echo "source changed since ${last:-the previous commit} but CHANGELOG.md $where is empty"
    fail=1
  fi
fi

[ $fail = 0 ] && echo "staleness check ok"
exit $fail
