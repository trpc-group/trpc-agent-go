//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package telemetry provides telemetry and observability functionality for the trpc-agent-go framework.
// It includes tracing, metrics, and monitoring capabilities for agent operations.
package telemetry

import (
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// telemetry service constants.
const (
	ServiceName      = "telemetry"
	ServiceVersion   = "v0.1.0"
	ServiceNamespace = "trpc-go-agent"
	InstrumentName   = "trpc.agent.go"

	SpanNameCallLLM           = "call_llm"
	SpanNamePrefixExecuteTool = "execute_tool"

	OperationExecuteTool = "execute_tool"
	OperationCallLLM     = "call_llm"
	OperationInvokeAgent = "invoke_agent"
)

const (
	// ProtocolGRPC uses gRPC protocol for OTLP exporter.
	ProtocolGRPC string = "grpc"
	// ProtocolHTTP uses HTTP protocol for OTLP exporter.
	ProtocolHTTP string = "http"
)

// telemetry attributes constants.
var (
	ResourceServiceNamespace = "trpc-go-agent"
	ResourceServiceName      = "telemetry"
	ResourceServiceVersion   = "v0.1.0"

	KeyEventID      = "trpc.go.agent.event_id"
	KeySessionID    = "trpc.go.agent.session_id"
	KeyInvocationID = "trpc.go.agent.invocation_id"
	KeyLLMRequest   = "trpc.go.agent.llm_request"
	KeyLLMResponse  = "trpc.go.agent.llm_response"

	// Runner-related attributes
	KeyRunnerName      = "trpc.go.agent.runner.name"
	KeyRunnerUserID    = "trpc.go.agent.runner.user_id"
	KeyRunnerSessionID = "trpc.go.agent.runner.session_id"
	KeyRunnerInput     = "trpc.go.agent.runner.input"
	KeyRunnerOutput    = "trpc.go.agent.runner.output"

	// Tool-related attributes
	KeyToolCallArgs = "trpc.go.agent.tool_call_args"
	KeyToolResponse = "trpc.go.agent.tool_response"
	KeyToolID       = "trpc.go.agent.tool_id"

	// GenAI operation attributes
	KeyGenAIOperationName     = "gen_ai.operation.name"
	KeyGenAISystem            = "gen_ai.system"
	KeyGenAIToolName          = "gen_ai.tool.name"
	KeyGenAIToolDesc          = "gen_ai.tool.description"
	KeyGenAIRequestModel      = "gen_ai.request.model"
	KeyGenAIInputMessages     = "gen_ai.input.messages"
	KeyGenAIOutputMessages    = "gen_ai.output.messages"
	KeyGenAIAgentName         = "gen_ai.agent.name"
	KeyGenAIConversationID    = "gen_ai.conversation.id"
	KeyGenAIResponseModel     = "gen_ai.response.model"
	KeyGenAIUsageOutputTokens = "gen_ai.usage.output_tokens" // #nosec G101 - this is a metric key name, not a credential.
	KeyGenAIResponseID        = "gen_ai.response.id"
	KeyGenAIUsageInputTokens  = "gen_ai.usage.input_tokens" // #nosec G101 - this is a metric key name, not a credential.

	// System value
	SystemTRPCGoAgent = "trpc.go.agent"
)

// TraceToolCall traces the invocation of a tool call.
func TraceToolCall(span trace.Span, declaration *tool.Declaration, args []byte, rspEvent *event.Event) {
	span.SetAttributes(
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(KeyGenAIToolName, declaration.Name),
		attribute.String(KeyGenAIToolDesc, declaration.Description),
	)
	if rspEvent != nil {
		span.SetAttributes(attribute.String(KeyEventID, rspEvent.ID))
	}

	// args is json-encoded.
	span.SetAttributes(attribute.String(KeyToolCallArgs, string(args)))
	if rspEvent != nil && rspEvent.Response != nil {
		span.SetAttributes(attribute.String(KeyToolID, rspEvent.Response.ID))
		if bts, err := json.Marshal(rspEvent.Response); err == nil {
			span.SetAttributes(attribute.String(KeyToolResponse, string(bts)))
		} else {
			span.SetAttributes(attribute.String(KeyToolResponse, "<not json serializable>"))
		}
	}

	// Setting empty llm request and response (as UI expect these) while not
	// applicable for tool_response.
	span.SetAttributes(
		attribute.String(KeyLLMRequest, "{}"),
		attribute.String(KeyLLMResponse, "{}"),
	)
}

// TraceMergedToolCalls traces the invocation of a merged tool call.
func TraceMergedToolCalls(span trace.Span, rspEvent *event.Event) {
	span.SetAttributes(
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(KeyGenAIToolName, "(merged tools)"),
		attribute.String(KeyGenAIToolDesc, "(merged tools)"),
		attribute.String(KeyEventID, rspEvent.ID),
		attribute.String(KeyToolID, rspEvent.Response.ID),
		attribute.String(KeyToolCallArgs, "N/A"),
	)

	if bts, err := json.Marshal(rspEvent.Response); err == nil {
		span.SetAttributes(attribute.String(KeyToolResponse, string(bts)))
	} else {
		span.SetAttributes(attribute.String(KeyToolResponse, "<not json serializable>"))
	}

	// Setting empty llm request and response (as UI expect these) while not
	// applicable for tool_response.
	span.SetAttributes(
		attribute.String(KeyLLMRequest, "{}"),
		attribute.String(KeyLLMResponse, "{}"),
	)
}

// TraceBeforeInvokeAgent traces the before invocation of an agent.
func TraceBeforeInvokeAgent(span trace.Span, invoke *agent.Invocation) {
	if bts, err := json.Marshal(&model.Request{Messages: []model.Message{invoke.Message}}); err == nil {
		span.SetAttributes(
			attribute.String(KeyGenAIInputMessages, string(bts)),
		)
	} else {
		span.SetAttributes(attribute.String(KeyGenAIInputMessages, "<not json serializable>"))
	}
	span.SetAttributes(
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
		attribute.String(KeyGenAIAgentName, invoke.AgentName),
		attribute.String(KeyInvocationID, invoke.InvocationID),
	)
	if invoke.Session != nil {
		span.SetAttributes(
			attribute.String(KeyRunnerUserID, invoke.Session.UserID),
			attribute.String(KeyGenAIConversationID, invoke.Session.ID),
		)
	}
}

// TraceAfterInvokeAgent traces the after invocation of an agent.
func TraceAfterInvokeAgent(span trace.Span, rsp *model.Response) {
	if len(rsp.Choices) > 0 {
		if bts, err := json.Marshal(rsp.Choices[0].Message); err == nil {
			span.SetAttributes(
				attribute.String(KeyGenAIOutputMessages, string(bts)),
			)
		}
	}
	span.SetAttributes(attribute.String(KeyGenAIResponseModel, rsp.Model))
	if rsp.Usage != nil {
		span.SetAttributes(attribute.Int(KeyGenAIUsageInputTokens, rsp.Usage.PromptTokens))
		span.SetAttributes(attribute.Int(KeyGenAIUsageOutputTokens, rsp.Usage.CompletionTokens))
	}
	span.SetAttributes(attribute.String(KeyGenAIResponseID, rsp.ID))

}

// TraceCallLLM traces the invocation of an LLM call.
func TraceCallLLM(span trace.Span, invoke *agent.Invocation, req *model.Request, rsp *model.Response, eventID string) {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationCallLLM),
		attribute.String(KeyInvocationID, invoke.InvocationID),
		attribute.String(KeyEventID, eventID),
	}

	// Add session ID if session exists
	if invoke.Session != nil {
		attrs = append(attrs, attribute.String(KeySessionID, invoke.Session.ID))
	} else {
		attrs = append(attrs, attribute.String(KeySessionID, ""))
	}

	// Add model name if model exists
	if invoke.Model != nil {
		attrs = append(attrs, attribute.String(KeyGenAIRequestModel, invoke.Model.Info().Name))
	} else {
		attrs = append(attrs, attribute.String(KeyGenAIRequestModel, ""))
	}

	span.SetAttributes(attrs...)

	if bts, err := json.Marshal(req); err == nil {
		span.SetAttributes(
			attribute.String(KeyLLMRequest, string(bts)),
		)
	} else {
		span.SetAttributes(attribute.String(KeyLLMRequest, "<not json serializable>"))
	}

	if bts, err := json.Marshal(rsp); err == nil {
		span.SetAttributes(
			attribute.String(KeyLLMResponse, string(bts)),
		)
	} else {
		span.SetAttributes(attribute.String(KeyLLMResponse, "<not json serializable>"))
	}
}

// NewGRPCConn creates a new gRPC connection to the OpenTelemetry Collector.
func NewGRPCConn(endpoint string) (*grpc.ClientConn, error) {
	// It connects the OpenTelemetry Collector through gRPC connection.
	// You can customize the endpoint using SetConfig() or environment variables.
	conn, err := grpc.Dial(endpoint,
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	return conn, err
}
