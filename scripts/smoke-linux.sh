#!/usr/bin/env bash
set -eu

# Run with xvfb-run, Xvfb, xauth and xwininfo installed. The supplied binary
# is started with an isolated configuration and no session credential store.
binary=${1:?usage: smoke-linux.sh /absolute/path/to/xstreaming}
work=$(mktemp -d "${TMPDIR:-/tmp}/xstreaming-smoke.XXXXXX")
mkdir -p "$work/config" "$work/data" "$work/cache" "$work/runtime"
chmod 700 "$work/runtime"

xvfb-run -a -s '-screen 0 1024x768x24 -nolisten tcp' \
  env GDK_BACKEND=x11 XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data" \
      XDG_CACHE_HOME="$work/cache" XDG_RUNTIME_DIR="$work/runtime" \
      DBUS_SESSION_BUS_ADDRESS=disabled \
  bash -c '
    set -eu
    binary=$1
    work=$2
    "$binary" >"$work/app.log" 2>&1 &
    app_pid=$!
    trap '\''kill "$app_pid" 2>/dev/null || true'\'' EXIT
    for attempt in $(seq 1 100); do
      if ! kill -0 "$app_pid" 2>/dev/null; then
        cat "$work/app.log"
        exit 1
      fi
      if xwininfo -root -tree >"$work/windows" 2>/dev/null && grep -Fq "XStreaming (Go preview)" "$work/windows"; then
        sleep 2
        if kill -0 "$app_pid" 2>/dev/null; then
          printf "Native preview window started and remained running.\n"
          exit 0
        fi
        break
      fi
      sleep 0.1
    done
    cat "$work/app.log"
    printf "Native preview window did not remain running.\n" >&2
    exit 1
  ' _ "$binary" "$work"
