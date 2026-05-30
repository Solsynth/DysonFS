package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App      AppConfig      `mapstructure:"app"`
	HTTP     HTTPConfig     `mapstructure:"http"`
	GRPC     GRPCConfig     `mapstructure:"grpc"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	NATS     NATSConfig     `mapstructure:"nats"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Bundled  BundledConfig  `mapstructure:"bundled"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Passport PassportConfig `mapstructure:"passport"`
	Quota    QuotaConfig    `mapstructure:"quota"`
	Mode     ModeConfig     `mapstructure:"mode"`
	Files    FileConfig     `mapstructure:"files"`
	WOPI     WOPIConfig     `mapstructure:"wopi"`
	WebDAV   WebDAVConfig   `mapstructure:"webdav"`
	Pools    []PoolConfig   `mapstructure:"pools"`
	S3       S3Config       `mapstructure:"s3"`
	Sentry   SentryConfig   `mapstructure:"sentry"`
}

type SentryConfig struct {
	DSN              string  `mapstructure:"dsn"`
	TracesSampleRate float64 `mapstructure:"tracesSampleRate"`
	Environment      string  `mapstructure:"environment"`
	Release          string  `mapstructure:"release"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
}

type HTTPConfig struct {
	Port string `mapstructure:"port"`
}

type GRPCConfig struct {
	Port     string `mapstructure:"port"`
	UseTLS   bool   `mapstructure:"useTLS"`
	CertFile string `mapstructure:"certFile"`
	KeyFile  string `mapstructure:"keyFile"`
}

type DatabaseConfig struct {
	DSN string `mapstructure:"dsn"`
}

type RedisConfig struct {
	Addr string `mapstructure:"addr"`
}

type NATSConfig struct {
	URL string `mapstructure:"url"`
}

type StorageConfig struct {
	TempDir  string `mapstructure:"tempDir"`
	LocalDir string `mapstructure:"localDir"`
}

type BundledConfig struct {
	Enable    bool `mapstructure:"enable"`
	WorkerNum int  `mapstructure:"worker_num"`
}

type AuthConfig struct {
	Target        string `mapstructure:"target"`
	UseTLS        bool   `mapstructure:"useTLS"`
	TLSSkipVerify bool   `mapstructure:"tlsSkipVerify"`
}

type PassportConfig struct {
	Target        string `mapstructure:"target"`
	UseTLS        bool   `mapstructure:"useTLS"`
	TLSSkipVerify bool   `mapstructure:"tlsSkipVerify"`
}

type QuotaConfig struct {
	Leveling LevelingQuotaConfig `mapstructure:"leveling"`
}

type LevelingQuotaConfig struct {
	Level1   int64 `mapstructure:"level1"`
	Level10  int64 `mapstructure:"level10"`
	Level60  int64 `mapstructure:"level60"`
	Level120 int64 `mapstructure:"level120"`
}

type ModeConfig struct {
	Master  bool `mapstructure:"master"`
	Worker  bool `mapstructure:"worker"`
	Storage bool `mapstructure:"storage"`
}

type FileConfig struct {
	PreferredStorage string `mapstructure:"preferredStorage"`
	GatewayURL       string `mapstructure:"gatewayUrl"`
	AccessSecret     string `mapstructure:"accessSecret"`
}

type WOPIConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	PublicURL     string        `mapstructure:"publicUrl"`
	CollaboraURL  string        `mapstructure:"collaboraUrl"`
	TokenTTL      time.Duration `mapstructure:"tokenTtl"`
	RequireProof  bool          `mapstructure:"requireProof"`
	ProofCacheTTL time.Duration `mapstructure:"proofCacheTtl"`
}

type WebDAVConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

type PoolConfig struct {
	ID      string            `mapstructure:"id"`
	Name    string            `mapstructure:"name"`
	Default bool              `mapstructure:"default"`
	Hidden  bool              `mapstructure:"hidden"`
	Storage StoragePoolConfig `mapstructure:"storage"`
	Billing BillingPoolConfig `mapstructure:"billing"`
	Policy  PolicyPoolConfig  `mapstructure:"policy"`
}

type StoragePoolConfig struct {
	EnableSigned   bool    `mapstructure:"enableSigned"`
	EnableSsl      bool    `mapstructure:"enableSsl"`
	Endpoint       string  `mapstructure:"endpoint"`
	AccessEndpoint *string `mapstructure:"accessEndpoint"`
	Bucket         string  `mapstructure:"bucket"`
	ImageProxy     *string `mapstructure:"imageProxy"`
	AccessProxy    *string `mapstructure:"accessProxy"`
	SecretId       string  `mapstructure:"secretId"`
	SecretKey      string  `mapstructure:"secretKey"`
}

