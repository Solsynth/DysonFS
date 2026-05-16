# Dyson FileSystem

Go implementation of the Dyson Network file service.

## Modes

- `master`: HTTP API, gRPC, uploads, file serving
- `worker`: post-upload media processing
- `storage`: optional local storage node

## Run

```bash
go run ./cmd --mode master
```

## Config

Use `--config` or `CONFIG_PATH` for a TOML config file.

Key settings:

- `database.dsn`
- `http.port`
- `grpc.port`
- `storage.tempDir`
- `storage.localDir`
- `files.preferredStorage`
- `s3.endpoint`
- `nats.url`

## API

- `GET /api/files/:id/info`
- `GET /api/files/:id/open`
- `GET /api/files/:id/permissions`
- `PUT /api/files/:id/permissions`
- `GET /api/pools`
- `GET /api/pools/:id/permissions`
- `PUT /api/pools/:id/permissions`

## Notes

- Public read is the default.
- Explicit ACL rows restrict access when present.
- `master` can use S3 directly; local storage is still supported.
- The Docker image expects `ffmpeg` and `libvips` runtime packages.
