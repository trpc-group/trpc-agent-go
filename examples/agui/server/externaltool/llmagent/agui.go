//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	demotool "trpc.group/trpc-go/trpc-agent-go/examples/agui/server/externaltool/llmagent/tool"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguiadapter "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var externalToolExecutionFilter = tool.NewExcludeToolNamesFilter(
	demotool.ExternalNoteName,
	demotool.ExternalApprovalName,
)

func newAGUIServer(run runner.Runner, sessionService session.Service) (*agui.Server, error) {
	return agui.New(
		run,
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithPath(*path),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithRunOptionResolver(resolveRunOptions),
		),
	)
}

func resolveRunOptions(_ context.Context, input *aguiadapter.RunAgentInput) ([]agent.RunOption, error) {
	if input == nil {
		return nil, errors.New("run input is nil")
	}
	if input.ThreadID == "" {
		return nil, errors.New("threadId is required")
	}
	if len(input.Messages) == 0 {
		return nil, errors.New("no messages provided")
	}
	return []agent.RunOption{
		agent.WithToolExecutionFilter(externalToolExecutionFilter),
	}, nil
}