type BillingPoolConfig struct {
	CostMultiplier *float64 `mapstructure:"costMultiplier"`
}

type PolicyPoolConfig struct {
	RequirePrivilege int      `mapstructure:"requirePrivilege"`
	PublicUsable     bool     `mapstructure:"publicUsable"`
	AllowEncryption  bool     `mapstructure:"allowEncryption"`
	AcceptTypes      []string `mapstructure:"acceptTypes"`
	MaxFileSize      *int64   `mapstructure:"maxFileSize"`
	NoOptimization   bool     `mapstructure:"noOptimization"`
}

type S3Config struct {
	Endpoint  string `mapstructure:"endpoint"`
	Bucket    string `mapstructure:"bucket"`
	AccessKey string `mapstructure:"accessKey"`
	SecretKey string `mapstructure:"secretKey"`
	Secure    bool   `mapstructure:"secure"`
}

func Load(configPath string) (*Config, error) {
	viper.Reset()
	viper.SetConfigType("toml")
	if configPath != "" {
		viper.SetConfigFile(configPath)
	}

	viper.SetDefault("app.name", "DysonFileSystem")
	viper.SetDefault("http.port", "8080")
	viper.SetDefault("grpc.port", "9090")
	viper.SetDefault("grpc.useTLS", false)
	viper.SetDefault("grpc.certFile", "")
	viper.SetDefault("grpc.keyFile", "")
	viper.SetDefault("database.dsn", "")
	viper.SetDefault("redis.addr", "")
	viper.SetDefault("nats.url", "")
	viper.SetDefault("storage.tempDir", "/tmp/dyson-drive")
	viper.SetDefault("storage.localDir", "/tmp/dyson-drive/data")
	viper.SetDefault("bundled.enable", false)
	viper.SetDefault("bundled.worker_num", 1)
	viper.SetDefault("auth.target", "padlock:7003")
	viper.SetDefault("auth.useTLS", false)
	viper.SetDefault("auth.tlsSkipVerify", false)
	viper.SetDefault("passport.target", "")
	viper.SetDefault("passport.useTLS", false)
	viper.SetDefault("passport.tlsSkipVerify", false)
	viper.SetDefault("quota.leveling.level1", 512)
	viper.SetDefault("quota.leveling.level10", 1024)
	viper.SetDefault("quota.leveling.level60", 5*1024)
	viper.SetDefault("quota.leveling.level120", 10*1024)
	viper.SetDefault("mode.master", true)
	viper.SetDefault("mode.worker", false)
	viper.SetDefault("mode.storage", false)
	viper.SetDefault("files.preferredStorage", "local")
	viper.SetDefault("files.gatewayUrl", "http://localhost:8080")
	viper.SetDefault("files.accessSecret", "dyson-network-default-access-token-secret-change-in-production")
	viper.SetDefault("wopi.enabled", false)
	viper.SetDefault("wopi.publicUrl", "")
	viper.SetDefault("wopi.collaboraUrl", "")
	viper.SetDefault("wopi.tokenTtl", 15*time.Minute)
	viper.SetDefault("wopi.requireProof", false)
	viper.SetDefault("wopi.proofCacheTtl", 1*time.Hour)
	viper.SetDefault("webdav.enabled", false)
	viper.SetDefault("webdav.prefix", "/webdav")
	viper.SetDefault("s3.secure", true)
	viper.SetDefault("sentry.dsn", "")
	viper.SetDefault("sentry.tracesSampleRate", 0.01)
	viper.SetDefault("sentry.environment", "")
	viper.SetDefault("sentry.release", "")

	if configPath != "" {
		if err := viper.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	applyEnvAliases()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}

func applyEnvAliases() {
	_ = time.Second
	if v := os.Getenv("SENTRY_DSN"); v != "" {
		viper.Set("sentry.dsn", v)
	}
	if v := viper.GetString("CONFIG_MODE"); v != "" {
		switch v {
		case "master":
			viper.Set("mode.master", true)
			viper.Set("mode.worker", false)
			viper.Set("mode.storage", false)
		case "worker":
			viper.Set("mode.master", false)
			viper.Set("mode.worker", true)
			viper.Set("mode.storage", false)
		case "storage":
			viper.Set("mode.master", false)
			viper.Set("mode.worker", false)
			viper.Set("mode.storage", true)
		case "bundled":
			viper.Set("bundled.enable", true)
		}
	}
}
