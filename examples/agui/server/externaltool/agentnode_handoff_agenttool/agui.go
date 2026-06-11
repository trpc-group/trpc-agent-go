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
	"fmt"

	agitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
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
		agui.WithGraphNodeInterruptActivityEnabled(true),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithRunOptionResolver(resolveRunOptions),
		),
		agui.WithMessagesSnapshotEnabled(true),
	)
}

func resolveRunOptions(_ context.Context, input *aguiadapter.RunAgentInput) ([]agent.RunOption, error) {
	if input == nil || len(input.Messages) == 0 {
		return nil, fmt.Errorf("run input must include at least one message")
	}
	last := input.Messages[len(input.Messages)-1]
	if last.Role != agitypes.RoleUser {
		return nil, fmt.Errorf("this handoff example expects a trailing user message")
	}
	options := []agent.RunOption{agent.WithGraphEmitFinalModelResponses(true)}
	runtimeState, err := resolveRuntimeState(input)
	if err != nil {
		return nil, err
	}
	if len(runtimeState) > 0 {
		options = append(options, agent.WithRuntimeState(runtimeState))
	}
	return options, nil
}

func resolveRuntimeState(input *aguiadapter.RunAgentInput) (map[string]any, error) {
	if input == nil || input.State == nil {
		return nil, nil
	}
	state, ok := input.State.(map[string]any)
	if !ok {
		return nil, errors.New("state must be an object")
	}
	runtimeState := make(map[string]any)
	if value, ok := state[graph.CfgKeyLineageID]; ok {
		lineageID, ok := value.(string)
		if !ok {
			return nil, errors.New("state.lineage_id must be a string")
		}
		runtimeState[graph.CfgKeyLineageID] = lineageID
	}
	if value, ok := state[graph.CfgKeyCheckpointID]; ok {
		checkpointID, ok := value.(string)
		if !ok {
			return nil, errors.New("state.checkpoint_id must be a string")
		}
		runtimeState[graph.CfgKeyCheckpointID] = checkpointID
	}
	if value, ok := state[graph.CfgKeyResumeMap]; ok {
		resumeMap, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("state.resume_map must be an object")
		}
		runtimeState[graph.StateKeyCommand] = &graph.Command{ResumeMap: resumeMap}
	}
	return runtimeState, nil
}
