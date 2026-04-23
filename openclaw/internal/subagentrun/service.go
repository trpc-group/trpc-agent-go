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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	publicsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type Service struct {
	path   string
	runner runner.Runner
	router *outbound.Router

	clock func() time.Time

	mu      sync.Mutex
	runs    map[string]*runRecord
	running map[string]*runningRun

	persistMu sync.Mutex

	startOnce sync.Once
	baseCtx   context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

type runningRun struct {
	cancel          context.CancelFunc
	skipNotify      bool
	cancelRequested bool
	childSession    string
	requestID       string
	startedAt       time.Time
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

	path := filepath.Join(
		strings.TrimSpace(stateDir),
		subagentDirName,
		subagentRunsFileName,
	)
	runs, err := loadRuns(path)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		path:    path,
		runner:  r,
		router:  router,
		clock:   time.Now,
		runs:    runs,
		running: make(map[string]*runningRun),
	}
	if normalizeLoadedRuns(svc.runs, svc.clock()) {
		if err := svc.persist(); err != nil {
			return nil, err
		}
	}
	return svc, nil
}

func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		s.baseCtx, s.cancel = context.WithCancel(ctx)
	})
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.stopAllRunning()
	s.wg.Wait()
	return s.persist()
}

func (s *Service) Spawn(
	ctx context.Context,
	req SpawnRequest,
) (publicsubagent.Run, error) {
	if s == nil {
		return publicsubagent.Run{}, fmt.Errorf("subagent: nil service")
	}
	if s.baseCtx == nil {
		return publicsubagent.Run{}, fmt.Errorf("subagent: not started")
	}

	ownerUserID := strings.TrimSpace(req.OwnerUserID)
	parentSessionID := strings.TrimSpace(req.ParentSessionID)
	task := strings.TrimSpace(req.Task)
	if ownerUserID == "" {
		return publicsubagent.Run{}, fmt.Errorf("subagent: empty owner")
	}
	if parentSessionID == "" {
		return publicsubagent.Run{}, fmt.Errorf(
			"subagent: empty parent session id",
		)
	}
	if task == "" {
		return publicsubagent.Run{}, fmt.Errorf("subagent: empty task")
	}

	now := s.clock()
	record := &runRecord{
		Run: publicsubagent.Run{
			ID:              uuid.NewString(),
			ParentSessionID: parentSessionID,
			Task:            task,
			Status:          publicsubagent.StatusQueued,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		OwnerUserID: ownerUserID,
		Delivery:    req.Delivery,
	}

	s.mu.Lock()
	s.runs[record.ID] = record
	s.mu.Unlock()
	if err := s.persist(); err != nil {
		s.mu.Lock()
		delete(s.runs, record.ID)
		s.mu.Unlock()
		return publicsubagent.Run{}, err
	}
	view := record.publicView()

	s.wg.Add(1)
	go func(
		parent context.Context,
		runID string,
		timeoutSeconds int,
	) {
		defer s.wg.Done()
		s.execute(parent, runID, timeoutSeconds)
	}(s.baseCtx, record.ID, req.TimeoutSeconds)

	return view, nil
}

func (s *Service) ListForUser(
	userID string,
	filter publicsubagent.ListFilter,
) []publicsubagent.Run {
	if s == nil {
		return nil
	}
	userID = strings.TrimSpace(userID)
	parentSessionID := strings.TrimSpace(filter.ParentSessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	runs := make([]publicsubagent.Run, 0, len(s.runs))
	for _, item := range s.runs {
		if item == nil || item.OwnerUserID != userID {
			continue
		}
		if parentSessionID != "" &&
			item.ParentSessionID != parentSessionID {
			continue
		}
		runs = append(runs, item.publicView())
	}
	sort.Slice(runs, func(i int, j int) bool {
		return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
	})
	return runs
}

func (s *Service) GetForUser(
	userID string,
	runID string,
) (*publicsubagent.Run, error) {
	record, err := s.runForUser(userID, runID)
	if err != nil {
		return nil, err
	}
	view := record.publicView()
	return &view, nil
}

func (s *Service) CancelForUser(
	userID string,
	runID string,
) (*publicsubagent.Run, bool, error) {
	record, err := s.runForUser(userID, runID)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	current := s.runs[record.ID]
	if current == nil {
		s.mu.Unlock()
		return nil, false, publicsubagent.ErrRunNotFound
	}
	if current.Status.IsTerminal() {
		view := current.publicView()
		s.mu.Unlock()
		return &view, false, nil
	}

	now := s.clock()
	current.Status = publicsubagent.StatusCanceled
	current.Error = ""
	current.Summary = summarizeResult("canceled")
	current.UpdatedAt = now
	current.FinishedAt = cloneTime(now)

	if running := s.running[current.ID]; running != nil {
		running.skipNotify = true
		running.cancelRequested = true
		if running.cancel != nil {
			running.cancel()
		}
	}
	view := current.publicView()
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		return nil, false, err
	}
	return &view, true, nil
}

func (s *Service) execute(
	parent context.Context,
	runID string,
	timeoutSeconds int,
) {
	record, runCtx, started, err := s.markRunning(
		parent,
		runID,
		timeoutSeconds,
	)
	if err != nil {
		return
	}
	if started.cancel != nil {
		defer started.cancel()
	}

	result := replyAccumulator{}
	runErr := s.runChild(runCtx, record, started, &result)
	output := sanitizeStoredResult(result.text)
	s.finishRun(runID, output, runErr)
}

func (s *Service) runChild(
	ctx context.Context,
	record *runRecord,
	started runningRun,
	result *replyAccumulator,
) error {
	if record == nil {
		return fmt.Errorf("subagent: nil run record")
	}
	runtimeState := map[string]any{
		runtimeStateSubagentRun:      true,
		runtimeStateSubagentRunID:    record.ID,
		runtimeStateSubagentParentID: record.ParentSessionID,
	}
	if record.Delivery.Channel != "" && record.Delivery.Target != "" {
		targetState := outbound.RuntimeStateForTarget(
			outbound.DeliveryTarget{
				Channel: record.Delivery.Channel,
				Target:  record.Delivery.Target,
			},
		)
		for key, value := range targetState {
			runtimeState[key] = value
		}
	}

	runOpts := []agent.RunOption{
		agent.WithRequestID(started.requestID),
		agent.WithRuntimeState(runtimeState),
		agent.WithInjectedContextMessages([]model.Message{
			model.NewSystemMessage(subagentRunPrompt),
		}),
	}

	events, err := s.runner.Run(
		ctx,
		record.OwnerUserID,
		started.childSession,
		model.NewUserMessage(record.Task),
		runOpts...,
	)
	if err != nil {
		return err
	}
	for evt := range events {
		result.consume(evt)
	}
	return result.err
}

func (s *Service) markRunning(
	parent context.Context,
	runID string,
	timeoutSeconds int,
) (*runRecord, context.Context, runningRun, error) {
	s.mu.Lock()
	record := s.runs[strings.TrimSpace(runID)]
	if record == nil {
		s.mu.Unlock()
		return nil, nil, runningRun{}, publicsubagent.ErrRunNotFound
	}
	if record.Status == publicsubagent.StatusCanceled {
		s.mu.Unlock()
		return nil, nil, runningRun{}, fmt.Errorf(
			"subagent: run canceled before start",
		)
	}

	now := s.clock()
	started := runningRun{
		startedAt:    now,
		childSession: newChildSessionID(record.ID, now),
		requestID:    newRequestID(record.ID, now),
	}

	runCtx := parent
	if runCtx == nil {
		runCtx = context.Background()
	}
	if timeoutSeconds > 0 {
		timeoutCtx, cancel := context.WithTimeout(
			runCtx,
			time.Duration(timeoutSeconds)*time.Second,
		)
		runCtx = timeoutCtx
		started.cancel = cancel
	} else {
		nextCtx, cancel := context.WithCancel(runCtx)
		runCtx = nextCtx
		started.cancel = cancel
	}

	record.Status = publicsubagent.StatusRunning
	record.ChildSessionID = started.childSession
	record.UpdatedAt = now
	record.StartedAt = cloneTime(now)
	record.FinishedAt = nil
	record.Error = ""
	record.Summary = ""
	record.Result = ""

	s.running[record.ID] = &runningRun{
		cancel:       started.cancel,
		childSession: started.childSession,
		requestID:    started.requestID,
		startedAt:    started.startedAt,
	}
	clone := record.clone()
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		if started.cancel != nil {
			started.cancel()
		}
		s.mu.Lock()
		delete(s.running, runID)
		if current := s.runs[runID]; current != nil {
			current.Status = publicsubagent.StatusFailed
			current.Error = err.Error()
			current.Summary = summarizeResult(current.Error)
			current.UpdatedAt = now
			current.FinishedAt = cloneTime(now)
		}
		s.mu.Unlock()
		return nil, nil, runningRun{}, err
	}
	return clone, runCtx, started, nil
}

