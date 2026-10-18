// Package app owns the DysonFS process bootstrap. It builds the shared core
// services, resolves the compiled-in module set and starts the host listeners.
// cmd/main.go is deliberately thin.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	gen "src.solsynth.dev/sosys/go/proto"
	sharedcache "src.solsynth.dev/sosys/go/pkg/cache"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/core"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/grpcsvc"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/module"
	"src.solsynth.dev/sosys/filesystem/internal/server"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	"src.solsynth.dev/sosys/filesystem/internal/worker"
)

// Runtime is a constructed process: the core service graph plus the listeners
// the mode owns. Modules are configured against it.
type Runtime struct {
	Config *config.Config
	Mode   string
	Core   *core.Services

	db       *database.DB
	redis    *redis.Client
	natsConn *nats.Conn

	httpSrv *http.Server
	grpcSrv *grpc.Server

	resolution *module.Resolution
}

// Files exposes the file service for CLI subcommands.
func (r *Runtime) Files() *service.FileService { return r.Core.Files }

// DB exposes the database handle for CLI subcommands.
func (r *Runtime) DB() *database.DB { return r.db }

// New constructs the runtime for a mode. The module set is resolved first, so
// the schema that is migrated is exactly the schema the enabled modules own;
// seeding then writes into tables that are guaranteed to exist.
func New(ctx context.Context, cfg *config.Config, mode string, factories []module.Factory) (*Runtime, error) {
	mode = normalizeMode(cfg, mode)

	resolution, err := module.Resolve(factories, cfg.Modules.Enabled, cfg.Modules.Disabled)
	if err != nil {
		return nil, fmt.Errorf("resolve modules: %w", err)
	}
	for _, warning := range resolution.Warnings {
		logging.Log.Warn().Msg(warning)
	}
	logging.Log.Info().Strs("modules", module.IDs(resolution.Factories)).Str("mode", mode).Msg("modules resolved")

	db, err := database.Open(cfg)
	if err != nil {
		return nil, err
	}

	var redisClient *redis.Client
	if cfg.Redis.Addr != "" {
		redisClient = redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr, DB: cfg.Redis.DB})
	}

	stor := storage.NewLocalBackend(cfg.Storage.LocalDir)

	var bus *eventbus.Bus
	var natsConn *nats.Conn
	if cfg.NATS.URL != "" {
		conn, err := nats.Connect(cfg.NATS.URL, nats.Name(cfg.App.Name), nats.MaxReconnects(-1), nats.ReconnectWait(2*time.Second))
		if err != nil {
			return nil, err
		}
		natsConn = conn
		bus = eventbus.New(conn)
	}

	files := service.NewFileService(db, stor)
	files.SetAccessSecret(cfg.Files.AccessSecret)
	if redisClient != nil {
		files.SetCache(sharedcache.NewRedisCacheService(redisClient))
	}
	wopi, err := service.NewWOPIService(cfg.WOPI, files)
	if err != nil {
		return nil, err
	}
	quota := service.NewQuotaService(db)
	quota.SetLevelingConfig(cfg.Quota.Leveling)
	if redisClient != nil {
		quota.SetCache(sharedcache.NewRedisCacheService(redisClient))
	}
	tasks := service.NewTaskService(db)

	if cfg.Workspace.Target != "" {
		workspaceClient, conn, err := service.NewWorkspaceClient(cfg.Workspace)
		if err != nil {
			return nil, err
		}
		quota.SetWorkspaceClient(workspaceClient)
		quota.SetSharedQuotaClient(gen.NewDyQuotaServiceClient(conn))
	}

	// Schema first: every model and SQL hook an enabled module declares, then
	// seed the configured pools into the tables that now exist.
	if err := migrate(ctx, db, resolution.Factories); err != nil {
		return nil, err
	}
	services := &core.Services{Enabled: resolution.Enabled}

	defaultPoolID, err := files.SeedPools(cfg)
	if err != nil {
		return nil, err
	}
	backend, err := files.BackendForPoolID(&defaultPoolID)
	if err != nil {
		return nil, err
	}
	files.SetStorage(backend)

	var dispatcher dispatch.Dispatcher
	if cfg.Bundled.Enable {
		count := cfg.Bundled.WorkerNum
		if count < 1 {
			count = 1
		}
		workers := make([]*worker.Worker, 0, count)
		for range count {
			workers = append(workers, worker.New(nil, files, db, cfg.Storage.TempDir))
		}
		dispatcher = dispatch.NewBundled(workers)
	}

	// The worker and the per-kind registries are shared objects: modules
	// enable kinds on them during Configure, and the worker reads the same
	// instances while processing. They are created here so the analysis and
	// derivative modules never have to (and never accidentally replace them).
	workerInstance := worker.New(bus, files, db, cfg.Storage.TempDir)
	analysisRegistry := core.NewAnalysisRegistry(files)
	derivativeRegistry := core.NewDerivativeRegistry()
	workerInstance.SetAnalysis(analysisRegistry)
	workerInstance.SetDerivatives(derivativeRegistry)

	services.Config = cfg
	services.Mode = mode
	services.DB = db
	services.Redis = redisClient
	services.Bus = bus
	services.Files = files
	services.Tasks = tasks
	services.Quota = quota
	services.Wopi = wopi
	services.Analysis = analysisRegistry
	services.Derivatives = derivativeRegistry
	services.Worker = workerInstance
	services.Log = logging.Log
	switch {
	case dispatcher != nil:
		services.Dispatcher = dispatcher
		services.Publisher = core.DispatcherPublisher{Dispatcher: dispatcher}
	case bus != nil:
		services.Publisher = core.BusPublisher{Bus: bus}
	}

	return &Runtime{
		Config: cfg, Mode: mode, Core: services,
		db: db, redis: redisClient, natsConn: natsConn,
		resolution: resolution,
	}, nil
}

