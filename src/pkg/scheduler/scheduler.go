package scheduler

import (
	"fmt"
	"sync"

	"model-control-plane/src/pkg/model"
)

type ModelPolicy struct {
	MutualExclusionGroup string `yaml:"mutual_exclusion_group" json:"mutual_exclusion_group"`
	Priority             int    `yaml:"priority" json:"priority"`
	AutoRecycle          bool   `yaml:"auto_recycle" json:"auto_recycle"`
	IdleTTLSeconds       int    `yaml:"idle_ttl_seconds" json:"idle_ttl_seconds"`
}

type Scheduler struct {
	mu             sync.RWMutex
	policies       map[string]ModelPolicy
	runningByGroup map[string]string
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		policies:       make(map[string]ModelPolicy),
		runningByGroup: make(map[string]string),
	}
}

func (s *Scheduler) SetPolicy(modelID string, policy ModelPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies[modelID] = policy
}

func (s *Scheduler) PolicyFor(modelID string) (ModelPolicy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policy, ok := s.policies[modelID]
	return policy, ok
}

func (s *Scheduler) CanRun(m model.Model) (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	policy, ok := s.policies[m.ID]
	if !ok || policy.MutualExclusionGroup == "" {
		return true, ""
	}

	runningModelID, exists := s.runningByGroup[policy.MutualExclusionGroup]
	if exists && runningModelID != m.ID {
		return false, fmt.Sprintf("互斥组 %s 正在运行模型 %s", policy.MutualExclusionGroup, runningModelID)
	}

	return true, ""
}

func (s *Scheduler) MarkRunning(m model.Model) {
	s.mu.Lock()
	defer s.mu.Unlock()

	policy, ok := s.policies[m.ID]
	if ok && policy.MutualExclusionGroup != "" {
		s.runningByGroup[policy.MutualExclusionGroup] = m.ID
	}
}

func (s *Scheduler) MarkStopped(modelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	policy, ok := s.policies[modelID]
	if !ok || policy.MutualExclusionGroup == "" {
		return
	}

	current := s.runningByGroup[policy.MutualExclusionGroup]
	if current == modelID {
		delete(s.runningByGroup, policy.MutualExclusionGroup)
	}
}
