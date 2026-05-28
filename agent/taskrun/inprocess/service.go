//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inprocess

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	childSessionPrefix = "taskrun:"
	requestIDPrefix    = "taskrun:"

	defaultFinalizerTimeout = time.Minute
)

// Option configures a Service.
type Option func(*Options)

// Options contains Service configuration.
type Options struct {
	Store     Store
	Observer  Observer
	Finalizer Finalizer
	Clock     func() time.Time
}

// WithStore configures persistent storage for runs.
func WithStore(store Store) Option {
	return func(opts *Options) {
		opts.Store = store
	}
}

// WithObserver configures lifecycle update observation.
func WithObserver(observer Observer) Option {
	return func(opts *Options) {
		opts.Observer = observer
	}
}

// WithFinalizer configures terminal metadata attachment.
func WithFinalizer(finalizer Finalizer) Option {
	return func(opts *Options) {
		opts.Finalizer = finalizer
	}
}

// WithClock configures the clock used by the service.
func WithClock(clock func() time.Time) Option {
	return func(opts *Options) {
		opts.Clock = clock
	}
}

// Service manages persistent background task runs.
type Service struct {
	runner    runner.Runner
	store     Store
	observer  Observer
	finalizer Finalizer
	clock     func() time.Time

	mu      sync.Mutex
	runs    map[string]*Run
	running map[string]*runningRun
	waiters map[string][]chan struct{}

	persistMu sync.Mutex

	startOnce sync.Once
	baseCtx   context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

type runningRun struct {
	cancel          context.CancelFunc
	cancelRequested bool
	exiting         bool
}

var _ taskrun.Controller = (*Service)(nil)

// NewService creates a taskrun service.
func NewService(r runner.Runner, opts ...Option) (*Service, error) {
	if r == nil {
		return nil, fmt.Errorf("taskrun: nil runner")
	}
	options := Options{
		Store: NewMemoryStore(),
		Clock: time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.Store == nil {
		options.Store = NewMemoryStore()
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}

	loaded, err := options.Store.Load(context.Background())
	if err != nil {
		return nil, err
	}
	s := &Service{
		runner:    r,
		store:     options.Store,
		observer:  options.Observer,
		finalizer: options.Finalizer,
		clock:     options.Clock,
		runs:      make(map[string]*Run, len(loaded)),
		running:   make(map[string]*runningRun),
		waiters:   make(map[string][]chan struct{}),
	}
	for _, run := range loaded {
		copied := cloneRun(run)
		if copied.ID == "" {
			continue
		}
		s.runs[copied.ID] = &copied
	}
	if normalizeLoadedRuns(s.runs, s.clock(), s.finalizer) {
		if err := s.persist(context.Background()); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Start starts the service lifecycle.
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

// Close cancels active runs and persists the latest state.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.stopAllRunning()
	s.wg.Wait()
	return s.persist(context.Background())
}

// Spawn implements Controller.
func (s *Service) Spawn(
	ctx context.Context,
	req SpawnRequest,
) (Run, error) {
	if s == nil {
		return Run{}, fmt.Errorf("taskrun: nil service")
	}
	if s.baseCtx == nil {
		return Run{}, ErrNotStarted
	}
	if err := validateSpawnRequest(req); err != nil {
		return Run{}, err
	}

	now := s.clock()
	runID := strings.TrimSpace(req.ID)
	if runID == "" {
		runID = uuid.NewString()
	}
	run := Run{
		ID:              runID,
		OwnerUserID:     strings.TrimSpace(req.OwnerUserID),
		ParentSessionID: strings.TrimSpace(req.ParentSessionID),
		ParentAppName:   strings.TrimSpace(req.ParentAppName),
		AppName:         appNameForSpawn(req),
		AgentName:       strings.TrimSpace(req.AgentName),
		Task:            strings.TrimSpace(req.Task),
		Status:          StatusQueued,
		Metadata:        cloneMetadata(req.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.mu.Lock()
	if s.runs[run.ID] != nil {
		s.mu.Unlock()
		return Run{}, ErrRunAlreadyExists
	}
	s.runs[run.ID] = runPtr(run)
	s.mu.Unlock()
	if err := s.persist(ctx); err != nil {
		s.mu.Lock()
		delete(s.runs, run.ID)
		s.mu.Unlock()
		return Run{}, err
	}

	view := cloneRun(run)
	s.notify(ctx, view)
	spawn := cloneSpawnRequest(req)
	s.wg.Add(1)
	go func(parent context.Context, runID string, spawn SpawnRequest) {
		defer s.wg.Done()
		s.execute(parent, runID, spawn)
	}(s.baseCtx, view.ID, spawn)
	return view, nil
}

// List implements Controller.
func (s *Service) List(
	ctx context.Context,
	filter ListFilter,
) ([]Run, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	runs := make([]Run, 0, len(s.runs))
	for _, item := range s.runs {
		if item == nil || !matchesFilter(*item, filter) {
			continue
		}
		runs = append(runs, cloneRun(*item))
	}
	sort.Slice(runs, func(i int, j int) bool {
		return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
	})
	return runs, nil
}

// Get implements Controller.
func (s *Service) Get(ctx context.Context, runID string) (*Run, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrRunNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	run := s.runs[strings.TrimSpace(runID)]
	if run == nil {
		return nil, ErrRunNotFound
	}
	view := cloneRun(*run)
	return &view, nil
}

// Cancel implements Controller.
func (s *Service) Cancel(
	ctx context.Context,
	runID string,
) (*Run, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, false, err
	}
	if s == nil {
		return nil, false, ErrRunNotFound
	}

	run, changed, finalize, err := s.markCanceled(strings.TrimSpace(runID))
	if err != nil || !changed {
		return run, changed, err
	}
	if finalize {
		run, err = s.finalizeCanceledRun(s.finalizerBaseContext(), run.ID)
		if err != nil {
			return nil, false, err
		}
	}
	if err := s.persist(ctx); err != nil {
		s.wake(run.ID)
		return nil, false, err
	}
	s.notify(ctx, *run)
	s.wake(run.ID)
	return run, true, nil
}

// Wait implements Controller.
func (s *Service) Wait(ctx context.Context, runID string) (*Run, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, ErrRunNotFound
	}
	runID = strings.TrimSpace(runID)
	for {
		s.mu.Lock()
		run := s.runs[runID]
		if run == nil {
			s.mu.Unlock()
			return nil, ErrRunNotFound
		}
		if run.Status.IsTerminal() {
			view := cloneRun(*run)
			s.mu.Unlock()
			return &view, nil
		}
		ch := make(chan struct{})
		s.waiters[runID] = append(s.waiters[runID], ch)
		s.mu.Unlock()

		select {
		case <-ch:
		case <-ctx.Done():
			s.removeWaiter(runID, ch)
			return nil, ctx.Err()
		}
	}
}

func validateSpawnRequest(req SpawnRequest) error {
	if strings.TrimSpace(req.OwnerUserID) == "" {
		return fmt.Errorf("taskrun: empty owner")
	}
	if strings.TrimSpace(req.ParentSessionID) == "" {
		return fmt.Errorf("taskrun: empty parent session id")
	}
	if strings.TrimSpace(req.Task) == "" {
		return fmt.Errorf("taskrun: empty task")
	}
	return nil
}

func appNameForSpawn(req SpawnRequest) string {
	runOpts := agent.NewRunOptions(req.RunOptions...)
	if appName := strings.TrimSpace(runOpts.AppName); appName != "" {
		return appName
	}
	return strings.TrimSpace(req.AppName)
}

func (s *Service) execute(
	parent context.Context,
	runID string,
	req SpawnRequest,
) {
	run, runCtx, cancel, err := s.markRunning(parent, runID, req)
	if err != nil {
		return
	}
	defer cancel()

	result := replyAccumulator{}
	progress := progressAccumulator{}
	runErr := s.runChild(runCtx, run, req, &result, &progress)
	output := trimResult(result.text)
	s.markExiting(runID)
	s.finishRun(runID, output, runErr, progress.snapshot())
}

func (s *Service) markRunning(
	parent context.Context,
	runID string,
	req SpawnRequest,
) (*Run, context.Context, context.CancelFunc, error) {
	s.mu.Lock()
	run := s.runs[strings.TrimSpace(runID)]
	if run == nil {
		s.mu.Unlock()
		return nil, nil, nil, ErrRunNotFound
	}
	if run.Status == StatusCanceled || run.Status == StatusCanceling {
		s.mu.Unlock()
		return nil, nil, nil, fmt.Errorf(
			"taskrun: run canceled before start",
		)
	}

	now := s.clock()
	childSessionID := strings.TrimSpace(req.ChildSessionID)
	if childSessionID == "" {
		childSessionID = newChildSessionID(run.ID, now)
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = newRequestID(run.ID, now)
	}

	runCtx := parent
	if runCtx == nil {
		runCtx = context.Background()
	}
	if req.RunContext != nil {
		if enriched := req.RunContext(runCtx); enriched != nil {
			runCtx = enriched
		}
	}
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, req.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(runCtx)
	}

	run.Status = StatusRunning
	run.ChildSessionID = childSessionID
	run.RequestID = requestID
	run.UpdatedAt = now
	run.StartedAt = cloneTime(now)
	run.FinishedAt = nil
	run.Error = ""
	run.Summary = ""
	run.Result = ""
	s.running[run.ID] = &runningRun{cancel: cancel}
	view := cloneRun(*run)
	s.mu.Unlock()

	if err := s.persist(context.Background()); err != nil {
		cancel()
		s.failPersistedRun(runID, err, now)
		return nil, nil, nil, err
	}
	s.notify(context.Background(), view)
	return &view, runCtx, cancel, nil
}

func (s *Service) runChild(
	ctx context.Context,
	run *Run,
	req SpawnRequest,
	result *replyAccumulator,
	progress *progressAccumulator,
) error {
	if run == nil {
		return fmt.Errorf("taskrun: nil run")
	}
	runOpts := append([]agent.RunOption(nil), req.RunOptions...)
	if run.AppName != "" {
		runOpts = append(runOpts, agent.WithAppName(run.AppName))
	}
	runOpts = append(runOpts,
		agent.WithRequestID(run.RequestID),
		agent.MergeRuntimeState(runtimeStateForRun(
			run,
			req.RuntimeState,
			req.RuntimeStateKeys,
		)),
	)
	if len(req.InjectedContextMessages) > 0 {
		runOpts = append(
			runOpts,
			agent.WithInjectedContextMessages(req.InjectedContextMessages),
		)
	}
	if run.AgentName != "" {
		runOpts = append(runOpts, agent.WithAgentByName(run.AgentName))
	}

	events, err := s.runner.Run(
		ctx,
		run.OwnerUserID,
		run.ChildSessionID,
		model.NewUserMessage(run.Task),
		runOpts...,
	)
	if err != nil {
		return err
	}
	for evt := range events {
		result.consume(evt)
		if progress != nil && progress.consume(evt, s.clock()) {
			s.updateProgress(run.ID, progress.snapshot())
		}
	}
	if result.err != nil {
		return result.err
	}
	return ctxErr(ctx)
}

func runtimeStateForRun(
	run *Run,
	extra map[string]any,
	keys RuntimeStateKeys,
) map[string]any {
	state := make(map[string]any, len(extra)+3)
	for key, value := range extra {
		state[key] = value
	}
	if run == nil {
		return state
	}
	keys = normalizeRuntimeStateKeys(keys)
	if keys.Run != "" {
		state[keys.Run] = true
	}
	if keys.RunID != "" {
		state[keys.RunID] = run.ID
	}
	if keys.ParentSessionID != "" {
		state[keys.ParentSessionID] = run.ParentSessionID
	}
	return state
}

func normalizeRuntimeStateKeys(keys RuntimeStateKeys) RuntimeStateKeys {
	if keys.Run != "" || keys.RunID != "" || keys.ParentSessionID != "" {
		return keys
	}
	return RuntimeStateKeys{
		Run:             RuntimeStateKeyRun,
		RunID:           RuntimeStateKeyRunID,
		ParentSessionID: RuntimeStateKeyParentSessionID,
	}
}

func (s *Service) markExiting(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	running := s.running[runID]
	if running == nil {
		return
	}
	running.exiting = true
}

func (s *Service) finishRun(
	runID string,
	output string,
	runErr error,
	progress *Progress,
) {
	s.mu.Lock()
	run := s.runs[runID]
	if run == nil {
		delete(s.running, runID)
		s.mu.Unlock()
		return
	}
	now := s.clock()
	view := finishedRunView(
		*run,
		output,
		runErr,
		progress,
		now,
	)
	*run = finalizingRunView(view, now)
	s.mu.Unlock()

	finalMetadata := s.finalizeRun(s.finalizerBaseContext(), view)

	s.mu.Lock()
	run = s.runs[runID]
	if run == nil {
		delete(s.running, runID)
		s.mu.Unlock()
		return
	}
	delete(s.running, runID)
	view.Metadata = mergeMetadata(view.Metadata, finalMetadata)
	view.UpdatedAt = s.clock()
	*run = cloneRun(view)
	s.mu.Unlock()

	if err := s.persist(context.Background()); err != nil {
		log.Warnf("taskrun: persist run %s failed: %v", runID, err)
		s.wake(runID)
		return
	}
	s.notify(context.Background(), view)
	s.wake(runID)
}

func finishedRunView(
	run Run,
	output string,
	runErr error,
	progress *Progress,
	now time.Time,
) Run {
	run = cloneRun(run)
	run.Result = output
	run.Progress = cloneProgress(progress)
	run.UpdatedAt = now
	run.FinishedAt = cloneTime(now)
	switch {
	case isCanceledRunResult(run, runErr):
		run.Status = StatusCanceled
		run.Error = ""
		run.Summary = summarizeText(statusCanceledSummary, 0)
	case errors.Is(runErr, context.DeadlineExceeded):
		run.Status = StatusFailed
		run.Error = runErr.Error()
		run.Summary = summarizeText(run.Error, defaultStoredSummaryRunes)
	case runErr != nil:
		run.Status = StatusFailed
		run.Error = runErr.Error()
		run.Summary = summarizeText(run.Error, defaultStoredSummaryRunes)
	default:
		run.Status = StatusCompleted
		run.Error = ""
		run.Summary = summarizeText(output, defaultStoredSummaryRunes)
	}
	return run
}

func isCanceledRunResult(run Run, runErr error) bool {
	if errors.Is(runErr, context.Canceled) {
		return true
	}
	return run.Status == StatusCanceling && runErr == nil
}

func (s *Service) updateProgress(runID string, progress *Progress) {
	if progress == nil {
		return
	}
	// Progress is an in-memory polling hint while the run is active.
	// Terminal updates persist the final snapshot and notify observers.
	s.mu.Lock()
	run := s.runs[runID]
	if run == nil || run.Status.IsTerminal() {
		s.mu.Unlock()
		return
	}
	run.Progress = cloneProgress(progress)
	s.mu.Unlock()
}

func finalizingRunView(run Run, now time.Time) Run {
	run = cloneRun(run)
	run.Status = StatusFinalizing
	run.UpdatedAt = now
	return run
}

func canceledRunView(run Run, now time.Time) Run {
	run = cloneRun(run)
	run.Status = StatusCanceled
	run.Error = ""
	run.Summary = summarizeText(statusCanceledSummary, 0)
	run.UpdatedAt = now
	run.FinishedAt = cloneTime(now)
	return run
}

func failedRunView(run Run, errText string, now time.Time) Run {
	run = cloneRun(run)
	run.Status = StatusFailed
	run.Error = errText
	run.Summary = summarizeText(run.Error, defaultStoredSummaryRunes)
	run.UpdatedAt = now
	run.FinishedAt = cloneTime(now)
	return run
}

func (s *Service) finalizeRun(ctx context.Context, run Run) map[string]string {
	if s == nil {
		return nil
	}
	return runFinalizer(ctx, s.finalizer, run)
}

func (s *Service) finalizerBaseContext() context.Context {
	if s == nil || s.baseCtx == nil {
		return context.Background()
	}
	return s.baseCtx
}

func runFinalizer(
	ctx context.Context,
	finalizer Finalizer,
	run Run,
) (metadata map[string]string) {
	if finalizer == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	finalCtx, cancel := context.WithTimeout(ctx, defaultFinalizerTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Warnf(
				"taskrun: finalizer panic for run %s: %v",
				run.ID,
				recovered,
			)
			metadata = nil
		}
	}()
	return finalizer.FinalizeRun(finalCtx, run)
}

func (s *Service) finalizeCanceledRun(
	ctx context.Context,
	runID string,
) (*Run, error) {
	s.mu.Lock()
	run := s.runs[runID]
	if run == nil {
		s.mu.Unlock()
		return nil, ErrRunNotFound
	}
	view := canceledRunView(*run, s.clock())
	s.mu.Unlock()

	finalMetadata := s.finalizeRun(ctx, view)

	s.mu.Lock()
	run = s.runs[runID]
	if run == nil {
		s.mu.Unlock()
		return nil, ErrRunNotFound
	}
	view = canceledRunView(*run, s.clock())
	view.Metadata = mergeMetadata(view.Metadata, finalMetadata)
	*run = cloneRun(view)
	s.mu.Unlock()
	return &view, nil
}

func (s *Service) markCanceled(runID string) (*Run, bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run := s.runs[runID]
	if run == nil {
		return nil, false, false, ErrRunNotFound
	}
	if run.Status.IsTerminal() {
		view := cloneRun(*run)
		return &view, false, false, nil
	}
	if run.Status == StatusCanceling {
		view := cloneRun(*run)
		return &view, false, false, nil
	}

	if running := s.running[run.ID]; running != nil {
		if running.exiting {
			view := cloneRun(*run)
			return &view, false, false, nil
		}
		running.cancelRequested = true
		if running.cancel != nil {
			running.cancel()
		}
		now := s.clock()
		run.Status = StatusCanceling
		run.Error = ""
		run.Summary = summarizeText(statusCancelingSummary, 0)
		run.UpdatedAt = now
		view := cloneRun(*run)
		return &view, true, false, nil
	}

	now := s.clock()
	run.Status = StatusCanceling
	run.Error = ""
	run.Summary = summarizeText(statusCancelingSummary, 0)
	run.UpdatedAt = now
	run.FinishedAt = nil
	view := cloneRun(*run)
	return &view, true, true, nil
}

func (s *Service) failPersistedRun(
	runID string,
	err error,
	now time.Time,
) {
	s.mu.Lock()
	run := s.runs[runID]
	if run == nil {
		delete(s.running, runID)
		s.mu.Unlock()
		return
	}
	running := s.running[runID]
	if running != nil {
		running.exiting = true
	}
	view := failedRunView(*run, err.Error(), now)
	*run = finalizingRunView(view, now)
	s.mu.Unlock()

	finalMetadata := s.finalizeRun(s.finalizerBaseContext(), view)

	s.mu.Lock()
	run = s.runs[runID]
	if run == nil {
		delete(s.running, runID)
		s.mu.Unlock()
		return
	}
	delete(s.running, runID)
	view.Metadata = mergeMetadata(view.Metadata, finalMetadata)
	view.UpdatedAt = s.clock()
	*run = cloneRun(view)
	s.mu.Unlock()
	if err := s.persist(context.Background()); err != nil {
		log.Warnf("taskrun: persist failed run %s failed: %v", runID, err)
		s.wake(runID)
		return
	}
	s.notify(context.Background(), view)
	s.wake(runID)
}

func (s *Service) stopAllRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, running := range s.running {
		if running == nil {
			delete(s.running, id)
			continue
		}
		if running.exiting {
			continue
		}
		running.cancelRequested = true
		if running.cancel != nil {
			running.cancel()
		}
	}
}