func normalizeMode(cfg *config.Config, mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "master"
	}
	if cfg.Bundled.Enable && mode == "master" {
		return "bundled"
	}
	return mode
}

// Run configures the resolved module set and serves until ctx is done. The
// module set and its schema were applied by New.
func (r *Runtime) Run(ctx context.Context) error {
	instances := make([]namedInstance, 0, len(r.resolution.Factories))
	for _, factory := range r.resolution.Factories {
		instance := factory.New()
		if instance == nil {
			return fmt.Errorf("module %s returned a nil instance", factory.ID)
		}
		instances = append(instances, namedInstance{factory: factory, instance: instance})
	}

	// Process-wide middleware is collected before the engine exists, so every
	// route a module later registers inherits it.
	middleware, err := prepareMiddleware(ctx, r.Core, instances)
	if err != nil {
		return err
	}

	host, err := r.host(r.resolution, middleware)
	if err != nil {
		return err
	}

	for _, entry := range instances {
		if provider, ok := entry.instance.(module.PortProvider); ok {
			if err := provider.ProvidePorts(ctx, host); err != nil {
				return &module.BuildError{Module: entry.factory.ID, Err: err}
			}
		}
	}

	registered := make([]module.Instance, 0, len(instances))
	for _, entry := range instances {
		if err := entry.instance.Configure(ctx, host); err != nil {
			stopInstances(registered)
			return &module.BuildError{Module: entry.factory.ID, Err: err}
		}
		registered = append(registered, entry.instance)
	}
	defer stopInstances(registered)

	if err := r.serve(ctx, host); err != nil {
		return err
	}

	<-ctx.Done()
	logging.Log.Info().Msg("shutting down")
	return r.Stop(context.Background())
}

// prepareMiddleware asks every module for its process-wide middleware, in
// dependency order. Only the auth module normally contributes one.
func prepareMiddleware(ctx context.Context, services *core.Services, instances []namedInstance) ([]gin.HandlerFunc, error) {
	var chain []gin.HandlerFunc
	for _, entry := range instances {
		preparer, ok := entry.instance.(module.PreHost)
		if !ok {
			continue
		}
		handlers, err := preparer.Prepare(ctx, services)
		if err != nil {
			return nil, &module.BuildError{Module: entry.factory.ID, Err: err}
		}
		chain = append(chain, handlers...)
	}
	return chain, nil
}

// migrate applies the schema owned by the resolved modules. Nothing is ever
// dropped, so disabling a module leaves its existing tables and data alone.
func migrate(ctx context.Context, db *database.DB, factories []module.Factory) error {
	if models := coreModels(factories); len(models) > 0 {
		if err := db.DB.AutoMigrate(models...); err != nil {
			return fmt.Errorf("core AutoMigrate: %w", err)
		}
	}
	return module.MigrateSchema(ctx, db.DB, factories)
}

// host assembles the module host for this mode. The HTTP engine exists only in
// the modes that serve HTTP; a worker-only process leaves Engine nil and the
// surface modules that need it are not part of its resolved set.
func (r *Runtime) host(resolution *module.Resolution, middleware []gin.HandlerFunc) (*module.Host, error) {
	host := &module.Host{
		Core:     r.Core,
		Ports:    module.NewPortRegistry(),
		Enabled:  resolution.Enabled,
		Settings: r.Config.Modules.Settings,
		Log:      logging.Log,
	}
	host.Ports.Select(r.Config.Ports)

	if servesHTTP(r.Mode) {
		srv := server.New(r.Config, r.Mode, r.Core.Files, middleware...)
		if srv.WebError != nil {
			logging.Log.Warn().Err(srv.WebError).Str("dist", r.Config.Web.StaticDir).Msg("web SPA hosting disabled")
		}
		srv.MountModules(modulesResponse(resolution.Factories))
		host.Engine = srv.Engine
		host.API = srv.API()
	}

	if servesGRPC(r.Mode) {
		grpcOpts := []grpc.ServerOption{}
		if r.Config.GRPC.UseTLS {
			if r.Config.GRPC.CertFile == "" || r.Config.GRPC.KeyFile == "" {
				return nil, fmt.Errorf("grpc tls requires grpc.certFile and grpc.keyFile")
			}
			creds, err := credentials.NewServerTLSFromFile(r.Config.GRPC.CertFile, r.Config.GRPC.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load grpc tls credentials: %w", err)
			}
			grpcOpts = append(grpcOpts, grpc.Creds(creds))
		}
		r.grpcSrv = grpc.NewServer(grpcOpts...)
		host.GRPC = r.grpcSrv
	}
	return host, nil
}

