package telemetry

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

func TraceToolCall(span trace.Span, declaration *tool.Declaration, args []byte, rspEvent *event.Event) {
	span.SetAttributes(
		semconv.GenAISystemKey.String("trpc.go.agent"),
		semconv.GenAIOperationNameExecuteTool,
		semconv.GenAIToolName(declaration.Name),
		semconv.GenAIToolDescription(declaration.Description),
		attribute.String("trpc.go.agent.event_id", rspEvent.ID),
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
		attribute.String("trpc.go.agent.llm_request", "{}"),
		attribute.String("trpc.go.agent.llm_response", "{}"),
	)
}

func TraceMergedToolCalls(span trace.Span, rspEvent *event.Event) {
	span.SetAttributes(
		semconv.GenAISystemKey.String("trpc.go.agent"),
		semconv.GenAIOperationNameExecuteTool,
		semconv.GenAIToolName("(merged tools)"),
		semconv.GenAIToolDescription("(merged tools)"),
		attribute.String("trpc.go.agent.event_id", rspEvent.ID),
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
		attribute.String("trpc.go.agent.llm_request", "{}"),
		attribute.String("trpc.go.agent.llm_response", "{}"),
	)
}

func TraceCallLLM(span trace.Span, invoke *agent.Invocation, req *model.Request, rsp *model.Response, eventID string) {
	span.SetAttributes(
		semconv.GenAISystemKey.String("trpc.go.agent"),
		attribute.String("trpc.go.agent.invokcation_id", invoke.InvocationID),
		attribute.String("trpc.go.agent.session_id", invoke.Session.ID),
		attribute.String("trpc.go.agent.event_id", eventID),
		semconv.GenAIRequestModelKey.String(invoke.Model.Info().Name),
	)

	if bts, err := json.Marshal(req); err == nil {
		span.SetAttributes(
			attribute.String("trpc.go.agent.llm_request", string(bts)),
		)
	} else {
		span.SetAttributes(attribute.String("trpc.go.agent.llm_request", "<not json serializable>"))
	}

	if bts, err := json.Marshal(rsp); err == nil {
		span.SetAttributes(
			attribute.String("trpc.go.agent.llm_request", string(bts)),
		)
	} else {
		span.SetAttributes(attribute.String("trpc.go.agent.llm_request", "<not json serializable>"))
	}
}
