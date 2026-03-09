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

type RuntimeType string

const (
	RuntimeTypeDocker    RuntimeType = "docker"
	RuntimeTypeLMStudio  RuntimeType = "lmstudio"
	RuntimeTypePortainer RuntimeType = "portainer"
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
	ID          string       `json:"id" yaml:"id"`
	Name        string       `json:"name" yaml:"name"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Role        NodeRole     `json:"role" yaml:"role"`
	Type        NodeType     `json:"type" yaml:"type"`
	Host        string       `json:"host" yaml:"host"`
	Status      NodeStatus   `json:"status" yaml:"status"`
	Platform    PlatformInfo `json:"platform" yaml:"platform"`
	Runtimes    []Runtime    `json:"runtimes" yaml:"runtimes"`
	LastSeenAt  time.Time    `json:"last_seen_at" yaml:"last_seen_at"`
	Metadata    interface{}  `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type PlatformInfo struct {
	Accelerator string `json:"accelerator" yaml:"accelerator"`
	GPU         string `json:"gpu" yaml:"gpu"`
	CUDAVersion string `json:"cuda_version" yaml:"cuda_version"`
	Driver      string `json:"driver" yaml:"driver"`
}

type Runtime struct {
	ID       string            `json:"id" yaml:"id"`
	Type     RuntimeType       `json:"type" yaml:"type"`
	Endpoint string            `json:"endpoint" yaml:"endpoint"`
	Enabled  bool              `json:"enabled" yaml:"enabled"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
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
