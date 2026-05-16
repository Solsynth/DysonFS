FROM golang:1.26.2-bookworm AS build
WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git pkg-config libvips-dev ffmpeg && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o /out/filesystem ./cmd

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg libvips42 && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/filesystem /usr/local/bin/filesystem

EXPOSE 8080 9090
ENTRYPOINT ["filesystem"]
