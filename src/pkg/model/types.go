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
	NodeRoleController NodeRole = "controller"
	NodeRoleManaged    NodeRole = "managed"
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

type ModelKind string

const (
	ModelKindUnknown   ModelKind = "unknown"
	ModelKindChat      ModelKind = "chat"
	ModelKindEmbedding ModelKind = "embedding"
	ModelKindRerank    ModelKind = "rerank"
	ModelKindUtility   ModelKind = "utility"
)

type ModelSourceType string

const (
	ModelSourceUnknown   ModelSourceType = "unknown"
	ModelSourceLocalPath ModelSourceType = "local_path"
	ModelSourceLMStudio  ModelSourceType = "lmstudio"
	ModelSourceHFRepo    ModelSourceType = "hf_repo"
	ModelSourceRemote    ModelSourceType = "remote"
)

type ModelFormat string

const (
	ModelFormatUnknown     ModelFormat = "unknown"
	ModelFormatGGUF        ModelFormat = "gguf"
	ModelFormatSafeTensors ModelFormat = "safetensors"
	ModelFormatMLX         ModelFormat = "mlx"
)

type RuntimeTemplateType string

const (
	RuntimeTemplateTypeUnknown         RuntimeTemplateType = "unknown"
	RuntimeTemplateTypeDockerCompose   RuntimeTemplateType = "docker_compose"
	RuntimeTemplateTypeSingleContainer RuntimeTemplateType = "single_container"
	RuntimeTemplateTypeProcess         RuntimeTemplateType = "process"
	RuntimeTemplateTypeAdapter         RuntimeTemplateType = "adapter"
)

type RuntimeKind string

const (
	RuntimeKindUnknown  RuntimeKind = "unknown"
	RuntimeKindTEI      RuntimeKind = "tei"
	RuntimeKindVLLM     RuntimeKind = "vllm"
	RuntimeKindLlamaCPP RuntimeKind = "llama.cpp"
	RuntimeKindLMStudio RuntimeKind = "lmstudio"
	RuntimeKindCustom   RuntimeKind = "custom"
)

type RuntimeBindingMode string

const (
	RuntimeBindingModeDedicated         RuntimeBindingMode = "dedicated"
	RuntimeBindingModeGenericInjected   RuntimeBindingMode = "generic_injected"
	RuntimeBindingModeGenericWithScript RuntimeBindingMode = "generic_with_script"
	RuntimeBindingModeCustomBundle      RuntimeBindingMode = "custom_bundle"
)

type CompatibilityStatus string

const (
	CompatibilityUnknown      CompatibilityStatus = "unknown"
	CompatibilityCompatible   CompatibilityStatus = "compatible"
	CompatibilityWarning      CompatibilityStatus = "warning"
	CompatibilityIncompatible CompatibilityStatus = "incompatible"
)

type ReadinessState string

const (
	ReadinessUnknown  ReadinessState = "unknown"
	ReadinessReady    ReadinessState = "ready"
	ReadinessNotReady ReadinessState = "not_ready"
)

type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusDispatched TaskStatus = "dispatched"
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusSuccess    TaskStatus = "success"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusTimeout    TaskStatus = "timeout"
	TaskStatusCanceled   TaskStatus = "canceled"
)

type TaskType string

const (
	TaskTypeRuntimeStart          TaskType = "runtime.start"
	TaskTypeRuntimeStop           TaskType = "runtime.stop"
	TaskTypeRuntimeRestart        TaskType = "runtime.restart"
	TaskTypeRuntimeRefresh        TaskType = "runtime.refresh"
	TaskTypeAgentRuntimeReadiness TaskType = "agent.runtime_readiness_check"
	TaskTypeAgentRuntimePrecheck  TaskType = "agent.runtime_precheck"
	TaskTypeAgentPortCheck        TaskType = "agent.port_check"
	TaskTypeAgentModelPathCheck   TaskType = "agent.model_path_check"
	TaskTypeAgentResourceSnapshot TaskType = "agent.resource_snapshot"
	TaskTypeAgentDockerInspect    TaskType = "agent.docker_inspect"
)

