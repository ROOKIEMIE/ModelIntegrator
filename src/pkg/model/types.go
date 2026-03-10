package model

import "time"

type NodeType string

const (
	NodeTypeLinux NodeType = "linux"
	NodeTypeMac   NodeType = "mac"
)

type NodeStatus string

const (
	NodeStatusUnknown NodeStatus = "unknown"
	NodeStatusOnline  NodeStatus = "online"
	NodeStatusOffline NodeStatus = "offline"
)

type NodeRole string

const (
	NodeRoleMain NodeRole = "main"
	NodeRoleSub  NodeRole = "sub"
)

type NodeClassification string

const (
	NodeClassificationController NodeClassification = "controller"
	NodeClassificationWorker     NodeClassification = "worker"
	NodeClassificationAgentHost  NodeClassification = "agent-host"
	NodeClassificationHybrid     NodeClassification = "hybrid"
	NodeClassificationUnknown    NodeClassification = "unknown"
)

type CapabilityTier string

const (
	CapabilityTierUnknown CapabilityTier = "unknown"
	CapabilityTier0       CapabilityTier = "tier-0"
	CapabilityTier1       CapabilityTier = "tier-1"
	CapabilityTier2       CapabilityTier = "tier-2"
	CapabilityTier3       CapabilityTier = "tier-3"
)

type CapabilitySource string

const (
	CapabilitySourceStatic        CapabilitySource = "static"
	CapabilitySourceRuntime       CapabilitySource = "runtime"
	CapabilitySourceAgentReported CapabilitySource = "agent-reported"
	CapabilitySourceMerged        CapabilitySource = "merged"
	CapabilitySourceUnknown       CapabilitySource = "unknown"
)

type RuntimeType string

const (
	RuntimeTypeDocker    RuntimeType = "docker"
	RuntimeTypeLMStudio  RuntimeType = "lmstudio"
	RuntimeTypePortainer RuntimeType = "portainer"
	RuntimeTypeOllama    RuntimeType = "ollama"
	RuntimeTypeVLLM      RuntimeType = "vllm"
	RuntimeTypeOpenAI    RuntimeType = "openai"
)

type RuntimeStatus string

const (
	RuntimeStatusUnknown RuntimeStatus = "unknown"
	RuntimeStatusOnline  RuntimeStatus = "online"
	RuntimeStatusOffline RuntimeStatus = "offline"
)

type AgentConnectionStatus string

const (
	AgentStatusNone    AgentConnectionStatus = "none"
	AgentStatusOnline  AgentConnectionStatus = "online"
	AgentStatusOffline AgentConnectionStatus = "offline"
)

type ModelState string

const (
	ModelStateUnknown ModelState = "unknown"
	ModelStateStopped ModelState = "stopped"
	ModelStateRunning ModelState = "running"
	ModelStateLoaded  ModelState = "loaded"
	ModelStateBusy    ModelState = "busy"
	ModelStateError   ModelState = "error"
)

