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

type PrecheckOverallStatus string

const (
	PrecheckStatusUnknown PrecheckOverallStatus = "unknown"
	PrecheckStatusOK      PrecheckOverallStatus = "ok"
	PrecheckStatusWarning PrecheckOverallStatus = "warning"
	PrecheckStatusFailed  PrecheckOverallStatus = "failed"
)

type PrecheckCheckStatus string

const (
	PrecheckCheckPass    PrecheckCheckStatus = "pass"
	PrecheckCheckWarning PrecheckCheckStatus = "warning"
	PrecheckCheckFailed  PrecheckCheckStatus = "failed"
	PrecheckCheckSkipped PrecheckCheckStatus = "skipped"
)

type PrecheckReasonCode string

const (
	PrecheckReasonModelPathMissing          PrecheckReasonCode = "model_path_missing"
	PrecheckReasonRequiredEnvMissing        PrecheckReasonCode = "required_env_missing"
	PrecheckReasonScriptMissing             PrecheckReasonCode = "script_missing"
	PrecheckReasonPortConflict              PrecheckReasonCode = "port_conflict"
	PrecheckReasonModelTypeMismatch         PrecheckReasonCode = "model_type_mismatch"
	PrecheckReasonModelFormatMismatch       PrecheckReasonCode = "model_format_mismatch"
	PrecheckReasonCommandOverrideNotAllowed PrecheckReasonCode = "command_override_not_allowed"
	PrecheckReasonScriptMountNotAllowed     PrecheckReasonCode = "script_mount_not_allowed"
	PrecheckReasonManifestInvalid           PrecheckReasonCode = "manifest_invalid"
	PrecheckReasonBindingInvalid            PrecheckReasonCode = "binding_invalid"
)

type RuntimePrecheckReason struct {
	Code     PrecheckReasonCode     `json:"code"`
	Message  string                 `json:"message"`
	Blocking bool                   `json:"blocking"`
	Detail   map[string]interface{} `json:"detail,omitempty"`
}

type RuntimePrecheckCheckResult struct {
	Name     string                 `json:"name"`
	Status   PrecheckCheckStatus    `json:"status"`
	Blocking bool                   `json:"blocking"`
	Message  string                 `json:"message,omitempty"`
	Detail   map[string]interface{} `json:"detail,omitempty"`
}

type RuntimePrecheckCompatibilityResult struct {
	ModelType              string   `json:"model_type,omitempty"`
	ModelFormat            string   `json:"model_format,omitempty"`
	SupportedModelTypes    []string `json:"supported_model_types,omitempty"`
	SupportedFormats       []string `json:"supported_formats,omitempty"`
	ModelTypeMatched       bool     `json:"model_type_matched"`
	ModelFormatMatched     bool     `json:"model_format_matched"`
	CommandOverrideAllowed *bool    `json:"command_override_allowed,omitempty"`
	ScriptMountAllowed     *bool    `json:"script_mount_allowed,omitempty"`
}

type RuntimePrecheckResult struct {
	OverallStatus       PrecheckOverallStatus              `json:"overall_status"`
	Gating              bool                               `json:"gating"`
	Reasons             []RuntimePrecheckReason            `json:"reasons,omitempty"`
	Checks              []RuntimePrecheckCheckResult       `json:"checks,omitempty"`
	ResolvedMounts      []string                           `json:"resolved_mounts,omitempty"`
	ResolvedEnv         map[string]string                  `json:"resolved_env,omitempty"`
	ResolvedScript      string                             `json:"resolved_script,omitempty"`
	ResolvedPorts       []string                           `json:"resolved_ports,omitempty"`
	CompatibilityResult RuntimePrecheckCompatibilityResult `json:"compatibility_result"`
	StartedAt           time.Time                          `json:"started_at,omitempty"`
	FinishedAt          time.Time                          `json:"finished_at,omitempty"`
}

type RuntimeSignalSource string

