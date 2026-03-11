package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"model-control-plane/src/pkg/model"
)

const DefaultConfigPath = "./resources/config/config.example.yaml"

type Config struct {
	SourcePath       string                  `yaml:"-"`
	Server           ServerConfig            `yaml:"server"`
	Log              LogConfig               `yaml:"log"`
	Storage          StorageConfig           `yaml:"storage"`
	Testing          TestingConfig           `yaml:"testing"`
	Auth             AuthConfig              `yaml:"auth"`
	Integrations     IntegrationsConfig      `yaml:"integrations"`
	Nodes            []model.Node            `yaml:"nodes"`
	Models           []model.Model           `yaml:"models"`
	RuntimeTemplates []model.RuntimeTemplate `yaml:"runtime_templates"`
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
	SQLitePath   string `yaml:"sqlite_path"`
	ModelRootDir string `yaml:"model_root_dir"`
}

type TestingConfig struct {
	LogRootDir string `yaml:"log_root_dir"`
}

type AuthConfig struct {
	Token string `yaml:"token"`
}

type IntegrationsConfig struct {
	LMStudio  LMStudioConfig `yaml:"lmstudio"`
	Docker    EndpointConfig `yaml:"docker"`
	Portainer EndpointConfig `yaml:"portainer"`
}

type LMStudioConfig struct {
	EndpointConfig      `yaml:",inline"`
	CacheEnabled        bool `yaml:"cache_enabled"`
	CacheRefreshSeconds int  `yaml:"cache_refresh_seconds"`
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
			StaticDir:              "./resources/web",
			ReadTimeoutSeconds:     15,
			WriteTimeoutSeconds:    30,
			ShutdownTimeoutSeconds: 10,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Storage: StorageConfig{
			SQLitePath:   "./resources/config/controller.db",
			ModelRootDir: "./resources/models",
		},
		Testing: TestingConfig{
			LogRootDir: "./testsystem/logs",
		},
		Integrations: IntegrationsConfig{
			LMStudio: LMStudioConfig{
				EndpointConfig: EndpointConfig{
					Enabled: true,
				},
				CacheEnabled:        true,
				CacheRefreshSeconds: 30,
			},
		},
		Nodes:            []model.Node{},
		Models:           []model.Model{},
		RuntimeTemplates: []model.RuntimeTemplate{},
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
	cfg.normalizeNodes()

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
	if v := os.Getenv("MCP_MODEL_ROOT_DIR"); v != "" {
		cfg.Storage.ModelRootDir = v
	}
	if v := os.Getenv("MCP_TEST_LOG_ROOT_DIR"); v != "" {
		cfg.Testing.LogRootDir = v
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
	if v := os.Getenv("MCP_LMSTUDIO_CACHE_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Integrations.LMStudio.CacheEnabled = parsed
		}
	}
	if v := os.Getenv("MCP_LMSTUDIO_CACHE_REFRESH_SECONDS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Integrations.LMStudio.CacheRefreshSeconds = parsed
		}
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
		c.Server.StaticDir = "./resources/web"
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
		c.Storage.SQLitePath = "./resources/config/controller.db"
	}
	if c.Storage.ModelRootDir == "" {
		c.Storage.ModelRootDir = "./resources/models"
	}
	if c.Testing.LogRootDir == "" {
		c.Testing.LogRootDir = "./testsystem/logs"
	}
	if c.Integrations.LMStudio.CacheRefreshSeconds <= 0 {
		c.Integrations.LMStudio.CacheRefreshSeconds = 30
	}
	return nil
}

func (c *Config) normalizeNodes() {
	nodeIDMap := make(map[string]string)
	runtimeIDMap := make(map[string]string)

	for i := range c.Nodes {
		node := &c.Nodes[i]
		originNodeID := strings.TrimSpace(node.ID)
		originNodeName := strings.TrimSpace(node.Name)
		description := strings.TrimSpace(node.Description)
		if description == "" {
			description = strings.TrimSpace(node.Name)
		}
		if description == "" {
			description = fmt.Sprintf("Node %d", i+1)
		}

		if i == 0 {
			node.Role = model.NodeRoleMain
			node.Name = "Main"
			node.ID = "node-main"
		} else {
			node.Role = model.NodeRoleSub
			node.Name = fmt.Sprintf("Sub%d", i)
			node.ID = fmt.Sprintf("node-sub-%d", i)
		}
		node.Description = description
		if originNodeID != "" {
			nodeIDMap[originNodeID] = node.ID
		}
		if originNodeName != "" {
			nodeIDMap[originNodeName] = node.ID
		}
		nodeIDMap[node.ID] = node.ID

		for j := range node.Runtimes {
			rt := &node.Runtimes[j]
			originRuntimeID := strings.TrimSpace(rt.ID)
			if rt.ID == "" {
				rt.ID = fmt.Sprintf("%s-%s-%d", node.ID, rt.Type, j+1)
			}
			if originRuntimeID != "" {
				runtimeIDMap[originRuntimeID] = rt.ID
			}
			runtimeIDMap[rt.ID] = rt.ID
		}
	}

	for i := range c.Models {
		if mappedNodeID, ok := nodeIDMap[c.Models[i].HostNodeID]; ok {
			c.Models[i].HostNodeID = mappedNodeID
		}
		if mappedRuntimeID, ok := runtimeIDMap[c.Models[i].RuntimeID]; ok {
			c.Models[i].RuntimeID = mappedRuntimeID
		}
	}
}
