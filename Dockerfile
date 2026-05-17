FROM golang:1.26.2-bookworm AS build
WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git pkg-config libvips-dev && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/dysonfs ./cmd

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg libvips42 && \
  rm -rf /var/lib/apt/lists/* /usr/share/doc/* /usr/share/man/* /usr/share/locale/* && \
  apt-get clean

COPY --from=build /out/dysonfs /usr/local/bin/dysonfs

EXPOSE 8080 9090
ENTRYPOINT ["dysonfs"]
