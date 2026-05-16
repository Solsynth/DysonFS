# Dyson FileSystem

Go implementation of the Dyson Network file service.

## Modes

- `master`: HTTP API, gRPC, uploads, file serving, health check
- `worker`: post-upload media processing, derived file generation, cleanup
- `storage`: optional local storage node for filesystem-backed deployments

## Logging

- `ZEROLOG_PRETTY=true` enables console-style pretty logs
- `LOG_LEVEL=debug|info|warn|error` sets the log level

## Run

```bash
go run ./cmd --mode master
```

## Config

Use `--config` or `CONFIG_PATH` for a TOML config file.

Key settings:

- `app.name`
- `database.dsn`
- `http.port`
- `grpc.port`
- `storage.tempDir`
- `storage.localDir`
- `auth.target`
- `auth.useTLS`
- `nats.url`
- `mode.master`
- `mode.worker`
- `mode.storage`

Pool storage is configured with `[[pools]]` and seeded into the database at startup.

Example:

```toml
[[pools]]
id = "500e5ed8-bd44-4359-bc0a-ec85e2adf447"
name = "Default"
default = true
hidden = false

[pools.storage]
endpoint = "http://minio:9000"
bucket = "dyson-files"
enableSigned = true
enableSsl = false
secretId = "minio"
secretKey = "minio123"
```

## API

- `GET /api/files/:id/info`
- `GET /api/files/:id/open`
- `GET /api/files/:id/children`
- `GET /api/files/:id/permissions`
- `PUT /api/files/:id/permissions`
- `GET /api/pools`
- `GET /api/pools/:id/permissions`
- `PUT /api/pools/:id/permissions`
- `GET /health`

## Notes

- Public read is the default.
- Explicit ACL rows restrict access when present.
- `master` resolves storage from pool config stored in the database.
- `worker` listens for file upload events and builds thumbnails, blurhash, and other derived artifacts.
- `master` can use S3 directly; local storage is still supported.
- The Docker image expects `ffmpeg` and `libvips` runtime packages.
