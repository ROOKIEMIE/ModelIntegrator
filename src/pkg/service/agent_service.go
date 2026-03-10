package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"ModelIntegrator/src/pkg/model"
)

var (
	ErrAgentNotFound = errors.New("agent not found")
	ErrInvalidAgent  = errors.New("invalid agent payload")
)

type AgentService struct {
	mu                sync.RWMutex
	agentsByID        map[string]model.Agent
	heartbeatTTL      time.Duration
	heartbeatInterval time.Duration
	logger            *slog.Logger
}

func NewAgentService(heartbeatTTL, heartbeatInterval time.Duration, logger *slog.Logger) *AgentService {
	if heartbeatTTL <= 0 {
		heartbeatTTL = 45 * time.Second
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentService{
		agentsByID:        make(map[string]model.AgentState),
		heartbeatTTL:      heartbeatTTL,
		heartbeatInterval: heartbeatInterval,
		logger:            logger,
	}
}

func (s *AgentService) Register(_ context.Context, req model.AgentRegisterRequest) (model.AgentRegisterResponse, error) {
	agentID := strings.TrimSpace(req.ID)
	if agentID == "" {
		agentID = strings.TrimSpace(req.AgentID)
	}
	nodeID := strings.TrimSpace(req.NodeID)
	if agentID == "" || nodeID == "" {
		return model.AgentRegisterResponse{}, ErrInvalidAgent
	}

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, existed := s.agentsByID[agentID]
	registeredAt := existing.RegisteredAt
	if registeredAt.IsZero() {
		registeredAt = now
	}

	address := strings.TrimSpace(req.Address)
	host := strings.TrimSpace(req.Host)
	if address == "" {
		address = host
	}
	if host == "" {
		host = address
	}
	name := strings.TrimSpace(req.Name)
	if name == "" && existed {
		name = existing.Name
	}
	if name == "" {
		name = agentID
	}

	agent := model.Agent{
		ID:                  agentID,
		AgentID:             agentID,
		NodeID:              nodeID,
		Name:                name,
		Version:             strings.TrimSpace(req.Version),
		Status:              model.AgentStatusOnline,
		Address:             address,
		Host:                host,
		RegisteredAt:        registeredAt,
		LastHeartbeatAt:     now,
		HeartbeatTTLSeconds: int(s.heartbeatTTL.Seconds()),
		Capabilities:        mergeSlices(existing.Capabilities, req.Capabilities),
		RuntimeCapabilities: existing.RuntimeCapabilities,
		Metadata:            mergeMaps(existing.Metadata, req.Metadata),
	}

	s.agentsByID[agentID] = agent

	return model.AgentRegisterResponse{
		Agent:                    agent,
		ServerTime:               now,
		HeartbeatIntervalSeconds: int(s.heartbeatInterval.Seconds()),
	}, nil
}

func (s *AgentService) Heartbeat(_ context.Context, agentID string, req model.AgentHeartbeatRequest) (model.AgentHeartbeatResponse, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return model.AgentHeartbeatResponse{}, ErrInvalidAgent
	}

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agentsByID[agentID]
	if !ok {
		return model.AgentHeartbeatResponse{}, ErrAgentNotFound
	}

	if nodeID := strings.TrimSpace(req.NodeID); nodeID != "" {
		agent.NodeID = nodeID
	}

	agent.LastHeartbeatAt = now
	if req.Status != "" {
		agent.Status = req.Status
	} else {
		agent.Status = model.AgentStatusOnline
	}
	agent.Metadata = mergeMaps(agent.Metadata, req.Metadata)
	s.agentsByID[agentID] = agent

	return model.AgentHeartbeatResponse{
		ID:             agent.ID,
		AgentID:        agent.ID,
		NodeID:         agent.NodeID,
		Status:         agent.Status,
		ServerTime:     now,
		NextDeadlineAt: now.Add(s.heartbeatTTL),
	}, nil
}