const (
	RuntimeSignalSourceUnknown    RuntimeSignalSource = "unknown"
	RuntimeSignalSourceAgent      RuntimeSignalSource = "agent"
	RuntimeSignalSourceController RuntimeSignalSource = "controller"
)

type RuntimeConflictStatus string

const (
	RuntimeConflictStatusUnknown RuntimeConflictStatus = "unknown"
	RuntimeConflictStatusClear   RuntimeConflictStatus = "clear"
	RuntimeConflictStatusWarning RuntimeConflictStatus = "warning"
	RuntimeConflictStatusBlocked RuntimeConflictStatus = "blocked"
)

type RuntimeGatingStatus string

const (
	RuntimeGatingStatusUnknown  RuntimeGatingStatus = "unknown"
	RuntimeGatingStatusAllowed  RuntimeGatingStatus = "allowed"
	RuntimeGatingStatusBlocked  RuntimeGatingStatus = "blocked"
	RuntimeGatingStatusDeferred RuntimeGatingStatus = "deferred"
)

type RuntimeLifecycleAction string

const (
	RuntimeLifecycleActionNone      RuntimeLifecycleAction = "none"
	RuntimeLifecycleActionLoad      RuntimeLifecycleAction = "load"
	RuntimeLifecycleActionStart     RuntimeLifecycleAction = "start"
	RuntimeLifecycleActionLoadStart RuntimeLifecycleAction = "load_start"
	RuntimeLifecycleActionStop      RuntimeLifecycleAction = "stop"
	RuntimeLifecycleActionUnload    RuntimeLifecycleAction = "unload"
	RuntimeLifecycleActionRestart   RuntimeLifecycleAction = "restart"
	RuntimeLifecycleActionRefresh   RuntimeLifecycleAction = "refresh"
	RuntimeLifecycleActionRelease   RuntimeLifecycleAction = "release_slot"
)

type RuntimeLifecyclePlanStatus string

const (
	RuntimeLifecyclePlanStatusUnknown   RuntimeLifecyclePlanStatus = "unknown"
	RuntimeLifecyclePlanStatusPlanned   RuntimeLifecyclePlanStatus = "planned"
	RuntimeLifecyclePlanStatusExecuting RuntimeLifecyclePlanStatus = "executing"
	RuntimeLifecyclePlanStatusCompleted RuntimeLifecyclePlanStatus = "completed"
	RuntimeLifecyclePlanStatusBlocked   RuntimeLifecyclePlanStatus = "blocked"
	RuntimeLifecyclePlanStatusDeferred  RuntimeLifecyclePlanStatus = "deferred"
	RuntimeLifecyclePlanStatusFailed    RuntimeLifecyclePlanStatus = "failed"
	RuntimeLifecyclePlanStatusRejected  RuntimeLifecyclePlanStatus = "rejected"
)

type RuntimeConditionReason struct {
	Code              string                 `json:"code,omitempty"`
	Message           string                 `json:"message,omitempty"`
	Blocking          bool                   `json:"blocking"`
	Source            RuntimeSignalSource    `json:"source,omitempty"`
	RelatedInstanceID string                 `json:"related_instance_id,omitempty"`
	RelatedNodeID     string                 `json:"related_node_id,omitempty"`
	RelatedBindingID  string                 `json:"related_binding_id,omitempty"`
	Detail            map[string]interface{} `json:"detail,omitempty"`
}

type RuntimePrecheckSummary struct {
	Status      PrecheckOverallStatus    `json:"status,omitempty"`
	Gating      bool                     `json:"gating"`
	Reasons     []RuntimeConditionReason `json:"reasons,omitempty"`
	GeneratedAt time.Time                `json:"generated_at,omitempty"`
	Source      RuntimeSignalSource      `json:"source,omitempty"`
}

