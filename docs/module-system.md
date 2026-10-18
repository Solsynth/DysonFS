# Modular DysonFS

DysonFS is built as a set of compile-time modules, the same way Stargate is
(see `../Stargate/docs/module-system-plan.md` for the shared guideline). A
module is linked into the binary and then selected at runtime by config. There
is no plugin loading.

## Why

Every optional surface — authentication, content editing, per-kind file
analysis, preview derivatives, media transforms, WebDAV, WOPI, the S3 gateways
— can be left out of a build. What is left out is not compiled in, cannot be
configured on, registers no routes, and owns no tables.

Two consequences matter operationally:

- A build without the auth module needs no Stargate. Every request belongs to a
  synthesized account and every permission check is skipped.
- A build without an analysis module stores files without derived metadata; a
  build without a derivative module stores no previews.

## Kernel

`internal/module` holds the SPI, the registry, the resolver, the port registry
and the schema migrator. `internal/core` holds the shared service graph that
modules receive.

```go
type Factory struct {
    ID string
    Kind Kind // "feature" | "integration"
    DefaultEnabled bool
    Required bool

    // Module dependency: orders construction and cascades disables.
    Requires []string

    // Port dependency: a runtime interface, resolved after all modules have
    // published what they provide.
    Ports    []PortRequirement
    Provides []PortID

    // Schema owned by this module. Only migrated while it is enabled.
    Models []any
    SQL    []SchemaHook

    Capabilities []string

    New func() Instance
}

type Instance interface {
    Configure(ctx context.Context, host *Host) error
    Stop(ctx context.Context) error
}
```

A module may additionally implement:

- `PortProvider` — `ProvidePorts(ctx, host)`, called for every enabled module
  before any `Configure`, so consumers resolve providers regardless of order.
- `PreHost` — `Prepare(ctx, core) ([]gin.HandlerFunc, error)`, called before the
  HTTP engine is exposed. Only the auth module uses this: gin applies
  middleware solely to routes registered after `Use`, so authentication cannot
  be installed from `Configure`.

### Resolution

1. Start from `DefaultEnabled`.
2. A non-empty `enabled` list becomes an allowlist.
3. Always include `Required` modules.
4. Apply `disabled`; disabling a `Required` module is a startup error.
5. Remove dependents whose `Requires` module is gone, warning for each.
6. Topologically sort by `Requires`, then by ID for deterministic order.
7. Fail on unknown IDs, duplicate IDs and cycles.

### Schema

`AutoMigrate` receives the models of enabled modules only, followed by each
module's `SQL` hooks in dependency order. Nothing is ever dropped: disabling a
module leaves its existing tables and data untouched, and re-enabling reuses
them.

| Situation | Result |
|---|---|
| fresh DB, module enabled | its tables are created |
| fresh DB, module disabled | its tables do not exist |
| existing DB, module disabled | tables and data stay |
| existing DB, module enabled later | AutoMigrate reconciles and reuses |

## Module inventory

Feature modules:

| ID | Surface | Requires | Schema |
|---|---|---|---|
| `app.solian.files.core` | shared file, pool, permission and lock tables | — | `file_pools`, `file_objects`, `cloud_files`, `file_permissions`, `pool_permissions`, `file_locks` |
| `app.solian.files.auth` | request authentication; synthesized identity when Stargate is absent | core | none |
| `app.solian.files.files` | `/api/files` read, listing, lifecycle | core | none |
| `app.solian.files.uploads` | `/api/files/upload` | core | `persistent_tasks`, `upload_chunks` |
| `app.solian.files.content-edit` | `PATCH\|PUT /api/files/:id/content` | core | none |
| `app.solian.files.pools` | `/api/pools` | core | none |
| `app.solian.files.billing` | `/api/billing` | core | none |
| `app.solian.files.account` | `/api/account/me` | — | none |
| `app.solian.files.tokens` | `/api/s3/tokens` | core | none |
| `app.solian.files.nodes` | `/api/nodes` | core | `storage_nodes` |
| `app.solian.files.admin` | `/api/admin/storage`, `/api/admin/uploads`, `/api/admin/reanalysis` | core | none |
| `app.solian.files.webdav` | `/webdav`, `/api/webdav/tokens` | core | `web_dav_tokens` |
| `app.solian.files.wopi` | `/wopi`, `POST /api/files/:id/edit` | core | none |
| `app.solian.files.grpc` | `DyFileService`, `DyQuotaService` | core | none |
| `app.solian.files.analysis.image` | image dimensions, EXIF, blurhash | — | none |
| `app.solian.files.analysis.video` | video probe, dimensions, aspect ratio | — | none |
| `app.solian.files.analysis.audio` | audio probe | — | none |
| `app.solian.files.analysis.reanalysis` | background reanalysis queue | — | none |
| `app.solian.files.derivatives.image` | compressed webp derivative | — | none |
| `app.solian.files.derivatives.video` | video thumbnail derivative | — | none |
| `app.solian.files.media` | on-the-fly transforms on content routes | core | none |
| `app.solian.files.worker` | processing loop and maintenance | — | none |
| `app.solian.files.storage-node` | `/_dfs` control surface | — | none |

Feature modules that default to **off** (they also honour their legacy config
switch, so existing configs keep working):

| ID | Legacy switch |
|---|---|
| `app.solian.files.webdav` | `webdav.enabled` |
| `app.solian.files.wopi` | `wopi.enabled` |
| `app.solian.files.media` | `media.enable` |
| `app.solian.files.s3` | `masterS3.enabled` |
| `app.solian.files.storage-node-s3` | storage mode only |

## Config

```toml
[modules]
# Empty enabled = every compiled-in module whose DefaultEnabled is set.
enabled = []
disabled = []
# Per-module settings, addressed by module ID.
# [modules.settings."app.solian.files.media"]
# cache = "disk"

# Explicit port selection. Only needed when two modules provide one port.
[ports]
# files = "app.solian.files.s3"

[auth]
# Empty target = no Stargate. Every request resolves to the synthesized
# account below and every permission check is skipped.
target = ""
syntheticAccountId = "00000000-0000-0000-0000-000000000001"
syntheticAccountName = "dyson"
syntheticAccountNick = "Dyson"
```

Env overrides, comma separated:

```text
DYSONFS_MODULES_ENABLED=app.solian.files.auth,app.solian.files.core,app.solian.files.files
DYSONFS_MODULES_DISABLED=app.solian.files.analysis.image,app.solian.files.media
```

`GET /api/modules` reports the resolved set and the capabilities it implies.

## Auth-absent behaviour

With `[auth] target = ""` the auth module synthesizes one account:

- `GET /api/account/me` returns that account for an unauthenticated request.
- File ownership is attributed to its UUID, so a re-enable of Stargate keeps
  the same rows.
- Permission checks are skipped: no `DyPermissionService` is dialed and
  `RequireAccountPermission` allows.
- The synthesized account is a superuser, so admin routes are reachable.
- WebDAV token authentication still runs when `webdav.enabled` is set; a
  request without a matching token falls through to the synthesized account.

This is a deployment mode, not a degraded one: a single-tenant DysonFS needs no
account service at all.

## Adding a module

1. Create `internal/modules/<name>/` with a `Factory()`.
2. Declare `Models` for any table you own; never add columns to another
   module's table and never drop one.
3. Register routes from `Configure` using the `internal/handler` `Register*`
   functions, or mount your own handler on `host.Engine`.
4. Add the factory to `internal/modules/standard/standard.go`.
5. Document the ID here and add a row to the inventory table.