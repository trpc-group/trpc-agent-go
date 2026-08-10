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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
)

func recordGraphToolExecutionTrace(
	ctx context.Context,
	invocation *agent.Invocation,
	stepID string,
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
	traceCtx := agent.NewInvocationContext(ctx, invocation)
	tracecapture.AddStepTools(traceCtx, stepID, []atrace.Tool{recordedTool})
	if skill, ok := tracecapture.LoadedSkillFromToolRecord(recordedTool); ok {
		tracecapture.AddStepSkill(traceCtx, stepID, skill)
	}
}