func (s *AgentService) ReportCapabilities(_ context.Context, agentID string, req model.AgentCapabilitiesReportRequest) (model.AgentCapabilitiesReportResponse, error) {
	agentID = strings.TrimSpace(agentID)
	nodeID := strings.TrimSpace(req.NodeID)
	if agentID == "" {
		return model.AgentCapabilitiesReportResponse{}, ErrInvalidAgent
	}

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, exists := s.agentsByID[agentID]
	if !exists {
		agent = model.Agent{
			ID:                  agentID,
			AgentID:             agentID,
			RegisteredAt:        now,
			HeartbeatTTLSeconds: int(s.heartbeatTTL.Seconds()),
			Name:                agentID,
			Status:              model.AgentStatusOnline,
		}
	}

	if nodeID != "" {
		agent.NodeID = nodeID
	}
	if strings.TrimSpace(agent.NodeID) == "" {
		return model.AgentCapabilitiesReportResponse{}, ErrInvalidAgent
	}
	if strings.TrimSpace(agent.ID) == "" {
		agent.ID = agentID
	}
	agent.AgentID = agent.ID
	if strings.TrimSpace(agent.Name) == "" {
		agent.Name = agent.ID
	}
	agent.LastHeartbeatAt = now
	agent.Status = model.AgentStatusOnline
	agent.Metadata = mergeMaps(agent.Metadata, req.Metadata)
	agent.Capabilities = mergeSlices(agent.Capabilities, req.Capabilities)
	agent.RuntimeCapabilities = normalizeRuntimeCapabilities(req.RuntimeCapabilities)

	s.agentsByID[agentID] = agent
	return model.AgentCapabilitiesReportResponse{
		Agent:            agent,
		CapabilitySource: capabilitySourceForAgent(agent),
		UpdatedAt:        now,
	}, nil
}

func (s *AgentService) List(_ context.Context) []model.AgentState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	out := make([]model.AgentState, 0, len(s.agentsByID))
	for _, item := range s.agentsByID {
		out = append(out, s.withComputedStatus(item, now))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *AgentService) GetByID(agentID string) (*model.AgentState, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.agentsByID[agentID]
	if !ok {
		return nil, false
	}
	withStatus := s.withComputedStatus(item, time.Now().UTC())
	return &withStatus, true
}

func (s *AgentService) ListByNodeID(nodeID string) []model.AgentState {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	out := make([]model.AgentState, 0, 2)
	for _, item := range s.agentsByID {
		if strings.TrimSpace(item.NodeID) != nodeID {
			continue
		}
		out = append(out, s.withComputedStatus(item, now))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == model.AgentStatusOnline
		}
		return out[i].LastHeartbeatAt.After(out[j].LastHeartbeatAt)
	})
	return out
}

func (s *AgentService) GetByNodeID(nodeID string) (*model.AgentState, bool) {
	agents := s.ListByNodeID(nodeID)
	if len(agents) == 0 {
		return nil, false
	}
	selected := agents[0]
	return &selected, true
}

func (s *AgentService) IsOnline(agentID string) bool {
	agent, ok := s.GetByID(agentID)
	if !ok {
		return false
	}
	return agent.Status == model.AgentStatusOnline
}

func (s *AgentService) withComputedStatus(item model.AgentState, now time.Time) model.AgentState {
	if strings.TrimSpace(item.ID) == "" {
		item.ID = strings.TrimSpace(item.AgentID)
	}
	if strings.TrimSpace(item.AgentID) == "" {
		item.AgentID = strings.TrimSpace(item.ID)
	}
	if strings.TrimSpace(item.Address) == "" {
		item.Address = strings.TrimSpace(item.Host)
	}
	if strings.TrimSpace(item.Host) == "" {
		item.Host = strings.TrimSpace(item.Address)
	}
	if strings.TrimSpace(item.Name) == "" {
		item.Name = item.ID
	}
	if item.LastHeartbeatAt.IsZero() {
		item.Status = model.AgentStatusOffline
		return item
	}
	if now.Sub(item.LastHeartbeatAt) > s.heartbeatTTL {
		item.Status = model.AgentStatusOffline
	} else {
		item.Status = model.AgentStatusOnline
	}
	return item
}

func mergeMaps(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	for k, v := range extra {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	return out
}

func normalizeItems(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
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

func normalizeRuntimeCapabilities(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string][]string, len(source))
	for k, v := range source {
		key := strings.TrimSpace(strings.ToLower(k))
		if key == "" {
			continue
		}
		out[key] = normalizeItems(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeSlices(base, extra []string) []string {
	return normalizeItems(append(base, extra...))
}

func capabilitySourceForAgent(agent model.AgentState) model.CapabilitySource {
	if len(agent.Capabilities) == 0 && len(agent.RuntimeCapabilities) == 0 {
		return model.CapabilitySourceUnknown
	}
	return model.CapabilitySourceAgentReported
}
