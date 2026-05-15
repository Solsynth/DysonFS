package config

import (
	"fmt"
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
	Auth     AuthConfig     `mapstructure:"auth"`
	Mode     ModeConfig     `mapstructure:"mode"`
	Files    FileConfig     `mapstructure:"files"`
	S3       S3Config       `mapstructure:"s3"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
}

type HTTPConfig struct {
	Port string `mapstructure:"port"`
}

type GRPCConfig struct {
	Port string `mapstructure:"port"`
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

type AuthConfig struct {
	Target string `mapstructure:"target"`
	UseTLS bool   `mapstructure:"useTLS"`
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
	viper.SetDefault("database.dsn", "")
	viper.SetDefault("redis.addr", "")
	viper.SetDefault("nats.url", "")
	viper.SetDefault("storage.tempDir", "/tmp/dyson-drive")
	viper.SetDefault("storage.localDir", "/tmp/dyson-drive/data")
	viper.SetDefault("auth.target", "padlock:7003")
	viper.SetDefault("auth.useTLS", false)
	viper.SetDefault("mode.master", true)
	viper.SetDefault("mode.worker", false)
	viper.SetDefault("mode.storage", false)
	viper.SetDefault("files.preferredStorage", "local")
	viper.SetDefault("files.gatewayUrl", "http://localhost:8080")
	viper.SetDefault("files.accessSecret", "dyson-network-default-access-token-secret-change-in-production")
	viper.SetDefault("s3.secure", true)

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
		}
	}
}
