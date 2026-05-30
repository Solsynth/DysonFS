package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/grpcsvc"
	"src.solsynth.dev/sosys/filesystem/internal/handler"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/server"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	"src.solsynth.dev/sosys/filesystem/internal/s3server"
	"src.solsynth.dev/sosys/filesystem/internal/worker"
	sharedcache "src.solsynth.dev/sosys/go/pkg/cache"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

type App struct {
	cfg         *config.Config
	mode        string
	db          *database.DB
	bus         *eventbus.Bus
	redis       *redis.Client
	stor        storage.Backend
	files       *service.FileService
	wopi        *service.WOPIService
	tasks       *service.TaskService
	quota       *service.QuotaService
	worker      *worker.Worker
	dispatcher  dispatch.Dispatcher
	httpSrv     *http.Server
	masterS3Srv *http.Server
	grpcSrv     *grpc.Server
	profileConn *grpc.ClientConn
	natsConn    *nats.Conn
	logger      zerolog.Logger
}

func (a *App) Files() *service.FileService { return a.files }

func New(cfg *config.Config, mode string) (*App, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "master"
	}
	if cfg.Bundled.Enable && mode == "master" {
		mode = "bundled"
	}

	db, err := database.Open(cfg)
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(); err != nil {
		return nil, err
	}

	var redisClient *redis.Client
	if cfg.Redis.Addr != "" {
		redisClient = redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr})
	}

	stor := storage.NewLocalBackend(cfg.Storage.LocalDir)

	var natsConn *nats.Conn
	if cfg.NATS.URL != "" {
		conn, err := nats.Connect(cfg.NATS.URL, nats.Name(cfg.App.Name), nats.MaxReconnects(-1), nats.ReconnectWait(2*time.Second))
		if err != nil {
			return nil, err
		}
		natsConn = conn
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
	app := &App{cfg: cfg, mode: mode, db: db, redis: redisClient, stor: stor, files: files, wopi: wopi, tasks: service.NewTaskService(db), quota: quota, natsConn: natsConn, logger: logging.Log}
	if cfg.Passport.Target != "" {
		profileClient, profileConn, err := service.NewProfileClient(cfg.Passport)
		if err != nil {
			return nil, err
		}
		app.profileConn = profileConn
		app.quota.SetProfileClient(profileClient)
	}
	defaultPoolID, err := app.files.SeedPools(cfg)
	if err != nil {
		return nil, err
	}
	backend, err := app.files.BackendForPoolID(&defaultPoolID)
	if err != nil {
		return nil, err
	}
	app.stor = backend
	app.files.SetStorage(backend)
	if natsConn != nil {
		app.bus = eventbus.New(natsConn)
	}
	app.worker = worker.New(app.bus, app.files, app.stor, app.db, app.cfg.Storage.TempDir)
	if cfg.Bundled.Enable {
		count := cfg.Bundled.WorkerNum
		if count < 1 {
			count = 1
		}
		workers := make([]*worker.Worker, 0, count)
		for i := 0; i < count; i++ {
			workers = append(workers, worker.New(nil, app.files, app.stor, app.db, app.cfg.Storage.TempDir))
		}
		app.dispatcher = dispatch.NewBundled(workers)
	}
	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	switch a.mode {
	case "master":
		return a.startMaster(ctx)
	case "bundled":
		return a.startBundled(ctx)
	case "worker":
		return a.startWorker(ctx)
	case "storage":
		return a.startStorage(ctx)
	default:
		return fmt.Errorf("unknown mode %q", a.mode)
	}
}

func (a *App) Stop(ctx context.Context) error {
	if a.httpSrv != nil {
		_ = a.httpSrv.Shutdown(ctx)
	}
	if a.masterS3Srv != nil {
		_ = a.masterS3Srv.Shutdown(ctx)
	}
	if a.grpcSrv != nil {
		a.grpcSrv.GracefulStop()
	}
	if a.natsConn != nil {
		a.natsConn.Close()
	}
	if a.profileConn != nil {
		_ = a.profileConn.Close()
	}
	if a.redis != nil {
		_ = a.redis.Close()
	}
	return nil
}

