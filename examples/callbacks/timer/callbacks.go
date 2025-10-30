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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// createToolCallbacks creates and configures tool callbacks for timing.
func (e *toolTimerExample) createToolCallbacks() *tool.Callbacks {
	toolCallbacks := tool.NewCallbacks()
	toolCallbacks.RegisterBeforeTool(e.createBeforeToolCallback())
	toolCallbacks.RegisterAfterTool(e.createAfterToolCallback())
	return toolCallbacks
}

// createAgentCallbacks creates and configures agent callbacks for timing.
func (e *toolTimerExample) createAgentCallbacks() *agent.Callbacks {
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterBeforeAgent(e.createBeforeAgentCallback())
	agentCallbacks.RegisterAfterAgent(e.createAfterAgentCallback())
	return agentCallbacks
}

// createModelCallbacks creates and configures model callbacks for timing.
func (e *toolTimerExample) createModelCallbacks() *model.Callbacks {
	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(e.createBeforeModelCallback())
	modelCallbacks.RegisterAfterModel(e.createAfterModelCallback())
	return modelCallbacks
}

// createBeforeAgentCallback creates the before agent callback for timing.
func (e *toolTimerExample) createBeforeAgentCallback() agent.BeforeAgentCallback {
	return func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
		// Record start time and store it in invocation callback state.
		startTime := time.Now()
		invocation.SetState("agent:start_time", startTime)

		// Create trace span for agent execution.
		_, span := atrace.Tracer.Start(
			ctx,
			"agent_execution",
			trace.WithAttributes(
				attribute.String("agent.name", invocation.AgentName),
				attribute.String("invocation.id", invocation.InvocationID),
				attribute.String("user.message", invocation.Message.Content),
			),
		)
		// Store span in invocation callback state.
		invocation.SetState("agent:span", span)

		fmt.Printf("⏱️  BeforeAgentCallback: %s started at %s\n", invocation.AgentName, startTime.Format("15:04:05.000"))
		fmt.Printf("   InvocationID: %s\n", invocation.InvocationID)
		fmt.Printf("   UserMsg: %q\n", invocation.Message.Content)

		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback for timing.
func (e *toolTimerExample) createAfterAgentCallback() agent.AfterAgentCallback {
	return func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
		// Get start time from invocation callback state.
		if startTimeVal, ok := invocation.GetState("agent:start_time"); ok {
			startTime := startTimeVal.(time.Time)
			duration := time.Since(startTime)
			durationSeconds := duration.Seconds()

			// Record metrics.
			e.agentDurationHistogram.Record(ctx, durationSeconds,
				metric.WithAttributes(
					attribute.String("agent.name", invocation.AgentName),
					attribute.String("invocation.id", invocation.InvocationID),
				),
			)
			e.agentCounter.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("agent.name", invocation.AgentName),
				),
			)

			// End trace span from invocation callback state.
			if spanVal, ok := invocation.GetState("agent:span"); ok {
				span := spanVal.(trace.Span)
				if runErr != nil {
					span.RecordError(runErr)
				}
				status := "success"
				if runErr != nil {
					status = "error"
				}
				span.SetAttributes(
					attribute.Float64("duration.seconds", durationSeconds),
					attribute.String("status", status),
				)
				span.End()
				// Clean up the span after use.
				invocation.DeleteState("agent:span")
			}

			fmt.Printf("⏱️  AfterAgentCallback: %s completed in %v\n", invocation.AgentName, duration)
			if runErr != nil {
				fmt.Printf("   Error: %v\n", runErr)
			}
			// Clean up the start time after use.
			invocation.DeleteState("agent:start_time")
		} else {
			fmt.Printf("⏱️  AfterAgentCallback: %s completed (no timing info available)\n", invocation.AgentName)
		}
		fmt.Println() // Add spacing after agent callback.

		return nil, nil // Return nil to use the original result.
	}
}

// createBeforeModelCallback creates the before model callback for timing.
func (e *toolTimerExample) createBeforeModelCallback() model.BeforeModelCallback {
	return func(ctx context.Context, req *model.Request) (*model.Response, error) {
		// Get invocation from context.
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			return nil, nil
		}

		// Record start time and store it in invocation callback state.
		startTime := time.Now()
		inv.SetState("model:start_time", startTime)

		// Create trace span for model inference.
		_, span := atrace.Tracer.Start(
			ctx,
			"model_inference",
			trace.WithAttributes(
				attribute.Int("messages.count", len(req.Messages)),
			),
		)
		// Store span in invocation callback state.
		inv.SetState("model:span", span)

		fmt.Printf("⏱️  BeforeModelCallback: model started at %s\n", startTime.Format("15:04:05.000"))
		fmt.Printf("   Messages: %d\n", len(req.Messages))

		return nil, nil
	}
}

