# Storage Nodes & Custom S3 Storage

DysonFS allows users to connect their own S3-compatible storage backends and run dedicated storage nodes that expose a standard S3-compatible API alongside DysonFS-specific endpoints. The master node can also expose an S3-compatible API where **buckets map to file pools**.

## Overview

There are three ways to use S3 with DysonFS:

1. **Master S3 API** — The master node exposes an S3-compatible API where buckets are file pools. Special buckets `auto` and `unindexed` provide views of indexed and unindexed files.
2. **Custom S3 Pools** — Point a pool at any S3-compatible service (AWS S3, MinIO, Backblaze B2, etc.). The master reads/writes directly to that bucket.
3. **Storage Nodes** — Run a dedicated DysonFS storage node process that exposes an S3-compatible API backed by local or remote storage, with additional DysonFS endpoints for node management.

## Master S3 API

The master node can expose an S3-compatible API that maps **file pools to S3 buckets**. This lets you use any S3 client (AWS CLI, boto3, rclone, etc.) to browse and manage files in DysonFS.

### Configuration

```toml
[masterS3]
enabled = true
port = "9001"
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the master S3 API |
| `port` | `9001` | HTTP port for the S3 API |

### Authentication

The master S3 API uses **per-user S3 tokens** (similar to WebDAV tokens). Each user creates their own S3 access key and secret key via the API. Tokens can optionally be restricted to a specific pool.

#### Creating an S3 Token

```http
POST /api/s3/tokens HTTP/1.1
Authorization: Bearer <session-token>
Content-Type: application/json

{
  "label": "My S3 Client",
  "pool_id": "optional-pool-id-to-restrict-access"
}
```

Response (keys shown only once):

```json
{
  "id": "01HZX...",
  "label": "My S3 Client",
  "pool_id": "optional-pool-id",
  "access_key": "01HZX...01HZX...",
  "secret_key": "01HZX...01HZX...",
  "created_at": "2026-05-30T12:00:00Z"
}
```

#### Listing S3 Tokens

```http
GET /api/s3/tokens HTTP/1.1
Authorization: Bearer <session-token>
```

#### Deleting an S3 Token

```http
DELETE /api/s3/tokens/:id HTTP/1.1
Authorization: Bearer <session-token>
```

Tokens are stored as SHA-256 hashes. The raw access key and secret key are only returned at creation time and cannot be retrieved later.

### Pool Restriction

When `pool_id` is set on a token:

- The token can only access that specific pool as a bucket
- The `auto` and `unindexed` special buckets are **not accessible**
- `ListBuckets` returns only the restricted pool
- All object operations are scoped to files in that pool

When `pool_id` is not set:

- The token can access all pools the user owns
- `auto` and `unindexed` special buckets are available
- `ListBuckets` returns all pools + `auto` + `unindexed`

### Bucket Mapping

S3 buckets map directly to DysonFS file pools:

| S3 Bucket | DysonFS Location |
|-----------|-----------------|
| `<pool-id>` | Files in that specific pool |
| `auto` | All indexed files owned by the account (default pool) |
| `unindexed` | All unindexed files owned by the account |

`auto` and `unindexed` are special virtual buckets that cannot be created or deleted.

### Object Keys

Object keys are **file IDs**. When listing objects, each file appears with its ID as the key. To download a file, use its ID as the key.

### Client Examples

**AWS CLI:**

```bash
# List all pools (buckets)
aws --endpoint-url http://localhost:9001 s3 ls

# List indexed files
aws --endpoint-url http://localhost:9001 s3 ls s3://auto/

# List files in a specific pool
aws --endpoint-url http://localhost:9001 s3 ls s3://<pool-id>/

# Download a file by ID
aws --endpoint-url http://localhost:9001 s3 cp s3://auto/<file-id> ./local-file.pdf

# Upload a file to a pool
aws --endpoint-url http://localhost:9001 s3 cp ./local-file.pdf s3://<pool-id>/report.pdf

# Upload to auto (default pool)
aws --endpoint-url http://localhost:9001 s3 cp ./photo.jpg s3://auto/photo.jpg

# Upload to unindexed
aws --endpoint-url http://localhost:9001 s3 cp ./temp.bin s3://unindexed/temp.bin

