//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	coretaskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	taskruninprocess "trpc.group/trpc-go/trpc-agent-go/agent/taskrun/inprocess"
	"trpc.group/trpc-go/trpc-agent-go/internal/gitworktree"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type serviceOptions struct {
	worktrees worktreeManager
}

// Option customizes the OpenClaw subagent service.
type Option func(*serviceOptions)

// WithWorktreeManager injects the manager used for worktree isolation.
func WithWorktreeManager(manager worktreeManager) Option {
	return func(opts *serviceOptions) {
		if manager != nil {
			opts.worktrees = manager
		}
	}
}

type Service struct {
	core      *taskruninprocess.Service
	router    *outbound.Router
	worktrees worktreeManager

	mu      sync.RWMutex
	baseCtx context.Context
}

func NewService(
	stateDir string,
	r runner.Runner,
	router *outbound.Router,
	opts ...Option,
) (*Service, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, fmt.Errorf("subagent: empty state dir")
	}
	if r == nil {
		return nil, fmt.Errorf("subagent: nil runner")
	}

	store, err := taskruninprocess.NewFileStore(subagentStorePath(stateDir))
	if err != nil {
		return nil, err
	}
	options := serviceOptions{
		worktrees: gitworktree.NewManager(
			subagentWorktreeRoot(stateDir),
			gitworktree.WithBranchPrefix(worktreeBranchPrefix),
		),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	svc := &Service{
		router:    router,
		worktrees: options.worktrees,
	}
	core, err := taskruninprocess.NewService(
		r,
		taskruninprocess.WithStore(store),
		taskruninprocess.WithObserver(svc),
		taskruninprocess.WithFinalizer(svc),
	)
	if err != nil {
		return nil, err
	}
	svc.core = core
	return svc, nil
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.core == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.core.Start(ctx)
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
}

func (s *Service) Close() error {
	if s == nil || s.core == nil {
		return nil
	}
	return s.core.Close()
}

func (s *Service) Spawn(
	ctx context.Context,
	req SpawnRequest,
) (openclawsubagent.Run, error) {
	if s == nil || s.core == nil {
		return openclawsubagent.Run{}, fmt.Errorf("subagent: nil service")
	}
	if !s.started() {
		return openclawsubagent.Run{}, openclawsubagent.ErrNotStarted
	}
	if err := validateSpawnRequest(req); err != nil {
		return openclawsubagent.Run{}, err
	}
	isolation, err := normalizeIsolation(req.Isolation)
	if err != nil {
		return openclawsubagent.Run{}, err
	}

	runID := newSubagentID()
	runtimeState := map[string]any{}
	if req.Delivery.Channel != "" && req.Delivery.Target != "" {
		targetState := outbound.RuntimeStateForTarget(
			outbound.DeliveryTarget{
				Channel: req.Delivery.Channel,
				Target:  req.Delivery.Target,
			},
		)
		for key, value := range targetState {
			runtimeState[key] = value
		}
	}

	var lease *gitworktree.Lease
	var worktreeMetadata map[string]string
	if isolation == isolationWorktree {
		created, err := s.createWorktree(ctx, runID)
		if err != nil {
			return openclawsubagent.Run{}, err
		}
		lease = &created
		worktreeMetadata = metadataForWorktreeLease(created)
		for key, value := range worktreeMetadata {
			runtimeState[key] = value
		}
	}

	runOptions := runOptionsFromContext(ctx, lease)
	runContext := runContextFromContext(ctx, lease)
	messages := []model.Message{model.NewSystemMessage(subagentRunPrompt)}
	if lease != nil {
		messages = append(messages, model.NewSystemMessage(worktreeRunPrompt(*lease)))
	}

	var deliveryMetadata map[string]string
	if !req.SuppressCompletionNotification {
		deliveryMetadata = metadataForDelivery(req.Delivery)
	}
	metadata := mergeMetadata(deliveryMetadata, worktreeMetadata)
	run, err := s.core.Spawn(ctx, coretaskrun.SpawnRequest{
		ID:                      runID,
		OwnerUserID:             req.OwnerUserID,
		ParentSessionID:         req.ParentSessionID,
		ChildSessionID:          runID,
		RequestID:               runID,
		Task:                    req.Task,
		Timeout:                 timeoutDuration(req.TimeoutSeconds),
		RuntimeState:            runtimeState,
		RunOptions:              runOptions,
		RunContext:              runContext,
		RuntimeStateKeys:        subagentRuntimeStateKeys(),
		InjectedContextMessages: messages,
		Metadata:                metadata,
	})
	if err != nil {
		s.cleanupUnspawnedWorktree(ctx, lease)
		return openclawsubagent.Run{}, translateCoreError(err)
	}
	return projectRun(run), nil
}

func (s *Service) ListForUser(
	userID string,
	filter openclawsubagent.ListFilter,
) []openclawsubagent.Run {
	if s == nil || s.core == nil {
		return nil
	}
	runs, err := s.core.List(context.Background(), coretaskrun.ListFilter{
		OwnerUserID:     strings.TrimSpace(userID),
		ParentSessionID: strings.TrimSpace(filter.ParentSessionID),
		Status:          coretaskrun.Status(filter.Status),
	})
	if err != nil {
		return nil
	}
	return projectRuns(runs)
}

