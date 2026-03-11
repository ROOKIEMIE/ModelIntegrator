package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"model-control-plane/src/pkg/model"
	sqlitestore "model-control-plane/src/pkg/store/sqlite"
)

var (
	ErrTaskNotFound      = errors.New("task not found")
	ErrTaskStoreNotReady = errors.New("task store is not ready")
)

type AgentRuntimeReadinessTaskRequest struct {
	AgentID        string
	ModelID        string
	Endpoint       string
	HealthPath     string
	TimeoutSeconds int
	TriggeredBy    string
}

type AgentTaskReport struct {
	Status     model.TaskStatus       `json:"status"`
	Progress   int                    `json:"progress,omitempty"`
	Message    string                 `json:"message,omitempty"`
	Detail     map[string]interface{} `json:"detail,omitempty"`
	Error      string                 `json:"error,omitempty"`
	AcceptedAt time.Time              `json:"accepted_at,omitempty"`
	StartedAt  time.Time              `json:"started_at,omitempty"`
	FinishedAt time.Time              `json:"finished_at,omitempty"`
}

type TaskService struct {
	store       *sqlitestore.Store
	modelSvc    *ModelService
	logger      *slog.Logger
	taskTimeout time.Duration
}

func NewTaskService(store *sqlitestore.Store, modelSvc *ModelService, logger *slog.Logger) *TaskService {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskService{
		store:       store,
		modelSvc:    modelSvc,
		logger:      logger,
		taskTimeout: 2 * time.Minute,
	}
}

func (s *TaskService) CreateRuntimeTask(ctx context.Context, taskType model.TaskType, modelID, triggeredBy string) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return model.Task{}, fmt.Errorf("model_id is empty")
	}
	if !isRuntimeTaskType(taskType) {
		return model.Task{}, fmt.Errorf("unsupported runtime task type: %s", taskType)
	}

	now := time.Now().UTC()
	task := model.Task{
		ID:         newTaskID("task"),
		Type:       taskType,
		TargetType: model.TaskTargetRuntime,
		TargetID:   modelID,
		Status:     model.TaskStatusPending,
		Progress:   0,
		Message:    "任务已创建",
		Payload: map[string]interface{}{
			"model_id":     modelID,
			"triggered_by": strings.TrimSpace(triggeredBy),
		},
		CreatedAt: now,
	}
	if err := s.store.UpsertTask(ctx, task); err != nil {
		return model.Task{}, err
	}
	go s.executeRuntimeTask(task.ID, taskType, modelID)
	return task, nil
}

func (s *TaskService) executeRuntimeTask(taskID string, taskType model.TaskType, modelID string) {
	if s.modelSvc == nil {
		s.failTask(taskID, model.TaskStatusFailed, "model service 未就绪", "model service is nil", nil)
		return
	}

	s.patchTask(taskID, func(task *model.Task) {
		task.Status = model.TaskStatusDispatched
		task.Progress = maxProgress(task.Progress, 10)
		task.Message = "任务已分发"
	})

	ctx, cancel := context.WithTimeout(context.Background(), s.taskTimeout)
	defer cancel()

	s.patchTask(taskID, func(task *model.Task) {
		now := time.Now().UTC()
		task.Status = model.TaskStatusRunning
		task.StartedAt = now
		task.Progress = maxProgress(task.Progress, 30)
		task.Message = "任务执行中"
	})

	var (
		result model.ActionResult
		err    error
	)

	switch taskType {
	case model.TaskTypeRuntimeStart:
		result, err = s.modelSvc.StartModel(ctx, modelID)
	case model.TaskTypeRuntimeStop:
		result, err = s.modelSvc.StopModel(ctx, modelID)
	case model.TaskTypeRuntimeRestart:
		_, stopErr := s.modelSvc.StopModel(ctx, modelID)
		if stopErr != nil {
			err = stopErr
			break
		}
		s.patchTask(taskID, func(task *model.Task) {
			task.Progress = maxProgress(task.Progress, 65)
			task.Message = "重启任务已完成停止阶段"
		})
		result, err = s.modelSvc.StartModel(ctx, modelID)
	case model.TaskTypeRuntimeRefresh:
		result, err = s.modelSvc.RefreshRuntimeStatus(ctx, modelID)
	default:
		err = fmt.Errorf("unsupported task type: %s", taskType)
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.failTask(taskID, model.TaskStatusTimeout, "任务超时", ctx.Err().Error(), map[string]interface{}{"task_type": string(taskType)})
		return
	}
	if err != nil {
		s.failTask(taskID, model.TaskStatusFailed, "任务执行失败", err.Error(), map[string]interface{}{"task_type": string(taskType)})
		return
	}
	if !result.Success {
		detail := result.Detail
		if detail == nil {
			detail = map[string]interface{}{}
		}
		s.failTask(taskID, model.TaskStatusFailed, firstNonEmpty(result.Message, "任务执行失败"), readErrorFromDetail(detail), detail)
		return
	}

	s.patchTask(taskID, func(task *model.Task) {
		now := time.Now().UTC()
		task.Status = model.TaskStatusSuccess
		task.Progress = 100
		task.Message = firstNonEmpty(result.Message, "任务执行成功")
		task.Detail = result.Detail
		task.Error = ""
		if task.StartedAt.IsZero() {
			task.StartedAt = now
		}
		task.FinishedAt = now
	})
}