func servesHTTP(mode string) bool { return mode == "master" || mode == "bundled" }

func servesGRPC(mode string) bool { return mode == "master" || mode == "bundled" }

func servesTaskSweep(mode string) bool {
	return mode == "master" || mode == "bundled" || mode == "worker"
}

// serve starts the mode's listeners after every module has configured itself.
func (r *Runtime) serve(ctx context.Context, host *module.Host) error {
	if host.Engine != nil {
		r.httpSrv = &http.Server{
			Addr:         ":" + r.Config.HTTP.Port,
			Handler:      host.Engine,
			ReadTimeout:  60 * time.Second,
			WriteTimeout: 60 * time.Second,
		}
		logging.Log.Info().Str("addr", r.httpSrv.Addr).Msg("http server listening")
		go func() {
			if err := r.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logging.Log.Error().Err(err).Msg("http server failed")
			}
		}()
	}

	if host.GRPC != nil {
		lis, err := net.Listen("tcp", ":"+r.Config.GRPC.Port)
		if err != nil {
			return err
		}
		// Core gRPC surface: the file and quota services are host-level, they
		// only exist where the file modules are enabled.
		if r.Core.Enabled[core.ModuleFilesGRPC] {
			grpcsvc.Register(host.GRPC, grpcsvc.Deps{
				Config: r.Config, Files: r.Core.Files, Tasks: r.Core.Tasks,
				Quota: r.Core.Quota, Publisher: r.Core.Publisher, Analysis: r.Core.Analysis,
			})
		}
		reflection.Register(host.GRPC)
		logging.Log.Info().Str("addr", ":"+r.Config.GRPC.Port).Msg("grpc server listening")
		go func() { _ = host.GRPC.Serve(lis) }()
	}

	if servesTaskSweep(r.Mode) {
		r.startUploadExpirySweep(ctx)
	}
	return nil
}

// Stop shuts the runtime down.
func (r *Runtime) Stop(ctx context.Context) error {
	if r.httpSrv != nil {
		_ = r.httpSrv.Shutdown(ctx)
	}
	for _, srv := range r.Core.Servers {
		_ = srv.Shutdown(ctx)
	}
	if r.grpcSrv != nil {
		r.grpcSrv.GracefulStop()
	}
	if r.natsConn != nil {
		r.natsConn.Close()
	}
	if r.redis != nil {
		_ = r.redis.Close()
	}
	return nil
}

// startUploadExpirySweep runs the stale-upload expiry sweep immediately and
// then every hour, in every mode that owns the task table. Broken direct
// uploads otherwise leave their multipart sessions and single-PUT objects in
// the storage pool forever.
func (r *Runtime) startUploadExpirySweep(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		r.runUploadExpirySweep(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.runUploadExpirySweep(ctx)
			}
		}
	}()
}

func (r *Runtime) runUploadExpirySweep(ctx context.Context) {
	started := time.Now()
	count, err := r.Core.Files.ExpireStaleUploadTasks(ctx)
	if err != nil {
		logging.Log.Error().Err(err).Msg("upload expiry sweep failed")
		return
	}
	if count > 0 {
		logging.Log.Info().Int("expired", count).Dur("duration", time.Since(started)).Msg("upload expiry sweep complete")
	}
}

// modulesResponse describes the resolved module set for GET /api/modules.
func modulesResponse(factories []module.Factory) server.ModulesResponse {
	out := make([]server.ModuleInfo, 0, len(factories))
	for _, factory := range factories {
		out = append(out, server.ModuleInfo{
			ID:           factory.ID,
			Kind:         string(factory.Kind),
			Capabilities: factory.Capabilities,
		})
	}
	return server.ModulesResponse{Modules: out, Capabilities: module.Capabilities(factories)}
}

// coreModels returns the models AutoMigrate applies: every model any enabled
// module declares, de-duplicated. A disabled module contributes nothing, which
// is what keeps a fresh database free of its tables. Module SQL hooks run
// afterwards through module.MigrateSchema.
func coreModels(factories []module.Factory) []any {
	seen := map[string]bool{}
	out := make([]any, 0, len(factories))
	for _, factory := range factories {
		for _, model := range factory.Models {
			name := fmt.Sprintf("%T", model)
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, model)
		}
	}
	return out
}

type namedInstance struct {
	factory  module.Factory
	instance module.Instance
}

func stopInstances(instances []module.Instance) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := len(instances) - 1; i >= 0; i-- {
		if err := instances[i].Stop(shutdownCtx); err != nil {
			logging.Log.Warn().Err(err).Msg("stop module")
		}
	}
}

// ensure the gin import is retained for the host engine type.
var _ *gin.Engine