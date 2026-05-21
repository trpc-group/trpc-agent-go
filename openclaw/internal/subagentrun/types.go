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
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	coretaskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
)

const (
	subagentDirName      = "subagents"
	subagentRunsFileName = "runs.json"
	subagentIDPrefix     = "subagent:"

	metadataDeliveryChannel = "openclaw.delivery.channel"
	metadataDeliveryTarget  = "openclaw.delivery.target"

	defaultNotifyTimeout = 15 * time.Second

	notificationPrefixCompleted = "✅ subagent 已完成"
	notificationPrefixFailed    = "⚠️ subagent 失败"

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

type SpawnRequest struct {
	OwnerUserID                    string
	ParentSessionID                string
	Task                           string
	TimeoutSeconds                 int
	Delivery                       deliveryTarget
	SuppressCompletionNotification bool
}

func subagentStorePath(stateDir string) string {
	return filepath.Join(
		strings.TrimSpace(stateDir),
		subagentDirName,
		subagentRunsFileName,
	)
}

func metadataForDelivery(target deliveryTarget) map[string]string {
	if target.Channel == "" || target.Target == "" {
		return nil
	}
	return map[string]string{
		metadataDeliveryChannel: target.Channel,
		metadataDeliveryTarget:  target.Target,
	}
}

func deliveryFromRun(run coretaskrun.Run) deliveryTarget {
	return deliveryTarget{
		Channel: strings.TrimSpace(run.Metadata[metadataDeliveryChannel]),
		Target:  strings.TrimSpace(run.Metadata[metadataDeliveryTarget]),
	}
}

func timeoutDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func newSubagentID() string {
	return subagentIDPrefix + uuid.NewString()
}

func subagentRuntimeStateKeys() coretaskrun.RuntimeStateKeys {
	return coretaskrun.RuntimeStateKeys{
		Run:             openclawsubagent.RuntimeStateKeyRun,
		RunID:           openclawsubagent.RuntimeStateKeyRunID,
		ParentSessionID: openclawsubagent.RuntimeStateKeyParentSessionID,
	}
}

func projectRun(run coretaskrun.Run) openclawsubagent.Run {
	return openclawsubagent.Run{
		ID:              run.ID,
		ParentSessionID: run.ParentSessionID,
		ChildSessionID:  run.ChildSessionID,
		Task:            run.Task,
		Status:          openclawsubagent.Status(run.Status),
		Summary:         run.Summary,
		Result:          run.Result,
		Error:           run.Error,
		CreatedAt:       run.CreatedAt,
		UpdatedAt:       run.UpdatedAt,
		StartedAt:       cloneTimePtr(run.StartedAt),
		FinishedAt:      cloneTimePtr(run.FinishedAt),
	}
}

func projectRunPtr(run *coretaskrun.Run) *openclawsubagent.Run {
	if run == nil {
		return nil
	}
	projected := projectRun(*run)
	return &projected
}

func projectRuns(runs []coretaskrun.Run) []openclawsubagent.Run {
	if len(runs) == 0 {
		return nil
	}
	out := make([]openclawsubagent.Run, 0, len(runs))
	for _, run := range runs {
		out = append(out, projectRun(run))
	}
	return out
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
