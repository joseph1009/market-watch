#!/usr/bin/env bash
# Bring the service's state down from Fly, and write the watchlist and feed
# changes made from Telegram into config/.
#
#   scripts/sync-from-fly.sh
#
# Two things, in order:
#
#   1. Copies the data volume's files -- prefs.yaml, covered.json, runs.json,
#      candidates.json, scorecard.json, terms.json -- into ./data, so a local run starts
#      where the server is. What they replace is kept in data/.backup/.
#   2. Runs `market-watch --fold`, which writes the companies added or removed
#      with /watchlist into config/companies.yaml and the feeds switched with
#      /sources into config/sources.yaml, and the terms the service learned
#      into config/glossary.yaml. Only the entries changed are touched.
#
# Then read the change with `git diff config/`, commit it and deploy. Once the
# deployed files say what the Telegram edits said, the service drops the edits
# on its next start: the list kept on the server is only ever what the files do
# not yet say.
#
# Run it from the repository root. It needs flyctl logged in, and Go.
set -euo pipefail

APP="${FLY_APP:-joseph-market-watch}"
FILES="prefs.yaml covered.json runs.json candidates.json scorecard.json terms.json"

die() { echo "sync-from-fly: $*" >&2; exit 1; }

FLY="$(command -v fly || command -v flyctl || true)"
[ -n "$FLY" ] || die "flyctl is not installed: https://fly.io/docs/flyctl/install/"
[ -f fly.toml ] || die "run this from the repository root, where fly.toml is"
[ -d config ] || die "run this from the repository root, where config/ is"
# Reading the app's status tests that the login can reach this app, whether
# it is a full login or a deploy token limited to the app.
"$FLY" status -a "$APP" >/dev/null 2>&1 \
  || die "cannot read $APP: run 'fly auth login', or set FLY_API_TOKEN to a deploy token"

mkdir -p data
backup="data/.backup/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup"

# received reports whether a download is the file rather than nothing or an
# error: not empty, and for JSON, starting like JSON.
received() {
  [ -s "$1" ] || return 1
  case "$2" in
    *.json) head -c 64 "$1" | tr -d ' \r\n\t' | grep -q '^[[{]' ;;
  esac
}

for f in $FILES; do
  tmp="$(mktemp)"
  # The exit status is not used: on Windows, flyctl prints the whole file and
  # then fails with "The handle is invalid". What arrived is checked instead,
  # and a file missing on the server, or a connection that fails, leaves the
  # local copy as it was rather than replacing it with nothing.
  "$FLY" ssh console -a "$APP" -C "cat /data/$f" >"$tmp" 2>/dev/null || true
  if received "$tmp" "$f"; then
    [ -f "data/$f" ] && cp -p "data/$f" "$backup/"
    mv "$tmp" "data/$f"
    echo "copied $f"
  else
    rm -f "$tmp"
    echo "kept the local $f: the server did not send one"
  fi
done
echo "the files replaced are in $backup"

echo
go run ./cmd/market-watch --fold

echo
echo "Next: git diff config/, then commit and deploy (scripts/fly-deploy.sh)."
