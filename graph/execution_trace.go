//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"slices"
	"sort"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
)

func filterNodeResponse(result any) any {
	if state, ok := stateFromAny(result); ok {
		return state.withoutInternalKeys()
	}
	output, _ := filterCommandUpdatesForTrace(result)
	return output
}

// filterCommandUpdatesForTrace copies command updates for tracing. Other node
// results pass through with their original JSON representation.
func filterCommandUpdatesForTrace(result any) (any, bool) {
	switch v := result.(type) {
	case Command:
		return commandForTrace(&v), true
	case *Command:
		if v == nil {
			// An absent command has no execution snapshot. A workflow span
			// still encodes this filtered value as JSON null.
			return nil, true
		}
		return commandForTrace(v), true
	case []*Command:
		commands := slices.Clone(v)
		for i, cmd := range commands {
			commands[i] = commandForTrace(cmd)
		}
		return commands, true
	default:
		return result, false
	}
}

func commandForTrace(cmd *Command) *Command {
	if cmd == nil {
		return nil
	}
	snapshot := *cmd
	snapshot.Update = cmd.Update.withoutInternalKeys()
	return &snapshot
}

type graphToolTraceRecorder struct {
	mu      sync.Mutex
	records []graphToolTraceRecord
}

type graphToolTraceRecord struct {
	index      int
	invocation *agent.Invocation
	stepID     string
	tool       atrace.Tool
}

func recordGraphToolExecutionTrace(
	ctx context.Context,
	invocation *agent.Invocation,
	stepID string,
	recorder *graphToolTraceRecorder,
	toolIndex int,
	toolName string,
	toolID string,
	arguments []byte,
	result any,
	resultContent []byte,
	traceErr error,
) {
	if invocation == nil || !invocation.RunOptions.ExecutionTraceEnabled || stepID == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	errorText := ""
	if traceErr != nil {
		errorText = traceErr.Error()
	}
	recordedTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:            toolID,
		Name:          toolName,
		Arguments:     arguments,
		Result:        result,
		ResultContent: resultContent,
		Error:         errorText,
	})
	if recorder != nil {
		recorder.add(graphToolTraceRecord{
			index:      toolIndex,
			invocation: invocation,
			stepID:     stepID,
			tool:       recordedTool,
		})
		return
	}
	addGraphToolExecutionTrace(ctx, invocation, stepID, recordedTool)
}

func (r *graphToolTraceRecorder) add(record graphToolTraceRecord) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.records = append(r.records, record)
	r.mu.Unlock()
}

func (r *graphToolTraceRecorder) flush(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	records := append([]graphToolTraceRecord(nil), r.records...)
	r.mu.Unlock()
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].index < records[j].index
	})
	for _, record := range records {
		addGraphToolExecutionTrace(
			ctx,
			record.invocation,
			record.stepID,
			record.tool,
		)
	}
}

func addGraphToolExecutionTrace(
	ctx context.Context,
	invocation *agent.Invocation,
	stepID string,
	recordedTool atrace.Tool,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	traceCtx := agent.NewInvocationContext(ctx, invocation)
	tracecapture.AddStepTools(traceCtx, stepID, []atrace.Tool{recordedTool})
	if skill, ok := tracecapture.LoadedSkillFromToolRecord(recordedTool); ok {
		tracecapture.AddStepSkill(traceCtx, stepID, skill)
	}
}