// createAfterModelCallback creates the after model callback for timing.
func (e *toolTimerExample) createAfterModelCallback() model.AfterModelCallback {
	return func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
		// Get invocation from context.
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			return nil, nil
		}

		// Get start time from invocation callback state.
		if startTimeVal, ok := inv.GetState("model:start_time"); ok {
			startTime := startTimeVal.(time.Time)
			duration := time.Since(startTime)
			durationSeconds := duration.Seconds()

			// Record metrics.
			e.modelDurationHistogram.Record(ctx, durationSeconds,
				metric.WithAttributes(
					attribute.Int("messages.count", len(req.Messages)),
				),
			)
			e.modelCounter.Add(ctx, 1)

			// End trace span from invocation callback state.
			if spanVal, ok := inv.GetState("model:span"); ok {
				span := spanVal.(trace.Span)
				if modelErr != nil {
					span.RecordError(modelErr)
				}
				status := "success"
				if modelErr != nil {
					status = "error"
				}
				span.SetAttributes(
					attribute.Float64("duration.seconds", durationSeconds),
					attribute.String("status", status),
				)
				span.End()
				// Clean up the span after use.
				inv.DeleteState("model:span")
			}

			fmt.Printf("⏱️  AfterModelCallback: model completed in %v\n", duration)
			if modelErr != nil {
				fmt.Printf("   Error: %v\n", modelErr)
			}
			// Clean up the start time after use.
			inv.DeleteState("model:start_time")
		} else {
			fmt.Printf("⏱️  AfterModelCallback: model completed (no timing info available)\n")
		}

		return nil, nil // Return nil to use the original result.
	}
}

// createBeforeToolCallback creates the before tool callback for timing.
func (e *toolTimerExample) createBeforeToolCallback() tool.BeforeToolCallback {
	return func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs *[]byte) (any, error) {
		// Get invocation from context.
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			return nil, nil
		}

		// Get tool call ID from context for concurrent tool call support.
		toolCallID, ok := tool.ToolCallIDFromContext(ctx)
		if !ok || toolCallID == "" {
			// Fallback: use "default" if tool call ID is not available.
			toolCallID = "default"
		}

		// Record start time and store it in invocation callback state.
		// Use tool call ID to ensure unique keys for concurrent calls.
		startTime := time.Now()
		key := fmt.Sprintf("tool:%s:%s:start_time", toolName, toolCallID)
		inv.SetState(key, startTime)

		// Create trace span for tool execution.
		_, span := atrace.Tracer.Start(
			ctx,
			"tool_execution",
			trace.WithAttributes(
				attribute.String("tool.name", toolName),
				attribute.String("tool.call_id", toolCallID),
				attribute.String("tool.args", func() string {
					if jsonArgs == nil {
						return ""
					}
					return string(*jsonArgs)
				}()),
			),
		)
		// Store span in invocation callback state.
		spanKey := fmt.Sprintf("tool:%s:%s:span", toolName, toolCallID)
		inv.SetState(spanKey, span)

		fmt.Printf("⏱️  BeforeToolCallback: %s (call %s) started at %s\n",
			toolName, toolCallID, startTime.Format("15:04:05.000"))
		if jsonArgs != nil {
			fmt.Printf("   Args: %s\n", string(*jsonArgs))
		} else {
			fmt.Printf("   Args: <nil>\n")
		}

		return nil, nil
	}
}

// createAfterToolCallback creates the after tool callback for timing.
func (e *toolTimerExample) createAfterToolCallback() tool.AfterToolCallback {
	return func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		// Get invocation from context.
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			return nil, nil
		}

		// Get tool call ID from context (must use same logic as BeforeToolCallback).
		toolCallID, ok := tool.ToolCallIDFromContext(ctx)
		if !ok || toolCallID == "" {
			toolCallID = "default"
		}

		// Get start time from invocation callback state.
		key := fmt.Sprintf("tool:%s:%s:start_time", toolName, toolCallID)
		if startTimeVal, ok := inv.GetState(key); ok {
			startTime := startTimeVal.(time.Time)
			duration := time.Since(startTime)
			durationSeconds := duration.Seconds()

			// Record metrics.
			e.toolDurationHistogram.Record(ctx, durationSeconds,
				metric.WithAttributes(
					attribute.String("tool.name", toolName),
					attribute.String("tool.call_id", toolCallID),
				),
			)
			e.toolCounter.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("tool.name", toolName),
				),
			)

			// End trace span from invocation callback state.
			spanKey := fmt.Sprintf("tool:%s:%s:span", toolName, toolCallID)
			if spanVal, ok := inv.GetState(spanKey); ok {
				span := spanVal.(trace.Span)
				if runErr != nil {
					span.RecordError(runErr)
				}
				status := "success"
				if runErr != nil {
					status = "error"
				}
				span.SetAttributes(
					attribute.Float64("duration.seconds", durationSeconds),
					attribute.String("status", status),
				)
				span.End()
				// Clean up the span after use.
				inv.DeleteState(spanKey)
			}

			fmt.Printf("⏱️  AfterToolCallback: %s (call %s) completed in %v\n",
				toolName, toolCallID, duration)
			fmt.Printf("   Result: %v\n", result)
			if runErr != nil {
				fmt.Printf("   Error: %v\n", runErr)
			}
			// Clean up the start time after use.
			inv.DeleteState(key)
		} else {
			fmt.Printf("⏱️  AfterToolCallback: %s (call %s) completed (no timing info available)\n",
				toolName, toolCallID)
		}

		return nil, nil // Return nil to use the original result.
	}
}
