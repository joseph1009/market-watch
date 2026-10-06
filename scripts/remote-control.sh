#!/usr/bin/env bash
# Runs on the Sprite as its "remote-control" service: Claude Code's Remote
# Control server, in the repository, so sessions can be started and steered
# from the Claude app on a phone.
#
# A running service keeps the Sprite awake, and billed, so this one is started
# only when wanted, by /code in the bot's chat, and stops itself after
# IDLE_MINUTES with no session activity. /code stop stops it sooner.
#
# scripts/sprite.sh setup installs it. See "Remote Control" in
# docs/RUNBOOK.md.
set -uo pipefail

SERVICE=remote-control
REPO="$HOME/market-watch"
IDLE_MINUTES="${IDLE_MINUTES:-120}"
CLAUDE="$HOME/.local/bin/claude"
SPRITE_ENV=/.sprite/bin/sprite-env
# Claude Code writes every session's transcript under here, so the newest
# write is the last time a session did anything.
TRANSCRIPTS="$HOME/.claude/projects"

log() { echo "remote-control: $*"; }

cd "$REPO" || exit 1
"$CLAUDE" remote-control --name market-watch </dev/null &
rc=$!
log "started, stopping after $IDLE_MINUTES quiet minutes"

trap 'kill "$rc" 2>/dev/null; wait; exit 0' TERM INT

started="$(date +%s)"
while kill -0 "$rc" 2>/dev/null; do
  sleep 60
  last="$(find "$TRANSCRIPTS" -name '*.jsonl' -printf '%T@\n' 2>/dev/null | sort -n | tail -1)"
  last="${last%.*}"
  [ -n "$last" ] && [ "$last" -gt "$started" ] || last="$started"
  if [ $(( $(date +%s) - last )) -ge $(( IDLE_MINUTES * 60 )) ]; then
    log "quiet for $IDLE_MINUTES minutes, stopping"
    # The service manager sends TERM, which the trap above answers. Exiting
    # without it would only get the service restarted.
    "$SPRITE_ENV" services stop "$SERVICE"
    sleep 30
  fi
done

# Remote Control ended on its own: exiting lets the manager restart it.
log "Remote Control exited"
exit 1
