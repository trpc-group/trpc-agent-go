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
	"text/template"
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
		"The schedule and delivery are already handled. " +
		"Respect the provided scheduled run context, " +
		"including the current run index and whether this " +
		"is the final run. When the task mentions per-run " +
		"counters, remaining runs, or final-run-only wording, " +
		"resolve them from that context instead of treating " +
		"them as fixed literal text. " +
		"Execute the task once now. Use exec_command for host " +
		"commands. Adapt commands to the current OS when " +
		"needed instead of blindly following old shell " +
		"snippets. Do not create, update, remove, clear, or " +
		"run cron jobs from within this scheduled run. " +
		"Do not use message unless you need an additional " +
		"side message beyond the final result. The final " +
		"answer will be delivered automatically to the job " +
		"target. Do not ask for confirmation unless blocked."

	scheduledRunMessagePrefix = "Execute the following existing " +
		"scheduled job once now. Ignore any wording about " +
		"future scheduling or sending to the current chat, " +
		"because scheduling and delivery are already handled.\n\n"

	scheduledRunContextPrefix = "Scheduled run context:\n"

	scheduledRunTaskPrefix = "\nTask:\n"

	scheduledRunTemplateMarker = "{{"
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
	running map[string]*jobRun

	persistMu sync.Mutex

	startOnce sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
	wg        sync.WaitGroup
}

type jobRun struct {
	token            string
	cancel           context.CancelFunc
	suppressDelivery bool
	startedAt        time.Time
	requestID        string
	sessionID        string
}

type queuedRun struct {
	job         *Job
	runCtx      context.Context
	runToken    string
	scheduledAt time.Time
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
		running:      make(map[string]*jobRun),
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
	s.stopAllRuns(true)
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

// ListForUser returns current jobs owned by a specific user.
func (s *Service) ListForUser(
	userID string,
	delivery outbound.DeliveryTarget,
) []*Job {
	if s == nil {
		return nil
	}

	userID = strings.TrimSpace(userID)
	filter := normalizeDeliveryFilter(delivery)

	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		if !matchesJobScope(job, userID, filter) {
			continue
		}
		jobs = append(jobs, job.clone())
	}
	return sortedJobs(jobs)
}

// RemoveForUser deletes scoped jobs owned by a specific user.
func (s *Service) RemoveForUser(
	userID string,
	delivery outbound.DeliveryTarget,
) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("cron: nil service")
	}

	userID = strings.TrimSpace(userID)
	filter := normalizeDeliveryFilter(delivery)

	s.mu.Lock()
	removed := 0
	for id, job := range s.jobs {
		if !matchesJobScope(job, userID, filter) {
			continue
		}
		s.removeJobLocked(id, true)
		removed++
	}
	s.mu.Unlock()

	if removed == 0 {
		return 0, nil
	}
	if err := s.persist(); err != nil {
		return 0, err
	}
	return removed, nil
}

// Get returns one job snapshot by id.
func (s *Service) Get(jobID string) *Job {
	if s == nil {
		return nil
	}

	id := strings.TrimSpace(jobID)
	if id == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return nil
	}
	return job.clone()
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
	if !next.Enabled {
		s.suppressRunLocked(id)
	}
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
	s.removeJobLocked(id, true)
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

	job, runCtx, runToken, err := s.markRunning(
		id,
		context.Background(),
	)
	if err != nil {
		return nil, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.executeJob(
			runCtx,
			job,
			runToken,
			false,
			time.Time{},
		)
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
	runs := make([]queuedRun, 0)
	needsPersist := false

	s.mu.Lock()
	for _, job := range s.jobs {
		if job == nil || !job.Enabled {
			continue
		}
		if retireJobLocked(job, now) {
			needsPersist = true
			continue
		}
		if job.NextRunAt == nil || job.NextRunAt.After(now) {
			continue
		}
		if _, busy := s.running[job.ID]; busy {
			switch effectiveOverlapPolicy(job.Policy) {
			case OverlapPolicyReplace:
				s.cancelRunLocked(job.ID)
			default:
				continue
			}
		}
		runCtx, cancel := s.newRunContext(ctx)
		runToken := uuid.NewString()
		s.running[job.ID] = &jobRun{
			token:     runToken,
			cancel:    cancel,
			startedAt: now,
		}
		job.Stats.RunCount++
		job.LastStatus = StatusRunning
		job.LastError = ""
		job.UpdatedAt = now
		runs = append(runs, queuedRun{
			job:         job.clone(),
			runCtx:      runCtx,
			runToken:    runToken,
			scheduledAt: scheduledRunBase(job, now),
		})
		needsPersist = true
	}
	s.mu.Unlock()

	if len(runs) == 0 && !needsPersist {
		return
	}
	if err := s.persist(); err != nil {
		log.Warnf("cron: persist running state: %v", err)
	}

	for _, run := range runs {
		run := run
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.executeJob(
				run.runCtx,
				run.job,
				run.runToken,
				true,
				run.scheduledAt,
			)
		}()
	}
}

