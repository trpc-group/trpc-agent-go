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
	if input == nil {
		return nil, errors.New("run input is nil")
	}
	if input.ThreadID == "" {
		return nil, errors.New("threadId is required")
	}
	if len(input.Messages) == 0 {
		return nil, errors.New("no messages provided")
	}
	last := input.Messages[len(input.Messages)-1]
	if last.Role != types.RoleUser && last.Role != types.RoleTool {
		return nil, errors.New("last message role must be user or tool")
	}
	runtimeState, err := graphRuntimeState(input, last)
	if err != nil {
		return nil, err
	}
	options := []agent.RunOption{agent.WithGraphEmitFinalModelResponses(true)}
	if len(runtimeState) > 0 {
		options = append(options, agent.WithRuntimeState(runtimeState))
	}
	return options, nil
}

func graphRuntimeState(input *aguiadapter.RunAgentInput, last types.Message) (map[string]any, error) {
	resumeRef, err := resumeRefFromForwardedProps(input.ForwardedProps)
	if err != nil {
		return nil, err
	}
	runtimeState := make(map[string]any)
	if resumeRef.lineageID != "" {
		runtimeState[graph.CfgKeyLineageID] = resumeRef.lineageID
	}
	if resumeRef.checkpointID != "" {
		runtimeState[graph.CfgKeyCheckpointID] = resumeRef.checkpointID
	}
	if last.Role != types.RoleTool {
		return runtimeState, nil
	}
	if resumeRef.lineageID == "" {
		return nil, fmt.Errorf("missing forwardedProps.%s", graph.CfgKeyLineageID)
	}
	if resumeRef.checkpointID == "" {
		return nil, fmt.Errorf("missing forwardedProps.%s", graph.CfgKeyCheckpointID)
	}
	resumeMap, err := toolResumeMapFromMessages(input.Messages)
	if err != nil {
		return nil, err
	}
	runtimeState[graph.StateKeyCommand] = &graph.Command{ResumeMap: resumeMap}
	return runtimeState, nil
}

type graphResumeRef struct {
	lineageID    string
	checkpointID string
}

func resumeRefFromForwardedProps(forwardedProps any) (graphResumeRef, error) {
	if forwardedProps == nil {
		return graphResumeRef{}, nil
	}
	props, ok := forwardedProps.(map[string]any)
	if !ok || props == nil {
		return graphResumeRef{}, errors.New("forwardedProps must be an object")
	}
	lineageID, err := forwardedString(props, graph.CfgKeyLineageID)
	if err != nil {
		return graphResumeRef{}, err
	}
	checkpointID, err := forwardedString(props, graph.CfgKeyCheckpointID)
	if err != nil {
		return graphResumeRef{}, err
	}
	return graphResumeRef{lineageID: lineageID, checkpointID: checkpointID}, nil
}

func forwardedString(props map[string]any, key string) (string, error) {
	rawValue, exists := props[key]
	if !exists {
		return "", nil
	}
	value, ok := rawValue.(string)
	if !ok {
		return "", fmt.Errorf("forwardedProps.%s must be a string", key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("forwardedProps.%s cannot be empty", key)
	}
	return value, nil
}

func toolResumeMapFromMessages(messages []types.Message) (map[string]any, error) {
	start := len(messages) - 1
	for start >= 0 && messages[start].Role == types.RoleTool {
		start--
	}
	resumeMap := make(map[string]any)
	for _, msg := range messages[start+1:] {
		if msg.ToolCallID == "" {
			return nil, errors.New("tool message missing toolCallId")
		}
		content, ok := msg.ContentString()
		if !ok {
			return nil, errors.New("tool message content must be a string")
		}
		if strings.TrimSpace(content) == "" {
			return nil, errors.New("tool message content cannot be empty")
		}
		resumeMap[msg.ToolCallID] = content
	}
	if len(resumeMap) == 0 {
		return nil, errors.New("no trailing tool messages found")
	}
	return resumeMap, nil
}
