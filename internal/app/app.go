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
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/grpcsvc"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/server"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	"src.solsynth.dev/sosys/filesystem/internal/worker"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type App struct {
	cfg      *config.Config
	mode     string
	db       *database.DB
	bus      *eventbus.Bus
	redis    *redis.Client
	stor     storage.Backend
	files    *service.FileService
	tasks    *service.TaskService
	httpSrv  *http.Server
	grpcSrv  *grpc.Server
	natsConn *nats.Conn
	logger   zerolog.Logger
}

func New(cfg *config.Config, mode string) (*App, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "master"
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

	var stor storage.Backend = storage.NewLocalBackend(cfg.Storage.LocalDir)
	if strings.EqualFold(cfg.Files.PreferredStorage, "s3") {
		s3Backend, err := storage.NewS3Backend(cfg.S3.Endpoint, cfg.S3.AccessKey, cfg.S3.SecretKey, cfg.S3.Bucket, cfg.S3.Secure)
		if err != nil {
			return nil, err
		}
		stor = s3Backend
	}

	var natsConn *nats.Conn
	if cfg.NATS.URL != "" {
		conn, err := nats.Connect(cfg.NATS.URL, nats.Name(cfg.App.Name), nats.MaxReconnects(-1), nats.ReconnectWait(2*time.Second))
		if err != nil {
			return nil, err
		}
		natsConn = conn
	}

	app := &App{cfg: cfg, mode: mode, db: db, redis: redisClient, stor: stor, files: service.NewFileService(db, stor), tasks: service.NewTaskService(db), natsConn: natsConn, logger: logging.Log}
	if natsConn != nil {
		app.bus = eventbus.New(natsConn)
	}
	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	switch a.mode {
	case "master":
		return a.startMaster(ctx)
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
	if a.grpcSrv != nil {
		a.grpcSrv.GracefulStop()
	}
	if a.natsConn != nil {
		a.natsConn.Close()
	}
	if a.redis != nil {
		_ = a.redis.Close()
	}
	return nil
}

func (a *App) startMaster(ctx context.Context) error {
	r := server.NewRouter(a.cfg, a.files, a.tasks, a.bus)
	a.httpSrv = &http.Server{Addr: ":" + a.cfg.HTTP.Port, Handler: r, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second}

	lis, err := net.Listen("tcp", ":"+a.cfg.GRPC.Port)
	if err != nil {
		return err
	}
	a.grpcSrv = grpc.NewServer()
	grpcsvc.Register(a.grpcSrv, a.files)
	reflection.Register(a.grpcSrv)

	go func() { _ = a.grpcSrv.Serve(lis) }()
	go func() { _ = a.httpSrv.ListenAndServe() }()
	logging.Log.Info().Str("mode", a.mode).Msg("master started")
	return nil
}

func (a *App) startWorker(context.Context) error {
	w := worker.New(a.bus, a.files, a.stor, a.db)
	go func() { _ = w.Start(context.Background()) }()
	logging.Log.Info().Str("mode", a.mode).Msg("worker started")
	return nil
}

func (a *App) startStorage(context.Context) error {
	logging.Log.Info().Str("mode", a.mode).Msg("storage mode started")
	return nil
}

func (a *App) runWorker() error { return nil }
