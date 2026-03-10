package capability

import (
	"sort"
	"strings"
	"time"

	"ModelIntegrator/src/pkg/model"
)

func EnrichNode(node *model.Node, agent *model.AgentState) {
	if node == nil {
		return
	}

	normalizedAgent := normalizeAgent(agent)
	node.Agent = normalizedAgent
	if normalizedAgent != nil {
		node.AgentStatus = normalizedAgent.Status
	} else {
		node.AgentStatus = model.AgentStatusNone
	}

	node.Classification = classifyNode(*node, normalizedAgent)
	node.CapabilitySource = deriveNodeCapabilitySource(*node, normalizedAgent)
	node.CapabilityNote = noteForSource(node.CapabilitySource)
	node.OperationLevel = operationLevelForSource(node.CapabilitySource)

	for i := range node.Runtimes {
		enrichRuntime(&node.Runtimes[i], normalizedAgent)
	}

	node.CapabilityTier = deriveNodeCapabilityTier(*node, normalizedAgent)
}

func classifyNode(node model.Node, agent *model.AgentState) model.NodeClassification {
	hasControllerRole := node.Role == model.NodeRoleMain
	hasRuntime := countEnabledRuntimes(node.Runtimes) > 0
	hasAgent := agentExists(agent)

	switch {
	case hasControllerRole && (hasRuntime || hasAgent):
		return model.NodeClassificationHybrid
	case hasControllerRole:
		return model.NodeClassificationController
	case hasRuntime && hasAgent:
		return model.NodeClassificationHybrid
	case hasRuntime:
		return model.NodeClassificationWorker
	case hasAgent:
		return model.NodeClassificationAgentHost
	default:
		return model.NodeClassificationUnknown
	}
}

func deriveNodeCapabilitySource(node model.Node, agent *model.AgentState) model.CapabilitySource {
	hasStatic := false
	hasRuntime := false
	hasAgentReported := hasAgentCapabilities(agent)
	for _, rt := range node.Runtimes {
		if !rt.Enabled {
			continue
		}
		hasStatic = true
		if rt.Status == model.RuntimeStatusOnline || rt.Status == model.RuntimeStatusOffline {
			hasRuntime = true
		}
	}

	switch {
	case hasAgentReported && (hasStatic || hasRuntime):
		return model.CapabilitySourceMerged
	case hasAgentReported:
		return model.CapabilitySourceAgentReported
	case hasRuntime:
		return model.CapabilitySourceRuntime
	case hasStatic:
		return model.CapabilitySourceStatic
	default:
		return model.CapabilitySourceUnknown
	}
}

func enrichRuntime(rt *model.Runtime, agent *model.AgentState) {
	if rt == nil {
		return
	}
	if !rt.Enabled && rt.Status == "" {
		rt.Status = model.RuntimeStatusUnknown
	}
	if (rt.Status == model.RuntimeStatusOnline || rt.Status == model.RuntimeStatusOffline) && rt.LastSeenAt.IsZero() {
		rt.LastSeenAt = time.Now().UTC()
	}

	baseCaps, baseActions := defaultRuntimeAbilities(rt.Type)
	reported := runtimeCapabilitiesFromAgent(agent, rt)
	capabilities := mergeUnique(baseCaps, reported)
	actions := mergeUnique(baseActions, extractActionsFromCapabilities(reported))

	switch {
	case len(reported) > 0 && len(baseCaps) > 0:
		rt.CapabilitySource = model.CapabilitySourceMerged
	case len(reported) > 0:
		rt.CapabilitySource = model.CapabilitySourceAgentReported
	case rt.Status == model.RuntimeStatusOnline || rt.Status == model.RuntimeStatusOffline:
		rt.CapabilitySource = model.CapabilitySourceRuntime
	case len(baseCaps) > 0:
		rt.CapabilitySource = model.CapabilitySourceStatic
	default:
		rt.CapabilitySource = model.CapabilitySourceUnknown
	}
	if rt.LastSeenAt.IsZero() && agent != nil && len(reported) > 0 && !agent.LastHeartbeatAt.IsZero() {
		rt.LastSeenAt = agent.LastHeartbeatAt
	}

	rt.CapabilityNote = noteForSource(rt.CapabilitySource)
	rt.Capabilities = capabilities
	rt.Actions = actions
}

func runtimeCapabilitiesFromAgent(agent *model.AgentState, rt *model.Runtime) []string {
	if agent == nil || rt == nil || len(agent.RuntimeCapabilities) == 0 {
		return nil
	}
	out := []string{}
	if v, ok := agent.RuntimeCapabilities[rt.ID]; ok {
		out = mergeUnique(out, v)
	}
	if v, ok := agent.RuntimeCapabilities[strings.TrimSpace(strings.ToLower(rt.ID))]; ok {
		out = mergeUnique(out, v)
	}
	if v, ok := agent.RuntimeCapabilities[strings.ToLower(string(rt.Type))]; ok {
		out = mergeUnique(out, v)
	}
	return out
}

