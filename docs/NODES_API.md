# Nodes API

The Nodes API lets an authenticated user register a DysonFS storage node they control and create an owned storage pool for it in one operation.

The public resource name is `nodes`:

```text
/api/nodes
```

This is separate from the S3 API exposed by the node itself.

## Authentication and ownership

Every endpoint requires the caller's normal DysonFS bearer authentication:

```http
Authorization: Bearer <user-token>
```

A node and its generated pool are owned by the authenticated account. Users can only list, inspect, or delete their own nodes.

The node's S3 access key and secret are used to configure the generated pool. They are not returned in the node response.

## Create a node and pool

```http
POST /api/nodes
Authorization: Bearer <user-token>
Content-Type: application/json
```

Request:

```json
{
  "name": "Home storage",
  "machine_id": "node-home-1",
  "endpoint": "https://storage.example.net",
  "auth_token": "shared-token-configured-on-the-node",
  "pool": {
    "name": "Home storage pool",
    "description": "Files stored on my own storage node",
    "bucket": "default",
    "access_key": "node-s3-access-key",
    "secret_key": "node-s3-secret-key",
    "enable_signed": true,
    "billing_config": {
      "cost_multiplier": 1
    },
    "policy_config": {
      "public_usable": false,
      "allow_encryption": true
    },
    "is_hidden": false
  }
}
```

Required fields:

| Field | Description |
| --- | --- |
| `name` | User-facing node name. |
| `machine_id` | Must match the node's `/_dfs/identity` response. |
| `endpoint` | Absolute `http://` or `https://` node URL. Paths, queries, and fragments are not accepted. |
| `auth_token` | Shared token accepted by the node's `/_dfs/auth/validate` endpoint. |
| `pool.name` | User-facing name for the generated pool. |
| `pool.bucket` | Virtual bucket exposed by the node, normally `default`. |
| `pool.access_key` | Node S3 access key. |
| `pool.secret_key` | Node S3 secret key. |

The server performs these checks before writing anything:

1. `GET <endpoint>/_dfs/identity`
2. Confirms `node_type == "storage"`.
3. Confirms the returned `machine_id` equals the request.
4. `POST <endpoint>/_dfs/auth/validate` with the supplied token.
5. Confirms the token response is valid and has the same machine ID.

On success, the server atomically creates:

- A `storage_nodes` row owned by the caller.
- A `file_pools` row owned by the caller.
- A `pool_id` link from the node to the generated pool.

The pool uses the node URL host as its S3 endpoint. HTTPS endpoints set the pool's `enable_ssl` value automatically.

Response: `201 Created`

```json
{
  "node": {
    "id": "node-id",
    "name": "Home storage",
    "machine_id": "node-home-1",
    "endpoint": "https://storage.example.net",
    "status": "online",
    "last_seen_at": "2026-08-25T12:00:00Z",
    "pool_id": "pool-id",
    "account_id": "account-id",
    "created_at": "2026-08-25T12:00:00Z",
    "updated_at": "2026-08-25T12:00:00Z"
  },
  "pool_id": "pool-id"
}
```
The node's `auth_token` is never serialized because the database model marks it as private. S3 credentials are stored in the generated pool configuration; the create-node response omits them, and clients must treat them as secrets.

### Failure responses

- `400 Bad Request` — malformed request or missing required fields.
- `401 Unauthorized` — no authenticated account.
- `502 Bad Gateway` — the endpoint could not be validated, identity did not match, or the token was rejected.
- `409 Conflict` — the machine ID or generated records conflict with an existing record.

## List owned nodes

```http
GET /api/nodes
Authorization: Bearer <user-token>
```

Response: `200 OK`

```json
[
  {
    "id": "node-id",
    "name": "Home storage",
    "machine_id": "node-home-1",
    "endpoint": "https://storage.example.net",
    "status": "online",
    "pool_id": "pool-id",
    "account_id": "account-id"
  }
]
```

The response includes `X-Total` with the number of returned nodes.

## Get one node

```http
GET /api/nodes/:id
Authorization: Bearer <user-token>
```

A node owned by another account is returned as `404 Not Found`, not disclosed as a forbidden resource.

## Update node or pool names

```http
PATCH /api/nodes/:id
Authorization: Bearer <user-token>
Content-Type: application/json
```

Request fields are optional:

```json
{
  "name": "Renamed storage node",
  "pool_name": "Renamed storage pool"
}
```

The endpoint updates only names. Endpoint URLs, node tokens, and S3
credentials are intentionally not changed through this operation.

## Delete a node

```http
DELETE /api/nodes/:id
Authorization: Bearer <user-token>
```

Response:

```json
{
  "ok": true,
  "pool_id": "pool-id"
}
```

Deleting a node removes the node record only. The generated pool remains an ordinary user-owned pool and must be deleted separately through `DELETE /api/pools/:id` if the user no longer needs it. Deleting the pool does not delete objects from the remote node.

## Node-side endpoints used during creation

The Nodes API validates the remote node through these unauthenticated HTTP routes:

```http
GET  /_dfs/identity
POST /_dfs/auth/validate
```

`/_dfs/identity` must return a storage node identity:

```json
{
  "machine_id": "node-home-1",
  "node_type": "storage",
  "version": "1.0.0"
}
```

`/_dfs/auth/validate` receives:

```json
{
  "token": "shared-token-configured-on-the-node"
}
```

and must return:

```json
{
  "valid": true,
  "machine_id": "node-home-1"
}
```

The Nodes API does not call `/_dfs/version`, and the storage node does not automatically register itself or send heartbeats. Node health monitoring remains a separate operational concern.

## Pool usage

The generated pool can be used anywhere a normal pool ID is accepted, including upload requests:

```json
{
  "file_name": "photo.jpg",
  "file_size": 123456,
  "content_type": "image/jpeg",
  "pool_id": "pool-id"
}
```

Pool permissions and policies continue to apply normally. The Nodes API does not bypass pool ownership, visibility, or file permissions.

## Security requirements

- Use HTTPS for node endpoints outside a trusted private network.
- Use a high-entropy `auth_token`; it is the node validation credential.
- Use separate high-entropy S3 credentials for the node's S3 API.
- Do not expose node S3 credentials in client logs or URLs.
- Keep the node endpoint restricted to intended DysonFS clients where possible.
- The server makes outbound validation requests to the user-supplied endpoint. Deploy network egress controls appropriate for your threat model.