func (s *Service) executeJob(
	ctx context.Context,
	job *Job,
	runToken string,
	reschedule bool,
	scheduledAt time.Time,
) {
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

	runtimeState := scheduledRunRuntimeState(job)
	runOpts := make([]agent.RunOption, 0, 3)
	if runtimeState != nil {
		runOpts = append(runOpts, agent.WithRuntimeState(runtimeState))
	}
	requestID := freshRequestID(job.ID, now)
	runOpts = append(
		runOpts,
		agent.WithRequestID(requestID),
		agent.WithInjectedContextMessages([]model.Message{
			model.NewSystemMessage(runContextPrompt),
		}),
	)

	sessionID := freshRunSessionID(job.ID, now)
	s.setRunMetadata(job.ID, runToken, sessionID, requestID)
	events, err := s.runner.Run(
		runCtx,
		job.UserID,
		sessionID,
		model.NewUserMessage(buildScheduledRunMessage(job)),
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
		s.deliveryAllowed(job.ID, runToken) &&
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

	s.finishRun(
		job.ID,
		runToken,
		scheduledAt,
		now,
		output,
		err,
		deliveryErr,
		reschedule,
	)
}

func (s *Service) finishRun(
	jobID string,
	runToken string,
	scheduledAt time.Time,
	now time.Time,
	output string,
	runErr error,
	deliveryErr error,
	reschedule bool,
) {
	s.mu.Lock()
	job := s.jobs[jobID]
	if job == nil {
		delete(s.running, jobID)
		s.mu.Unlock()
		return
	}

	current := s.running[jobID]
	if current == nil || current.token != runToken {
		s.mu.Unlock()
		return
	}

	delete(s.running, jobID)
	job.LastRunAt = &now
	job.LastOutput = output
	job.UpdatedAt = now

	switch {
	case runErr != nil:
		job.Stats.FailureCount++
		job.LastStatus = StatusFailed
		job.LastError = runErr.Error()
	case deliveryErr != nil:
		job.Stats.DeliveryFailureCount++
		job.LastStatus = StatusDeliveryFailed
		job.LastError = deliveryErr.Error()
	default:
		job.Stats.SuccessCount++
		job.LastStatus = StatusSucceeded
		job.LastError = ""
	}

	if reschedule {
		nextBase := scheduledRunBase(job, scheduledAt)
		next, err := computeNextAfterRun(job.Schedule, nextBase, now)
		if err != nil {
			job.Enabled = false
			job.NextRunAt = nil
			job.LastStatus = StatusFailed
			job.LastError = err.Error()
		} else {
			applyNextRunPolicy(job, next, now)
		}
	} else {
		applyNextRunPolicy(job, cloneTimePtr(job.NextRunAt), now)
	}
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		log.Warnf("cron: persist finished state: %v", err)
	}
}

func (s *Service) markRunning(
	jobID string,
	parent context.Context,
) (*Job, context.Context, string, error) {
	s.mu.Lock()
	job := s.jobs[jobID]
	if job == nil {
		s.mu.Unlock()
		return nil, nil, "", fmt.Errorf(
			"cron: unknown job: %s",
			jobID,
		)
	}
	if _, busy := s.running[jobID]; busy {
		s.mu.Unlock()
		return nil, nil, "", fmt.Errorf(
			"cron: job is already running",
		)
	}
	if retireJobLocked(job, s.clock()) {
		s.mu.Unlock()
		return nil, nil, "", fmt.Errorf(
			"cron: job is no longer schedulable",
		)
	}
	runCtx, cancel := s.newRunContext(parent)
	now := s.clock()
	runToken := uuid.NewString()
	s.running[jobID] = &jobRun{
		token:     runToken,
		cancel:    cancel,
		startedAt: now,
	}
	job.Stats.RunCount++
	job.LastStatus = StatusRunning
	job.LastError = ""
	job.UpdatedAt = now
	clone := job.clone()
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		cancel()
		s.mu.Lock()
		delete(s.running, jobID)
		if current := s.jobs[jobID]; current != nil {
			current.LastStatus = StatusIdle
			current.LastError = ""
			current.UpdatedAt = now
			if current.Stats.RunCount > 0 {
				current.Stats.RunCount--
			}
		}
		s.mu.Unlock()
		return nil, nil, "", err
	}
	return clone, runCtx, runToken, nil
}

func (s *Service) newRunContext(
	parent context.Context,
) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithCancel(parent)
}

