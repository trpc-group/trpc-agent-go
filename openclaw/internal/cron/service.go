//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cron

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
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	runContextPrompt = "You are running an OpenClaw scheduled job. " +
		"Use exec_command for host commands. The final answer " +
		"will be delivered automatically to the job target. " +
		"Do not ask for confirmation unless blocked."
)

// Service runs and persists scheduled jobs.
type Service struct {
	path   string
	runner runner.Runner
	router *outbound.Router

	tickInterval time.Duration
	clock        func() time.Time

	mu      sync.Mutex
	jobs    map[string]*Job
	running map[string]struct{}

	startOnce sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
	wg        sync.WaitGroup
}

// Option customizes the cron service.
type Option func(*Service)

// WithTickInterval overrides the scheduler poll interval.
func WithTickInterval(interval time.Duration) Option {
	return func(s *Service) {
		if interval > 0 {
			s.tickInterval = interval
		}
	}
}

// WithClock overrides time.Now in tests.
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.clock = fn
		}
	}
}

// NewService creates a new scheduler backed by the given state dir.
func NewService(
	stateDir string,
	r runner.Runner,
	router *outbound.Router,
	opts ...Option,
) (*Service, error) {
	if r == nil {
		return nil, fmt.Errorf("cron: nil runner")
	}

	path := filepath.Join(stateDir, defaultCronDir, defaultJobsFile)
	loaded, err := loadJobs(path)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		path:         path,
		runner:       r,
		router:       router,
		tickInterval: defaultTickInterval,
		clock:        time.Now,
		jobs:         make(map[string]*Job),
		running:      make(map[string]struct{}),
		done:         make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}

	now := svc.clock()
	for _, job := range loaded {
		if job == nil || strings.TrimSpace(job.ID) == "" {
			continue
		}
		normalized, err := normalizeLoadedJob(job, now)
		if err != nil {
			log.Warnf("cron: skip invalid job %q: %v", job.ID, err)
			continue
		}
		svc.jobs[normalized.ID] = normalized
	}
	return svc, nil
}

// Start begins the background scheduler loop.
func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		s.cancel = cancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer close(s.done)
			s.loop(runCtx)
		}()
	})
}

// Close stops the scheduler and persists current state.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	select {
	case <-s.done:
	default:
	}
	s.wg.Wait()
	return s.persist()
}

// Status returns a scheduler summary.
func (s *Service) Status() map[string]any {
	if s == nil {
		return map[string]any{"running": false}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"running":      s.cancel != nil,
		"jobs":         len(s.jobs),
		"jobs_running": len(s.running),
		"channels":     s.channelsLocked(),
	}
}

// List returns a sorted snapshot of current jobs.
func (s *Service) List() []*Job {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedJobs(cloneJobs(mapJobs(s.jobs)))
}

// Add registers a new job.
func (s *Service) Add(job *Job) (*Job, error) {
	if s == nil {
		return nil, fmt.Errorf("cron: nil service")
	}

	now := s.clock()
	normalized, err := normalizeNewJob(job, now)
	if err != nil {
		return nil, err
	}
	normalized.ID = uuid.NewString()

	s.mu.Lock()
	s.jobs[normalized.ID] = normalized
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		return nil, err
	}
	return normalized.clone(), nil
}

// Update mutates an existing job.
func (s *Service) Update(
	jobID string,
	patch Patch,
) (*Job, error) {
	if s == nil {
		return nil, fmt.Errorf("cron: nil service")
	}

	now := s.clock()
	id := strings.TrimSpace(jobID)
	if id == "" {
		return nil, fmt.Errorf("cron: job id is required")
	}

	s.mu.Lock()
	current := s.jobs[id]
	if current == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("cron: unknown job: %s", id)
	}
	next := current.clone()
	s.mu.Unlock()

	if err := applyPatch(next, patch, now); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.jobs[id] = next
	s.mu.Unlock()
	if err := s.persist(); err != nil {
		return nil, err
	}
	return next.clone(), nil
}

