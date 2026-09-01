#!/usr/bin/env bash
# Development helper for testing SerialForge without physical hardware, on
# macOS/Linux, using a socat-linked virtual serial (PTY) pair. Not a runtime
# dependency of SerialForge itself — this is a dev/test-only shell script;
# the equivalent automated coverage lives in
# internal/serial/pty_test.go (raw byte fidelity) and
# internal/session/pty_test.go (structured packet decode), both skipped
# automatically if socat isn't installed.
#
# Usage:
#   scripts/pty-dev-test.sh            # automated send/receive smoke check
#   scripts/pty-dev-test.sh --manual   # just set up the PTY pair and print
#                                       # instructions for interactive testing
#
# Requires: socat (macOS: `brew install socat`), go.
set -euo pipefail

# Portable timeout: macOS ships neither GNU `timeout` nor `gtimeout` by
# default, so a bounded-wait helper has to be hand-rolled rather than
# relying on either being installed.
run_timeout() {
  local secs="$1"; shift
  "$@" &
  local cmd_pid=$!
  ( sleep "$secs"; kill -9 "$cmd_pid" 2>/dev/null ) &
  local watcher_pid=$!
  wait "$cmd_pid" 2>/dev/null
  local status=$?
  kill "$watcher_pid" 2>/dev/null
  wait "$watcher_pid" 2>/dev/null
  return $status
}

LINK_A="/tmp/serialforge-a"
LINK_B="/tmp/serialforge-b"
BIN_DIR="$(mktemp -d)"
BIN="$BIN_DIR/serialforge"
SOCAT_PID=""

cleanup() {
  [ -n "$SOCAT_PID" ] && kill "$SOCAT_PID" 2>/dev/null || true
  rm -rf "$BIN_DIR" "$LINK_A" "$LINK_B"
}
trap cleanup EXIT

if ! command -v socat >/dev/null 2>&1; then
  echo "error: socat not found — install it first (macOS: brew install socat)" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
echo "== building serialforge =="
go -C "$repo_root" build -o "$BIN" ./cmd/serialforge

echo "== starting socat PTY pair =="
rm -f "$LINK_A" "$LINK_B"
socat -d -d \
  pty,raw,echo=0,link="$LINK_A" \
  pty,raw,echo=0,link="$LINK_B" \
  2>/tmp/serialforge-pty-dev-test-socat.log &
SOCAT_PID=$!

for i in $(seq 1 50); do
  [ -e "$LINK_A" ] && [ -e "$LINK_B" ] && break
  sleep 0.1
done
if [ ! -e "$LINK_A" ] || [ ! -e "$LINK_B" ]; then
  echo "error: socat did not create both PTY links — see /tmp/serialforge-pty-dev-test-socat.log" >&2
  exit 1
fi
echo "  $LINK_A <-> $LINK_B  (socat pid $SOCAT_PID)"

if [ "${1:-}" = "--manual" ]; then
  cat <<EOF

PTY pair ready. In one terminal:
  $BIN monitor --port $LINK_A --baud 115200 --hex

In another, inject bytes as if a device were transmitting:
  printf '\xAA\x55\x01\x02' > $LINK_B

Or use --path instead of --port (they're equivalent), or save it as a
reusable alias first:
  $BIN device add --alias virtual --path $LINK_A --baud 115200
  $BIN monitor virtual --hex

This script's PTY pair is torn down when you Ctrl+C it — the monitor
process above will need to be stopped separately.
EOF
  echo "Press Ctrl+C to tear down the PTY pair."
  wait "$SOCAT_PID"
  exit 0
fi

echo "== automated smoke check: TX (SerialForge -> socat) =="
# Binary-safety test vector: embedded NUL (0x00) and non-ASCII bytes.
TEST_HEX="AA5502C017FF0080"
"$BIN" send --port "$LINK_A" --hex "$TEST_HEX" --baud 115200
GOT_TX=$(run_timeout 3 head -c 8 "$LINK_B" | xxd -p | tr -d '\n')
WANT_TX=$(echo -n "$TEST_HEX" | tr '[:upper:]' '[:lower:]')
if [ "$GOT_TX" = "$WANT_TX" ]; then
  echo "  PASS: socat received $GOT_TX"
else
  echo "  FAIL: socat received $GOT_TX, want $WANT_TX"
  exit 1
fi

echo "== automated smoke check: RX (socat -> SerialForge) =="
printf '\xAA\x55\x02\xC0\x17\xFF\x00\x80' > "$LINK_B" &
# The monitor line looks like "15:04:05.000 RX AA 55 02 ..." — anchor on
# "RX " specifically so nothing in the leading timestamp (which also
# contains digit pairs) is mistaken for the payload.
GOT_RX=$(run_timeout 3 "$BIN" monitor --port "$LINK_A" --baud 115200 --hex 2>/dev/null | head -1 | sed -n 's/.* RX \([0-9A-F][0-9A-F ]*\).*/\1/p' | tr -d ' ')
WANT_RX="aa5502c017ff0080"
GOT_RX_LC=$(echo -n "$GOT_RX" | tr '[:upper:]' '[:lower:]')
if [ "$GOT_RX_LC" = "$WANT_RX" ]; then
  echo "  PASS: SerialForge received $GOT_RX"
else
  echo "  FAIL: SerialForge received '$GOT_RX', want $WANT_RX"
  exit 1
fi

echo "== all smoke checks passed =="
