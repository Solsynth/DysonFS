FROM oven/bun:1 AS web
WORKDIR /src/web

COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile

COPY web/ ./
# Type-check is a developer/CI gate (bun run type-check); the tsgo-backed
# compiler in this project cannot resolve .vue modules on Linux builders, so
# the image runs the Vite build directly.
RUN bun run build-only

FROM golang:1.26.2-bookworm AS build
WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git pkg-config libvips-dev && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG GIT_COMMIT=unknown
# BuildKit sets TARGETARCH to the target platform (arm64 locally, amd64 on the
# CI runner); an empty value falls back to the Go host default.
ARG TARGETARCH
RUN CGO_ENABLED=1 GOOS=linux GOARCH=${TARGETARCH} go build \
  -ldflags="-s -w -X src.solsynth.dev/sosys/filesystem/internal/version.Version=${VERSION} -X src.solsynth.dev/sosys/filesystem/internal/version.GitCommit=${GIT_COMMIT}" \
  -o /out/dysonfs ./cmd

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg libvips42 && \
  rm -rf /var/lib/apt/lists/* /usr/share/doc/* /usr/share/man/* /usr/share/locale/* && \
  apt-get clean

COPY --from=build /out/dysonfs /usr/local/bin/dysonfs
COPY --from=web /src/web/dist /app/web/dist

EXPOSE 8080 9090 9000 9001
ENTRYPOINT ["dysonfs"]
