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
		// Get callback message from context.
		msg := agent.CallbackMessage(ctx)
		if msg == nil {
			fmt.Println("⚠️  Warning: callback message not found in context")
			return nil, nil
		}

		// Record start time and store it in the callback message.
		startTime := time.Now()
		msg.Set("agent_start_time", startTime)

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
		// Store span in callback message for later use.
		msg.Set("agent_span", span)

		fmt.Printf("⏱️  BeforeAgentCallback: %s started at %s\n", invocation.AgentName, startTime.Format("15:04:05.000"))
		fmt.Printf("   InvocationID: %s\n", invocation.InvocationID)
		fmt.Printf("   UserMsg: %q\n", invocation.Message.Content)

		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback for timing.
func (e *toolTimerExample) createAfterAgentCallback() agent.AfterAgentCallback {
	return func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
		// Get callback message from context.
		msg := agent.CallbackMessage(ctx)
		if msg == nil {
			fmt.Printf("⏱️  AfterAgentCallback: %s completed (no timing info available)\n", invocation.AgentName)
			return nil, nil
		}

		// Get start time from callback message.
		startTimeVal, ok := msg.Get("agent_start_time")
		if !ok {
			fmt.Printf("⏱️  AfterAgentCallback: %s completed (no timing info available)\n", invocation.AgentName)
			return nil, nil
		}

		startTime, ok := startTimeVal.(time.Time)
		if !ok {
			fmt.Printf("⏱️  AfterAgentCallback: %s completed (invalid timing info)\n", invocation.AgentName)
			return nil, nil
		}

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

		// End trace span from callback message.
		if spanVal, ok := msg.Get("agent_span"); ok {
			if span, ok := spanVal.(trace.Span); ok {
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
			}
		}

		fmt.Printf("⏱️  AfterAgentCallback: %s completed in %v\n", invocation.AgentName, duration)
		if runErr != nil {
			fmt.Printf("   Error: %v\n", runErr)
		}

		return nil, nil
	}
}

// createBeforeModelCallback creates the before model callback for timing.
func (e *toolTimerExample) createBeforeModelCallback() model.BeforeModelCallback {
	return func(ctx context.Context, req *model.Request) (*model.Response, error) {
		// Get callback message from context.
		msg := model.CallbackMessage(ctx)
		if msg == nil {
			fmt.Println("⚠️  Warning: callback message not found in context")
			return nil, nil
		}

		// Record start time and store it in the callback message.
		startTime := time.Now()
		msg.Set("model_start_time", startTime)

		// Create trace span for model inference.
		_, span := atrace.Tracer.Start(
			ctx,
			"model_inference",
			trace.WithAttributes(
				attribute.Int("messages.count", len(req.Messages)),
			),
		)
		// Store span in callback message for later use.
		msg.Set("model_span", span)

		fmt.Printf("⏱️  BeforeModelCallback: model started at %s\n", startTime.Format("15:04:05.000"))
		fmt.Printf("   Messages: %d\n", len(req.Messages))

		return nil, nil
	}
}

// createAfterModelCallback creates the after model callback for timing.
func (e *toolTimerExample) createAfterModelCallback() model.AfterModelCallback {
	return func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
		// Get callback message from context.
		msg := model.CallbackMessage(ctx)
		if msg == nil {
			fmt.Println("⏱️  AfterModelCallback: model completed (no timing info available)")
			return nil, nil
		}

		// Get start time from callback message.
		startTimeVal, ok := msg.Get("model_start_time")
		if !ok {
			fmt.Println("⏱️  AfterModelCallback: model completed (no timing info available)")
			return nil, nil
		}

		startTime, ok := startTimeVal.(time.Time)
		if !ok {
			fmt.Println("⏱️  AfterModelCallback: model completed (invalid timing info)")
			return nil, nil
		}

		duration := time.Since(startTime)
		durationSeconds := duration.Seconds()

		// Record metrics.
		e.modelDurationHistogram.Record(ctx, durationSeconds,
			metric.WithAttributes(
				attribute.Int("messages.count", len(req.Messages)),
			),
		)
		e.modelCounter.Add(ctx, 1)

		// End trace span from callback message.
		if spanVal, ok := msg.Get("model_span"); ok {
			if span, ok := spanVal.(trace.Span); ok {
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
			}
		}

		fmt.Printf("⏱️  AfterModelCallback: model completed in %v\n", duration)
		if modelErr != nil {
			fmt.Printf("   Error: %v\n", modelErr)
		}

		return nil, nil
	}
}

// createBeforeToolCallback creates the before tool callback for timing.
func (e *toolTimerExample) createBeforeToolCallback() tool.BeforeToolCallback {
	return func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs *[]byte) (any, error) {
		// Get callback message from context.
		msg := tool.CallbackMessage(ctx)
		if msg == nil {
			fmt.Println("⚠️  Warning: callback message not found in context")
			return nil, nil
		}

		// Record start time and store it in the callback message.
		startTime := time.Now()
		msg.Set("tool_start_time", startTime)

		// Create trace span for tool execution.
		_, span := atrace.Tracer.Start(
			ctx,
			"tool_execution",
			trace.WithAttributes(
				attribute.String("tool.name", toolName),
				attribute.String("tool.args", func() string {
					if jsonArgs == nil {
						return ""
					}
					return string(*jsonArgs)
				}()),
			),
		)
		// Store span in callback message for later use.
		msg.Set("tool_span", span)

		fmt.Printf("⏱️  BeforeToolCallback: %s started at %s\n", toolName, startTime.Format("15:04:05.000"))
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
		// Get callback message from context.
		msg := tool.CallbackMessage(ctx)
		if msg == nil {
			fmt.Printf("⏱️  AfterToolCallback: %s completed (no timing info available)\n", toolName)
			return nil, nil
		}

		// Get start time from callback message.
		startTimeVal, ok := msg.Get("tool_start_time")
		if !ok {
			fmt.Printf("⏱️  AfterToolCallback: %s completed (no timing info available)\n", toolName)
			return nil, nil
		}

		startTime, ok := startTimeVal.(time.Time)
		if !ok {
			fmt.Printf("⏱️  AfterToolCallback: %s completed (invalid timing info)\n", toolName)
			return nil, nil
		}

		duration := time.Since(startTime)
		durationSeconds := duration.Seconds()

		// Record metrics.
		e.toolDurationHistogram.Record(ctx, durationSeconds,
			metric.WithAttributes(
				attribute.String("tool.name", toolName),
			),
		)
		e.toolCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("tool.name", toolName),
			),
		)

		// End trace span from callback message.
		if spanVal, ok := msg.Get("tool_span"); ok {
			if span, ok := spanVal.(trace.Span); ok {
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
			}
		}

		fmt.Printf("⏱️  AfterToolCallback: %s completed in %v\n", toolName, duration)
		fmt.Printf("   Result: %v\n", result)
		if runErr != nil {
			fmt.Printf("   Error: %v\n", runErr)
		}

		return nil, nil
	}
}
