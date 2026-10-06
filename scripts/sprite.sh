#!/usr/bin/env bash
# Set up the Sprite the code is worked on from, and carry the private working
# files between it and this machine.
#
#   scripts/sprite.sh setup   create the Sprite if it is missing, and fit it out
#   scripts/sprite.sh push    copy HANDOVER.md and the memory notes to it
#   scripts/sprite.sh pull    copy them back from it
#
# Run it from the repository root, on the machine that has the sprite CLI,
# logged in with `sprite login`. The Sprite is a Fly Sprite of its own, apart
# from the bot's machine. See "The Sprite" in docs/RUNBOOK.md.
#
# HANDOVER.md and the memory notes are not in git, since the repository is
# public, so they travel this way. Work in one place at a time: whichever copy
# was not worked on is overwritten.
set -euo pipefail

SPRITE_NAME="${SPRITE_NAME:-market-watch-dev}"
REPO_URL="${REPO_URL:-https://github.com/joseph1009/market-watch.git}"
REMOTE_REPO=/home/sprite/market-watch
# Claude Code keeps a project's memory under a folder named after its path,
# with every character other than a letter or digit turned into a dash.
REMOTE_MEMORY=/home/sprite/.claude/projects/-home-sprite-market-watch/memory

die() { echo "sprite: $*" >&2; exit 1; }

# Git Bash would rewrite the Sprite's paths as Windows ones.
export MSYS_NO_PATHCONV=1

SPRITE="$(command -v sprite || command -v sprite.exe || true)"
[ -n "$SPRITE" ] || die "the sprite CLI is not installed: https://sprites.dev"
[ -f fly.toml ] || die "run this from the repository root, where fly.toml is"

on() { "$SPRITE" exec -s "$SPRITE_NAME" -- bash -lc "$1"; }

# This machine's memory folder, named the way Claude Code names it.
local_memory() {
  if [ -n "${CLAUDE_MEMORY:-}" ]; then
    echo "$CLAUDE_MEMORY"
    return
  fi
  local here
  here="$(pwd -W 2>/dev/null || pwd)"
  # Claude Code writes a Windows drive letter in lower case.
  here="$(printf '%s' "${here:0:1}" | tr 'A-Z' 'a-z')${here:1}"
  echo "$HOME/.claude/projects/$(printf '%s' "$here" | sed 's/[^A-Za-z0-9]/-/g')/memory"
}

# Standard input is the only way in that keeps the Sprite's path as written:
# `sprite file push` turns it into a Windows one on Windows.
put() { "$SPRITE" exec -s "$SPRITE_NAME" -- bash -c "cat > '$2'" < "$1"; }
get() { "$SPRITE" exec -s "$SPRITE_NAME" -- cat "$1" > "$2"; }

push() {
  local memory
  memory="$(local_memory)"
  [ -d "$memory" ] || die "no memory notes at $memory: set CLAUDE_MEMORY"
  on "mkdir -p $REMOTE_MEMORY"
  [ -f HANDOVER.md ] && put HANDOVER.md "$REMOTE_REPO/HANDOVER.md"
  for f in "$memory"/*.md; do
    put "$f" "$REMOTE_MEMORY/$(basename "$f")"
  done
  echo "copied HANDOVER.md and $(ls "$memory"/*.md | wc -l) memory notes to $SPRITE_NAME"
}

pull() {
  local memory names
  memory="$(local_memory)"
  mkdir -p "$memory"
  get "$REMOTE_REPO/HANDOVER.md" HANDOVER.md
  names="$(on "ls $REMOTE_MEMORY")"
  for name in $names; do
    get "$REMOTE_MEMORY/$name" "$memory/$name"
  done
  echo "copied HANDOVER.md and $(printf '%s\n' $names | wc -l) memory notes from $SPRITE_NAME"
}

setup() {
  if ! "$SPRITE" list 2>/dev/null | grep -qw "$SPRITE_NAME"; then
    "$SPRITE" create --skip-console "$SPRITE_NAME"
  fi

  echo "== flyctl"
  on 'command -v fly >/dev/null || [ -x ~/.fly/bin/fly ] || curl -fsSL https://fly.io/install.sh | sh >/dev/null'
  on 'grep -q FLYCTL_INSTALL ~/.profile || printf "\nexport FLYCTL_INSTALL=\"\$HOME/.fly\"\nexport PATH=\"\$FLYCTL_INSTALL/bin:\$PATH\"\n" >> ~/.profile'
  # The deploy token, once there, is read from a file only its owner can read.
  on 'grep -q fly-deploy-token ~/.profile || printf "[ -f ~/.config/fly-deploy-token ] && export FLY_API_TOKEN=\"\$(cat ~/.config/fly-deploy-token)\"\n" >> ~/.profile'

  echo "== the repository"
  on "[ -d $REMOTE_REPO/.git ] || git clone -q $REPO_URL $REMOTE_REPO"
  # The files kept out of git here are kept out there too.
  on "cd $REMOTE_REPO && for p in HANDOVER.md analysis.md .claude/ internal/app/zz_preview_test.go; do grep -qxF \"\$p\" .git/info/exclude || echo \"\$p\" >> .git/info/exclude; done"
  on "git config --global user.name '$(git config user.name)' && git config --global user.email '$(git config user.email)'"

  echo "== the working files"
  push

  echo "== a test run"
  on "cd $REMOTE_REPO && go vet ./... && go test ./... >/tmp/test.log 2>&1 && echo tests pass || { tail -30 /tmp/test.log; exit 1; }"

  cat <<EOF

The Sprite is ready apart from the logins, which only you can do. Run
  sprite console -s $SPRITE_NAME
and in it:
  claude              log in with your subscription, then /exit
  gh auth login       then: gh auth setup-git
The Fly deploy token goes in ~/.config/fly-deploy-token: see "The Sprite"
in docs/RUNBOOK.md for the command, and how to replace the token.
EOF
}

case "${1:-}" in
  setup) setup ;;
  push) push ;;
  pull) pull ;;
  *) die "use: scripts/sprite.sh setup | push | pull" ;;
esac
