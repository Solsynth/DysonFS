# Storage Node Deployment

This guide deploys DysonFS in `storage` mode as a standalone, S3-compatible object-storage endpoint backed by a local filesystem directory.

A storage node is not a master API server. It does not expose the normal file, gRPC, account, quota, or indexing routes. It exposes:

- An S3-compatible API on `storageNode.port` (default `9000`)
- `GET /_dfs/version`
- `GET /_dfs/identity`
- `POST /_dfs/auth/validate`

## Deployment requirements

A storage-mode process still initializes the DysonFS database and pool configuration during startup. Provide all of the following:

1. A reachable PostgreSQL database. The database DSN is required even for storage mode.
2. At least one configured pool.
3. Exactly one pool marked `default = true`.
4. A persistent filesystem directory for object data.
5. A persistent configuration file containing both S3 credentials.

The storage node's local directory is selected from the default pool's storage endpoint. Keep the endpoint path and the mounted filesystem path identical inside the process.

## Configuration

Create a storage-node configuration such as `config.storage.toml`:

```toml
[app]
name = "DysonStorageNode-us-east-1"

[database]
dsn = "host=postgres.example.internal port=5432 user=dyson password=CHANGE_ME dbname=dyson_drive sslmode=require"

[storage]
tempDir = "/var/lib/dyson-drive/tmp"
localDir = "/var/lib/dyson-drive/data"

[[pools]]
id = "01STORAGEPOOL0000000000000000"
name = "storage-us-east-1"
default = true
hidden = false

[pools.storage]
endpoint = "/var/lib/dyson-drive/data"
bucket = "local"
enableSigned = true

[storageNode]
port = "9000"
machineId = "node-us-east-1"
authToken = "GENERATE_A_LONG_RANDOM_SHARED_TOKEN"
s3AccessKey = "GENERATE_A_LONG_RANDOM_ACCESS_KEY"
s3SecretKey = "GENERATE_A_LONG_RANDOM_SECRET_KEY"
```

### Configuration fields

| Field | Purpose |
| --- | --- |
| `database.dsn` | PostgreSQL connection used for migrations and pool configuration. |
| `storage.localDir` | Initial local backend directory and default data directory. |
| `pools.storage.endpoint` | Absolute local path used by the selected pool. |
| `storageNode.port` | HTTP/S3 listen port. Defaults to `9000`. |
| `storageNode.machineId` | Stable node identifier used by the control plane. |
| `storageNode.authToken` | Shared token checked by `/_dfs/auth/validate`. It does not replace S3 credentials. |
| `storageNode.s3AccessKey` | S3 access key accepted by the node. |
| `storageNode.s3SecretKey` | S3 secret used to verify AWS Signature V4 requests. |

Set both `s3AccessKey` and `s3SecretKey`. If either is empty, the current S3 server does not enable request authentication.

Generate credentials outside the configuration file and inject them through your secret-management system. Do not commit production credentials.

## Docker deployment

Build the image locally:

```bash
docker build \
  --build-arg VERSION=1.0.0 \
  --build-arg GIT_COMMIT="$(git rev-parse HEAD)" \
  -t dysonfs:1.0.0 .
```

The repository workflow publishes images to GHCR. Replace the image name below with the image produced by your registry.

Create `compose.storage.yml`:

```yaml
services:
  storage-node:
    image: ghcr.io/YOUR_OWNER/dfs:latest
    command: ["--mode", "storage", "--config", "/etc/dyson/config.toml"]
    restart: unless-stopped
    ports:
      - "9000:9000"
    environment:
      CONFIG_PATH: /etc/dyson/config.toml
    volumes:
      - ./config.storage.toml:/etc/dyson/config.toml:ro
      - dyson-storage-data:/var/lib/dyson-drive

volumes:
  dyson-storage-data:
```

The container path `/var/lib/dyson-drive/data` must match both `storage.localDir` and `pools.storage.endpoint` in the configuration.

Start it:

```bash
docker compose -f compose.storage.yml up -d storage-node
docker compose -f compose.storage.yml logs -f storage-node
```

The image currently builds a Linux `amd64` binary. On an ARM host, build or run it for the deployment platform explicitly, or publish a matching multi-architecture image.

## Bare-metal deployment

Build the binary on the target host or in a compatible build environment:

```bash
go build -o dysonfs ./cmd
sudo install -m 0755 dysonfs /usr/local/bin/dysonfs
```

Create the service account and persistent directories:

```bash
sudo useradd --system --home /var/lib/dyson-drive --shell /usr/sbin/nologin dysonfs
sudo install -d -o dysonfs -g dysonfs /var/lib/dyson-drive/data
sudo install -d -o dysonfs -g dysonfs /var/lib/dyson-drive/tmp
sudo install -d -o root -g dysonfs -m 0750 /etc/dyson
sudo install -o root -g dysonfs -m 0640 config.storage.toml /etc/dyson/config.storage.toml
```

Create `/etc/systemd/system/dysonfs-storage.service`:

```ini
[Unit]
Description=DysonFS storage node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=dysonfs
Group=dysonfs
ExecStart=/usr/local/bin/dysonfs --mode storage --config /etc/dyson/config.storage.toml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/var/lib/dyson-drive/data /var/lib/dyson-drive/tmp

[Install]
WantedBy=multi-user.target
```