type Node struct {
	ID               string                `json:"id" yaml:"id"`
	Name             string                `json:"name" yaml:"name"`
	Description      string                `json:"description,omitempty" yaml:"description,omitempty"`
	Role             NodeRole              `json:"role" yaml:"role"`
	Type             NodeType              `json:"type" yaml:"type"`
	Host             string                `json:"host" yaml:"host"`
	Status           NodeStatus            `json:"status" yaml:"status"`
	Platform         PlatformInfo          `json:"platform" yaml:"platform"`
	Runtimes         []Runtime             `json:"runtimes" yaml:"runtimes"`
	LastSeenAt       time.Time             `json:"last_seen_at" yaml:"last_seen_at"`
	Metadata         interface{}           `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Classification   NodeClassification    `json:"classification,omitempty" yaml:"-"`
	CapabilityTier   CapabilityTier        `json:"capability_tier,omitempty" yaml:"-"`
	CapabilitySource CapabilitySource      `json:"capability_source,omitempty" yaml:"-"`
	AgentStatus      AgentConnectionStatus `json:"agent_status,omitempty" yaml:"-"`
	CapabilityNote   string                `json:"capability_note,omitempty" yaml:"-"`
	OperationLevel   string                `json:"operation_level,omitempty" yaml:"-"`
	Agent            *Agent                `json:"agent,omitempty" yaml:"-"`
}

type PlatformInfo struct {
	Accelerator string `json:"accelerator" yaml:"accelerator"`
	GPU         string `json:"gpu" yaml:"gpu"`
	CUDAVersion string `json:"cuda_version" yaml:"cuda_version"`
	Driver      string `json:"driver" yaml:"driver"`
}

type Runtime struct {
	ID               string            `json:"id" yaml:"id"`
	Type             RuntimeType       `json:"type" yaml:"type"`
	Endpoint         string            `json:"endpoint" yaml:"endpoint"`
	Enabled          bool              `json:"enabled" yaml:"enabled"`
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Status           RuntimeStatus     `json:"status,omitempty" yaml:"-"`
	CapabilitySource CapabilitySource  `json:"capability_source,omitempty" yaml:"-"`
	LastSeenAt       time.Time         `json:"last_seen_at,omitempty" yaml:"-"`
	CapabilityNote   string            `json:"capability_note,omitempty" yaml:"-"`
	Capabilities     []string          `json:"capabilities,omitempty" yaml:"-"`
	Actions          []string          `json:"actions,omitempty" yaml:"-"`
}

type Agent struct {
	ID                  string                `json:"id"`
	AgentID             string                `json:"agent_id,omitempty"`
	NodeID              string                `json:"node_id"`
	Name                string                `json:"name,omitempty"`
	Version             string                `json:"version,omitempty"`
	Status              AgentConnectionStatus `json:"status"`
	Address             string                `json:"address,omitempty"`
	Host                string                `json:"host,omitempty"`
	Capabilities        []string              `json:"capabilities,omitempty"`
	RuntimeCapabilities map[string][]string   `json:"runtime_capabilities,omitempty"`
	LastHeartbeatAt     time.Time             `json:"last_heartbeat_at"`
	RegisteredAt        time.Time             `json:"registered_at"`
	HeartbeatTTLSeconds int                   `json:"heartbeat_ttl_seconds,omitempty"`
	Metadata            map[string]string     `json:"metadata,omitempty"`
}

type AgentState = Agent

type AgentRegisterRequest struct {
	ID           string            `json:"id,omitempty"`
	AgentID      string            `json:"agent_id,omitempty"`
	NodeID       string            `json:"node_id"`
	Name         string            `json:"name,omitempty"`
	Version      string            `json:"version,omitempty"`
	Address      string            `json:"address,omitempty"`
	Host         string            `json:"host,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type AgentRegisterResponse struct {
	Agent                    Agent     `json:"agent"`
	ServerTime               time.Time `json:"server_time"`
	HeartbeatIntervalSeconds int       `json:"heartbeat_interval_seconds"`
}

type AgentHeartbeatRequest struct {
	NodeID   string                `json:"node_id,omitempty"`
	Status   AgentConnectionStatus `json:"status,omitempty"`
	Metadata map[string]string     `json:"metadata,omitempty"`
}

type AgentHeartbeatResponse struct {
	ID             string                `json:"id"`
	AgentID        string                `json:"agent_id,omitempty"`
	NodeID         string                `json:"node_id"`
	Status         AgentConnectionStatus `json:"status"`
	ServerTime     time.Time             `json:"server_time"`
	NextDeadlineAt time.Time             `json:"next_deadline_at"`
}

type AgentCapabilitiesReportRequest struct {
	NodeID              string              `json:"node_id"`
	Capabilities        []string            `json:"capabilities,omitempty"`
	RuntimeCapabilities map[string][]string `json:"runtime_capabilities,omitempty"`
	Metadata            map[string]string   `json:"metadata,omitempty"`
}

type AgentCapabilitiesReportResponse struct {
	Agent            Agent            `json:"agent"`
	CapabilitySource CapabilitySource `json:"capability_source"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type AgentCapabilityReportRequest = AgentCapabilitiesReportRequest

type RuntimeTemplate struct {
	ID          string            `json:"id" yaml:"id"`
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	RuntimeType RuntimeType       `json:"runtime_type" yaml:"runtime_type"`
	Image       string            `json:"image,omitempty" yaml:"image,omitempty"`
	Command     []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Env         map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Volumes     []string          `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Ports       []string          `json:"ports,omitempty" yaml:"ports,omitempty"`
	NeedsGPU    bool              `json:"needs_gpu" yaml:"needs_gpu"`
	Source      string            `json:"source,omitempty" yaml:"source,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type RuntimeTemplateValidationResult struct {
	Valid      bool            `json:"valid"`
	Errors     []string        `json:"errors,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
	Normalized RuntimeTemplate `json:"normalized"`
}

type Model struct {
	ID            string            `json:"id" yaml:"id"`
	Name          string            `json:"name" yaml:"name"`
	Provider      string            `json:"provider" yaml:"provider"`
	BackendType   RuntimeType       `json:"backend_type" yaml:"backend_type"`
	HostNodeID    string            `json:"host_node_id" yaml:"host_node_id"`
	RuntimeID     string            `json:"runtime_id" yaml:"runtime_id"`
	Endpoint      string            `json:"endpoint" yaml:"endpoint"`
	State         ModelState        `json:"state" yaml:"state"`
	ContextLength int               `json:"context_length" yaml:"context_length"`
	Metadata      map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type ActionResult struct {
	Success   bool                   `json:"success" yaml:"success"`
	Message   string                 `json:"message" yaml:"message"`
	Detail    map[string]interface{} `json:"detail,omitempty" yaml:"detail,omitempty"`
	Timestamp time.Time              `json:"timestamp" yaml:"timestamp"`
}