# Delete a file
aws --endpoint-url http://localhost:9001 s3 rm s3://auto/<file-id>
```

Use your S3 token's `access_key` and `secret_key` as credentials.

**rclone:**

```ini
[dysonfs]
type = s3
provider = Other
endpoint = http://localhost:9001
access_key_id = <your-s3-token-access-key>
secret_access_key = <your-s3-token-secret-key>
```

```bash
# List buckets
rclone lsd dysonfs:

# List files in auto bucket
rclone ls dysonfs:auto/

# Copy file
rclone copy dysonfs:auto/<file-id> ./local-file.pdf
```

**Python (boto3):**

```python
import boto3

s3 = boto3.client(
    "s3",
    endpoint_url="http://localhost:9001",
    aws_access_key_id="<your-s3-token-access-key>",
    aws_secret_access_key="<your-s3-token-secret-key>",
    region_name="us-east-1",
)

# List buckets (pools)
for bucket in s3.list_buckets()["Buckets"]:
    print(bucket["Name"])

# List files in auto
for obj in s3.list_objects_v2(Bucket="auto").get("Contents", []):
    print(obj["Key"], obj["Size"])

# Download
s3.download_file("auto", "<file-id>", "local-file.pdf")

# Upload to a pool
s3.upload_file("local-file.pdf", "<pool-id>", "report.pdf")
```

### Operations

| S3 Operation | DysonFS Action |
|---|---|
| `ListBuckets` | Lists all pools + `auto` + `unindexed` |
| `HeadBucket` | Verifies pool exists |
| `CreateBucket` | Creates a new pool (uses default storage config) |
| `DeleteBucket` | Deletes a pool (fails if not empty) |
| `ListObjects` | Lists files in the pool/bucket |
| `GetObject` | Downloads file content by file ID |
| `PutObject` | Uploads a new file to the pool |
| `DeleteObject` | Permanently deletes a file |
| `HeadObject` | Returns file metadata (size, mime, hash) |

### Notes

- The `auto` bucket shows all indexed root-level files. Files in subfolders are accessible by their ID but not listed in the root listing.
- The `unindexed` bucket shows files that haven't been organized into the file tree.
- Uploads via S3 create new files in the target pool. The object key is used as the filename.
- Deletions are permanent (hard delete, not recycle).

## Custom S3 Pools

### Creating a Pool via API

```http
POST /api/pools HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json

{
  "name": "My S3 Bucket",
  "description": "Personal storage on MinIO",
  "storage_config": {
    "endpoint": "minio.example.com",
    "bucket": "dysonfs",
    "secret_id": "AKIAIOSFODNN7EXAMPLE",
    "secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    "enable_ssl": true,
    "enable_signed": true
  },
  "billing_config": {
    "cost_multiplier": 1.0
  },
  "policy_config": {
    "public_usable": false,
    "accept_types": ["image/*", "application/pdf"],
    "max_file_size": 104857600
  }
}
```

### Storage Config Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `endpoint` | string | Yes | S3 endpoint hostname (e.g. `s3.amazonaws.com`) or absolute local path |
| `bucket` | string | Yes* | S3 bucket name (*required for S3 backends) |
| `secret_id` | string | No | S3 access key ID. If empty with an absolute path, uses local filesystem |
| `secret_key` | string | No | S3 secret access key |
| `enable_ssl` | bool | No | Use HTTPS for S3 connections |
| `enable_signed` | bool | No | Enable signed URL generation |
| `access_endpoint` | string | No | Alternative endpoint for public access (CDN, proxy) |
| `image_proxy` | string | No | Image proxy URL |
| `access_proxy` | string | No | Access proxy URL |

### Backend Selection

DysonFS automatically selects the storage backend based on the config:

- **Local filesystem**: If `secret_id` and `secret_key` are empty and `endpoint` is an absolute path (e.g. `/data/storage`), a local filesystem backend is used.
- **S3-compatible**: Otherwise, an S3 backend is created using the MinIO client.

### Pool CRUD Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/pools` | Create a new pool |
| `GET` | `/api/pools` | List accessible pools |
| `GET` | `/api/pools/:id` | Get a single pool |
| `PATCH` | `/api/pools/:id` | Update pool config |
| `DELETE` | `/api/pools/:id` | Delete a pool |
| `GET` | `/api/pools/:id/permissions` | List pool permissions |
| `PUT` | `/api/pools/:id/permissions` | Update pool permissions |