// Remove deletes a job.
func (s *Service) Remove(jobID string) error {
	if s == nil {
		return fmt.Errorf("cron: nil service")
	}
	id := strings.TrimSpace(jobID)
	if id == "" {
		return fmt.Errorf("cron: job id is required")
	}

	s.mu.Lock()
	if _, ok := s.jobs[id]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("cron: unknown job: %s", id)
	}
	delete(s.jobs, id)
	delete(s.running, id)
	s.mu.Unlock()
	return s.persist()
}

// RunNow triggers a job immediately.
func (s *Service) RunNow(jobID string) (*Job, error) {
	if s == nil {
		return nil, fmt.Errorf("cron: nil service")
	}
	id := strings.TrimSpace(jobID)
	if id == "" {
		return nil, fmt.Errorf("cron: job id is required")
	}

	job, runCtx, err := s.markRunning(id)
	if err != nil {
		return nil, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.executeJob(runCtx, job)
	}()
	return job.clone(), nil
}

func (s *Service) loop(ctx context.Context) {
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()

	for {
		s.triggerDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) triggerDue(ctx context.Context) {
	now := s.clock()
	due := make([]*Job, 0)

	s.mu.Lock()
	for _, job := range s.jobs {
		if job == nil || !job.Enabled || job.NextRunAt == nil {
			continue
		}
		if _, busy := s.running[job.ID]; busy {
			continue
		}
		if job.NextRunAt.After(now) {
			continue
		}
		s.running[job.ID] = struct{}{}
		job.LastStatus = StatusRunning
		job.LastError = ""
		job.UpdatedAt = now
		due = append(due, job.clone())
	}
	s.mu.Unlock()

	if len(due) == 0 {
		return
	}
	if err := s.persist(); err != nil {
		log.Warnf("cron: persist running state: %v", err)
	}

	for _, job := range due {
		job := job
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.executeJob(ctx, job)
		}()
	}
}

func (s *Service) executeJob(ctx context.Context, job *Job) {
	now := s.clock()
	runCtx := ctx
	if job.TimeoutSec > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(
			ctx,
			time.Duration(job.TimeoutSec)*time.Second,
		)
		defer cancel()
	}

	runtimeState := outbound.RuntimeStateForTarget(job.Delivery)
	runOpts := make([]agent.RunOption, 0, 3)
	if runtimeState != nil {
		runOpts = append(runOpts, agent.WithRuntimeState(runtimeState))
	}
	runOpts = append(
		runOpts,
		agent.WithRequestID(freshRequestID(job.ID, now)),
		agent.WithInjectedContextMessages([]model.Message{
			model.NewSystemMessage(runContextPrompt),
		}),
	)

	sessionID := freshRunSessionID(job.ID, now)
	events, err := s.runner.Run(
		runCtx,
		job.UserID,
		sessionID,
		model.NewUserMessage(job.Message),
		runOpts...,
	)

	result := cronReplyAccumulator{}
	if err == nil {
		for evt := range events {
			result.consume(evt)
		}
		if result.err != nil {
			err = result.err
		}
	}

	deliveryErr := error(nil)
	output := sanitizeStoredOutput(result.text)
	if err == nil &&
		job.Delivery.Channel != "" &&
		job.Delivery.Target != "" &&
		strings.TrimSpace(result.text) != "" {
		if s.router == nil {
			deliveryErr = fmt.Errorf("cron: nil outbound router")
		} else {
			deliveryErr = s.router.SendText(
				runCtx,
				job.Delivery,
				result.text,
			)
		}
	}

	s.finishRun(job.ID, now, output, err, deliveryErr)
}