func (s *Service) setRunMetadata(
	jobID string,
	runToken string,
	sessionID string,
	requestID string,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run := s.running[jobID]
	if run == nil || run.token != runToken {
		return
	}
	run.sessionID = sessionID
	run.requestID = requestID
}

func (s *Service) deliveryAllowed(jobID string, runToken string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	run := s.running[jobID]
	if run == nil || run.token != runToken {
		return false
	}
	return !run.suppressDelivery
}

func (s *Service) suppressRunLocked(jobID string) {
	run := s.running[jobID]
	if run == nil {
		return
	}
	run.suppressDelivery = true
}

func (s *Service) cancelRunLocked(jobID string) {
	run := s.running[jobID]
	if run == nil {
		return
	}
	run.suppressDelivery = true
	if run.cancel != nil {
		run.cancel()
	}
}

func (s *Service) removeJobLocked(jobID string, cancel bool) {
	if cancel {
		s.cancelRunLocked(jobID)
	} else if _, ok := s.running[jobID]; ok {
		s.suppressRunLocked(jobID)
	} else {
		delete(s.running, jobID)
	}
	delete(s.jobs, jobID)
}

func (s *Service) stopAllRuns(cancel bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, run := range s.running {
		if run == nil {
			continue
		}
		run.suppressDelivery = true
		if cancel && run.cancel != nil {
			run.cancel()
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
	if err := normalizeFields(next, false, now); err != nil {
		return nil, err
	}
	if next.Enabled {
		if next.NextRunAt == nil || next.NextRunAt.IsZero() {
			runAt, err := computeInitialNextRun(next.Schedule, now)
			if err != nil {
				return nil, err
			}
			applyNextRunPolicy(next, runAt, now)
		} else {
			applyNextRunPolicy(
				next,
				cloneTimePtr(next.NextRunAt),
				now,
			)
		}
	} else {
		next.NextRunAt = nil
	}
	retireJobLocked(next, now)
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
	if err := normalizeFields(job, defaultEnabled, now); err != nil {
		return nil, err
	}

	next, err := computeInitialNextRun(job.Schedule, now)
	if err != nil {
		return nil, err
	}
	if !job.Enabled {
		job.NextRunAt = nil
		return job, nil
	}
	applyNextRunPolicy(job, next, now)
	return job, nil
}

func normalizeFields(
	job *Job,
	defaultEnabled bool,
	now time.Time,
) error {
	if job == nil {
		return fmt.Errorf("cron: nil job")
	}
	job.Name = strings.TrimSpace(job.Name)
	job.Message = strings.TrimSpace(job.Message)
	job.UserID = strings.TrimSpace(job.UserID)
	job.Delivery = outbound.DeliveryTarget{
		Channel: strings.TrimSpace(job.Delivery.Channel),
		Target:  strings.TrimSpace(job.Delivery.Target),
	}
	if job.Policy.EndsAt != nil && job.Policy.EndsAt.IsZero() {
		job.Policy.EndsAt = nil
	}
	overlap, err := normalizeOverlapPolicy(job.Policy.OverlapPolicy)
	if err != nil {
		return err
	}
	job.Policy.OverlapPolicy = overlap

	if job.Message == "" {
		return fmt.Errorf("cron: message is required")
	}
	if job.UserID == "" {
		return fmt.Errorf("cron: user id is required")
	}
	if job.Policy.MaxRuns < 0 {
		return fmt.Errorf("cron: max_runs must be non-negative")
	}
	if job.Stats.RunCount < 0 ||
		job.Stats.SuccessCount < 0 ||
		job.Stats.FailureCount < 0 ||
		job.Stats.DeliveryFailureCount < 0 {
		return fmt.Errorf("cron: execution stats must be non-negative")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if !job.Enabled && defaultEnabled {
		job.Enabled = true
	}
	if job.LastStatus == "" || job.LastStatus == StatusRunning {
		job.LastStatus = StatusIdle
	}
	if _, err := computeNextRun(job.Schedule, now); err != nil {
		return err
	}
	job.UpdatedAt = now
	return nil
}

func effectiveOverlapPolicy(policy ExecutionPolicy) string {
	value, err := normalizeOverlapPolicy(policy.OverlapPolicy)
	if err != nil {
		return OverlapPolicySkip
	}
	return value
}

func retireJobLocked(job *Job, now time.Time) bool {
	if job == nil || !job.Enabled {
		return false
	}
	if executionLimitReached(job) || executionWindowClosed(job, now) {
		job.Enabled = false
		job.NextRunAt = nil
		job.UpdatedAt = now
		return true
	}
	return false
}

func executionLimitReached(job *Job) bool {
	if job == nil {
		return false
	}
	return job.Policy.MaxRuns > 0 &&
		job.Stats.RunCount >= job.Policy.MaxRuns
}

func executionWindowClosed(job *Job, now time.Time) bool {
	if job == nil || job.Policy.EndsAt == nil {
		return false
	}
	return !job.Policy.EndsAt.After(now)
}

func nextRunAllowed(job *Job, next *time.Time, now time.Time) bool {
	if executionLimitReached(job) || executionWindowClosed(job, now) {
		return false
	}
	if next == nil || job == nil || job.Policy.EndsAt == nil {
		return true
	}
	return next.Before(*job.Policy.EndsAt)
}

func applyNextRunPolicy(
	job *Job,
	next *time.Time,
	now time.Time,
) {
	if job == nil {
		return
	}
	if !nextRunAllowed(job, next, now) {
		job.Enabled = false
		job.NextRunAt = nil
		return
	}
	job.NextRunAt = next
	if next == nil {
		job.Enabled = false
	}
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

func normalizeDeliveryFilter(
	target outbound.DeliveryTarget,
) outbound.DeliveryTarget {
	return outbound.DeliveryTarget{
		Channel: strings.TrimSpace(target.Channel),
		Target:  strings.TrimSpace(target.Target),
	}
}

func matchesJobScope(
	job *Job,
	userID string,
	delivery outbound.DeliveryTarget,
) bool {
	if job == nil || strings.TrimSpace(job.UserID) != userID {
		return false
	}
	if delivery.Channel != "" &&
		job.Delivery.Channel != delivery.Channel {
		return false
	}
	if delivery.Target != "" &&
		job.Delivery.Target != delivery.Target {
		return false
	}
	return true
}

func scheduledRunBase(job *Job, fallback time.Time) time.Time {
	if job != nil && job.NextRunAt != nil && !job.NextRunAt.IsZero() {
		return *job.NextRunAt
	}
	return fallback
}

func scheduledRunRuntimeState(job *Job) map[string]any {
	runtimeState := outbound.RuntimeStateForTarget(job.Delivery)
	if runtimeState == nil {
		runtimeState = make(map[string]any, 7)
	}
	runContext := scheduledRunContext(job)
	runtimeState[runtimeStateScheduledRun] = true
	runtimeState[runtimeStateJobID] = strings.TrimSpace(job.ID)
	runtimeState[runtimeStateRunIndex] = runContext.RunIndex
	runtimeState[runtimeStateHasMaxRuns] = runContext.HasMaxRuns
	runtimeState[runtimeStateMaxRuns] = runContext.MaxRuns
	runtimeState[runtimeStateRemaining] = runContext.RemainingRuns
	runtimeState[runtimeStateIsFinalRun] = runContext.IsFinalRun
	return runtimeState
}

func buildScheduledRunMessage(job *Job) string {
	runContext := scheduledRunContext(job)
	task := ""
	if job != nil {
		task = strings.TrimSpace(job.Message)
	}
	renderedTask := renderScheduledRunTask(task, runContext)

	var builder strings.Builder
	builder.WriteString(scheduledRunMessagePrefix)
	builder.WriteString(scheduledRunContextPrefix)
	fmt.Fprintf(&builder, "- run_index: %d\n", runContext.RunIndex)
	fmt.Fprintf(
		&builder,
		"- has_max_runs: %t\n",
		runContext.HasMaxRuns,
	)
	fmt.Fprintf(&builder, "- max_runs: %d\n", runContext.MaxRuns)
	fmt.Fprintf(
		&builder,
		"- remaining_runs: %d\n",
		runContext.RemainingRuns,
	)
	fmt.Fprintf(
		&builder,
		"- is_final_run: %t\n",
		runContext.IsFinalRun,
	)
	builder.WriteString(scheduledRunTaskPrefix)
	builder.WriteString(renderedTask)
	return builder.String()
}

func renderScheduledRunTask(
	task string,
	runContext cronRunTemplateData,
) string {
	trimmedTask := strings.TrimSpace(task)
	if trimmedTask == "" {
		return ""
	}
	if !strings.Contains(trimmedTask, scheduledRunTemplateMarker) {
		return trimmedTask
	}

	tmpl, err := template.New("cron_task").
		Option("missingkey=error").
		Parse(trimmedTask)
	if err != nil {
		log.Warnf("cron: parse task template failed: %v", err)
		return trimmedTask
	}

	data := scheduledRunTemplateData{Cron: runContext}
	var builder strings.Builder
	if err := tmpl.Execute(&builder, data); err != nil {
		log.Warnf("cron: execute task template failed: %v", err)
		return trimmedTask
	}
	return strings.TrimSpace(builder.String())
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