func defaultRuntimeAbilities(rtType model.RuntimeType) ([]string, []string) {
	switch rtType {
	case model.RuntimeTypeLMStudio, model.RuntimeTypeOllama:
		return []string{"list", "load", "unload", "health"}, []string{"load", "unload"}
	case model.RuntimeTypeDocker, model.RuntimeTypePortainer:
		return []string{"list", "load", "unload", "start", "stop", "pull", "health"}, []string{"load", "unload", "start", "stop"}
	case model.RuntimeTypeVLLM:
		return []string{"list", "health", "metrics"}, []string{}
	case model.RuntimeTypeOpenAI:
		return []string{"list", "health"}, []string{}
	default:
		return []string{"health"}, []string{}
	}
}

func operationLevelForSource(source model.CapabilitySource) string {
	switch source {
	case model.CapabilitySourceMerged:
		return "controller-agent-merged"
	case model.CapabilitySourceAgentReported:
		return "agent-assisted-control"
	case model.CapabilitySourceRuntime:
		return "runtime-observed-control"
	case model.CapabilitySourceStatic:
		return "static-profile-control"
	default:
		return "unknown"
	}
}

func noteForSource(source model.CapabilitySource) string {
	switch source {
	case model.CapabilitySourceMerged:
		return "运行时能力与 agent 上报能力已合并"
	case model.CapabilitySourceAgentReported:
		return "能力由 agent 主动上报"
	case model.CapabilitySourceRuntime:
		return "能力由运行时状态推导"
	case model.CapabilitySourceStatic:
		return "能力由静态模板和运行时类型推导"
	default:
		return "能力来源未知"
	}
}

func deriveNodeCapabilityTier(node model.Node, agent *model.AgentState) model.CapabilityTier {
	capSet := make(map[string]struct{})
	for _, rt := range node.Runtimes {
		for _, item := range rt.Capabilities {
			key := strings.TrimSpace(strings.ToLower(item))
			if key != "" {
				capSet[key] = struct{}{}
			}
		}
		for _, item := range rt.Actions {
			key := strings.TrimSpace(strings.ToLower(item))
			if key != "" {
				capSet["action:"+key] = struct{}{}
			}
		}
	}
	if agent != nil {
		for _, item := range agent.Capabilities {
			key := strings.TrimSpace(strings.ToLower(item))
			if key != "" {
				capSet[key] = struct{}{}
			}
		}
	}
	switch node.Classification {
	case model.NodeClassificationController, model.NodeClassificationHybrid:
		capSet["orchestration"] = struct{}{}
		capSet["node-scheduling"] = struct{}{}
	}

	score := len(capSet)
	tier := model.CapabilityTierUnknown
	switch {
	case score == 0:
		tier = model.CapabilityTier0
	case score <= 4:
		tier = model.CapabilityTier1
	case score <= 8:
		tier = model.CapabilityTier2
	default:
		tier = model.CapabilityTier3
	}
	if node.Classification == model.NodeClassificationController || node.Classification == model.NodeClassificationHybrid {
		if tier == model.CapabilityTier0 || tier == model.CapabilityTier1 {
			tier = model.CapabilityTier2
		}
	}
	if node.CapabilitySource == model.CapabilitySourceMerged && score >= 8 {
		tier = model.CapabilityTier3
	}
	return tier
}

func extractActionsFromCapabilities(capabilities []string) []string {
	if len(capabilities) == 0 {
		return nil
	}
	known := map[string]struct{}{
		"load":   {},
		"unload": {},
		"start":  {},
		"stop":   {},
		"pull":   {},
	}
	out := make([]string, 0, len(capabilities))
	for _, item := range capabilities {
		key := strings.TrimSpace(strings.ToLower(item))
		if _, ok := known[key]; !ok {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return dedupSorted(out)
}

func normalizeAgent(agent *model.AgentState) *model.AgentState {
	if agent == nil {
		return nil
	}
	copyAgent := *agent
	id := strings.TrimSpace(copyAgent.ID)
	if id == "" {
		id = strings.TrimSpace(copyAgent.AgentID)
	}
	copyAgent.ID = id
	copyAgent.AgentID = id
	if strings.TrimSpace(copyAgent.Address) == "" {
		copyAgent.Address = strings.TrimSpace(copyAgent.Host)
	}
	if strings.TrimSpace(copyAgent.Host) == "" {
		copyAgent.Host = strings.TrimSpace(copyAgent.Address)
	}
	if copyAgent.Status == "" {
		copyAgent.Status = model.AgentStatusNone
	}
	return &copyAgent
}

func hasAgentCapabilities(agent *model.AgentState) bool {
	if !agentExists(agent) {
		return false
	}
	if len(agent.Capabilities) > 0 {
		return true
	}
	for _, items := range agent.RuntimeCapabilities {
		if len(items) > 0 {
			return true
		}
	}
	return false
}

func agentExists(agent *model.AgentState) bool {
	if agent == nil {
		return false
	}
	return strings.TrimSpace(agent.ID) != "" || strings.TrimSpace(agent.AgentID) != ""
}

func mergeUnique(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, item := range base {
		key := strings.TrimSpace(strings.ToLower(item))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	for _, item := range extra {
		key := strings.TrimSpace(strings.ToLower(item))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func dedupSorted(items []string) []string {
	if len(items) < 2 {
		return items
	}
	out := items[:1]
	for i := 1; i < len(items); i++ {
		if items[i] == items[i-1] {
			continue
		}
		out = append(out, items[i])
	}
	return out
}

func countEnabledRuntimes(runtimes []model.Runtime) int {
	count := 0
	for _, rt := range runtimes {
		if rt.Enabled {
			count++
		}
	}
	return count
}
