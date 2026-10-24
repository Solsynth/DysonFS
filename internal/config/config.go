package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App         AppConfig         `mapstructure:"app"`
	HTTP        HTTPConfig        `mapstructure:"http"`
	GRPC        GRPCConfig        `mapstructure:"grpc"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Redis       RedisConfig       `mapstructure:"redis"`
	NATS        NATSConfig        `mapstructure:"nats"`
	Storage     StorageConfig     `mapstructure:"storage"`
	Bundled     BundledConfig     `mapstructure:"bundled"`
	Auth        AuthConfig        `mapstructure:"auth"`
	Workspace   WorkspaceConfig   `mapstructure:"workspace"`
	Quota       QuotaConfig       `mapstructure:"quota"`
	Mode        ModeConfig        `mapstructure:"mode"`
	Files       FileConfig        `mapstructure:"files"`
	WOPI        WOPIConfig        `mapstructure:"wopi"`
	WebDAV      WebDAVConfig      `mapstructure:"webdav"`
	Handoff     HandoffConfig     `mapstructure:"handoff"`
	Web         WebConfig         `mapstructure:"web"`
	Media       MediaConfig       `mapstructure:"media"`
	Pools       []PoolConfig      `mapstructure:"pools"`
	S3          S3Config          `mapstructure:"s3"`
	MasterS3    MasterS3Config    `mapstructure:"masterS3"`
	StorageNode StorageNodeConfig `mapstructure:"storageNode"`
	Sentry      SentryConfig      `mapstructure:"sentry"`
	Modules     ModulesConfig     `mapstructure:"modules"`
	Ports       map[string]string `mapstructure:"ports"`
}

// ModulesConfig selects which compiled-in modules run.
type ModulesConfig struct {
	// Enabled is an allowlist. Empty means "every compiled-in module whose
	// DefaultEnabled is set".
	Enabled []string `mapstructure:"enabled"`
	// Disabled removes modules; dependents cascade away with a warning.
	Disabled []string `mapstructure:"disabled"`
	// Settings is the decoded [modules.settings."<id>"] subtree.
	Settings map[string]map[string]any `mapstructure:"settings"`
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
	DB   int    `mapstructure:"db"`
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

// HandoffConfig points the picker's hand-off redeem route at the Stargate
// issuer. A host on another registrable domain cannot share the drive's
// SameSite=Lax session cookie, so such a host obtains a single-use hand-off
// code for its own session and hands it to the picker frame, which redeems it
// here for a bearer token. When Issuer is empty the redeem route is not
// mounted and the cookie-backed picker session behaves exactly as before.
type HandoffConfig struct {
	// Issuer is the identity host's base URL, e.g. "https://id.solian.app".
	Issuer string `mapstructure:"issuer"`
	// ClientID names this drive's registered hand-off client.
	ClientID string `mapstructure:"clientId"`
	// ClientSecret authenticates that client. It stays on the server: the
	// picker frame and the embedding host never receive it.
	ClientSecret string `mapstructure:"clientSecret"`
}

// AuthConfig points at the Stargate gRPC endpoint. When Target is empty the
// auth module synthesizes one account and skips every permission check.
type AuthConfig struct {
	Target        string `mapstructure:"target"`
	UseTLS        bool   `mapstructure:"useTLS"`
	TLSSkipVerify bool   `mapstructure:"tlsSkipVerify"`

	// Synthesized identity used when Target is empty. Stable IDs keep file
	// ownership intact across restarts.
	SyntheticAccountID   string `mapstructure:"syntheticAccountId"`
	SyntheticAccountName string `mapstructure:"syntheticAccountName"`
	SyntheticAccountNick string `mapstructure:"syntheticAccountNick"`
}

// WorkspaceConfig configures the WattEngine workspace gRPC endpoint used to
// validate workspace uploads and retrieve plan storage limits.
type WorkspaceConfig struct {
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

// WebConfig points the master at the built web console (Vite's dist output).
// When the directory holds an index.html, the master serves it as an SPA; when
// it does not, the console is simply not mounted and unknown paths keep
// returning JSON 404s.
type WebConfig struct {
	StaticDir string `mapstructure:"staticDir"`
}

// MediaConfig configures the on-the-fly media transform proxy on the content
// routes (/api/files/:id and /api/files/:id/open).
type MediaConfig struct {
	Enable             bool                         `mapstructure:"enable"`
	MaxSourceBytes     int64                        `mapstructure:"maxSourceBytes"`
	MaxWidth           int                          `mapstructure:"maxWidth"`
	MaxHeight          int                          `mapstructure:"maxHeight"`
	MaxPixels          int64                        `mapstructure:"maxPixels"`
	MaxOutputBytes     int64                        `mapstructure:"maxOutputBytes"`
	AllowedFormats     []string                     `mapstructure:"allowedFormats"`
	QualityMin         int                          `mapstructure:"qualityMin"`
	QualityMax         int                          `mapstructure:"qualityMax"`
	DefaultQuality     int                          `mapstructure:"defaultQuality"`
	EscalateToOriginal bool                         `mapstructure:"escalateToOriginal"`
	MaxConcurrent      int                          `mapstructure:"maxConcurrent"`
	CacheControlMaxAge time.Duration                `mapstructure:"cacheControlMaxAge"`
	Cache              MediaCacheConfig             `mapstructure:"cache"`
	Presets            map[string]MediaPresetConfig `mapstructure:"presets"`
}

// MediaCacheConfig configures the derivative cache tier for media transforms.
// Kind is one of "none" (default), "memory" (in-process LRU), "disk" (LRU on
// local disk) or "s3" (dedicated object-storage pool bucket).
type MediaCacheConfig struct {
	Kind     string        `mapstructure:"kind"`
	Dir      string        `mapstructure:"dir"`    // required when kind = "disk"
	PoolID   string        `mapstructure:"poolId"` // file pool id when kind = "s3"
	MaxBytes int64         `mapstructure:"maxBytes"`
	TTL      time.Duration `mapstructure:"ttl"`
}

// MediaPresetConfig is a named transform preset. Zero-value fields fall back
// to the same defaults as explicit query parameters.
type MediaPresetConfig struct {
	Width   int    `mapstructure:"width"`
	Height  int    `mapstructure:"height"`
	Fit     string `mapstructure:"fit"`
	Gravity string `mapstructure:"gravity"`
	Format  string `mapstructure:"format"`
	Quality int    `mapstructure:"quality"`
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

type MasterS3Config struct {
	Enabled bool   `mapstructure:"enabled"`
	Port    string `mapstructure:"port"`
}

type StorageNodeConfig struct {
	Port        string `mapstructure:"port"`
	MachineID   string `mapstructure:"machineId"`
	AuthToken   string `mapstructure:"authToken"`
	S3AccessKey string `mapstructure:"s3AccessKey"`
	S3SecretKey string `mapstructure:"s3SecretKey"`
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
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("nats.url", "")
	viper.SetDefault("storage.tempDir", "/tmp/dyson-drive")
	viper.SetDefault("storage.localDir", "/tmp/dyson-drive/data")
	viper.SetDefault("bundled.enable", false)
	viper.SetDefault("bundled.worker_num", 1)
	viper.SetDefault("auth.target", "stargate:9090")
	viper.SetDefault("auth.useTLS", true)
	viper.SetDefault("auth.tlsSkipVerify", true)
	viper.SetDefault("auth.syntheticAccountId", "00000000-0000-0000-0000-000000000001")
	viper.SetDefault("auth.syntheticAccountName", "dyson")
	viper.SetDefault("auth.syntheticAccountNick", "Dyson")
	viper.SetDefault("workspace.target", "")
	viper.SetDefault("workspace.useTLS", false)
	viper.SetDefault("workspace.tlsSkipVerify", false)
	viper.SetDefault("wallet.target", "")
	viper.SetDefault("wallet.useTLS", false)
	viper.SetDefault("wallet.tlsSkipVerify", false)
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
	viper.SetDefault("handoff.issuer", "")
	viper.SetDefault("handoff.clientId", "")
	viper.SetDefault("handoff.clientSecret", "")
	viper.SetDefault("webdav.enabled", false)
	viper.SetDefault("webdav.prefix", "/webdav")
	viper.SetDefault("web.staticDir", "web/dist")
	viper.SetDefault("media.enable", false)
	viper.SetDefault("media.maxSourceBytes", 10*1024*1024) // stop for more than 10MB files
	viper.SetDefault("media.maxWidth", 4096)
	viper.SetDefault("media.maxHeight", 4096)
	viper.SetDefault("media.maxPixels", 40_000_000)
	viper.SetDefault("media.maxOutputBytes", 32*1024*1024)
	viper.SetDefault("media.allowedFormats", []string{"jpeg", "png", "webp", "avif"})
	viper.SetDefault("media.qualityMin", 40)
	viper.SetDefault("media.qualityMax", 90)
	viper.SetDefault("media.defaultQuality", 80)
	viper.SetDefault("media.escalateToOriginal", true)
	viper.SetDefault("media.maxConcurrent", 0) // 0 = runtime.NumCPU()
	viper.SetDefault("media.cacheControlMaxAge", time.Hour)
	viper.SetDefault("media.cache.kind", "none")
	viper.SetDefault("media.cache.maxBytes", 10*1024*1024*1024)
	viper.SetDefault("media.cache.ttl", time.Duration(0)) // 0 = never expire by time; eviction is budget-driven
	viper.SetDefault("s3.secure", true)
	viper.SetDefault("storageNode.port", "9000")
	viper.SetDefault("storageNode.machineId", "")
	viper.SetDefault("storageNode.authToken", "")
	viper.SetDefault("storageNode.s3AccessKey", "")
	viper.SetDefault("storageNode.s3SecretKey", "")
	viper.SetDefault("masterS3.enabled", false)
	viper.SetDefault("masterS3.port", "9001")
	viper.SetDefault("sentry.dsn", "")
	viper.SetDefault("sentry.tracesSampleRate", 0.01)
	viper.SetDefault("sentry.environment", "")
	viper.SetDefault("sentry.release", "")
	viper.SetDefault("modules.enabled", []string{})
	viper.SetDefault("modules.disabled", []string{})

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
	// Module selection overrides, comma separated. They mirror Stargate so one
	// fleet convention drives both services.
	for env, key := range map[string]string{
		"DYSONFS_MODULES_ENABLED":  "modules.enabled",
		"DYSONFS_MODULES_DISABLED": "modules.disabled",
	} {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			viper.Set(key, splitList(value))
		}
	}
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

// splitList splits a comma-separated environment value, dropping blanks.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
