// Package surfaces mounts the HTTP surfaces as modules. Each module owns one
// route group; disabling it removes the group and nothing else.
package surfaces

import (
	"context"

	"github.com/gin-gonic/gin"

	"src.solsynth.dev/sosys/filesystem/internal/core"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/handler"
	"src.solsynth.dev/sosys/filesystem/internal/module"
)

// Module IDs.
const (
	IDFiles       = "app.solian.files.files"
	IDUploads     = "app.solian.files.uploads"
	IDPools       = "app.solian.files.pools"
	IDBilling     = "app.solian.files.billing"
	IDAccount     = "app.solian.files.account"
	IDContentEdit = "app.solian.files.content-edit"
	IDTokens      = "app.solian.files.tokens"
	IDNodes       = "app.solian.files.nodes"
	IDAdmin       = "app.solian.files.admin"
	IDWebDAV      = "app.solian.files.webdav"
	IDWOPI        = "app.solian.files.wopi"
	IDStorageNode = "app.solian.files.storage-node"
	IDGRPC        = core.ModuleFilesGRPC
)

// GRPC returns the gRPC surface module. It gates the file and quota gRPC
// services; without it the gRPC listener stays empty.
func GRPC() module.Factory {
	return module.Factory{
		ID:             IDGRPC,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Requires:       []string{IDCore},
		Capabilities:   []string{"files.grpc", "files.grpc.upload"},
		New:            func() module.Instance { return &instance{} },
	}
}

// Files returns the file read/write surface module.
func Files() module.Factory {
	return module.Factory{
		ID:             IDFiles,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Requires:       []string{IDCore},
		Capabilities:   []string{"files.read", "files.write", "files.permissions"},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) {
				handler.RegisterFileRoutes(host.Engine, handler.FromCore(host.Core))
			}}
		},
	}
}

// Uploads returns the upload surface module. It owns the upload task tables.
func Uploads() module.Factory {
	return module.Factory{
		ID:             IDUploads,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Requires:       []string{IDCore},
		Capabilities:   []string{"files.upload", "files.upload.chunked", "files.upload.direct"},
		Models:         []any{&database.PersistentTask{}, &database.UploadChunk{}},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) {
				handler.RegisterUploadRoutes(host.Engine, handler.FromCore(host.Core))
			}}
		},
	}
}

// Pools returns the pool management module.
func Pools() module.Factory {
	return surface(IDPools, "Storage pools (/api/pools)", []string{"files.pools"}, IDCore, handler.RegisterPoolRoutes)
}

// Billing returns the quota and usage module.
func Billing() module.Factory {
	return surface(IDBilling, "Quota and usage reporting (/api/billing)", []string{"files.billing"}, IDCore, handler.RegisterBillingRoutes)
}

// Account returns the identity module.
func Account() module.Factory {
	return surface(IDAccount, "Account identity (/api/account)", []string{"files.account"}, "", handler.RegisterAccountRoutes)
}

// ContentEdit returns binary patch and content replacement.
func ContentEdit() module.Factory {
	return surface(IDContentEdit, "Binary patch and content replacement (/api/files/:id/content)",
		[]string{"files.content-edit", "files.locks"}, IDCore, handler.RegisterContentEditRoutes)
}

// Tokens returns the S3 token module.
func Tokens() module.Factory {
	return surface(IDTokens, "S3 access tokens (/api/s3/tokens)", []string{"files.s3-tokens"}, IDCore, handler.RegisterS3TokenRoutes)
}

// Nodes returns the storage node management module. It owns the storage node
// registry table.
func Nodes() module.Factory {
	return module.Factory{
		ID:             IDNodes,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Requires:       []string{IDCore},
		Capabilities:   []string{"files.nodes"},
		Models:         []any{&database.StorageNode{}},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) {
				handler.RegisterNodeRoutes(host.Engine, handler.FromCore(host.Core))
			}}
		},
	}
}

// Admin returns the administrative module: storage administration and
// metadata repair.
func Admin() module.Factory {
	return module.Factory{
		ID:             IDAdmin,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Requires:       []string{IDCore},
		Capabilities:   []string{"files.admin"},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) {
				deps := handler.FromCore(host.Core)
				handler.RegisterStorageAdminRoutes(host.Engine, deps)
				// Metadata repair is only meaningful when the analysis module
				// is present; the route group itself is core admin surface.
				handler.RegisterReanalysisRoutes(host.Engine, deps)
			}}
		},
	}
}

// WebDAV returns the WebDAV module. It is off by default and also honours the
// legacy webdav.enabled switch so existing configs keep working.
func WebDAV() module.Factory {
	return module.Factory{
		ID:             IDWebDAV,
		Kind:           module.KindFeature,
		DefaultEnabled: false,
		Requires:       []string{IDCore},
		Capabilities:   []string{"files.webdav"},
		Models:         []any{&database.WebDAVToken{}},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) {
				if !host.Core.Config.WebDAV.Enabled {
					host.Log.Info().Msg("webdav module compiled in but webdav.enabled is false; not mounted")
					return
				}
				handler.RegisterWebDAVRoutes(host.Engine, handler.FromCore(host.Core))
			}}
		},
	}
}

// WOPI returns the WOPI module. It is off by default and also honours the
// legacy wopi.enabled switch.
func WOPI() module.Factory {
	return module.Factory{
		ID:             IDWOPI,
		Kind:           module.KindFeature,
		DefaultEnabled: false,
		Requires:       []string{IDCore},
		Capabilities:   []string{"files.wopi"},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) {
				if host.Core.Wopi == nil || !host.Core.Wopi.Enabled() {
					host.Log.Info().Msg("wopi module compiled in but wopi.enabled is false; not mounted")
					return
				}
				handler.RegisterWOPIRoutes(host.Engine, handler.FromCore(host.Core))
			}}
		},
	}
}

// StorageNode returns the storage-node control surface (/_dfs).
func StorageNode() module.Factory {
	return surface(IDStorageNode, "Storage node control surface (/_dfs)", []string{"files.storage-node"}, "", handler.RegisterStorageNodeRoutes)
}

func surface(id, description string, capabilities []string, requires string, register func(*gin.Engine, handler.Deps)) module.Factory {
	factory := module.Factory{
		ID:             id,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Capabilities:   capabilities,
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) {
				register(host.Engine, handler.FromCore(host.Core))
			}}
		},
	}
	if requires != "" {
		factory.Requires = []string{requires}
	}
	return factory
}

type instance struct{ configure func(*module.Host) }

func (i *instance) Configure(_ context.Context, host *module.Host) error {
	if i.configure != nil {
		i.configure(host)
	}
	return nil
}

func (i *instance) Stop(context.Context) error { return nil }