type RuntimeConflictSummary struct {
	Status      RuntimeConflictStatus    `json:"status,omitempty"`
	Blocking    bool                     `json:"blocking"`
	Reasons     []RuntimeConditionReason `json:"reasons,omitempty"`
	GeneratedAt time.Time                `json:"generated_at,omitempty"`
	Source      RuntimeSignalSource      `json:"source,omitempty"`
}

type RuntimeGatingSummary struct {
	Status       RuntimeGatingStatus      `json:"status,omitempty"`
	Allowed      bool                     `json:"allowed"`
	Reasons      []RuntimeConditionReason `json:"reasons,omitempty"`
	GeneratedAt  time.Time                `json:"generated_at,omitempty"`
	Source       RuntimeSignalSource      `json:"source,omitempty"`
	TargetAction RuntimeLifecycleAction   `json:"target_action,omitempty"`
}

type RuntimeLifecyclePlanSummary struct {
	PlanID             string                     `json:"plan_id,omitempty"`
	Action             RuntimeLifecycleAction     `json:"action,omitempty"`
	Status             RuntimeLifecyclePlanStatus `json:"status,omitempty"`
	Message            string                     `json:"message,omitempty"`
	ReasonCodes        []string                   `json:"reason_codes,omitempty"`
	BlockedReasonCodes []string                   `json:"blocked_reason_codes,omitempty"`
	ReleaseTargets     []string                   `json:"release_targets,omitempty"`
	RequestedTaskType  TaskType                   `json:"requested_task_type,omitempty"`
	TriggeredBy        string                     `json:"triggered_by,omitempty"`
	Source             RuntimeSignalSource        `json:"source,omitempty"`
	RelatedTaskID      string                     `json:"related_task_id,omitempty"`
	GeneratedAt        time.Time                  `json:"generated_at,omitempty"`
	UpdatedAt          time.Time                  `json:"updated_at,omitempty"`
}

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
	TaskTypeAgentDockerStart      TaskType = "agent.docker_start_container"
	TaskTypeAgentDockerStop       TaskType = "agent.docker_stop_container"
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

type RuntimeInstanceAgentTaskSummary struct {
	TaskID          string     `json:"task_id,omitempty"`
	TaskType        TaskType   `json:"task_type,omitempty"`
	TaskStatus      TaskStatus `json:"task_status,omitempty"`
	Message         string     `json:"message,omitempty"`
	WorkerID        string     `json:"worker_id,omitempty"`
	AssignedAgentID string     `json:"assigned_agent_id,omitempty"`
	TriggeredBy     string     `json:"triggered_by,omitempty"`
	FinishedAt      time.Time  `json:"finished_at,omitempty"`
}

