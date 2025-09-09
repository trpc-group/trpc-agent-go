//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"encoding/json"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
)

var _ trace.Span = (*span)(nil)

// span wraps an existing Span to add custom behavior.
type span struct {
	embedded.Span
	underlying trace.Span
	spanName   string

	mutex sync.RWMutex
	attrs map[attribute.Key]attribute.Value
}

func (s *span) AddLink(link trace.Link) {
	s.underlying.AddLink(link)
}

// End finishes the span with custom attribute transformation.
func (s *span) End(options ...trace.SpanEndOption) {
	s.mutex.RLock()
	s.transformAttributes(s.attrs)
	s.mutex.RUnlock()

	s.underlying.End(options...)
}

// transformAttributes applies custom transformations to span attributes.
func (s *span) transformAttributes(attrs map[attribute.Key]attribute.Value) {
	operationName, ok := attrs[attribute.Key("gen_ai.operation.name")]
	if !ok {
		return
	}
	if operationName.AsString() == "call_llm" {
		s.transformCallLLM(attrs)
	} else if operationName.AsString() == "execute_tool" {
		s.transformExecuteTool(attrs)
	} else if operationName.AsString() == "run_runner" {
		s.transformRunRunner(attrs)
	}
}

func (s *span) transformRunRunner(attrs map[attribute.Key]attribute.Value) {
	if name, ok := attrs["trpc.go.agent.runner.name"]; ok {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.name", name.AsString()))
		s.underlying.SetAttributes(attribute.String("trpc.go.agent.runner.name", ""))
	} else {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.name", "N/A"))
	}

	if userID, ok := attrs["trpc.go.agent.runner.user_id"]; ok {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.user_id", userID.AsString()))
		s.underlying.SetAttributes(attribute.String("trpc.go.agent.runner.user_id", ""))
	} else {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.user_id", "N/A"))
	}

	if sessionID, ok := attrs["trpc.go.agent.runner.session_id"]; ok {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.session_id", sessionID.AsString()))
		s.underlying.SetAttributes(attribute.String("trpc.go.agent.runner.session_id", ""))
	} else {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.session_id", "N/A"))
	}

	if input, ok := attrs["trpc.go.agent.runner.input"]; ok {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.input", input.AsString()))
		s.underlying.SetAttributes(attribute.String("trpc.go.agent.runner.input", ""))
	} else {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.input", "N/A"))
	}

	if output, ok := attrs["trpc.go.agent.runner.output"]; ok {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.output", output.AsString()))
		s.underlying.SetAttributes(attribute.String("trpc.go.agent.runner.output", ""))
	} else {
		s.underlying.SetAttributes(attribute.String("langfuse.trace.output", "N/A"))
	}

}

func (s *span) transformExecuteTool(attrs map[attribute.Key]attribute.Value) {
	s.underlying.SetAttributes(attribute.String("langfuse.observation.type", "tool"))
	if callArgs, ok := attrs[attribute.Key("trpc.go.agent.tool_call_args")]; ok {
		s.underlying.SetAttributes(attribute.String("langfuse.observation.input", callArgs.AsString()))
		// Exclude tool_call_args as they're mapped separately
		s.underlying.SetAttributes(attribute.String("tool_call_args", ""))
	} else {
		s.underlying.SetAttributes(attribute.String("langfuse.observation.input", "N/A"))
	}

	if toolResult, ok := attrs[attribute.Key("trpc.go.agent.tool_response")]; ok {
		s.underlying.SetAttributes(attribute.String("langfuse.observation.output", toolResult.AsString()))
		// Exclude tool_response as they're mapped separately
		s.underlying.SetAttributes(attribute.String("trpc.go.agent.tool_response", ""))
	} else {
		s.underlying.SetAttributes(attribute.String("langfuse.observation.output", "N/A"))
	}

}

// processLLMGenerationSpan handles the transformation for LLM generation spans.
func (s *span) transformCallLLM(attrs map[attribute.Key]attribute.Value) {
	s.underlying.SetAttributes(attribute.String("langfuse.observation.type", "generation"))

	if request, ok := attrs[attribute.Key(itelemetry.KeyLLMRequest)]; ok {
		s.underlying.SetAttributes(
			attribute.String("langfuse.observation.input", request.AsString()),
		)
		// generation_config
		req := make(map[string]interface{})
		if err := json.Unmarshal([]byte(request.AsString()), &req); err == nil {
			if genConfig, exists := req["generation_config"]; exists {
				jsonConfig, err := json.Marshal(genConfig)
				if err == nil {
					s.underlying.SetAttributes(
						attribute.String("langfuse.observation.model.parameters", string(jsonConfig)),
					)
				}
			}
		}
		// Exclude llm_request as they're mapped separately
		s.underlying.SetAttributes(attribute.String(itelemetry.KeyLLMRequest, ""))
	} else {
		// If no request attribute, set a default input
		s.underlying.SetAttributes(
			attribute.String("langfuse.observation.input", "N/A"),
		)
	}

	if response, ok := attrs[attribute.Key(itelemetry.KeyLLMResponse)]; ok {
		s.underlying.SetAttributes(
			attribute.String("langfuse.observation.output", response.AsString()),
		)
		// Exclude llm_response as they're mapped separately
		s.underlying.SetAttributes(attribute.String(itelemetry.KeyLLMResponse, ""))
	} else {
		// If no response attribute, set a default output
		s.underlying.SetAttributes(
			attribute.String("langfuse.observation.output", "N/A"),
		)
	}
}

func (s *span) SpanContext() trace.SpanContext {
	return s.underlying.SpanContext()
}

func (s *span) SetStatus(code codes.Code, description string) {
	s.underlying.SetStatus(code, description)
}

func (s *span) SetName(name string) {
	s.underlying.SetName(name)
}

func (s *span) SetAttributes(kv ...attribute.KeyValue) {
	s.mutex.Lock()
	for _, v := range kv {
		s.attrs[v.Key] = v.Value
	}
	s.mutex.Unlock()
	s.underlying.SetAttributes(kv...)
}

func (s *span) AddEvent(name string, options ...trace.EventOption) {
	s.underlying.AddEvent(name, options...)
}

func (s *span) IsRecording() bool {
	return s.underlying.IsRecording()
}

func (s *span) RecordError(err error, options ...trace.EventOption) {
	s.underlying.RecordError(err, options...)
}

func (s *span) TracerProvider() trace.TracerProvider {
	return s.underlying.TracerProvider()
}
