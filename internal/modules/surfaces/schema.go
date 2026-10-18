package surfaces

import (
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/module"
)

// IDCore is the module that owns the file-domain tables. It is required: every
// other file module reads or joins those tables.
const IDCore = "app.solian.files.core"

// Core returns the base schema module. Every file, pool, permission and lock
// table lives here, so a service built without, for example, WebDAV never
// creates the WebDAV token table.
func Core() module.Factory {
	return module.Factory{
		ID:             IDCore,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Required:       true,
		Capabilities:   []string{"files.storage", "files.objects", "files.permissions", "files.locks"},
		Models: []any{
			&database.FilePool{}, &database.FileObject{}, &database.CloudFile{},
			&database.FilePermission{}, &database.PoolPermission{}, &database.FileLock{},
		},
		New: func() module.Instance { return &instance{} },
	}
}