type TaskTargetType string

const (
	TaskTargetRuntime TaskTargetType = "runtime"
	TaskTargetNode    TaskTargetType = "node"
	TaskTargetAgent   TaskTargetType = "agent"
)

type TestRunStatus string

const (
	TestRunStatusPending TestRunStatus = "pending"
	TestRunStatusRunning TestRunStatus = "running"
	TestRunStatusSuccess TestRunStatus = "success"
	TestRunStatusFailed  TestRunStatus = "failed"
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
	ID                     string                 `json:"id" yaml:"id"`
	Name                   string                 `json:"name" yaml:"name"`
	Description            string                 `json:"description,omitempty" yaml:"description,omitempty"`
	TemplateType           RuntimeTemplateType    `json:"template_type,omitempty" yaml:"template_type,omitempty"`
	RuntimeKind            RuntimeKind            `json:"runtime_kind,omitempty" yaml:"runtime_kind,omitempty"`
	SupportedModelTypes    []ModelKind            `json:"supported_model_types,omitempty" yaml:"supported_model_types,omitempty"`
	SupportedFormats       []ModelFormat          `json:"supported_formats,omitempty" yaml:"supported_formats,omitempty"`
	Capabilities           []ModelKind            `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	ComposeRef             string                 `json:"compose_ref,omitempty" yaml:"compose_ref,omitempty"`
	ImageRef               string                 `json:"image_ref,omitempty" yaml:"image_ref,omitempty"`
	CommandTemplate        []string               `json:"command_template,omitempty" yaml:"command_template,omitempty"`
	InjectableMounts       []string               `json:"injectable_mounts,omitempty" yaml:"injectable_mounts,omitempty"`
	InjectableEnv          []string               `json:"injectable_env,omitempty" yaml:"injectable_env,omitempty"`
	CommandOverrideAllowed bool                   `json:"command_override_allowed,omitempty" yaml:"command_override_allowed,omitempty"`
	ScriptMountAllowed     bool                   `json:"script_mount_allowed,omitempty" yaml:"script_mount_allowed,omitempty"`
	Healthcheck            RuntimeHealthcheck     `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
	ExposedPorts           []string               `json:"exposed_ports,omitempty" yaml:"exposed_ports,omitempty"`
	Dedicated              bool                   `json:"dedicated,omitempty" yaml:"dedicated,omitempty"`
	Manifest               *RuntimeBundleManifest `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	RuntimeType            RuntimeType            `json:"runtime_type" yaml:"runtime_type"`
	Image                  string                 `json:"image,omitempty" yaml:"image,omitempty"`
	Command                []string               `json:"command,omitempty" yaml:"command,omitempty"`
	Env                    map[string]string      `json:"env,omitempty" yaml:"env,omitempty"`
	Volumes                []string               `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Ports                  []string               `json:"ports,omitempty" yaml:"ports,omitempty"`
	NeedsGPU               bool                   `json:"needs_gpu" yaml:"needs_gpu"`
	Source                 string                 `json:"source,omitempty" yaml:"source,omitempty"`
	Metadata               map[string]string      `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type RuntimeTemplateValidationResult struct {
	Valid      bool            `json:"valid"`
	Errors     []string        `json:"errors,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
	Normalized RuntimeTemplate `json:"normalized"`
}