func (s *TaskService) CreateAgentRuntimeReadinessTask(ctx context.Context, req AgentRuntimeReadinessTaskRequest) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	agentID := strings.TrimSpace(req.AgentID)
	modelID := strings.TrimSpace(req.ModelID)
	if agentID == "" || modelID == "" {
		return model.Task{}, fmt.Errorf("agent_id/model_id is required")
	}
	timeoutSeconds := req.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 3
	}
	healthPath := strings.TrimSpace(req.HealthPath)
	if healthPath == "" {
		healthPath = "/health"
	}
	task := model.Task{
		ID:              newTaskID("task"),
		Type:            model.TaskTypeAgentRuntimeReadiness,
		TargetType:      model.TaskTargetRuntime,
		TargetID:        modelID,
		AssignedAgentID: agentID,
		Status:          model.TaskStatusPending,
		Message:         "等待 agent 拉取任务",
		Payload: map[string]interface{}{
			"model_id":        modelID,
			"endpoint":        strings.TrimSpace(req.Endpoint),
			"health_path":     healthPath,
			"timeout_seconds": timeoutSeconds,
			"triggered_by":    strings.TrimSpace(req.TriggeredBy),
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.UpsertTask(ctx, task); err != nil {
		return model.Task{}, err
	}
	return task, nil
}

func (s *TaskService) PullNextAgentTask(ctx context.Context, agentID string) (model.Task, bool, error) {
	if s.store == nil {
		return model.Task{}, false, ErrTaskStoreNotReady
	}
	return s.store.ClaimPendingTaskForAgent(ctx, agentID, []model.TaskType{model.TaskTypeAgentRuntimeReadiness})
}

func (s *TaskService) ReportAgentTask(ctx context.Context, agentID, taskID string, report AgentTaskReport) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	agentID = strings.TrimSpace(agentID)
	taskID = strings.TrimSpace(taskID)
	if agentID == "" || taskID == "" {
		return model.Task{}, fmt.Errorf("agent_id/task_id is required")
	}
	task, ok, err := s.store.GetTaskByID(ctx, taskID)
	if err != nil {
		return model.Task{}, err
	}
	if !ok {
		return model.Task{}, ErrTaskNotFound
	}
	if strings.TrimSpace(task.AssignedAgentID) != "" && strings.TrimSpace(task.AssignedAgentID) != agentID {
		return model.Task{}, fmt.Errorf("task does not belong to agent: %s", agentID)
	}

	now := time.Now().UTC()
	task.WorkerID = agentID
	if !report.AcceptedAt.IsZero() {
		task.AcceptedAt = report.AcceptedAt.UTC()
	}
	if report.Status != "" {
		task.Status = report.Status
	}
	if report.Progress > 0 {
		task.Progress = normalizeProgress(report.Progress)
	}
	if msg := strings.TrimSpace(report.Message); msg != "" {
		task.Message = msg
	}
	if report.Detail != nil {
		task.Detail = report.Detail
	}
	if errText := strings.TrimSpace(report.Error); errText != "" {
		task.Error = errText
	}

	switch task.Status {
	case model.TaskStatusRunning:
		if report.StartedAt.IsZero() {
			task.StartedAt = now
		} else {
			task.StartedAt = report.StartedAt.UTC()
		}
		if task.Progress == 0 {
			task.Progress = 70
		}
	case model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusTimeout, model.TaskStatusCanceled:
		if report.StartedAt.IsZero() {
			if task.StartedAt.IsZero() {
				task.StartedAt = now
			}
		} else {
			task.StartedAt = report.StartedAt.UTC()
		}
		if report.FinishedAt.IsZero() {
			task.FinishedAt = now
		} else {
			task.FinishedAt = report.FinishedAt.UTC()
		}
		if task.Progress < 100 {
			task.Progress = 100
		}
	}

	if err := s.store.UpsertTask(ctx, task); err != nil {
		return model.Task{}, err
	}

	if task.Type == model.TaskTypeAgentRuntimeReadiness && s.modelSvc != nil {
		ready := task.Status == model.TaskStatusSuccess
		applyErr := s.modelSvc.ApplyAgentReadiness(ctx, task.TargetID, ready, firstNonEmpty(task.Message, "agent runtime readiness report"), task.Detail)
		if applyErr != nil {
			s.logger.Warn("apply agent readiness failed", "task_id", task.ID, "model_id", task.TargetID, "error", applyErr)
		}
	}

	return task, nil
}