func (s *Service) finishRun(
	jobID string,
	now time.Time,
	output string,
	runErr error,
	deliveryErr error,
) {
	s.mu.Lock()
	job := s.jobs[jobID]
	if job == nil {
		delete(s.running, jobID)
		s.mu.Unlock()
		return
	}

	delete(s.running, jobID)
	job.LastRunAt = &now
	job.LastOutput = output
	job.UpdatedAt = now

	switch {
	case runErr != nil:
		job.LastStatus = StatusFailed
		job.LastError = runErr.Error()
	case deliveryErr != nil:
		job.LastStatus = StatusDeliveryFailed
		job.LastError = deliveryErr.Error()
	default:
		job.LastStatus = StatusSucceeded
		job.LastError = ""
	}

	next, err := computeNextAfterRun(job.Schedule, now)
	if err != nil {
		job.Enabled = false
		job.NextRunAt = nil
		job.LastStatus = StatusFailed
		job.LastError = err.Error()
	} else {
		job.NextRunAt = next
		if next == nil {
			job.Enabled = false
		}
	}
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		log.Warnf("cron: persist finished state: %v", err)
	}
}

func (s *Service) markRunning(
	jobID string,
) (*Job, context.Context, error) {
	s.mu.Lock()
	job := s.jobs[jobID]
	if job == nil {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("cron: unknown job: %s", jobID)
	}
	if _, busy := s.running[jobID]; busy {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("cron: job is already running")
	}
	s.running[jobID] = struct{}{}
	job.LastStatus = StatusRunning
	job.LastError = ""
	job.UpdatedAt = s.clock()
	clone := job.clone()
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		return nil, nil, err
	}
	return clone, context.Background(), nil
}

func (s *Service) persist() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	jobs := mapJobs(s.jobs)
	s.mu.Unlock()
	return saveJobs(s.path, jobs)
}

func (s *Service) channelsLocked() []string {
	if s.router == nil {
		return nil
	}
	return s.router.Channels()
}

func normalizeLoadedJob(job *Job, now time.Time) (*Job, error) {
	next := job.clone()
	if next == nil {
		return nil, fmt.Errorf("cron: nil job")
	}
	if strings.TrimSpace(next.ID) == "" {
		return nil, fmt.Errorf("cron: empty job id")
	}
	if _, err := normalizeCommon(next, false, now); err != nil {
		return nil, err
	}
	return next, nil
}

func normalizeNewJob(job *Job, now time.Time) (*Job, error) {
	next := &Job{}
	if job != nil {
		*next = *job
	}
	next.CreatedAt = now
	next.UpdatedAt = now
	return normalizeCommon(next, true, now)
}

func normalizeCommon(
	job *Job,
	defaultEnabled bool,
	now time.Time,
) (*Job, error) {
	if job == nil {
		return nil, fmt.Errorf("cron: nil job")
	}
	job.Name = strings.TrimSpace(job.Name)
	job.Message = strings.TrimSpace(job.Message)
	job.UserID = strings.TrimSpace(job.UserID)
	job.Delivery = outbound.DeliveryTarget{
		Channel: strings.TrimSpace(job.Delivery.Channel),
		Target:  strings.TrimSpace(job.Delivery.Target),
	}

	if job.Message == "" {
		return nil, fmt.Errorf("cron: message is required")
	}
	if job.UserID == "" {
		return nil, fmt.Errorf("cron: user id is required")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	if !job.Enabled && defaultEnabled {
		job.Enabled = true
	}
	if job.LastStatus == "" {
		job.LastStatus = StatusIdle
	}

	next, err := computeInitialNextRun(job.Schedule, now)
	if err != nil {
		return nil, err
	}
	if job.Enabled {
		job.NextRunAt = next
	} else {
		job.NextRunAt = nil
	}
	return job, nil
}

func mapJobs(items map[string]*Job) []*Job {
	if len(items) == 0 {
		return nil
	}
	out := make([]*Job, 0, len(items))
	for _, job := range items {
		if job == nil {
			continue
		}
		out = append(out, job.clone())
	}
	return out
}

func sortedJobs(jobs []*Job) []*Job {
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].ID < jobs[j].ID
	})
	return jobs
}

type cronReplyAccumulator struct {
	text     string
	builder  strings.Builder
	seenFull bool
	err      error
}

func (a *cronReplyAccumulator) consume(evt *event.Event) {
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

func (a *cronReplyAccumulator) consumeFull(rsp *model.Response) {
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

func (a *cronReplyAccumulator) consumeDelta(rsp *model.Response) {
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