func (a *App) startMaster(ctx context.Context) error {
	r := server.NewRouter(a.cfg, a.mode, a.files, a.wopi, a.tasks, a.quota, a.bus, nil)
	a.httpSrv = &http.Server{Addr: ":" + a.cfg.HTTP.Port, Handler: r, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second}

	lis, err := net.Listen("tcp", ":"+a.cfg.GRPC.Port)
	if err != nil {
		return err
	}
	grpcOpts := []grpc.ServerOption{}
	if a.cfg.GRPC.UseTLS {
		if a.cfg.GRPC.CertFile == "" || a.cfg.GRPC.KeyFile == "" {
			return fmt.Errorf("grpc tls requires grpc.certFile and grpc.keyFile")
		}
		creds, err := credentials.NewServerTLSFromFile(a.cfg.GRPC.CertFile, a.cfg.GRPC.KeyFile)
		if err != nil {
			return fmt.Errorf("load grpc tls credentials: %w", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
	}
	a.grpcSrv = grpc.NewServer(grpcOpts...)
	grpcsvc.Register(a.grpcSrv, a.files)
	reflection.Register(a.grpcSrv)

	go func() { _ = a.grpcSrv.Serve(lis) }()
	go func() { _ = a.httpSrv.ListenAndServe() }()
	a.startMasterS3()
	logging.Log.Info().Str("mode", a.mode).Msg("master started")
	return nil
}

func (a *App) startWorker(context.Context) error {
	go func() { _ = a.worker.Start(context.Background()) }()
	logging.Log.Info().Str("mode", a.mode).Msg("worker started")
	return nil
}

func (a *App) startBundled(ctx context.Context) error {
	go func() { _ = a.worker.Start(ctx) }()
	r := server.NewRouter(a.cfg, a.mode, a.files, a.wopi, a.tasks, a.quota, nil, a.dispatcher)
	a.httpSrv = &http.Server{Addr: ":" + a.cfg.HTTP.Port, Handler: r, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second}
	lis, err := net.Listen("tcp", ":"+a.cfg.GRPC.Port)
	if err != nil {
		return err
	}
	grpcOpts := []grpc.ServerOption{}
	if a.cfg.GRPC.UseTLS {
		if a.cfg.GRPC.CertFile == "" || a.cfg.GRPC.KeyFile == "" {
			return fmt.Errorf("grpc tls requires grpc.certFile and grpc.keyFile")
		}
		creds, err := credentials.NewServerTLSFromFile(a.cfg.GRPC.CertFile, a.cfg.GRPC.KeyFile)
		if err != nil {
			return fmt.Errorf("load grpc tls credentials: %w", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
	}
	a.grpcSrv = grpc.NewServer(grpcOpts...)
	grpcsvc.Register(a.grpcSrv, a.files)
	reflection.Register(a.grpcSrv)
	go func() { _ = a.grpcSrv.Serve(lis) }()
	go func() { _ = a.httpSrv.ListenAndServe() }()
	a.startMasterS3()
	logging.Log.Info().Str("mode", a.mode).Msg("bundled started")
	return nil
}

func (a *App) startStorage(context.Context) error {
	logging.Log.Info().Str("mode", a.mode).Msg("storage mode started")

	r := gin.New()
	r.Use(gin.Recovery())

	dfs := r.Group("/_dfs")
	{
		dfs.GET("/version", func(c *gin.Context) {
			handler.StorageNodeVersion(c, a.cfg.StorageNode)
		})
		dfs.GET("/identity", func(c *gin.Context) {
			handler.StorageNodeIdentity(c, a.cfg.StorageNode)
		})
		dfs.POST("/auth/validate", func(c *gin.Context) {
			handler.StorageNodeAuthValidate(c, a.cfg.StorageNode)
		})
	}

	backend := s3server.NewStorageBackend(a.stor)
	s3srv := s3server.New(backend, a.cfg.StorageNode.S3AccessKey, a.cfg.StorageNode.S3SecretKey)
	s3handler := s3srv.Handler()
	r.NoRoute(func(c *gin.Context) { s3handler(c.Writer, c.Request) })

	a.httpSrv = &http.Server{
		Addr:         ":" + a.cfg.StorageNode.Port,
		Handler:      r,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		logging.Log.Info().Str("addr", a.httpSrv.Addr).Msg("storage node HTTP server listening")
		if err := a.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Log.Fatal().Err(err).Msg("storage node HTTP server failed")
		}
	}()

	return nil
}

func (a *App) startMasterS3() {
	if !a.cfg.MasterS3.Enabled {
		return
	}
	backend := s3server.NewMasterBackend(a.files, a.cfg.Storage.TempDir)
	s3srv := s3server.NewWithResolver(backend, backend)
	s3handler := s3srv.Handler()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s3handler)
	a.masterS3Srv = &http.Server{
		Addr:         ":" + a.cfg.MasterS3.Port,
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		logging.Log.Info().Str("addr", a.masterS3Srv.Addr).Msg("master S3 API listening")
		if err := a.masterS3Srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Log.Fatal().Err(err).Msg("master S3 server failed")
		}
	}()
}

func (a *App) runWorker() error { return nil }
