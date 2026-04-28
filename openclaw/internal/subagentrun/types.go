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
	"fmt"
	"strings"
	"time"

	publicsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
)

const (
	subagentDirName       = "subagents"
	subagentRunsFileName  = "runs.json"
	subagentSessionPrefix = "subagent:"
	subagentRequestPrefix = "subagent:"

	runtimeStateSubagentRun      = "openclaw.subagent.run"
	runtimeStateSubagentRunID    = "openclaw.subagent.run_id"
	runtimeStateSubagentParentID = "openclaw.subagent.parent_session_id"

	defaultStoredResultRunes  = 4000
	defaultStoredSummaryRunes = 240
	defaultNotifyTimeout      = 15 * time.Second

	notificationPrefixCompleted = "✅ subagent 已完成"
	notificationPrefixFailed    = "⚠️ subagent 失败"
	notificationPrefixCanceled  = "🛑 subagent 已取消"

	subagentRunPrompt = "You are running as an OpenClaw background " +
		"subagent. Complete the delegated task once. The parent " +
		"chat will receive your final result automatically. Keep " +
		"the result concise and action-oriented. Do not return " +
		"only a statement of what you will do; complete the " +
		"task and report the result or exact blocker. Do not " +
		"spawn more subagents from inside this subagent."
)

type deliveryTarget struct {
	Channel string `json:"channel,omitempty"`
	Target  string `json:"target,omitempty"`
}

type runRecord struct {
	publicsubagent.Run

	OwnerUserID string         `json:"owner_user_id,omitempty"`
	Delivery    deliveryTarget `json:"delivery,omitempty"`
}

type storeFile struct {
	Version int         `json:"version"`
	Runs    []runRecord `json:"runs,omitempty"`
}

type SpawnRequest struct {
	OwnerUserID     string
	ParentSessionID string
	Task            string
	TimeoutSeconds  int
	Delivery        deliveryTarget
}

func (r *runRecord) clone() *runRecord {
	if r == nil {
		return nil
	}
	out := *r
	if r.StartedAt != nil {
		startedAt := *r.StartedAt
		out.StartedAt = &startedAt
	}
	if r.FinishedAt != nil {
		finishedAt := *r.FinishedAt
		out.FinishedAt = &finishedAt
	}
	return &out
}

func (r *runRecord) publicView() publicsubagent.Run {
	if r == nil {
		return publicsubagent.Run{}
	}
	return r.Run
}

func newChildSessionID(runID string, now time.Time) string {
	return fmt.Sprintf(
		"%s%s:%d",
		subagentSessionPrefix,
		strings.TrimSpace(runID),
		now.UnixNano(),
	)
}

func newRequestID(runID string, now time.Time) string {
	return fmt.Sprintf(
		"%s%s:%d",
		subagentRequestPrefix,
		strings.TrimSpace(runID),
		now.UnixNano(),
	)
}

func sanitizeStoredResult(text string) string {
	return truncateRunes(text, defaultStoredResultRunes)
}

func summarizeResult(text string) string {
	return truncateRunes(text, defaultStoredSummaryRunes)
}

func truncateRunes(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if limit <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit])
}