### Updating a Pool

```http
PATCH /api/pools/:id HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json

{
  "name": "Renamed Pool",
  "storage_config": {
    "endpoint": "s3.amazonaws.com",
    "bucket": "my-new-bucket",
    "secret_id": "AKIA...",
    "secret_key": "...",
    "enable_ssl": true
  }
}
```

Only the pool owner or superusers can update or delete a pool. All fields in the request are optional — only provided fields are updated.

### Ownership

When a user creates a pool, their account ID is stored as the pool owner. Only the owner (or superusers) can modify or delete the pool. Pool permissions control who can read from or write to the pool.

## Storage Nodes

A storage node is a standalone DysonFS process that runs in `storage` mode. It exposes:

- A **standard S3-compatible API** for object storage operations
- **DysonFS-specific endpoints** under `/_dfs` for node identification and authentication

### Configuration

```toml
[storageNode]
port = "9000"
machineId = "node-us-east-1"
authToken = "shared-secret-between-master-and-node"
s3AccessKey = "AKIAIOSFODNN7EXAMPLE"
s3SecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
```

| Field | Default | Description |
|-------|---------|-------------|
| `port` | `9000` | HTTP port for the storage node |
| `machineId` | — | Unique identifier for this node |
| `authToken` | — | Pre-shared token for master ↔ node communication |
| `s3AccessKey` | — | Access key for the S3-compatible API (clients use this) |
| `s3SecretKey` | — | Secret key for the S3-compatible API |

### Running a Storage Node

```bash
# With config file
dysonfs --mode storage --config config.toml

# With environment variables
CONFIG_MODE=storage dysonfs --config config.toml

# With version injection (build time)
go build -ldflags="-X src.solsynth.dev/sosys/filesystem/internal/version.Version=1.0.0 \
  -X src.solsynth.dev/sosys/filesystem/internal/version.GitCommit=$(git rev-parse HEAD)" \
  ./cmd
```

### DysonFS Endpoints (`/_dfs`)

These endpoints are used for node identification and authentication between the master and storage nodes.

#### `GET /_dfs/version`

Returns node version information.

```json
{
  "version": "1.0.0",
  "git_commit": "a1b2c3d4",
  "api_version": 1,
  "machine_id": "node-us-east-1"
}
```

#### `GET /_dfs/identity`

Returns node identity.

```json
{
  "machine_id": "node-us-east-1",
  "node_type": "storage",
  "version": "1.0.0"
}
```

#### `POST /_dfs/auth/validate`

Validates an auth token. Used by the master to verify a storage node's identity.

**Request:**

```json
{
  "token": "shared-secret-between-master-and-node"
}
```

**Response (valid):**

```json
{
  "valid": true,
  "machine_id": "node-us-east-1"
}
```

**Response (invalid):**

```json
{
  "valid": false,
  "machine_id": "node-us-east-1"
}
```

#### Authentication

All `/_dfs` endpoints (except version and identity in open mode) require the `authToken` to be passed as:

- `Authorization: Bearer <token>` header, or
- `?token=<token>` query parameter

### S3-Compatible API

The storage node exposes a standard S3-compatible API. Any S3 client (AWS CLI, boto3, MinIO client, etc.) can connect using the configured `s3AccessKey` and `s3SecretKey`.

#### Supported Operations

| S3 Operation | HTTP Method | Path |
|---|---|---|
| `ListBuckets` | `GET /` | List all buckets |
| `CreateBucket` | `PUT /<bucket>` | Create a bucket |
| `DeleteBucket` | `DELETE /<bucket>` | Delete a bucket |
| `HeadBucket` | `HEAD /<bucket>` | Check bucket exists |
| `ListObjects` | `GET /<bucket>?prefix=...` | List objects (v1) |
| `ListObjectsV2` | `GET /<bucket>?list-type=2` | List objects (v2) |
| `GetObject` | `GET /<bucket>/<key>` | Download an object |
| `PutObject` | `PUT /<bucket>/<key>` | Upload an object |
| `DeleteObject` | `DELETE /<bucket>/<key>` | Delete an object |
| `HeadObject` | `HEAD /<bucket>/<key>` | Get object metadata |
| `InitiateMultipartUpload` | `POST /<key>?uploads` | Start multipart upload |
| `UploadPart` | `PUT /<key>?partNumber=N&uploadId=ID` | Upload a part |
| `CompleteMultipartUpload` | `POST /<key>?uploadId=ID` | Complete multipart |
| `AbortMultipartUpload` | `DELETE /<key>?uploadId=ID` | Abort multipart |

