#!/usr/bin/env bash
# Runs on the Sprite as its "remote-control" service: Claude Code's Remote
# Control server, in the repository, so sessions can be started and steered
# from the Claude app on a phone.
#
# A running service keeps the Sprite awake, and billed, so this one is started
# only when wanted, by /code in the bot's chat, and stops itself after
# IDLE_MINUTES with no session activity. /code stop stops it sooner.
#
#   remote-control.sh        run Remote Control (what the service runs)
#   remote-control.sh stop   stop it cleanly, before the service is stopped
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
# Claude Code's own debug log, kept on the Sprite only, with the run before's
# beside it. Twice a session opened from the app sat with no reply and left
# an empty transcript (13 and 16 Oct 2026), and nothing else said why.
DEBUG_LOG="$HOME/.claude/remote-control-debug.log"
# Remote Control's process, and the mark that it is being stopped on purpose.
PIDFILE="$HOME/.claude/remote-control.pid"
STOPPING="$HOME/.claude/remote-control.stopping"
# How long Remote Control has to close its sessions and take itself off the
# Claude app's list. The service manager kills it about a second after its
# own stop, which was too soon: each run left a "market-watch" entry behind
# in the app (16 Oct 2026). Alone it takes half a second; with a session open
# it archives the session first.
STOP_WAIT=30

log() { echo "remote-control: $*"; }

# stop_rc asks Remote Control to stop, and waits for it to finish.
stop_rc() {
  local pid
  pid="$(cat "$PIDFILE" 2>/dev/null)" || return 0
  kill -0 "$pid" 2>/dev/null || return 0
  touch "$STOPPING"
  kill -TERM "$pid"
  for _ in $(seq 1 $(( STOP_WAIT * 4 ))); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.25
  done
  log "Remote Control did not stop within $STOP_WAIT seconds"
}

if [ "${1:-}" = stop ]; then
  stop_rc
  exit 0
fi

cd "$REPO" || exit 1
rm -f "$STOPPING"
[ -f "$DEBUG_LOG" ] && mv -f "$DEBUG_LOG" "$DEBUG_LOG.1"
# No session made in advance. Since 2.1.296 Remote Control opens one as it
# starts, and both times the owner used it (13 and 16 Oct 2026) it took no
# message and left an empty transcript. A session started from the app, as
# before 2.1.296, is made when it is asked for.
"$CLAUDE" remote-control --name market-watch --no-create-session-in-dir --debug-file "$DEBUG_LOG" </dev/null &
rc=$!
echo "$rc" > "$PIDFILE"
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
    stop_rc
    # The service manager sends TERM, which the trap above answers. Exiting
    # without it would only get the service restarted.
    "$SPRITE_ENV" services stop "$SERVICE"
    sleep 30
  fi
done

# Stopped on purpose: wait for the service to be stopped, rather than exit
# and be started again.
if [ -f "$STOPPING" ]; then
  log "Remote Control stopped"
  sleep 60
  exit 0
fi

# Remote Control ended on its own: exiting lets the manager restart it.
log "Remote Control exited"
exit 1
