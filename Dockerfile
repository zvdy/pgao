# syntax=docker/dockerfile:1.7
#
# pgao runtime image. Multi-stage; produces a non-root, minor-pinned
# Alpine image. Digest pinning happens at release time — the
# release workflow records the resolved digest and operators are
# encouraged to deploy by digest, not by tag.

# ---- Build stage --------------------------------------------------------
FROM golang:1.25-alpine3.22 AS builder

RUN apk add --no-cache git make gcc musl-dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY src/ ./src/

# pg_query_go links libpg_query via cgo, so CGO_ENABLED stays on. The
# binary is dynamically linked against musl libc, which is present in
# the alpine runtime stage below. -trimpath strips local filesystem
# paths so two builds from the same git ref produce identical output.
ARG TARGETOS
ARG TARGETARCH
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-w -s" -o /out/pgao ./src/main.go

# ---- Runtime stage ------------------------------------------------------
FROM alpine:3.22

# tzdata + ca-certificates power TLS to Postgres and correct timestamps
# in JSON logs. wget powers the HEALTHCHECK below. postgresql-client is
# intentionally NOT installed: pgao talks to Postgres via pgx.
RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -g 1000 pgao \
 && adduser -D -u 1000 -G pgao pgao

WORKDIR /app

COPY --from=builder --chown=pgao:pgao /out/pgao /app/pgao

USER pgao:pgao

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/pgao"]
