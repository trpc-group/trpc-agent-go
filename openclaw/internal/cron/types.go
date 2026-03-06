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

// Job is one persisted scheduled agent turn.
type Job struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name,omitempty"`
	Enabled    bool                    `json:"enabled"`
	Schedule   Schedule                `json:"schedule"`
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
	return &out
}

func sanitizeStoredOutput(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxStoredOutputRunes {
		return string(runes)
	}
	return string(runes[:maxStoredOutputRunes])
}

func freshRunSessionID(jobID string, now time.Time) string {
	return fmt.Sprintf("cron:%s:%d", jobID, now.UnixNano())
}

func freshRequestID(jobID string, now time.Time) string {
	return fmt.Sprintf("cron:%s:%d", jobID, now.UnixNano())
}
