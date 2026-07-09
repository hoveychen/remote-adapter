#!/usr/bin/env bash
# e2e-paircode.sh — prove the single-binary pairing-code flow end to end on one
# host, with NO claude dependency (CI-friendly):
#
#   rca serve (libp2p, loopback) prints a pairing code
#   rca <toy binary> --code <code> runs the target injected; the toy binary
#   fopen()s a routed file (absolute path -> fs IO-RPC) and system()s a command
#   (posix_spawn under a remote cwd -> rca _spawn-proxy -> executor).
#
# Passes iff the executor log shows the routed OPEN and the spawned child sees
# the executor's RCC_EXECUTOR=1 marker.
#
# Requirements: macOS, Go, a C compiler. Usage: scripts/e2e-paircode.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(cd "$(mktemp -d /tmp/rccPAIR.XXXX)" && pwd -P)"
VFS="$WORK/vfs"
SERVE_OUT="$WORK/serve.out"
SERVE_LOG="$WORK/serve.log"
MARKER="PAIRCODE-4417"

SERVE_PID=""
cleanup() { [[ -n "$SERVE_PID" ]] && kill "$SERVE_PID" 2>/dev/null || true; }
trap cleanup EXIT

echo "== build =="
( cd "$REPO" && make >/dev/null )

echo "== toy target: fopen a routed file + system() a subprocess =="
mkdir -p "$VFS"
printf '%s\n' "$MARKER" > "$VFS/routed.txt"
cat > "$WORK/toy.c" <<EOF
#include <stdio.h>
#include <stdlib.h>
int main(void) {
  char buf[128] = {0};
  FILE *f = fopen("$VFS/routed.txt", "r");
  if (!f) { perror("fopen"); return 1; }
  if (fgets(buf, sizeof buf, f)) printf("READ:%s", buf);
  fclose(f);
  return system("/usr/bin/env | grep RCC_EXECUTOR || echo RCC_EXECUTOR-missing");
}
EOF
cc -O2 -o "$WORK/toy" "$WORK/toy.c"

echo "== start rca serve (libp2p loopback) =="
"$REPO/bin/rca" serve --listen /ip4/127.0.0.1/tcp/0 >"$SERVE_OUT" 2>"$SERVE_LOG" &
SERVE_PID=$!
CODE=""
for _ in $(seq 1 50); do
  CODE="$(grep -o 'rca1\.[A-Za-z0-9_-]*' "$SERVE_OUT" 2>/dev/null | head -1 || true)"
  [[ -n "$CODE" ]] && break
  sleep 0.1
done
[[ -n "$CODE" ]] || { echo "no pairing code from rca serve"; exit 1; }
echo "pairing code: $CODE"

echo "== run the toy through rca --code =="
OUT="$("$REPO/bin/rca" --code "$CODE" --workdir "$VFS" --remote-prefix "$VFS" "$WORK/toy" 2>"$WORK/run.err" || true)"
echo "toy said: $OUT"

echo
echo "===== VERDICT ====="
PASS=1
if echo "$OUT" | grep -q "READ:$MARKER"; then
  echo "[PASS] toy read the routed file through the injected fopen"
else
  echo "[FAIL] toy did not read the routed file"; PASS=0
fi
if grep -qE "OPEN .*routed.txt" "$SERVE_LOG"; then
  echo "[PASS] the read was served by the executor over libp2p (OPEN in serve log)"
else
  echo "[FAIL] no executor OPEN for the routed file"; PASS=0
fi
if echo "$OUT" | grep -q "RCC_EXECUTOR=1"; then
  echo "[PASS] subprocess ran on the executor (RCC_EXECUTOR=1 via rca _spawn-proxy)"
else
  echo "[FAIL] subprocess did not run on the executor"; PASS=0
fi
echo "==================="
echo "logs: $WORK"
exit $(( PASS ? 0 : 1 ))
