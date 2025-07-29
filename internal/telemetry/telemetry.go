//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package telemetry

import (
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
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
)

// telemetry attributes constants.
var (
	KeyEventID      = "trpc.go.agent.event_id"
	KeySessionID    = "trpc.go.agent.session_id"
	KeyInvocationID = "trpc.go.agent.invocation_id"
	KeyLLMRequest   = "trpc.go.agent.llm_request"
	KeyLLMResponse  = "trpc.go.agent.llm_response"
)

// TraceToolCall traces the invocation of a tool call.
func TraceToolCall(span trace.Span, declaration *tool.Declaration, args []byte, rspEvent *event.Event) {
	span.SetAttributes(
		semconv.GenAISystemKey.String("trpc.go.agent"),
		semconv.GenAIOperationNameExecuteTool,
		semconv.GenAIToolName(declaration.Name),
		semconv.GenAIToolDescription(declaration.Description),
		attribute.String(KeyEventID, rspEvent.ID),
		attribute.String("trpc.go.agent.tool_id", rspEvent.Response.ID),
	)

	if bts, err := json.Marshal(args); err == nil {
		span.SetAttributes(attribute.String("trpc.go.agent.tool_call_args", string(bts)))
	} else {
		span.SetAttributes(attribute.String("trpc.go.agent.tool_call_args", "<not json serializable>"))
	}

	if bts, err := json.Marshal(rspEvent.Response); err == nil {
		span.SetAttributes(attribute.String("trpc.go.agent.tool_response", string(bts)))
	} else {
		span.SetAttributes(attribute.String("trpc.go.agent.tool_response", "<not json serializable>"))
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
		semconv.GenAISystemKey.String("trpc.go.agent"),
		semconv.GenAIOperationNameExecuteTool,
		semconv.GenAIToolName("(merged tools)"),
		semconv.GenAIToolDescription("(merged tools)"),
		attribute.String(KeyEventID, rspEvent.ID),
		attribute.String("trpc.go.agent.tool_id", rspEvent.Response.ID),
		attribute.String("trpc.go.agent.tool_call_args", "N/A"),
	)

	if bts, err := json.Marshal(rspEvent.Response); err == nil {
		span.SetAttributes(attribute.String("trpc.go.agent.tool_response", string(bts)))
	} else {
		span.SetAttributes(attribute.String("trpc.go.agent.tool_response", "<not json serializable>"))
	}

	// Setting empty llm request and response (as UI expect these) while not
	// applicable for tool_response.
	span.SetAttributes(
		attribute.String(KeyLLMRequest, "{}"),
		attribute.String(KeyLLMResponse, "{}"),
	)
}

// TraceCallLLM traces the invocation of an LLM call.
func TraceCallLLM(span trace.Span, invoke *agent.Invocation, req *model.Request, rsp *model.Response, eventID string) {
	span.SetAttributes(
		semconv.GenAISystemKey.String("trpc.go.agent"),
		attribute.String(KeyInvocationID, invoke.InvocationID),
		attribute.String(KeySessionID, invoke.Session.ID),
		attribute.String(KeyEventID, eventID),
		semconv.GenAIRequestModelKey.String(invoke.Model.Info().Name),
	)

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

// NewConn creates a new gRPC connection to the OpenTelemetry Collector.
func NewConn(endpoint string) (*grpc.ClientConn, error) {
	// It connects the OpenTelemetry Collector through gRPC connection.
	// You can customize the endpoint using SetConfig() or environment variables.
	conn, err := grpc.NewClient(endpoint,
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	return conn, err
}
