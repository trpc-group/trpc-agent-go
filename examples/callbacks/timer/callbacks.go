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
func (e *toolTimerExample) createBeforeAgentCallback() agent.BeforeAgentCallbackStructured {
	return func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		// Record start time and store it in invocation callback state.
		startTime := time.Now()
		args.Invocation.SetState("agent:start_time", startTime)

		// Create trace span for agent execution.
		_, span := atrace.Tracer.Start(
			ctx,
			"agent_execution",
			trace.WithAttributes(
				attribute.String("agent.name", args.Invocation.AgentName),
				attribute.String("invocation.id", args.Invocation.InvocationID),
				attribute.String("user.message", args.Invocation.Message.Content),
			),
		)
		// Store span in invocation callback state.
		args.Invocation.SetState("agent:span", span)

		fmt.Printf("⏱️  BeforeAgentCallback: %s started at %s\n", args.Invocation.AgentName, startTime.Format("15:04:05.000"))
		fmt.Printf("   InvocationID: %s\n", args.Invocation.InvocationID)
		fmt.Printf("   UserMsg: %q\n", args.Invocation.Message.Content)

		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback for timing.
func (e *toolTimerExample) createAfterAgentCallback() agent.AfterAgentCallbackStructured {
	return func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		// Get start time from invocation callback state.
		if startTimeVal, ok := args.Invocation.GetState("agent:start_time"); ok {
			startTime := startTimeVal.(time.Time)
			duration := time.Since(startTime)
			durationSeconds := duration.Seconds()

			// Record metrics.
			e.agentDurationHistogram.Record(ctx, durationSeconds,
				metric.WithAttributes(
					attribute.String("agent.name", args.Invocation.AgentName),
					attribute.String("invocation.id", args.Invocation.InvocationID),
				),
			)
			e.agentCounter.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("agent.name", args.Invocation.AgentName),
				),
			)

			// End trace span from invocation callback state.
			if spanVal, ok := args.Invocation.GetState("agent:span"); ok {
				span := spanVal.(trace.Span)
				if args.Error != nil {
					span.RecordError(args.Error)
				}
				status := "success"
				if args.Error != nil {
					status = "error"
				}
				span.SetAttributes(
					attribute.Float64("duration.seconds", durationSeconds),
					attribute.String("status", status),
				)
				span.End()
				// Clean up the span after use.
				args.Invocation.DeleteState("agent:span")
			}

			fmt.Printf("⏱️  AfterAgentCallback: %s completed in %v\n", args.Invocation.AgentName, duration)
			if args.Error != nil {
				fmt.Printf("   Error: %v\n", args.Error)
			}
			// Clean up the start time after use.
			args.Invocation.DeleteState("agent:start_time")
		} else {
			fmt.Printf("⏱️  AfterAgentCallback: %s completed (no timing info available)\n", args.Invocation.AgentName)
		}
		fmt.Println() // Add spacing after agent callback.

		return nil, nil // Return nil to use the original result.
	}
}

// createBeforeModelCallback creates the before model callback for timing.
func (e *toolTimerExample) createBeforeModelCallback() model.BeforeModelCallbackStructured {
	return func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
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
				attribute.Int("messages.count", len(args.Request.Messages)),
			),
		)
		// Store span in invocation callback state.
		inv.SetState("model:span", span)

		fmt.Printf("⏱️  BeforeModelCallback: model started at %s\n", startTime.Format("15:04:05.000"))
		fmt.Printf("   Messages: %d\n", len(args.Request.Messages))

		return nil, nil
	}
}

// createAfterModelCallback creates the after model callback for timing.
func (e *toolTimerExample) createAfterModelCallback() model.AfterModelCallbackStructured {
	return func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
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
					attribute.Int("messages.count", len(args.Request.Messages)),
				),
			)
			e.modelCounter.Add(ctx, 1)

			// End trace span from invocation callback state.
			if spanVal, ok := inv.GetState("model:span"); ok {
				span := spanVal.(trace.Span)
				if args.Error != nil {
					span.RecordError(args.Error)
				}
				status := "success"
				if args.Error != nil {
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
			if args.Error != nil {
				fmt.Printf("   Error: %v\n", args.Error)
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
func (e *toolTimerExample) createBeforeToolCallback() tool.BeforeToolCallbackStructured {
	return func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
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
		key := fmt.Sprintf("tool:%s:%s:start_time", args.ToolName, toolCallID)
		inv.SetState(key, startTime)

		// Create trace span for tool execution.
		_, span := atrace.Tracer.Start(
			ctx,
			"tool_execution",
			trace.WithAttributes(
				attribute.String("tool.name", args.ToolName),
				attribute.String("tool.call_id", toolCallID),
				attribute.String("tool.args", func() string {
					if args.Arguments == nil {
						return ""
					}
					return string(args.Arguments)
				}()),
			),
		)
		// Store span in invocation callback state.
		spanKey := fmt.Sprintf("tool:%s:%s:span", args.ToolName, toolCallID)
		inv.SetState(spanKey, span)

		fmt.Printf("⏱️  BeforeToolCallback: %s (call %s) started at %s\n",
			args.ToolName, toolCallID, startTime.Format("15:04:05.000"))
		if args.Arguments != nil {
			fmt.Printf("   Args: %s\n", string(args.Arguments))
		} else {
			fmt.Printf("   Args: <nil>\n")
		}

		return nil, nil
	}
}

// createAfterToolCallback creates the after tool callback for timing.
func (e *toolTimerExample) createAfterToolCallback() tool.AfterToolCallbackStructured {
	return func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
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
		key := fmt.Sprintf("tool:%s:%s:start_time", args.ToolName, toolCallID)
		if startTimeVal, ok := inv.GetState(key); ok {
			startTime := startTimeVal.(time.Time)
			duration := time.Since(startTime)
			durationSeconds := duration.Seconds()

			// Record metrics.
			e.toolDurationHistogram.Record(ctx, durationSeconds,
				metric.WithAttributes(
					attribute.String("tool.name", args.ToolName),
					attribute.String("tool.call_id", toolCallID),
				),
			)
			e.toolCounter.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("tool.name", args.ToolName),
				),
			)

			// End trace span from invocation callback state.
			spanKey := fmt.Sprintf("tool:%s:%s:span", args.ToolName, toolCallID)
			if spanVal, ok := inv.GetState(spanKey); ok {
				span := spanVal.(trace.Span)
				if args.Error != nil {
					span.RecordError(args.Error)
				}
				status := "success"
				if args.Error != nil {
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
				args.ToolName, toolCallID, duration)
			fmt.Printf("   Result: %v\n", args.Result)
			if args.Error != nil {
				fmt.Printf("   Error: %v\n", args.Error)
			}
			// Clean up the start time after use.
			inv.DeleteState(key)
		} else {
			fmt.Printf("⏱️  AfterToolCallback: %s (call %s) completed (no timing info available)\n",
				args.ToolName, toolCallID)
		}

		return nil, nil // Return nil to use the original result.
	}
}