func (s *Service) GetForUser(
	userID string,
	runID string,
) (*openclawsubagent.Run, error) {
	run, err := s.runForUser(context.Background(), userID, runID)
	if err != nil {
		return nil, err
	}
	return projectRunPtr(run), nil
}

func (s *Service) CancelForUser(
	userID string,
	runID string,
) (*openclawsubagent.Run, bool, error) {
	run, err := s.runForUser(context.Background(), userID, runID)
	if err != nil {
		return nil, false, err
	}
	canceled, changed, err := s.core.Cancel(context.Background(), run.ID)
	if errors.Is(err, coretaskrun.ErrRunNotFound) {
		return nil, false, openclawsubagent.ErrRunNotFound
	}
	return projectRunPtr(canceled), changed, err
}

func (s *Service) WaitForUser(
	ctx context.Context,
	userID string,
	runID string,
) (*openclawsubagent.Run, error) {
	run, err := s.runForUser(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	final, err := s.core.Wait(ctx, run.ID)
	if errors.Is(err, coretaskrun.ErrRunNotFound) {
		return nil, openclawsubagent.ErrRunNotFound
	}
	return projectRunPtr(final), err
}

func (s *Service) OnRunUpdate(ctx context.Context, run coretaskrun.Run) {
	if s == nil || !run.Status.IsTerminal() {
		return
	}
	if run.Status == coretaskrun.StatusCanceled {
		return
	}
	s.notifyCompletion(run)
}

func (s *Service) FinalizeRun(
	ctx context.Context,
	run coretaskrun.Run,
) map[string]string {
	if s == nil || !run.Status.IsTerminal() {
		return nil
	}
	return s.finalizeWorktree(ctx, run)
}

func (s *Service) notifyCompletion(run coretaskrun.Run) {
	if s == nil || s.router == nil {
		return
	}
	delivery := deliveryFromRun(run)
	if delivery.Channel == "" || delivery.Target == "" {
		return
	}
	message := formatNotification(run)
	if strings.TrimSpace(message) == "" {
		return
	}

	notifyCtx := s.notificationContext()
	notifyCtx, cancel := context.WithTimeout(
		notifyCtx,
		defaultNotifyTimeout,
	)
	defer cancel()
	err := s.router.SendText(
		notifyCtx,
		outbound.DeliveryTarget{
			Channel: delivery.Channel,
			Target:  delivery.Target,
		},
		message,
	)
	if err != nil {
		log.Warnf("subagent: notify run %s failed: %v", run.ID, err)
	}
}

func (s *Service) notificationContext() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.baseCtx != nil {
		return s.baseCtx
	}
	return context.Background()
}

func (s *Service) started() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.baseCtx != nil
}

func formatNotification(run coretaskrun.Run) string {
	var prefix string
	switch run.Status {
	case coretaskrun.StatusCompleted:
		prefix = notificationPrefixCompleted
	case coretaskrun.StatusFailed:
		prefix = notificationPrefixFailed
	default:
		return ""
	}

	lines := []string{
		fmt.Sprintf("%s #%s", prefix, run.ID),
	}
	if detail := notificationDetail(run); detail != "" {
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}

func notificationDetail(run coretaskrun.Run) string {
	var details []string
	if run.Status == coretaskrun.StatusCompleted {
		result := strings.TrimSpace(run.Result)
		if result != "" {
			details = append(details, result)
		}
	}
	if len(details) == 0 {
		if summary := strings.TrimSpace(run.Summary); summary != "" {
			details = append(details, summary)
		}
	}
	if len(details) == 0 {
		if errText := strings.TrimSpace(run.Error); errText != "" {
			details = append(details, errText)
		}
	}
	if detail := worktreeNotificationDetail(run); detail != "" {
		details = append(details, detail)
	}
	return strings.Join(details, "\n")
}

func (s *Service) runForUser(
	ctx context.Context,
	userID string,
	runID string,
) (*coretaskrun.Run, error) {
	if s == nil || s.core == nil {
		return nil, openclawsubagent.ErrRunNotFound
	}
	run, err := s.core.Get(ctx, strings.TrimSpace(runID))
	if errors.Is(err, coretaskrun.ErrRunNotFound) {
		return nil, openclawsubagent.ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(run.OwnerUserID) != strings.TrimSpace(userID) {
		return nil, openclawsubagent.ErrRunNotFound
	}
	return run, nil
}

func validateSpawnRequest(req SpawnRequest) error {
	if strings.TrimSpace(req.OwnerUserID) == "" {
		return fmt.Errorf("subagent: empty owner")
	}
	if strings.TrimSpace(req.ParentSessionID) == "" {
		return fmt.Errorf("subagent: empty parent session id")
	}
	if strings.TrimSpace(req.Task) == "" {
		return fmt.Errorf("subagent: empty task")
	}
	return nil
}

func normalizeIsolation(raw string) (string, error) {
	isolation := strings.TrimSpace(raw)
	if isolation == "" {
		return "", nil
	}
	if isolation == isolationWorktree {
		return isolation, nil
	}
	return "", fmt.Errorf("subagent: unsupported isolation %q", isolation)
}

func translateCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, coretaskrun.ErrRunNotFound):
		return openclawsubagent.ErrRunNotFound
	case errors.Is(err, coretaskrun.ErrRunAlreadyExists):
		return openclawsubagent.ErrRunAlreadyExists
	case errors.Is(err, coretaskrun.ErrNotStarted):
		return openclawsubagent.ErrNotStarted
	default:
		return err
	}
}