Enable and start it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now dysonfs-storage
sudo systemctl status dysonfs-storage
sudo journalctl -u dysonfs-storage -f
```

## TLS and network exposure

The storage-mode listener is plain HTTP. Put it behind a TLS-terminating reverse proxy or private load balancer before exposing it outside a trusted network.

Example Caddy configuration:

```caddy
s3.example.com {
    reverse_proxy 127.0.0.1:9000
}
```

Recommended network policy:

- Allow the public S3 endpoint only from intended clients.
- Allow the master to reach the node endpoint if the node is registered to a pool.
- Keep PostgreSQL private; the storage node only needs outbound database access.
- Do not expose the node's database port or host filesystem.
- Restrict `/_dfs/auth/validate` to the master or control-plane network where possible.

## Verify the node

Check the node identity before testing S3:

```bash
curl --fail http://127.0.0.1:9000/_dfs/version
curl --fail http://127.0.0.1:9000/_dfs/identity
```

Validate the shared node token:

```bash
curl --fail \
  -H 'Content-Type: application/json' \
  -d '{"token":"GENERATE_A_LONG_RANDOM_SHARED_TOKEN"}' \
  http://127.0.0.1:9000/_dfs/auth/validate
```

Use an S3 client with the configured static credentials:

```bash
export AWS_ACCESS_KEY_ID='GENERATE_A_LONG_RANDOM_ACCESS_KEY'
export AWS_SECRET_ACCESS_KEY='GENERATE_A_LONG_RANDOM_SECRET_KEY'
export AWS_DEFAULT_REGION='us-east-1'

aws --endpoint-url http://127.0.0.1:9000 s3api list-buckets
printf 'storage-node smoke test\n' >/tmp/storage-node-smoke.txt
aws --endpoint-url http://127.0.0.1:9000 s3 cp \
  /tmp/storage-node-smoke.txt s3://default/healthcheck.txt
aws --endpoint-url http://127.0.0.1:9000 s3 cp \
  s3://default/healthcheck.txt /tmp/storage-node-smoke-downloaded.txt
cmp /tmp/storage-node-smoke.txt /tmp/storage-node-smoke-downloaded.txt
aws --endpoint-url http://127.0.0.1:9000 s3 rm s3://default/healthcheck.txt
```

The node uses virtual buckets: object keys are stored below the local directory as `<bucket>/<key>`. Bucket creation does not create separate bucket metadata.

## Add a user-owned storage pool

For a normal user-owned node, the user creates an ordinary owned pool whose storage configuration points at the node's S3 API. No special pool type is required.

Create the pool with the user's authenticated account:

```bash
curl --fail -X POST https://files.example.com/api/pools \
  -H "Authorization: Bearer ${USER_AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "My home storage node",
    "description": "Storage hosted on my own machine",
    "storage_config": {
      "endpoint": "storage-node.example.net:9000",
      "bucket": "default",
      "enable_ssl": false,
      "enable_signed": true,
      "secret_id": "NODE_S3_ACCESS_KEY",
      "secret_key": "NODE_S3_SECRET_KEY"
    },
    "policy_config": {
      "public_usable": false
    },
    "is_hidden": false
  }'
```

The created pool is owned by the authenticated account. Files uploaded with this pool use the node through the generic S3 backend. The node's S3 credentials belong in `storage_config.secret_id` and `storage_config.secret_key`; the node's `authToken` is only for the separate `/_dfs/auth/validate` endpoint.

Use the node's hostname and port without `http://` or `https://`; `enable_ssl` controls whether the master uses HTTPS.

For an existing pool, an account with storage-admin permission can replace its storage configuration:

```bash
curl --fail -X PATCH \
  "https://files.example.com/api/admin/storage/config/POOL_ID" \
  -H "Authorization: Bearer ${MASTER_AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
    "storage_config": {
      "endpoint": "storage-node.example.net:9000",
      "bucket": "default",
      "enable_ssl": false,
      "enable_signed": true,
      "secret_id": "NODE_S3_ACCESS_KEY",
      "secret_key": "NODE_S3_SECRET_KEY"
    }
  }'
```

The `POST /api/pools` call is the normal user flow and the part that actually makes file operations use the node.

## Add the node through the user API

The user-facing flow is `POST /api/nodes`. It validates the deployed node through `/_dfs/identity` and `/_dfs/auth/validate`, then atomically creates the user's node record and owned storage pool.

See [`docs/NODES_API.md`](NODES_API.md) for the request and response contract.

The direct `POST /api/pools` flow remains available for arbitrary S3-compatible endpoints, but it does not verify that the endpoint is a DysonFS storage node.

The node process does not automatically register itself or send heartbeats. The Nodes API performs validation during creation; ongoing health monitoring remains an operational concern.

## Storage behavior and limitations

- S3 authentication uses AWS Signature V4 with the configured static access key and secret key.
- The `/_dfs/*` routes are mounted before the S3 fallback. `/_dfs/version` and `/_dfs/identity` are public; `/_dfs/auth/validate` checks the submitted JSON token directly. The configured `authToken` is not middleware protecting the S3 API.
- Buckets are virtual namespaces inferred from object-key prefixes.
- `CreateBucket` is accepted without persistent bucket creation.
- `HeadBucket` is permissive; object access is the meaningful storage check.
- Multipart upload sessions are held in the S3 server process. Active multipart sessions are lost on restart, and completion combines uploaded parts before writing the final object.
- The node does not apply master-side account permissions, user S3 tokens, quotas, or file indexing.
- The database and the data directory are both startup dependencies even though the node serves object traffic.

## Backups, upgrades, and recovery

Back up both the PostgreSQL database and the storage data directory. The database contains DysonFS metadata and pool configuration; the filesystem contains object bytes. Restoring only one produces an inconsistent deployment.

For an upgrade:

1. Drain or stop clients.
2. Wait for active uploads to complete.
3. Back up PostgreSQL and the data directory.
4. Replace the image or binary.
5. Start the node and verify `/_dfs/version`.
6. Run the S3 smoke test.
7. Confirm the master sees the registration and latest heartbeat status.

Do not delete or rename the storage directory during an upgrade. Keep `machineId`, pool ID, and S3 credentials stable unless performing an intentional node replacement.