func (s *Service) finishRun(
	runID string,
	output string,
	runErr error,
) {
	s.mu.Lock()
	record := s.runs[runID]
	if record == nil {
		delete(s.running, runID)
		s.mu.Unlock()
		return
	}
	now := s.clock()
	running := s.running[runID]
	delete(s.running, runID)

	record.Result = output
	record.UpdatedAt = now
	record.FinishedAt = cloneTime(now)

	switch {
	case running != nil && running.cancelRequested:
		record.Status = publicsubagent.StatusCanceled
		record.Error = ""
		record.Summary = summarizeResult("canceled")
	case errors.Is(runErr, context.Canceled):
		record.Status = publicsubagent.StatusCanceled
		record.Error = ""
		record.Summary = summarizeResult("canceled")
	case runErr != nil:
		record.Status = publicsubagent.StatusFailed
		record.Error = runErr.Error()
		record.Summary = summarizeResult(record.Error)
	default:
		record.Status = publicsubagent.StatusCompleted
		record.Error = ""
		record.Summary = summarizeResult(output)
	}
	clone := record.clone()
	notify := running != nil &&
		!running.skipNotify &&
		record.Status != publicsubagent.StatusCanceled
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		log.Warnf("subagent: persist run %s failed: %v", runID, err)
	}
	if notify {
		s.notifyCompletion(clone)
	}
}