func (s *Service) persist(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	runs := make([]Run, 0, len(s.runs))
	for _, item := range s.runs {
		if item == nil || item.ID == "" {
			continue
		}
		runs = append(runs, cloneRun(*item))
	}
	s.mu.Unlock()
	return s.store.Save(ctx, runs)
}

func (s *Service) notify(ctx context.Context, run Run) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.OnRunUpdate(ctx, run)
}

func (s *Service) wake(runID string) {
	s.mu.Lock()
	waiters := s.waiters[runID]
	delete(s.waiters, runID)
	s.mu.Unlock()
	for _, waiter := range waiters {
		close(waiter)
	}
}

func (s *Service) removeWaiter(runID string, ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	waiters := s.waiters[runID]
	for i, waiter := range waiters {
		if waiter != ch {
			continue
		}
		waiters = append(waiters[:i], waiters[i+1:]...)
		break
	}
	if len(waiters) == 0 {
		delete(s.waiters, runID)
		return
	}
	s.waiters[runID] = waiters
}

func matchesFilter(run Run, filter ListFilter) bool {
	if filter.OwnerUserID != "" &&
		run.OwnerUserID != strings.TrimSpace(filter.OwnerUserID) {
		return false
	}
	if filter.ParentSessionID != "" &&
		run.ParentSessionID != strings.TrimSpace(filter.ParentSessionID) {
		return false
	}
	if filter.ParentAppName != "" &&
		run.ParentAppName != strings.TrimSpace(filter.ParentAppName) {
		return false
	}
	if filter.Status != "" && run.Status != filter.Status {
		return false
	}
	return true
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func mergeMetadata(base map[string]string, extra map[string]string) map[string]string {
	out := cloneMetadata(base)
	for key, value := range extra {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func cloneSpawnRequest(req SpawnRequest) SpawnRequest {
	out := req
	out.RuntimeState = cloneRuntimeState(req.RuntimeState)
	if req.RunOptions != nil {
		out.RunOptions = append([]agent.RunOption(nil), req.RunOptions...)
	}
	if req.InjectedContextMessages != nil {
		out.InjectedContextMessages = append(
			[]model.Message(nil),
			req.InjectedContextMessages...,
		)
	}
	out.Metadata = cloneMetadata(req.Metadata)
	return out
}

func cloneRuntimeState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	out := make(map[string]any, len(state))
	for key, value := range state {
		out[key] = value
	}
	return out
}

func runPtr(run Run) *Run {
	copied := run
	return &copied
}

func newChildSessionID(runID string, now time.Time) string {
	return fmt.Sprintf(
		"%s%s:%d",
		childSessionPrefix,
		strings.TrimSpace(runID),
		now.UnixNano(),
	)
}

func newRequestID(runID string, now time.Time) string {
	return fmt.Sprintf(
		"%s%s:%d",
		requestIDPrefix,
		strings.TrimSpace(runID),
		now.UnixNano(),
	)
}
