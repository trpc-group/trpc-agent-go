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
	"fmt"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	coresubagent "trpc.group/trpc-go/trpc-agent-go/subagent"
)

type Service struct {
	core   *coresubagent.Service
	router *outbound.Router

	mu      sync.RWMutex
	baseCtx context.Context
}

func NewService(
	stateDir string,
	r runner.Runner,
	router *outbound.Router,
) (*Service, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, fmt.Errorf("subagent: empty state dir")
	}
	if r == nil {
		return nil, fmt.Errorf("subagent: nil runner")
	}

	store, err := coresubagent.NewFileStore(subagentStorePath(stateDir))
	if err != nil {
		return nil, err
	}
	svc := &Service{router: router}
	core, err := coresubagent.NewService(
		r,
		coresubagent.WithStore(store),
		coresubagent.WithObserver(svc),
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
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
	s.core.Start(ctx)
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
) (coresubagent.Run, error) {
	if s == nil || s.core == nil {
		return coresubagent.Run{}, fmt.Errorf("subagent: nil service")
	}
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
	return s.core.Spawn(ctx, coresubagent.SpawnRequest{
		OwnerUserID:     req.OwnerUserID,
		ParentSessionID: req.ParentSessionID,
		Task:            req.Task,
		Timeout:         timeoutDuration(req.TimeoutSeconds),
		RuntimeState:    runtimeState,
		InjectedContextMessages: []model.Message{
			model.NewSystemMessage(subagentRunPrompt),
		},
		Metadata: metadataForDelivery(req.Delivery),
	})
}

func (s *Service) ListForUser(
	userID string,
	filter coresubagent.ListFilter,
) []coresubagent.Run {
	if s == nil || s.core == nil {
		return nil
	}
	filter.OwnerUserID = strings.TrimSpace(userID)
	runs, err := s.core.List(context.Background(), filter)
	if err != nil {
		return nil
	}
	return runs
}

func (s *Service) GetForUser(
	userID string,
	runID string,
) (*coresubagent.Run, error) {
	run, err := s.runForUser(context.Background(), userID, runID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Service) CancelForUser(
	userID string,
	runID string,
) (*coresubagent.Run, bool, error) {
	run, err := s.runForUser(context.Background(), userID, runID)
	if err != nil {
		return nil, false, err
	}
	return s.core.Cancel(context.Background(), run.ID)
}

func (s *Service) WaitForUser(
	ctx context.Context,
	userID string,
	runID string,
) (*coresubagent.Run, error) {
	run, err := s.runForUser(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	return s.core.Wait(ctx, run.ID)
}

func (s *Service) OnRunUpdate(ctx context.Context, run coresubagent.Run) {
	if s == nil || !run.Status.IsTerminal() ||
		run.Status == coresubagent.StatusCanceled {
		return
	}
	s.notifyCompletion(run)
}

func (s *Service) notifyCompletion(run coresubagent.Run) {
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

func formatNotification(run coresubagent.Run) string {
	var prefix string
	switch run.Status {
	case coresubagent.StatusCompleted:
		prefix = notificationPrefixCompleted
	case coresubagent.StatusFailed:
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

func notificationDetail(run coresubagent.Run) string {
	if run.Status == coresubagent.StatusCompleted {
		result := strings.TrimSpace(run.Result)
		if result != "" {
			return result
		}
	}
	if summary := strings.TrimSpace(run.Summary); summary != "" {
		return summary
	}
	return strings.TrimSpace(run.Error)
}

func (s *Service) runForUser(
	ctx context.Context,
	userID string,
	runID string,
) (*coresubagent.Run, error) {
	if s == nil || s.core == nil {
		return nil, coresubagent.ErrRunNotFound
	}
	run, err := s.core.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(run.OwnerUserID) != strings.TrimSpace(userID) {
		return nil, coresubagent.ErrRunNotFound
	}
	return run, nil
}
