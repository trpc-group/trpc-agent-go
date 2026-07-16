//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	hookUpdateCount    = 10
	hookUpdateInterval = 100 * time.Millisecond
)

func newAGUIServer(agent *llmagent.LLMAgent, sessionService session.Service) (*agui.Server, func(), error) {
	coreRunner := runner.NewRunner(appName, agent, runner.WithSessionService(sessionService))
	server, err := agui.New(
		coreRunner,
		agui.WithAppName(appName),
		agui.WithPath(*path),
		agui.WithSessionService(sessionService),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithMessagesSnapshotPath(*messagesSnapshotPath),
		agui.WithRunHook(pushBackgroundReportStatus),
	)
	closeRunner := func() {
		_ = coreRunner.Close()
	}
	if err != nil {
		closeRunner()
		return nil, nil, err
	}
	return server, closeRunner, nil
}

func pushBackgroundReportStatus(ctx context.Context, run *aguirunner.Run) error {
	ticker := time.NewTicker(hookUpdateInterval)
	defer ticker.Stop()
	for step := 1; step <= hookUpdateCount; step++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		err := run.Emit(ctx, aguievents.NewCustomEvent(
			reportEventName,
			aguievents.WithValue(map[string]any{
				"progress": step * 100 / hookUpdateCount,
			}),
		))
		if err != nil {
			return err
		}
	}
	return nil
}
