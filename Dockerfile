# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies are cached separately from the source, so a code change does not
# re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

# CGO is off so the binary is static and cross-compiles without a C toolchain;
# the SQLite driver is pure Go for the same reason.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/open-plaato-keg ./cmd/open-plaato-keg


FROM alpine:3.20

# ca-certificates is needed to reach the BarHelper API over HTTPS.
RUN apk add --no-cache ca-certificates tzdata wget

COPY --from=build /out/open-plaato-keg /usr/local/bin/open-plaato-keg

# The database and uploaded images live here; mount a volume to keep them.
RUN mkdir -p /db && adduser -D -H -u 10001 plaato && chown plaato /db
USER plaato

ENV DATABASE_FILE_PATH=/db/open-plaato-keg.db \
    KEG_LISTENER_PORT=4545 \
    HTTP_LISTENER_PORT=8085

VOLUME /db
EXPOSE 4545 8085

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8085/api/alive || exit 1

ENTRYPOINT ["/usr/local/bin/open-plaato-keg"]