func (s *Service) notifyCompletion(record *runRecord) {
	if s == nil || s.router == nil || record == nil {
		return
	}
	if strings.TrimSpace(record.Delivery.Channel) == "" ||
		strings.TrimSpace(record.Delivery.Target) == "" {
		return
	}
	message := formatNotification(record)
	if strings.TrimSpace(message) == "" {
		return
	}
	notifyCtx := s.baseCtx
	if notifyCtx == nil {
		notifyCtx = context.Background()
	}
	notifyCtx, cancel := context.WithTimeout(
		notifyCtx,
		defaultNotifyTimeout,
	)
	defer cancel()
	err := s.router.SendText(
		notifyCtx,
		outbound.DeliveryTarget{
			Channel: record.Delivery.Channel,
			Target:  record.Delivery.Target,
		},
		message,
	)
	if err != nil {
		log.Warnf(
			"subagent: notify run %s failed: %v",
			record.ID,
			err,
		)
	}
}

func formatNotification(record *runRecord) string {
	if record == nil {
		return ""
	}
	var prefix string
	switch record.Status {
	case publicsubagent.StatusCompleted:
		prefix = notificationPrefixCompleted
	case publicsubagent.StatusFailed:
		prefix = notificationPrefixFailed
	case publicsubagent.StatusCanceled:
		prefix = notificationPrefixCanceled
	default:
		return ""
	}

	lines := []string{
		fmt.Sprintf("%s #%s", prefix, record.ID),
	}
	if detail := notificationDetail(record); detail != "" {
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}

func notificationDetail(record *runRecord) string {
	if record == nil {
		return ""
	}
	if record.Status == publicsubagent.StatusCompleted {
		result := strings.TrimSpace(record.Result)
		if result != "" {
			return result
		}
	}
	if summary := strings.TrimSpace(record.Summary); summary != "" {
		return summary
	}
	return strings.TrimSpace(record.Error)
}

func (s *Service) runForUser(
	userID string,
	runID string,
) (*runRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record := s.runs[strings.TrimSpace(runID)]
	if record == nil ||
		record.OwnerUserID != strings.TrimSpace(userID) {
		return nil, publicsubagent.ErrRunNotFound
	}
	return record.clone(), nil
}

func (s *Service) stopAllRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, running := range s.running {
		if running == nil {
			delete(s.running, id)
			continue
		}
		running.skipNotify = true
		running.cancelRequested = true
		if running.cancel != nil {
			running.cancel()
		}
	}
}

func (s *Service) persist() error {
	if s == nil {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	runs := make(map[string]*runRecord, len(s.runs))
	for id, item := range s.runs {
		runs[id] = item.clone()
	}
	s.mu.Unlock()
	return saveRuns(s.path, runs)
}

type replyAccumulator struct {
	text     string
	builder  strings.Builder
	seenFull bool
	err      error
}

func (a *replyAccumulator) consume(evt *event.Event) {
	if evt == nil {
		return
	}
	if evt.Error != nil {
		a.err = errors.New(evt.Error.Message)
		return
	}
	if evt.Response == nil {
		return
	}
	switch evt.Object {
	case model.ObjectTypeChatCompletion:
		a.consumeFull(evt.Response)
	case model.ObjectTypeChatCompletionChunk:
		a.consumeDelta(evt.Response)
	}
}

func (a *replyAccumulator) consumeFull(rsp *model.Response) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return
	}
	content := rsp.Choices[0].Message.Content
	if content == "" {
		return
	}
	a.text = content
	a.seenFull = true
}

func (a *replyAccumulator) consumeDelta(rsp *model.Response) {
	if rsp == nil || a.seenFull {
		return
	}
	for _, choice := range rsp.Choices {
		if choice.Delta.Content == "" {
			continue
		}
		a.builder.WriteString(choice.Delta.Content)
	}
	a.text = a.builder.String()
}
