# Two stages: a toolchain to compile with, and a small Debian to run on.
#
# The running image used to be distroless, with no shell or package manager at
# all. It now has to carry Claude Code, which answers every model call and is a
# native Linux binary that expects an ordinary glibc system. Debian slim is the
# smallest base that is, and the packages that fetch and verify Claude Code are
# removed again once it is installed.

FROM golang:1.24-alpine AS build

WORKDIR /src

# Dependencies first, so a change to the code does not re-download the module
# cache on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off gives a static binary with no system library to depend on. The build
# stamps nothing: the version that matters is the image tag.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/market-watch ./cmd/market-watch


FROM debian:bookworm-slim

# Claude Code from Anthropic's signed apt repository, on its stable channel: a
# release about a week old that skips any with a known regression. Package
# installs do not update themselves, so the version is the one this image was
# built with until the image is rebuilt.
#
# The signing key is checked against the fingerprint Anthropic publishes before
# it is trusted, and the build stops if they differ.
ARG CLAUDE_REPO_FINGERPRINT=31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg; \
    install -d -m 0755 /etc/apt/keyrings; \
    curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o /etc/apt/keyrings/claude-code.asc; \
    gpg --show-keys --with-colons /etc/apt/keyrings/claude-code.asc | grep -q "^fpr:::::::::${CLAUDE_REPO_FINGERPRINT}:"; \
    echo "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/stable stable main" \
      > /etc/apt/sources.list.d/claude-code.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends claude-code; \
    apt-get purge -y --auto-remove curl gnupg; \
    rm -rf /var/lib/apt/lists/*; \
    claude --version

# The user the service runs as, by the same number the distroless image used,
# so a volume made for either has the right owner.
RUN groupadd --gid 65532 app \
 && useradd --uid 65532 --gid 65532 --home-dir /home/app --create-home --shell /usr/sbin/nologin app \
 && mkdir -p /data \
 && chown 65532:65532 /data

# The SEC refuses a request without a contact address, and several publishers
# rate-limit an unidentified client. The deployment must set this to a real
# address; the default is a placeholder that EDGAR will reject, which fails
# loudly rather than quietly collecting nothing.
ENV USER_AGENT="Market Watch set-USER_AGENT@example.com"

# Where prefs.yaml, the covered-story record, the candidate names, the run
# history and the relay's run directories live. Mount a volume here or all of
# it is lost on every deploy.
ENV DATA_DIR=/data

# Claude Code keeps its own settings under HOME. Nothing there needs to
# survive a restart: it logs in from CLAUDE_CODE_OAUTH_TOKEN every time, and
# every call is made with --no-session-persistence.
ENV HOME=/home/app

# Every model call is answered by Claude Code, headless, on this machine.
ENV RELAY_ANSWER=claude

COPY --from=build /out/market-watch /usr/local/bin/market-watch

# Not root, by number: a platform that sets the volume's owner from the image's
# user should not have to look a name up to do it.
USER 65532:65532
WORKDIR /home/app

ENTRYPOINT ["/usr/local/bin/market-watch"]