#### Authentication

The S3 API uses **AWS Signature Version 4**. Configure your S3 client with:

```
Endpoint:  http://<node-host>:9000
Access Key: <s3AccessKey>
Secret Key: <s3SecretKey>
Region:     us-east-1 (any value works)
```

#### Client Examples

**AWS CLI:**

```bash
aws --endpoint-url http://localhost:9000 s3 ls
aws --endpoint-url http://localhost:9000 s3 cp file.txt s3://default/file.txt
aws --endpoint-url http://localhost:9000 s3 ls s3://default/
```

**MinIO Client (mc):**

```bash
mc alias set dysonfs http://localhost:9000 AKIAIOSFODNN7EXAMPLE wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
mc ls dysonfs
mc cp file.txt dysonfs/default/file.txt
```

**Python (boto3):**

```python
import boto3

s3 = boto3.client(
    "s3",
    endpoint_url="http://localhost:9000",
    aws_access_key_id="AKIAIOSFODNN7EXAMPLE",
    aws_secret_access_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    region_name="us-east-1",
)

s3.put_object(Bucket="default", Key="file.txt", Body=b"hello")
obj = s3.get_object(Bucket="default", Key="file.txt")
print(obj["Body"].read())
```

### Registering a Storage Node with the Master

Storage nodes register themselves with the master so it can route storage operations to them.

```http
POST /api/storage-nodes/register HTTP/1.1
Authorization: Bearer <master-token>
Content-Type: application/json

{
  "name": "US East Node",
  "machine_id": "node-us-east-1",
  "endpoint": "http://storage-node-1.internal:9000",
  "auth_token": "shared-secret-between-master-and-node",
  "pool_id": "optional-pool-id"
}
```

### Storage Node Management Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/storage-nodes/register` | Register a new storage node |
| `GET` | `/api/storage-nodes` | List your registered nodes |
| `GET` | `/api/storage-nodes/:id` | Get a single node |
| `PATCH` | `/api/storage-nodes/:id` | Update node config |
| `DELETE` | `/api/storage-nodes/:id` | Deregister a node |
| `POST` | `/api/storage-nodes/heartbeat/:machineId` | Node heartbeat |

### Heartbeat

Storage nodes should periodically send heartbeats to the master to report their status:

```http
POST /api/storage-nodes/heartbeat/node-us-east-1 HTTP/1.1
Authorization: Bearer <token>
```

The master updates the node's `last_seen_at` timestamp and sets its status to `online`.

### StorageNode Model

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier |
| `name` | string | Display name |
| `machine_id` | string | Unique machine identifier |
| `endpoint` | string | Node's S3 API endpoint URL |
| `auth_token` | string | Auth token (hidden from JSON responses) |
| `status` | string | `online`, `offline`, or `draining` |
| `last_seen_at` | time | Last heartbeat timestamp |
| `pool_id` | string | Associated pool ID (optional) |
| `account_id` | uuid | Owner's account ID |

## Architecture

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   Client     │────▶│   DysonFS Master │────▶│  S3 / MinIO /   │
│  (WebDAV,    │     │  (HTTP + gRPC)   │     │  Local Storage  │
│   WOPI, API) │     └──────────────────┘     └─────────────────┘
└─────────────┘              │
                             │ Pool routes to storage node
                             ▼
                    ┌──────────────────┐     ┌─────────────────┐
                    │  DysonFS Storage │────▶│  Local Disk /   │
                    │  Node (:9000)    │     │  S3 Backend     │
                    │  S3 + /_dfs      │     └─────────────────┘
                    └──────────────────┘
```

- **Master**: Routes storage operations to the correct backend based on pool config
- **Storage Node**: Exposes S3-compatible API + `/_dfs` management endpoints
- **Pool**: Links a storage config (S3 credentials or storage node endpoint) to a set of files