type RuntimeInstance struct {
	ID                  string                           `json:"id"`
	ModelID             string                           `json:"model_id"`
	TemplateID          string                           `json:"template_id"`
	BindingID           string                           `json:"binding_id"`
	BindingMode         RuntimeBindingMode               `json:"binding_mode,omitempty"`
	ManifestID          string                           `json:"manifest_id,omitempty"`
	NodeID              string                           `json:"node_id"`
	DesiredState        string                           `json:"desired_state,omitempty"`
	ObservedState       string                           `json:"observed_state,omitempty"`
	Readiness           ReadinessState                   `json:"readiness,omitempty"`
	HealthMessage       string                           `json:"health_message,omitempty"`
	DriftReason         string                           `json:"drift_reason,omitempty"`
	Endpoint            string                           `json:"endpoint,omitempty"`
	LaunchedCommand     []string                         `json:"launched_command,omitempty"`
	MountedPaths        []string                         `json:"mounted_paths,omitempty"`
	InjectedEnv         map[string]string                `json:"injected_env,omitempty"`
	ScriptUsed          string                           `json:"script_used,omitempty"`
	ResolvedMounts      []string                         `json:"resolved_mounts,omitempty"`
	ResolvedPorts       []string                         `json:"resolved_ports,omitempty"`
	ResolvedScript      string                           `json:"resolved_script,omitempty"`
	LastReconciledAt    time.Time                        `json:"last_reconciled_at,omitempty"`
	Metadata            map[string]string                `json:"metadata,omitempty"`
	LastAgentTask       *RuntimeInstanceAgentTaskSummary `json:"last_agent_task,omitempty"`
	PrecheckStatus      PrecheckOverallStatus            `json:"precheck_status,omitempty"`
	PrecheckGating      bool                             `json:"precheck_gating"`
	PrecheckReasons     []string                         `json:"precheck_reasons,omitempty"`
	PrecheckSummary     *RuntimePrecheckSummary          `json:"precheck_summary,omitempty"`
	ConflictStatus      RuntimeConflictStatus            `json:"conflict_status,omitempty"`
	ConflictBlocking    bool                             `json:"conflict_blocking"`
	ConflictReasons     []string                         `json:"conflict_reasons,omitempty"`
	ConflictSource      RuntimeSignalSource              `json:"conflict_source,omitempty"`
	ConflictGeneratedAt time.Time                        `json:"conflict_generated_at,omitempty"`
	ConflictSummary     *RuntimeConflictSummary          `json:"conflict_summary,omitempty"`
	GatingStatus        RuntimeGatingStatus              `json:"gating_status,omitempty"`
	GatingAllowed       bool                             `json:"gating_allowed"`
	GatingReasons       []string                         `json:"gating_reasons,omitempty"`
	GatingSource        RuntimeSignalSource              `json:"gating_source,omitempty"`
	GatingGeneratedAt   time.Time                        `json:"gating_generated_at,omitempty"`
	GatingSummary       *RuntimeGatingSummary            `json:"gating_summary,omitempty"`
	LastPlanAction      RuntimeLifecycleAction           `json:"last_plan_action,omitempty"`
	LastPlanStatus      RuntimeLifecyclePlanStatus       `json:"last_plan_status,omitempty"`
	LastPlanReason      string                           `json:"last_plan_reason,omitempty"`
	LastPlanGeneratedAt time.Time                        `json:"last_plan_generated_at,omitempty"`
	LastPlanDetail      map[string]interface{}           `json:"last_plan_detail,omitempty"`
	LastLifecyclePlan   *RuntimeLifecyclePlanSummary     `json:"last_lifecycle_plan,omitempty"`
	LastPrecheckTaskID  string                           `json:"last_precheck_task_id,omitempty"`
	LastPrecheckAt      time.Time                        `json:"last_precheck_at,omitempty"`
	PrecheckResult      *RuntimePrecheckResult           `json:"precheck_result,omitempty"`
	CreatedAt           time.Time                        `json:"created_at,omitempty"`
	UpdatedAt           time.Time                        `json:"updated_at,omitempty"`
}

