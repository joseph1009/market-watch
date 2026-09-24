#!/usr/bin/env bash
# Deploy Market Watch to Fly.io: the first time, and every time after.
#
#   scripts/fly-deploy.sh
#
# Creates the app and its volume if they are missing, sets the secrets from
# .env, and deploys. Run it from the repository root. It prints no secret: they
# are read from .env and handed to `fly secrets import` on standard input.
#
# Before the first run:
#   1. Install flyctl and log in: https://fly.io/docs/flyctl/install/, then
#      `fly auth login`.
#   2. On your own machine, run `claude setup-token` and add the token to .env
#      as CLAUDE_CODE_OAUTH_TOKEN=... It lasts a year and uses your subscription.
#   3. Stop any copy of the service running elsewhere. Two copies polling one
#      bot token fight over every message, and two schedulers send two briefs.
set -euo pipefail

APP="${FLY_APP:-joseph-market-watch}"
REGION="${FLY_REGION:-sin}"
ORG="${FLY_ORG:-personal}"
VOLUME="market_watch_data"

die() { echo "fly-deploy: $*" >&2; exit 1; }

FLY="$(command -v fly || command -v flyctl || true)"
[ -n "$FLY" ] || die "flyctl is not installed: https://fly.io/docs/flyctl/install/"
[ -f .env ] || die "run this from the repository root, where .env is"
[ -f fly.toml ] || die "run this from the repository root, where fly.toml is"
"$FLY" auth whoami >/dev/null 2>&1 || die "not logged in to Fly: run 'fly auth login'"

# The secrets the service reads, where .env gives them a value; an optional one
# left empty is not sent. ANTHROPIC_API_KEY is deliberately not one of them:
# nothing uses it, and Claude Code would prefer it to the subscription.
SECRETS='^(TELEGRAM_BOT_TOKEN|CLAUDE_CODE_OAUTH_TOKEN|TELEGRAM_CHAT_ID|TELEGRAM_CHANNEL_ID|USER_AGENT|FRED_API_KEY|FINNHUB_API_KEY|TAVILY_API_KEY|MASSIVE_API_KEY)=[^[:space:]]'
# The chat id is required because it pins the one chat the bot will answer.
# Without it, whoever sends /start first on the new machine becomes its owner.
for name in TELEGRAM_BOT_TOKEN CLAUDE_CODE_OAUTH_TOKEN TELEGRAM_CHAT_ID USER_AGENT; do
  grep -Eq "^${name}=.+" .env || die "$name is missing from .env"
done

# The organisation decides who else can reach the machine. Anyone who can ssh
# into it can read CLAUDE_CODE_OAUTH_TOKEN from its environment, so the default
# is your personal organisation rather than a shared one.
echo "== app $APP in $REGION, organisation $ORG"
if ! "$FLY" status -a "$APP" >/dev/null 2>&1; then
  "$FLY" apps create "$APP" --org "$ORG"
fi

echo "== volume $VOLUME"
if ! "$FLY" volumes list -a "$APP" 2>/dev/null | grep -q "$VOLUME"; then
  "$FLY" volumes create "$VOLUME" --size 1 --region "$REGION" -a "$APP" --yes
fi

# Staged, so they arrive with the deploy below instead of restarting the
# machine on their own. Windows line endings and surrounding quotes are
# stripped, since .env allows both and a secret must not carry either.
echo "== secrets (names only): $(grep -E "$SECRETS" .env | cut -d= -f1 | tr '\n' ' ')"
grep -E "$SECRETS" .env \
  | sed -e 's/\r$//' -e 's/^\([A-Z_]*\)="\(.*\)"$/\1=\2/' \
  | "$FLY" secrets import --stage -a "$APP"

echo "== deploy"
"$FLY" deploy -a "$APP"

# As the service's own user, not root: anything this writes to /data has to
# stay writable by the service afterwards.
#
# On Windows, flyctl ends every ssh command with "The handle is invalid" and a
# failing exit status when it is not attached to a console, which it is not
# under Git Bash, even when the command succeeded. A command that really failed
# says "Process exited with status" instead, so that is what decides.
echo "== check"
status=0
out="$("$FLY" ssh console -a "$APP" -C "runuser -u app -- env HOME=/home/app /usr/local/bin/market-watch --check" 2>&1)" || status=$?
printf '%s\n' "$out" | grep -v 'The handle is invalid' || true
if [ "$status" -ne 0 ]; then
  if printf '%s' "$out" | grep -q 'Process exited with status' \
    || ! printf '%s' "$out" | grep -q 'The handle is invalid'; then
    die "the check failed on the machine"
  fi
fi

cat <<EOF

Deployed. The volume starts empty, so the service seeded its own watchlists
and knows nothing of what earlier briefs covered. To carry over the local
record, see "Moving the data up" in docs/RUNBOOK.md.
EOF
