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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
)

const (
	ScheduleKindAt    = "at"
	ScheduleKindEvery = "every"
	ScheduleKindCron  = "cron"
)

const (
	OverlapPolicySkip    = "skip"
	OverlapPolicyReplace = "replace"
)

const (
	runtimeStateScheduledRun = "openclaw.cron.scheduled_run"
	runtimeStateJobID        = "openclaw.cron.job_id"
	runtimeStateRunIndex     = "openclaw.cron.run_index"
	runtimeStateHasMaxRuns   = "openclaw.cron.has_max_runs"
	runtimeStateMaxRuns      = "openclaw.cron.max_runs"
	runtimeStateRemaining    = "openclaw.cron.remaining_runs"
	runtimeStateIsFinalRun   = "openclaw.cron.is_final_run"
)

const cronSessionPrefix = "cron:"

const (
	StatusIdle           = "idle"
	StatusRunning        = "running"
	StatusSucceeded      = "succeeded"
	StatusFailed         = "failed"
	StatusDeliveryFailed = "delivery_failed"
)

const (
	defaultTickInterval = time.Second

	defaultCronDir  = "cron"
	defaultJobsFile = "jobs.json"

	maxStoredOutputRunes = 2_000
)

// Schedule describes when a cron job should run.
type Schedule struct {
	Kind     string `json:"kind"`
	At       string `json:"at,omitempty"`
	Every    string `json:"every,omitempty"`
	EveryMS  int64  `json:"every_ms,omitempty"`
	CronExpr string `json:"cron_expr,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// ExecutionPolicy describes run bounds and overlap handling.
type ExecutionPolicy struct {
	MaxRuns       int        `json:"max_runs,omitempty"`
	EndsAt        *time.Time `json:"ends_at,omitempty"`
	OverlapPolicy string     `json:"overlap_policy,omitempty"`
}

// ExecutionStats tracks scheduler-visible run counters.
type ExecutionStats struct {
	RunCount             int `json:"run_count,omitempty"`
	SuccessCount         int `json:"success_count,omitempty"`
	FailureCount         int `json:"failure_count,omitempty"`
	DeliveryFailureCount int `json:"delivery_failure_count,omitempty"`
}

type scheduledRunTemplateData struct {
	Cron cronRunTemplateData
}

type cronRunTemplateData struct {
	RunIndex      int
	HasMaxRuns    bool
	MaxRuns       int
	RemainingRuns int
	IsFinalRun    bool
}

// Job is one persisted scheduled agent turn.
type Job struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name,omitempty"`
	Enabled    bool                    `json:"enabled"`
	Schedule   Schedule                `json:"schedule"`
	Policy     ExecutionPolicy         `json:"policy,omitempty"`
	Stats      ExecutionStats          `json:"stats,omitempty"`
	Message    string                  `json:"message"`
	UserID     string                  `json:"user_id"`
	TimeoutSec int                     `json:"timeout_sec,omitempty"`
	Delivery   outbound.DeliveryTarget `json:"delivery,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`

	LastStatus string `json:"last_status,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastOutput string `json:"last_output,omitempty"`
}

func (j *Job) clone() *Job {
	if j == nil {
		return nil
	}
	out := *j
	if j.LastRunAt != nil {
		last := *j.LastRunAt
		out.LastRunAt = &last
	}
	if j.NextRunAt != nil {
		next := *j.NextRunAt
		out.NextRunAt = &next
	}
	if j.Policy.EndsAt != nil {
		endsAt := *j.Policy.EndsAt
		out.Policy.EndsAt = &endsAt
	}
	return &out
}

func sanitizeStoredOutput(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxStoredOutputRunes {
		return string(runes)
	}
	return string(runes[:maxStoredOutputRunes])
}

// IsRunSessionID reports whether a session id belongs to cron execution.
func IsRunSessionID(sessionID string) bool {
	return strings.HasPrefix(
		strings.TrimSpace(sessionID),
		cronSessionPrefix,
	)
}

// ScheduleSummary returns a stable human-readable schedule summary.
func ScheduleSummary(schedule Schedule) string {
	switch strings.ToLower(strings.TrimSpace(schedule.Kind)) {
	case ScheduleKindAt:
		return "at " + strings.TrimSpace(schedule.At)
	case ScheduleKindEvery:
		if every := strings.TrimSpace(schedule.Every); every != "" {
			return "every " + every
		}
		if schedule.EveryMS > 0 {
			return fmt.Sprintf("every %dms", schedule.EveryMS)
		}
	case ScheduleKindCron:
		expr := strings.TrimSpace(schedule.CronExpr)
		if expr == "" {
			return ScheduleKindCron
		}
		tz := strings.TrimSpace(schedule.Timezone)
		if tz == "" {
			return "cron " + expr
		}
		return fmt.Sprintf("cron %s (%s)", expr, tz)
	}
	return strings.TrimSpace(schedule.Kind)
}

func freshRunSessionID(jobID string, now time.Time) string {
	return fmt.Sprintf(
		"%s%s:%d",
		cronSessionPrefix,
		jobID,
		now.UnixNano(),
	)
}

func freshRequestID(jobID string, now time.Time) string {
	return fmt.Sprintf("cron:%s:%d", jobID, now.UnixNano())
}

func normalizeOverlapPolicy(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return OverlapPolicySkip, nil
	}
	switch value {
	case OverlapPolicySkip, OverlapPolicyReplace:
		return value, nil
	default:
		return "", fmt.Errorf(
			"cron: unsupported overlap policy: %s",
			raw,
		)
	}
}

func cloneTimePtr(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	next := *src
	return &next
}

func scheduledRunContext(job *Job) cronRunTemplateData {
	runIndex := 0
	if job != nil {
		runIndex = job.Stats.RunCount
	}

	maxRuns := 0
	hasMaxRuns := false
	remainingRuns := 0
	isFinalRun := false
	if job != nil && job.Policy.MaxRuns > 0 {
		hasMaxRuns = true
		maxRuns = job.Policy.MaxRuns
		if maxRuns > runIndex {
			remainingRuns = maxRuns - runIndex
		}
		isFinalRun = runIndex >= maxRuns
	}

	return cronRunTemplateData{
		RunIndex:      runIndex,
		HasMaxRuns:    hasMaxRuns,
		MaxRuns:       maxRuns,
		RemainingRuns: remainingRuns,
		IsFinalRun:    isFinalRun,
	}
}
