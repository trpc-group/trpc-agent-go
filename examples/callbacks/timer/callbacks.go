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
func (e *toolTimerExample) createToolCallbacks() *tool.CallbacksStructured {
	toolCallbacks := tool.NewCallbacksStructured()
	toolCallbacks.RegisterBeforeTool(e.createBeforeToolCallback())
	toolCallbacks.RegisterAfterTool(e.createAfterToolCallback())
	return toolCallbacks
}

// createAgentCallbacks creates and configures agent callbacks for timing.
func (e *toolTimerExample) createAgentCallbacks() *agent.CallbacksStructured {
	agentCallbacks := agent.NewCallbacksStructured()
	agentCallbacks.RegisterBeforeAgent(e.createBeforeAgentCallback())
	agentCallbacks.RegisterAfterAgent(e.createAfterAgentCallback())
	return agentCallbacks
}

// createModelCallbacks creates and configures model callbacks for timing.
func (e *toolTimerExample) createModelCallbacks() *model.CallbacksStructured {
	modelCallbacks := model.NewCallbacksStructured()
	modelCallbacks.RegisterBeforeModel(e.createBeforeModelCallback())
	modelCallbacks.RegisterAfterModel(e.createAfterModelCallback())
	return modelCallbacks
}

// createBeforeAgentCallback creates the before agent callback for timing.
func (e *toolTimerExample) createBeforeAgentCallback() agent.BeforeAgentCallbackStructured {
	return func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		// Record start time and store it in the instance variable.
		startTime := time.Now()
		if e.agentStartTimes == nil {
			e.agentStartTimes = make(map[string]time.Time)
		}
		e.agentStartTimes[args.Invocation.InvocationID] = startTime

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
		// Store span in instance variable for later use.
		if e.agentSpans == nil {
			e.agentSpans = make(map[string]trace.Span)
		}
		e.agentSpans[args.Invocation.InvocationID] = span

		fmt.Printf("⏱️  BeforeAgentCallback: %s started at %s\n", args.Invocation.AgentName, startTime.Format("15:04:05.000"))
		fmt.Printf("   InvocationID: %s\n", args.Invocation.InvocationID)
		fmt.Printf("   UserMsg: %q\n", args.Invocation.Message.Content)

		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback for timing.
func (e *toolTimerExample) createAfterAgentCallback() agent.AfterAgentCallbackStructured {
	return func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		// Get start time from the instance variable.
		if startTime, exists := e.agentStartTimes[args.Invocation.InvocationID]; exists {
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

			// End trace span from instance variable.
			if span, exists := e.agentSpans[args.Invocation.InvocationID]; exists {
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
				delete(e.agentSpans, args.Invocation.InvocationID)
			}

			fmt.Printf("⏱️  AfterAgentCallback: %s completed in %v\n", args.Invocation.AgentName, duration)
			if args.Error != nil {
				fmt.Printf("   Error: %v\n", args.Error)
			}
			// Clean up the start time after use.
			delete(e.agentStartTimes, args.Invocation.InvocationID)
		} else {
			fmt.Printf("⏱️  AfterAgentCallback: %s completed (no timing info available)\n", args.Invocation.AgentName)
		}

		return nil, nil
	}
}

// createBeforeModelCallback creates the before model callback for timing.
func (e *toolTimerExample) createBeforeModelCallback() model.BeforeModelCallbackStructured {
	return func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		// Record start time and store it in the instance variable.
		startTime := time.Now()
		if e.modelStartTimes == nil {
			e.modelStartTimes = make(map[string]time.Time)
		}
		// Use a unique key for model timing.
		modelKey := fmt.Sprintf("model_%d", startTime.UnixNano())
		e.modelStartTimes[modelKey] = startTime
		e.currentModelKey = modelKey // Store the current model key.

		// Create trace span for model inference.
		_, span := atrace.Tracer.Start(
			ctx,
			"model_inference",
			trace.WithAttributes(
				attribute.Int("messages.count", len(args.Request.Messages)),
				attribute.String("model.key", modelKey),
			),
		)
		// Store span in instance variable for later use.
		if e.modelSpans == nil {
			e.modelSpans = make(map[string]trace.Span)
		}
		e.modelSpans[modelKey] = span

		fmt.Printf("⏱️  BeforeModelCallback: model started at %s\n", startTime.Format("15:04:05.000"))
		fmt.Printf("   ModelKey: %s\n", modelKey)
		fmt.Printf("   Messages: %d\n", len(args.Request.Messages))

		return nil, nil
	}
}

