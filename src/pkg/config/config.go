package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"

	"ModelIntegrator/src/pkg/model"
)

const DefaultConfigPath = "./resource/config/config.example.yaml"

type Config struct {
	SourcePath   string             `yaml:"-"`
	Server       ServerConfig       `yaml:"server"`
	Log          LogConfig          `yaml:"log"`
	Storage      StorageConfig      `yaml:"storage"`
	Auth         AuthConfig         `yaml:"auth"`
	Integrations IntegrationsConfig `yaml:"integrations"`
	Nodes        []model.Node       `yaml:"nodes"`
	Models       []model.Model      `yaml:"models"`
}

type ServerConfig struct {
	Address                string `yaml:"address"`
	StaticDir              string `yaml:"static_dir"`
	ReadTimeoutSeconds     int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds    int    `yaml:"write_timeout_seconds"`
	ShutdownTimeoutSeconds int    `yaml:"shutdown_timeout_seconds"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type StorageConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
}

type AuthConfig struct {
	Token string `yaml:"token"`
}

type IntegrationsConfig struct {
	LMStudio  EndpointConfig `yaml:"lmstudio"`
	Docker    EndpointConfig `yaml:"docker"`
	Portainer EndpointConfig `yaml:"portainer"`
}

type EndpointConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
	Token    string `yaml:"token"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Address:                ":8080",
			StaticDir:              "./resource/web",
			ReadTimeoutSeconds:     15,
			WriteTimeoutSeconds:    30,
			ShutdownTimeoutSeconds: 10,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Storage: StorageConfig{
			SQLitePath: "./resource/config/modelintegrator.db",
		},
		Nodes:  []model.Node{},
		Models: []model.Model{},
	}
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	resolvedPath := resolveConfigPath(path)

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败 (%s): %w", resolvedPath, err)
	}

	if err := yaml.Unmarshal(content, cfg); err != nil {
		return nil, fmt.Errorf("解析 YAML 配置失败 (%s): %w", resolvedPath, err)
	}

	cfg.SourcePath = resolvedPath
	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func resolveConfigPath(path string) string {
	if path != "" {
		return path
	}
	if envPath := os.Getenv("MCP_CONFIG"); envPath != "" {
		return envPath
	}
	return DefaultConfigPath
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MCP_SERVER_ADDRESS"); v != "" {
		cfg.Server.Address = v
	}
	if v := os.Getenv("MCP_WEB_STATIC_DIR"); v != "" {
		cfg.Server.StaticDir = v
	}
	if v := os.Getenv("MCP_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("MCP_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("MCP_SQLITE_PATH"); v != "" {
		cfg.Storage.SQLitePath = v
	}
	if v := os.Getenv("MCP_AUTH_TOKEN"); v != "" {
		cfg.Auth.Token = v
	}
	if v := os.Getenv("MCP_LMSTUDIO_ENDPOINT"); v != "" {
		cfg.Integrations.LMStudio.Endpoint = v
	}
	if v := os.Getenv("MCP_LMSTUDIO_TOKEN"); v != "" {
		cfg.Integrations.LMStudio.Token = v
	}
	if v := os.Getenv("MCP_DOCKER_ENDPOINT"); v != "" {
		cfg.Integrations.Docker.Endpoint = v
	}
	if v := os.Getenv("MCP_DOCKER_TOKEN"); v != "" {
		cfg.Integrations.Docker.Token = v
	}
	if v := os.Getenv("MCP_PORTAINER_ENDPOINT"); v != "" {
		cfg.Integrations.Portainer.Endpoint = v
	}
	if v := os.Getenv("MCP_PORTAINER_TOKEN"); v != "" {
		cfg.Integrations.Portainer.Token = v
	}
	if v := os.Getenv("MCP_SERVER_READ_TIMEOUT_SECONDS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Server.ReadTimeoutSeconds = parsed
		}
	}
	if v := os.Getenv("MCP_SERVER_WRITE_TIMEOUT_SECONDS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Server.WriteTimeoutSeconds = parsed
		}
	}
}

func (c *Config) Validate() error {
	if c.Server.Address == "" {
		return fmt.Errorf("配置校验失败: server.address 不能为空")
	}
	if c.Server.StaticDir == "" {
		c.Server.StaticDir = "./resource/web"
	}
	if c.Server.ReadTimeoutSeconds <= 0 {
		c.Server.ReadTimeoutSeconds = 15
	}
	if c.Server.WriteTimeoutSeconds <= 0 {
		c.Server.WriteTimeoutSeconds = 30
	}
	if c.Server.ShutdownTimeoutSeconds <= 0 {
		c.Server.ShutdownTimeoutSeconds = 10
	}
	if c.Storage.SQLitePath == "" {
		c.Storage.SQLitePath = "./resource/config/modelintegrator.db"
	}
	return nil
}
