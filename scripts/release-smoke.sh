#!/usr/bin/env bash
# Smoke-tests a release binary: it reports the expected version, runs a
# one-machine world, and serves the real web client rather than the
# placeholder page a binary without the client serves.
#
# Usage: scripts/release-smoke.sh path/to/skylands [expected-version]
#
# The world uses its own ports, data directory, and socket, so a world
# already running on this machine is left alone.
set -euo pipefail

bin=$1
want=${2:-}
web=127.0.0.1:18480

got=$("$bin" version)
echo "$got"
if [[ -n $want && $got != "skylands $want" ]]; then
  echo "FAIL: version is '$got', want 'skylands $want'" >&2
  exit 1
fi

dir=$(mktemp -d)
"$bin" devworld up --host smoke --data-dir "$dir/data" --socket "$dir/daemon.sock" \
  --http "$web" --nats-listen 127.0.0.1:14480 --leaf-listen 127.0.0.1:17480 \
  >"$dir/log" 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true; wait $pid 2>/dev/null || true; rm -rf "$dir"' EXIT

fail() {
  echo "--- devworld log" >&2
  cat "$dir/log" >&2
  echo "FAIL: $*" >&2
  exit 1
}

# Wait for the citadel's web server, then for the daemon's socket.
up=""
for _ in $(seq 60); do
  kill -0 $pid 2>/dev/null || fail "devworld exited"
  if curl -fsS "http://$web/healthz" >/dev/null 2>&1 &&
    "$bin" daemon status --socket "$dir/daemon.sock" >"$dir/status" 2>&1; then
    up=yes
    break
  fi
  sleep 0.5
done
[[ -n $up ]] || fail "devworld did not come up within 30 seconds"
cat "$dir/status"

page=$(curl -fsS "http://$web/")
script=$(grep -o 'src="/assets/index-[^"]*\.js"' <<<"$page" | head -1 | cut -d'"' -f2) ||
  fail "/ does not serve the built web client"
curl -fsS -o /dev/null "http://$web$script" || fail "the client's script $script does not load"

echo "PASS: $got serves the web client and runs a daemon"
