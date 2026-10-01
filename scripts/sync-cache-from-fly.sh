#!/usr/bin/env bash
# Bring the latest runs' data down from Fly into ./data/cache, to work on a
# change from what the last brief, analysis or closer look gathered instead of
# running it again.
#
#   scripts/sync-cache-from-fly.sh                    all four
#   scripts/sync-cache-from-fly.sh analysis           or any of: brief,
#                                                     analysis, recommendations, industry
#
# The server keeps one folder per kind, holding only its latest run (see
# internal/runcache): the data at each step as JSON, the messages as sent, the
# model calls under model/, and run.json. Each folder fetched replaces the
# local one whole. A kind the server has no run of yet leaves the local folder
# as it was.
#
# A folder travels as a gzipped tar in base64 through `fly ssh console`, which
# on Windows cannot carry binary output intact.
#
# Run it from the repository root. It needs flyctl logged in.
set -euo pipefail

APP="${FLY_APP:-joseph-market-watch}"
KINDS="${*:-brief analysis recommendations industry}"

die() { echo "sync-cache-from-fly: $*" >&2; exit 1; }

FLY="$(command -v fly || command -v flyctl || true)"
[ -n "$FLY" ] || die "flyctl is not installed: https://fly.io/docs/flyctl/install/"
[ -f fly.toml ] || die "run this from the repository root, where fly.toml is"
"$FLY" auth whoami >/dev/null 2>&1 || die "not logged in to Fly: run 'fly auth login'"

mkdir -p data/cache
for kind in $KINDS; do
  case "$kind" in
    brief|analysis|recommendations|industry) ;;
    *) die "no such kind '$kind': use brief, analysis, recommendations or industry" ;;
  esac

  tmp="$(mktemp -d)"
  # The exit status is not used: on Windows, flyctl sends everything and then
  # fails with "The handle is invalid". Whether a whole archive arrived is
  # checked instead.
  "$FLY" ssh console -a "$APP" \
    -C "sh -c 'test -d /data/cache/$kind && tar -C /data/cache -czf - $kind | base64'" \
    >"$tmp/$kind.b64" 2>/dev/null || true

  if tr -d '\r' <"$tmp/$kind.b64" | base64 -d >"$tmp/$kind.tgz" 2>/dev/null &&
     [ -s "$tmp/$kind.tgz" ] && tar -tzf "$tmp/$kind.tgz" >/dev/null 2>&1; then
    rm -rf "data/cache/$kind"
    tar -C data/cache -xzf "$tmp/$kind.tgz"
    started="$(sed -n 's/.*"started": "\([^"]*\)".*/\1/p' "data/cache/$kind/run.json" 2>/dev/null || true)"
    echo "copied $kind${started:+, the run started $started}"
  else
    echo "kept the local $kind: the server sent none"
  fi
  rm -rf "$tmp"
done
