//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type streamDrainResult struct {
	chunks    []tool.StreamChunk
	snapshot  outputSnapshot
	violation *outputViolation
	err       error
}

func (wrapper *executionWrapper) stream(
	ctx context.Context,
	arguments []byte,
) (*tool.StreamReader, error) {
	report, err := wrapper.precheck(ctx, arguments)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(normalizeContext(ctx), wrapper.guard.policy.maxTimeout)
	defer cancel()
	reader, err := wrapper.streamable.StreamableCall(runCtx, arguments)
	if err != nil {
		return nil, wrapper.inspectToolError(ctx, report, err)
	}
	if reader == nil {
		return nil, wrapper.rejectOutput(ctx, report, uninspectableOutputViolation())
	}
	result := drainStream(reader, wrapper.guard.policy.maxOutputBytes)
	reader.Close()
	if result.err != nil {
		return nil, wrapper.inspectToolError(ctx, report, result.err)
	}
	if result.violation != nil {
		return nil, wrapper.rejectOutput(ctx, report, *result.violation)
	}
	if err := wrapper.inspectOutput(ctx, report, result.snapshot); err != nil {
		return nil, err
	}
	return replayStream(result.chunks), nil
}

func drainStream(reader *tool.StreamReader, maxBytes int64) streamDrainResult {
	result := streamDrainResult{chunks: make([]tool.StreamChunk, 0)}
	var serialized []byte
	var sensitive strings.Builder
	for {
		chunk, err := reader.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.err = err
			return result
		}
		encoded, err := json.Marshal(chunk)
		if err != nil {
			violation := uninspectableOutputViolation()
			result.violation = &violation
			return result
		}
		serialized = append(serialized, encoded...)
		appendSensitiveChunk(&sensitive, chunk)
		result.chunks = append(result.chunks, chunk)
		if int64(len(serialized)) > maxBytes {
			violation := outputLimitViolation()
			result.violation = &violation
			return result
		}
	}
	result.snapshot = outputSnapshot{
		serialized: serialized,
		sensitive:  sensitive.String(),
	}
	return result
}

func appendSensitiveChunk(builder *strings.Builder, chunk tool.StreamChunk) {
	if text, ok := chunk.Content.(string); ok {
		builder.WriteString(text)
		return
	}
	encoded, err := json.Marshal(chunk.Content)
	if err == nil {
		builder.Write(encoded)
	}
}

func replayStream(chunks []tool.StreamChunk) *tool.StreamReader {
	stream := tool.NewStream(len(chunks))
	for _, chunk := range chunks {
		stream.Writer.Send(chunk, nil)
	}
	stream.Writer.Close()
	return stream.Reader
}

func outputLimitViolation() outputViolation {
	return outputViolation{
		ruleID:         "RESOURCE_OUTPUT_LIMIT_EXCEEDED",
		riskLevel:      RiskLevelHigh,
		decision:       DecisionDeny,
		evidence:       "tool output exceeds the configured byte limit",
		recommendation: "reduce output size or use a bounded result format",
	}
}

func uninspectableOutputViolation() outputViolation {
	return outputViolation{
		ruleID:         "TOOL_OUTPUT_UNINSPECTABLE",
		riskLevel:      RiskLevelHigh,
		decision:       DecisionNeedsHumanReview,
		evidence:       "tool output could not be serialized for safety checks",
		recommendation: "return a JSON-serializable result and review the tool",
	}
}