func (s *TaskService) ListTasks(ctx context.Context, targetType, targetID string, limit int) ([]model.Task, error) {
	if s.store == nil {
		return nil, ErrTaskStoreNotReady
	}
	return s.store.ListTasks(ctx, targetType, targetID, limit)
}

func (s *TaskService) GetTask(ctx context.Context, id string) (model.Task, error) {
	if s.store == nil {
		return model.Task{}, ErrTaskStoreNotReady
	}
	item, ok, err := s.store.GetTaskByID(ctx, id)
	if err != nil {
		return model.Task{}, err
	}
	if !ok {
		return model.Task{}, ErrTaskNotFound
	}
	return item, nil
}

func (s *TaskService) AwaitTask(ctx context.Context, id string, pollInterval time.Duration) (model.Task, error) {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		item, err := s.GetTask(ctx, id)
		if err != nil {
			return model.Task{}, err
		}
		if isTaskTerminal(item.Status) {
			return item, nil
		}
		select {
		case <-ctx.Done():
			return model.Task{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *TaskService) patchTask(taskID string, patch func(*model.Task)) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	item, ok, err := s.store.GetTaskByID(ctx, taskID)
	if err != nil || !ok {
		return
	}
	patch(&item)
	if err := s.store.UpsertTask(ctx, item); err != nil {
		s.logger.Warn("patch task failed", "task_id", taskID, "error", err)
	}
}

func (s *TaskService) failTask(taskID string, status model.TaskStatus, message, errText string, detail map[string]interface{}) {
	s.patchTask(taskID, func(task *model.Task) {
		now := time.Now().UTC()
		task.Status = status
		task.Progress = 100
		task.Message = firstNonEmpty(message, "任务执行失败")
		task.Error = strings.TrimSpace(errText)
		task.Detail = detail
		if task.StartedAt.IsZero() {
			task.StartedAt = now
		}
		task.FinishedAt = now
	})
}

func isRuntimeTaskType(taskType model.TaskType) bool {
	switch taskType {
	case model.TaskTypeRuntimeStart, model.TaskTypeRuntimeStop, model.TaskTypeRuntimeRestart, model.TaskTypeRuntimeRefresh:
		return true
	default:
		return false
	}
}

func isTaskTerminal(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusTimeout, model.TaskStatusCanceled:
		return true
	default:
		return false
	}
}

func normalizeProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func maxProgress(current, incoming int) int {
	if incoming > current {
		return normalizeProgress(incoming)
	}
	return normalizeProgress(current)
}

func newTaskID(prefix string) string {
	if strings.TrimSpace(prefix) == "" {
		prefix = "task"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}

func readErrorFromDetail(detail map[string]interface{}) string {
	if detail == nil {
		return ""
	}
	if raw, ok := detail["error"]; ok {
		v := strings.TrimSpace(fmt.Sprint(raw))
		if v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}
