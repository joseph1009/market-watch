# Two stages: a toolchain to compile with, and almost nothing to run on.
#
# The running image carries no shell, no package manager and no Go toolchain,
# because this process holds a Telegram token, an Anthropic key and a chat it
# can write to. What is not in the image cannot be used against it.

FROM golang:1.24-alpine AS build

WORKDIR /src

# Dependencies first, so a change to the code does not re-download the module
# cache on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off gives a static binary that runs on a distroless base. The build stamps
# nothing: the version that matters is the image tag.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/market-watch ./cmd/market-watch

# The running image has no shell to make a directory with, so the data
# directory is made here and copied across with its owner set.
RUN mkdir -p /out/data


FROM gcr.io/distroless/static-debian12:nonroot

# The SEC refuses a request without a contact address, and several publishers
# rate-limit an unidentified client. The deployment must set this to a real
# address; the default is a placeholder that EDGAR will reject, which fails
# loudly rather than quietly collecting nothing.
ENV USER_AGENT="Market Watch set-USER_AGENT@example.com"

# Where prefs.yaml, the covered-story record, the candidate names and the run
# history live. Mount a volume here or all four are lost on every deploy: the
# chat id, what has already been reported, and the statistics.
ENV DATA_DIR=/data

COPY --from=build /out/market-watch /market-watch

# A volume comes up owned by root unless something says otherwise, and this
# process is not root, so without this the first thing it does -- save the
# chat id -- is refused. Docker copies this directory's owner onto an empty
# volume; Fly chowns the mount to the image's user. Both need /data to exist.
COPY --from=build --chown=65532:65532 /out/data /data

# nonroot from the base image, by number: a platform that sets the volume's
# owner from the image's user should not have to look a name up to do it.
# Nothing here needs to write outside /data.
USER 65532:65532

ENTRYPOINT ["/market-watch"]
