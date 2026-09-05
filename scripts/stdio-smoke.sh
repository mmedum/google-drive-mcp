#!/usr/bin/env bash
# Drives the binary over stdio without credentials: initialize, list tools
# and resource templates, and call a tool, which must answer with an
# [auth] tool error rather than crash or exit. Run once per protocol
# version so an SDK upgrade that drops one is caught here.
set -euo pipefail
BIN=${1:-./google-drive-mcp}
TMP=$(mktemp -d)
trap 'rm -r -f "$TMP"' EXIT
export GDRIVE_CONFIG_DIR="$TMP/cfg" GDRIVE_LOG_LEVEL=error

# The newest version the SDK knows, and the one it negotiates down to.
# go-sdk v1.7.0 answers every initialize with 2025-11-25: initialize is
# deprecated in 2026-07-28, so the handshake caps there by design. Both
# requests must still produce a working session.
for proto in 2026-07-28 2025-11-25; do
  out="$TMP/out-$proto.jsonl"
  {
    echo "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"$proto\",\"capabilities\":{},\"clientInfo\":{\"name\":\"smoke\",\"version\":\"0\"}}}"
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
    echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_file","arguments":{"file":"1SyntheticFixtureFileIdAAAAAAAAAAAA"}}}'
    echo '{"jsonrpc":"2.0","id":4,"method":"resources/templates/list"}'
    sleep 1
  } | timeout 20 "$BIN" > "$out"
  grep -q '"id":1' "$out" || { echo "$proto: no initialize response"; cat "$out"; exit 1; }
  grep -q '"protocolVersion":"2025-11-25"' "$out" || { echo "$proto: server did not negotiate a protocol version"; cat "$out"; exit 1; }
  for tool in get_account get_file search_files list_folder; do
    grep -q "\"name\":\"$tool\"" "$out" || { echo "$proto: $tool missing from tools/list"; exit 1; }
  done
  grep '"id":3' "$out" | grep -q '"isError":true' || { echo "$proto: tool call without credentials should be a tool error"; cat "$out"; exit 1; }
  grep '"id":3' "$out" | grep -q '\[auth\]' || { echo "$proto: tool error should carry the [auth] class"; exit 1; }
  grep -q '"id":4' "$out" || { echo "$proto: no resources/templates/list response"; exit 1; }
done

# Stdout must carry JSON-RPC frames and nothing else.
while IFS= read -r line; do
  [ -z "$line" ] && continue
  printf '%s' "$line" | python3 -c 'import json,sys; json.loads(sys.stdin.read())' \
    || { echo "non-JSON line on stdout: $line"; exit 1; }
done < "$TMP/out-2025-11-25.jsonl"

# A tool the configuration removes must not be registered.
GDRIVE_READ_ONLY=true bash -c '
  { echo "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-11-25\",\"capabilities\":{},\"clientInfo\":{\"name\":\"smoke\",\"version\":\"0\"}}}"
    echo "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}"
    echo "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}"
    sleep 1
  } | timeout 20 "$0" > "$1"
' "$BIN" "$TMP/readonly.jsonl"
grep -q '"name":"get_file"' "$TMP/readonly.jsonl" || { echo "read-only mode dropped a read tool"; exit 1; }

echo "stdio smoke ok"
