//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import (
	"bytes"
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// IsToolCallArgumentsJSONRepairEnabled reports whether tool call arguments JSON repair is enabled.
func IsToolCallArgumentsJSONRepairEnabled(invocation *agent.Invocation) bool {
	if invocation == nil {
		return false
	}
	enabled := invocation.RunOptions.ToolCallArgumentsJSONRepairEnabled
	return enabled != nil && *enabled
}

// RepairToolCallArguments returns repaired tool call arguments when the input is not valid JSON.
func RepairToolCallArguments(ctx context.Context, toolName string, arguments []byte) []byte {
	trimmed := bytes.TrimSpace(arguments)
	if len(trimmed) == 0 || json.Valid(trimmed) {
		return arguments
	}
	repaired, err := Repair(arguments)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Tool call arguments JSON repair failed for %s: %v",
			toolName,
			err,
		)
		return arguments
	}
	chosen, usedRepair := chooseToolCallArguments(arguments, repaired)
	if !usedRepair {
		log.ErrorfContext(
			ctx,
			"Tool call arguments JSON repair produced invalid JSON for %s",
			toolName,
		)
		return arguments
	}
	log.InfofContext(
		ctx,
		"Tool call arguments JSON repaired for %s",
		toolName,
	)
	return chosen
}

// chooseToolCallArguments prefers repaired when it is a non-empty JSON payload.
func chooseToolCallArguments(arguments []byte, repaired []byte) ([]byte, bool) {
	repairedTrimmed := bytes.TrimSpace(repaired)
	if len(repairedTrimmed) == 0 || !json.Valid(repairedTrimmed) {
		return arguments, false
	}
	return repaired, true
}

// RepairToolCallArgumentsInPlace repairs the tool call arguments in place when needed.
func RepairToolCallArgumentsInPlace(ctx context.Context, toolCall *model.ToolCall) {
	if toolCall == nil {
		return
	}
	toolCall.Function.Arguments = RepairToolCallArguments(
		ctx,
		toolCall.Function.Name,
		toolCall.Function.Arguments,
	)
}

// RepairToolCallsArgumentsInPlace repairs tool call arguments in place when needed.
func RepairToolCallsArgumentsInPlace(ctx context.Context, toolCalls []model.ToolCall) {
	for i := range toolCalls {
		RepairToolCallArgumentsInPlace(ctx, &toolCalls[i])
	}
}

// RepairResponseToolCallArgumentsInPlace repairs tool call arguments inside the response in place when needed.
func RepairResponseToolCallArgumentsInPlace(ctx context.Context, response *model.Response) {
	if response == nil || response.IsPartial {
		return
	}
	for i := range response.Choices {
		RepairToolCallsArgumentsInPlace(ctx, response.Choices[i].Message.ToolCalls)
		RepairToolCallsArgumentsInPlace(ctx, response.Choices[i].Delta.ToolCalls)
	}
}