// RuntimeInstanceReconcileSummary captures the latest instance-first reconcile
// interpretation from controller, including precheck/drift/readiness explanation.
type RuntimeInstanceReconcileSummary struct {
	RuntimeInstanceID string                           `json:"runtime_instance_id"`
	ModelID           string                           `json:"model_id,omitempty"`
	NodeID            string                           `json:"node_id,omitempty"`
	RuntimeTemplateID string                           `json:"runtime_template_id,omitempty"`
	BindingMode       RuntimeBindingMode               `json:"binding_mode,omitempty"`
	ManifestID        string                           `json:"manifest_id,omitempty"`
	DesiredState      string                           `json:"desired_state,omitempty"`
	ObservedState     string                           `json:"observed_state,omitempty"`
	Readiness         ReadinessState                   `json:"readiness,omitempty"`
	HealthMessage     string                           `json:"health_message,omitempty"`
	DriftReason       string                           `json:"drift_reason,omitempty"`
	PrecheckStatus    PrecheckOverallStatus            `json:"precheck_status,omitempty"`
	PrecheckGating    bool                             `json:"precheck_gating"`
	PrecheckReasons   []string                         `json:"precheck_reasons,omitempty"`
	ConflictStatus    RuntimeConflictStatus            `json:"conflict_status,omitempty"`
	ConflictBlocking  bool                             `json:"conflict_blocking"`
	ConflictReasons   []string                         `json:"conflict_reasons,omitempty"`
	GatingStatus      RuntimeGatingStatus              `json:"gating_status,omitempty"`
	GatingAllowed     bool                             `json:"gating_allowed"`
	GatingReasons     []string                         `json:"gating_reasons,omitempty"`
	PlannedAction     RuntimeLifecycleAction           `json:"planned_action,omitempty"`
	AgentStatus       string                           `json:"agent_status,omitempty"`
	AgentOnline       bool                             `json:"agent_online"`
	ObservationStale  bool                             `json:"observation_stale"`
	LastObservationAt time.Time                        `json:"last_observation_at,omitempty"`
	LastReconciledAt  time.Time                        `json:"last_reconciled_at,omitempty"`
	ReconcileReasons  []string                         `json:"reconcile_reasons,omitempty"`
	Trigger           string                           `json:"trigger,omitempty"`
	LastAgentTask     *RuntimeInstanceAgentTaskSummary `json:"last_agent_task,omitempty"`
	LastLifecyclePlan *RuntimeLifecyclePlanSummary     `json:"last_lifecycle_plan,omitempty"`
}

type LaunchProfile = RuntimeBinding
type RuntimeDeployment = RuntimeInstance

type AgentTaskResolvedContext struct {
	TaskScope              string              `json:"task_scope,omitempty"`
	RuntimeInstanceID      string              `json:"runtime_instance_id,omitempty"`
	RuntimeBindingID       string              `json:"runtime_binding_id,omitempty"`
	RuntimeTemplateID      string              `json:"runtime_template_id,omitempty"`
	ManifestID             string              `json:"manifest_id,omitempty"`
	NodeID                 string              `json:"node_id,omitempty"`
	ModelID                string              `json:"model_id,omitempty"`
	BindingMode            RuntimeBindingMode  `json:"binding_mode,omitempty"`
	RuntimeKind            RuntimeKind         `json:"runtime_kind,omitempty"`
	TemplateType           RuntimeTemplateType `json:"template_type,omitempty"`
	Endpoint               string              `json:"endpoint,omitempty"`
	HealthPath             string              `json:"health_path,omitempty"`
	ModelPath              string              `json:"model_path,omitempty"`
	ScriptRef              string              `json:"script_ref,omitempty"`
	RuntimeContainerID     string              `json:"runtime_container_id,omitempty"`
	ExposedPorts           []string            `json:"exposed_ports,omitempty"`
	RequiredEnv            []string            `json:"required_env,omitempty"`
	OptionalEnv            []string            `json:"optional_env,omitempty"`
	MountPoints            []string            `json:"mount_points,omitempty"`
	CommandOverrideAllowed *bool               `json:"command_override_allowed,omitempty"`
	ScriptMountAllowed     *bool               `json:"script_mount_allowed,omitempty"`
	CommandOverride        []string            `json:"command_override,omitempty"`
	BindingMountRules      []string            `json:"binding_mount_rules,omitempty"`
	BindingEnvOverrides    map[string]string   `json:"binding_env_overrides,omitempty"`
	SupportedModelTypes    []string            `json:"supported_model_types,omitempty"`
	SupportedFormats       []string            `json:"supported_formats,omitempty"`
	ModelType              string              `json:"model_type,omitempty"`
	ModelFormat            string              `json:"model_format,omitempty"`
	ManifestVersion        string              `json:"manifest_version,omitempty"`
	Metadata               map[string]string   `json:"metadata,omitempty"`
}

type AgentTaskProtocolError struct {
	Code          string                 `json:"code"`
	Message       string                 `json:"message"`
	MissingFields []string               `json:"missing_fields,omitempty"`
	Recoverable   bool                   `json:"recoverable"`
	Detail        map[string]interface{} `json:"detail,omitempty"`
}

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