// createAfterModelCallback creates the after model callback for timing.
func (e *toolTimerExample) createAfterModelCallback() model.AfterModelCallbackStructured {
	return func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		// Use the stored model key.
		modelKey := e.currentModelKey

		// Get start time from the instance variable.
		if startTime, exists := e.modelStartTimes[modelKey]; exists {
			duration := time.Since(startTime)
			durationSeconds := duration.Seconds()

			// Record metrics.
			e.modelDurationHistogram.Record(ctx, durationSeconds,
				metric.WithAttributes(
					attribute.String("model.key", modelKey),
					attribute.Int("messages.count", len(args.Request.Messages)),
				),
			)
			e.modelCounter.Add(ctx, 1)

			// End trace span from instance variable.
			if span, exists := e.modelSpans[modelKey]; exists {
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
				delete(e.modelSpans, modelKey)
			}

			fmt.Printf("⏱️  AfterModelCallback: model completed in %v\n", duration)
			if args.Error != nil {
				fmt.Printf("   Error: %v\n", args.Error)
			}
			// Clean up the start time after use.
			delete(e.modelStartTimes, modelKey)
			e.currentModelKey = "" // Clear the current model key.
		} else {
			fmt.Printf("⏱️  AfterModelCallback: model completed (no timing info available)\n")
		}

		return nil, nil
	}
}

// createBeforeToolCallback creates the before tool callback for timing.
func (e *toolTimerExample) createBeforeToolCallback() tool.BeforeToolCallbackStructured {
	return func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		// Record start time and store it in the instance variable.
		startTime := time.Now()
		if e.toolStartTimes == nil {
			e.toolStartTimes = make(map[string]time.Time)
		}
		e.toolStartTimes[args.ToolName] = startTime

		// Create trace span for tool execution.
		_, span := atrace.Tracer.Start(
			ctx,
			"tool_execution",
			trace.WithAttributes(
				attribute.String("tool.name", args.ToolName),
				attribute.String("tool.args", func() string {
					if args.Arguments == nil {
						return ""
					}
					return string(args.Arguments)
				}()),
			),
		)
		// Store span in instance variable for later use.
		if e.toolSpans == nil {
			e.toolSpans = make(map[string]trace.Span)
		}
		e.toolSpans[args.ToolName] = span

		fmt.Printf("⏱️  BeforeToolCallback: %s started at %s\n", args.ToolName, startTime.Format("15:04:05.000"))
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
		// Get start time from the instance variable.
		if startTime, exists := e.toolStartTimes[args.ToolName]; exists {
			duration := time.Since(startTime)
			durationSeconds := duration.Seconds()

			// Record metrics.
			e.toolDurationHistogram.Record(ctx, durationSeconds,
				metric.WithAttributes(
					attribute.String("tool.name", args.ToolName),
				),
			)
			e.toolCounter.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("tool.name", args.ToolName),
				),
			)

			// End trace span from instance variable.
			if span, exists := e.toolSpans[args.ToolName]; exists {
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
				delete(e.toolSpans, args.ToolName)
			}

			fmt.Printf("⏱️  AfterToolCallback: %s completed in %v\n", args.ToolName, duration)
			fmt.Printf("   Result: %v\n", args.Result)
			if args.Error != nil {
				fmt.Printf("   Error: %v\n", args.Error)
			}
			// Clean up the start time after use.
			delete(e.toolStartTimes, args.ToolName)
		} else {
			fmt.Printf("⏱️  AfterToolCallback: %s completed (no timing info available)\n", args.ToolName)
		}

		return nil, nil
	}
}
