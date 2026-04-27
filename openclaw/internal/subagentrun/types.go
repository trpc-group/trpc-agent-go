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

	coresubagent "trpc.group/trpc-go/trpc-agent-go/subagent"
)

const (
	subagentDirName      = "subagents"
	subagentRunsFileName = "runs.json"

	metadataDeliveryChannel = "openclaw.delivery.channel"
	metadataDeliveryTarget  = "openclaw.delivery.target"

	defaultNotifyTimeout = 15 * time.Second

	notificationPrefixCompleted = "✅ subagent 已完成"
	notificationPrefixFailed    = "⚠️ subagent 失败"

	subagentRunPrompt = "You are running as an OpenClaw background " +
		"subagent. Complete the delegated task once. The parent " +
		"chat will receive your final result automatically. Keep " +
		"the result concise and action-oriented. Do not spawn more " +
		"subagents from inside this subagent."
)

type deliveryTarget struct {
	Channel string `json:"channel,omitempty"`
	Target  string `json:"target,omitempty"`
}

type SpawnRequest struct {
	OwnerUserID     string
	ParentSessionID string
	Task            string
	TimeoutSeconds  int
	Delivery        deliveryTarget
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

func deliveryFromRun(run coresubagent.Run) deliveryTarget {
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