type Model struct {
	ID               string            `json:"id" yaml:"id"`
	Name             string            `json:"name" yaml:"name"`
	DisplayName      string            `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	ModelType        ModelKind         `json:"model_type,omitempty" yaml:"model_type,omitempty"`
	SourceType       ModelSourceType   `json:"source_type,omitempty" yaml:"source_type,omitempty"`
	Format           ModelFormat       `json:"format,omitempty" yaml:"format,omitempty"`
	PathOrRef        string            `json:"path_or_ref,omitempty" yaml:"path_or_ref,omitempty"`
	SizeBytes        int64             `json:"size_bytes,omitempty" yaml:"size_bytes,omitempty"`
	DefaultArgs      map[string]string `json:"default_args,omitempty" yaml:"default_args,omitempty"`
	RequiresScript   bool              `json:"requires_script,omitempty" yaml:"requires_script,omitempty"`
	ScriptRef        string            `json:"script_ref,omitempty" yaml:"script_ref,omitempty"`
	Tags             []string          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Provider         string            `json:"provider" yaml:"provider"`
	BackendType      RuntimeType       `json:"backend_type" yaml:"backend_type"`
	HostNodeID       string            `json:"host_node_id" yaml:"host_node_id"`
	RuntimeID        string            `json:"runtime_id" yaml:"runtime_id"`
	Endpoint         string            `json:"endpoint" yaml:"endpoint"`
	State            ModelState        `json:"state" yaml:"state"`
	DesiredState     string            `json:"desired_state,omitempty" yaml:"desired_state,omitempty"`
	ObservedState    string            `json:"observed_state,omitempty" yaml:"observed_state,omitempty"`
	Readiness        ReadinessState    `json:"readiness,omitempty" yaml:"readiness,omitempty"`
	HealthMessage    string            `json:"health_message,omitempty" yaml:"health_message,omitempty"`
	LastReconciledAt time.Time         `json:"last_reconciled_at,omitempty" yaml:"last_reconciled_at,omitempty"`
	ContextLength    int               `json:"context_length" yaml:"context_length"`
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type RuntimeHealthcheck struct {
	Path            string `json:"path,omitempty" yaml:"path,omitempty"`
	Method          string `json:"method,omitempty" yaml:"method,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty" yaml:"interval_seconds,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	SuccessCodes    []int  `json:"success_codes,omitempty" yaml:"success_codes,omitempty"`
}

type RuntimeBundleManifest struct {
	ID                     string              `json:"id" yaml:"id"`
	TemplateID             string              `json:"template_id,omitempty" yaml:"template_id,omitempty"`
	ManifestVersion        string              `json:"manifest_version" yaml:"manifest_version"`
	TemplateType           RuntimeTemplateType `json:"template_type" yaml:"template_type"`
	RuntimeKind            RuntimeKind         `json:"runtime_kind" yaml:"runtime_kind"`
	SupportedModelTypes    []ModelKind         `json:"supported_model_types,omitempty" yaml:"supported_model_types,omitempty"`
	SupportedFormats       []ModelFormat       `json:"supported_formats,omitempty" yaml:"supported_formats,omitempty"`
	Capabilities           []ModelKind         `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	MountPoints            []string            `json:"mount_points,omitempty" yaml:"mount_points,omitempty"`
	RequiredEnv            []string            `json:"required_env,omitempty" yaml:"required_env,omitempty"`
	OptionalEnv            []string            `json:"optional_env,omitempty" yaml:"optional_env,omitempty"`
	CommandOverrideAllowed bool                `json:"command_override_allowed,omitempty" yaml:"command_override_allowed,omitempty"`
	ScriptMountAllowed     bool                `json:"script_mount_allowed,omitempty" yaml:"script_mount_allowed,omitempty"`
	ModelInjectionMode     RuntimeBindingMode  `json:"model_injection_mode,omitempty" yaml:"model_injection_mode,omitempty"`
	Healthcheck            RuntimeHealthcheck  `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
	ExposedPorts           []string            `json:"exposed_ports,omitempty" yaml:"exposed_ports,omitempty"`
	Notes                  string              `json:"notes,omitempty" yaml:"notes,omitempty"`
	Metadata               map[string]string   `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type RuntimeBinding struct {
	ID                   string              `json:"id"`
	ModelID              string              `json:"model_id"`
	TemplateID           string              `json:"template_id"`
	BindingMode          RuntimeBindingMode  `json:"binding_mode"`
	NodeSelector         map[string]string   `json:"node_selector,omitempty"`
	PreferredNode        string              `json:"preferred_node,omitempty"`
	MountRules           []string            `json:"mount_rules,omitempty"`
	EnvOverrides         map[string]string   `json:"env_overrides,omitempty"`
	CommandOverride      []string            `json:"command_override,omitempty"`
	ScriptRef            string              `json:"script_ref,omitempty"`
	CompatibilityStatus  CompatibilityStatus `json:"compatibility_status,omitempty"`
	CompatibilityMessage string              `json:"compatibility_message,omitempty"`
	Enabled              bool                `json:"enabled"`
	ManifestID           string              `json:"manifest_id,omitempty"`
	Metadata             map[string]string   `json:"metadata,omitempty"`
	CreatedAt            time.Time           `json:"created_at,omitempty"`
	UpdatedAt            time.Time           `json:"updated_at,omitempty"`
}

type RuntimeInstance struct {
	ID               string            `json:"id"`
	ModelID          string            `json:"model_id"`
	TemplateID       string            `json:"template_id"`
	BindingID        string            `json:"binding_id"`
	NodeID           string            `json:"node_id"`
	DesiredState     string            `json:"desired_state,omitempty"`
	ObservedState    string            `json:"observed_state,omitempty"`
	Readiness        ReadinessState    `json:"readiness,omitempty"`
	HealthMessage    string            `json:"health_message,omitempty"`
	DriftReason      string            `json:"drift_reason,omitempty"`
	Endpoint         string            `json:"endpoint,omitempty"`
	LaunchedCommand  []string          `json:"launched_command,omitempty"`
	MountedPaths     []string          `json:"mounted_paths,omitempty"`
	InjectedEnv      map[string]string `json:"injected_env,omitempty"`
	ScriptUsed       string            `json:"script_used,omitempty"`
	LastReconciledAt time.Time         `json:"last_reconciled_at,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        time.Time         `json:"created_at,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at,omitempty"`
}

