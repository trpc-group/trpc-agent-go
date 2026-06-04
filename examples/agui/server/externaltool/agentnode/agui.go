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
	"fmt"
	"strings"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguiadapter "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func newAGUIServer(r runner.Runner, sessionService session.Service) (*agui.Server, error) {
	return agui.New(
		r,
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithPath(*path),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithRunOptionResolver(resolveRunOptions),
		),
		agui.WithGraphNodeInterruptActivityEnabled(true),
		agui.WithMessagesSnapshotEnabled(true),
	)
}

func resolveRunOptions(_ context.Context, input *aguiadapter.RunAgentInput) ([]agent.RunOption, error) {
	options := []agent.RunOption{agent.WithGraphEmitFinalModelResponses(true)}
	if input.Messages[len(input.Messages)-1].Role != types.RoleTool {
		return options, nil
	}
	last := input.Messages[len(input.Messages)-1]
	if len(input.Messages) > 1 && input.Messages[len(input.Messages)-2].Role == types.RoleTool {
		return nil, fmt.Errorf("expected exactly one trailing tool message")
	}
	props, ok := input.ForwardedProps.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("forwardedProps must be an object")
	}
	lineageID, _ := props[graph.CfgKeyLineageID].(string)
	checkpointID, _ := props[graph.CfgKeyCheckpointID].(string)
	lineageID = strings.TrimSpace(lineageID)
	checkpointID = strings.TrimSpace(checkpointID)
	if lineageID == "" {
		return nil, fmt.Errorf("missing forwardedProps.%s", graph.CfgKeyLineageID)
	}
	if checkpointID == "" {
		return nil, fmt.Errorf("missing forwardedProps.%s", graph.CfgKeyCheckpointID)
	}
	content, _ := last.ContentString()
	runtimeState := map[string]any{
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointID: checkpointID,
		graph.StateKeyCommand: &graph.Command{ResumeMap: map[string]any{
			last.ToolCallID: content,
		}},
	}
	return append(options, agent.WithRuntimeState(runtimeState)), nil
}