type LaunchProfile = RuntimeBinding
type RuntimeDeployment = RuntimeInstance

type Task struct {
	ID              string                 `json:"id"`
	Type            TaskType               `json:"type"`
	TargetType      TaskTargetType         `json:"target_type"`
	TargetID        string                 `json:"target_id"`
	AssignedAgentID string                 `json:"assigned_agent_id,omitempty"`
	WorkerID        string                 `json:"worker_id,omitempty"`
	Status          TaskStatus             `json:"status"`
	Progress        int                    `json:"progress"`
	Message         string                 `json:"message,omitempty"`
	Detail          map[string]interface{} `json:"detail,omitempty"`
	Payload         map[string]interface{} `json:"payload,omitempty"`
	Error           string                 `json:"error,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	AcceptedAt      time.Time              `json:"accepted_at,omitempty"`
	StartedAt       time.Time              `json:"started_at,omitempty"`
	FinishedAt      time.Time              `json:"finished_at,omitempty"`
}

type TestRun struct {
	TestRunID   string        `json:"test_run_id"`
	Scenario    string        `json:"scenario"`
	Status      TestRunStatus `json:"status"`
	StartedAt   time.Time     `json:"started_at,omitempty"`
	FinishedAt  time.Time     `json:"finished_at,omitempty"`
	LogPath     string        `json:"log_path,omitempty"`
	Summary     string        `json:"summary,omitempty"`
	TriggeredBy string        `json:"triggered_by,omitempty"`
	Error       string        `json:"error,omitempty"`
	CreatedAt   time.Time     `json:"created_at,omitempty"`
}

type ActionResult struct {
	Success   bool                   `json:"success" yaml:"success"`
	Message   string                 `json:"message" yaml:"message"`
	Detail    map[string]interface{} `json:"detail,omitempty" yaml:"detail,omitempty"`
	Timestamp time.Time              `json:"timestamp" yaml:"timestamp"`
}
