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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/tracetransform"
)

var _ trace.SpanExporter = (*exporter)(nil)

type exporter struct {
	client otlptrace.Client

	mu      sync.RWMutex
	started bool

	startOnce sync.Once
	stopOnce  sync.Once
}

func newExporter(ctx context.Context, opts ...otlptracehttp.Option) (*exporter, error) {
	e := &exporter{client: otlptracehttp.NewClient(opts...)}
	if err := e.Start(ctx); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *exporter) ExportSpans(ctx context.Context, ss []trace.ReadOnlySpan) error {
	protoSpans := tracetransform.Spans(ss)

	protoSpans = transform(protoSpans)

	err := e.client.UploadTraces(ctx, protoSpans)
	if err != nil {
		return fmt.Errorf("exporting spans: uploading traces: %w", err)
	}
	return nil
}

func transform(ss []*tracepb.ResourceSpans) []*tracepb.ResourceSpans {
	if len(ss) == 0 {
		return ss
	}

	for _, rs := range ss {
		if rs == nil {
			continue
		}

		for _, scopeSpans := range rs.ScopeSpans {
			if scopeSpans == nil {
				continue
			}

			for _, span := range scopeSpans.Spans {
				if span == nil {
					continue
				}

				transformSpan(span)
			}
		}
	}

	return ss
}

// transformSpan applies langfuse-specific transformations to a span
func transformSpan(span *tracepb.Span) {
	if span.Attributes == nil {
		return
	}

	// Find the operation name
	var operationName string
	for _, attr := range span.Attributes {
		if attr.Key == itelemetry.KeyGenAIOperationName {
			if attr.Value != nil && attr.Value.GetStringValue() != "" {
				operationName = attr.Value.GetStringValue()
				break
			}
		}
	}

	switch operationName {
	case itelemetry.OperationInvokeAgent:
		transformInvokeAgent(span)
	case itelemetry.OperationChat:
		transformCallLLM(span)
	case itelemetry.OperationExecuteTool:
		transformExecuteTool(span)
	case itelemetry.OperationWorkflow:
		transformWorkflow(span)
	default:
	}
}

func transformInvokeAgent(span *tracepb.Span) {
	var newAttributes []*commonpb.KeyValue

	newAttributes = append(newAttributes, &commonpb.KeyValue{
		Key: observationType,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: observationTypeAgent},
		},
	})

	for _, attr := range span.Attributes {
		switch attr.Key {
		case itelemetry.KeyGenAIInputMessages:
			if attr.Value != nil {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationInput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value.GetStringValue()},
					},
				})
			}
			// Skip this attribute (delete it)
		case itelemetry.KeyGenAIOutputMessages:
			if attr.Value != nil {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationOutput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value.GetStringValue()},
					},
				})
			}
			// Skip token usage attributes for InvokeAgent observations.
			//
			// Reason: the top-level span represents the whole trace, and Langfuse aggregates token
			// usage across all spans in the trace. We recently started emitting token usage for
			// InvokeAgent spans (to support Galileo); previously only Chat spans had token usage.
			// Keeping token attributes on InvokeAgent would make Langfuse double count tokens
			// compared to the old behavior (Chat-only token accounting).
		case itelemetry.KeyGenAIUsageInputTokens, itelemetry.KeyGenAIUsageOutputTokens,
			itelemetry.KeyGenAIUsageInputTokensCached, itelemetry.KeyGenAIUsageInputTokensCacheRead,
			itelemetry.KeyGenAIUsageInputTokensCacheCreation:
		default:
			newAttributes = append(newAttributes, attr)
		}
	}
	span.Attributes = newAttributes
}

// transformCallLLM transforms LLM call spans for Langfuse
// llmSpanCollected holds the intermediate values collected from an LLM span's attributes.
type llmSpanCollected struct {
	sessionID       *commonpb.AnyValue
	llmRequest      *string
	inputMessages   *string
	toolDefinitions *string
	usage           usageDetails
	attrs           []*commonpb.KeyValue // non-LLM attributes to keep
}

// transformCallLLM transforms LLM call spans for Langfuse.
func transformCallLLM(span *tracepb.Span) {
	collected := collectLLMSpanAttributes(span.Attributes)

	newAttributes := []*commonpb.KeyValue{{
		Key:   observationType,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: observationTypeGeneration}},
	}}
	newAttributes = append(newAttributes, collected.attrs...)

	// observation.input
	newAttributes = append(newAttributes, stringKV(observationInput, buildLLMObservationInput(collected)))

	// observation.model_parameters (generation_config from llm request)
	if kv := extractModelParameters(collected.llmRequest); kv != nil {
		newAttributes = append(newAttributes, kv)
	}

	// observation.usage_details
	if !collected.usage.empty() {
		if usageJSON, err := json.Marshal(collected.usage); err == nil {
			newAttributes = append(newAttributes, stringKV(observationUsageDetails, string(usageJSON)))
		}
	}

	if collected.sessionID != nil {
		newAttributes = append(newAttributes, &commonpb.KeyValue{Key: traceSessionID, Value: collected.sessionID})
	}

	span.Attributes = newAttributes
}

// collectLLMSpanAttributes iterates over the raw span attributes once, collecting
// the pieces needed by subsequent build steps and filtering out OTEL-specific keys.
func collectLLMSpanAttributes(attrs []*commonpb.KeyValue) llmSpanCollected {
	var c llmSpanCollected
	for _, attr := range attrs {
		switch attr.Key {
		case itelemetry.KeyGenAIConversationID, itelemetry.KeyRunnerSessionID, traceSessionID:
			c.sessionID = attr.Value
		case itelemetry.KeyRunnerUserID:
			c.attrs = append(c.attrs, &commonpb.KeyValue{Key: traceUserID, Value: attr.Value})
		case itelemetry.KeyLLMRequest:
			c.llmRequest = getStringPtr(attr.Value)
		case itelemetry.KeyGenAIInputMessages:
			c.inputMessages = getStringPtr(attr.Value)
		case itelemetry.KeyGenAIRequestToolDefinitions:
			c.toolDefinitions = getStringPtr(attr.Value)
		case itelemetry.KeyLLMResponse:
			c.attrs = append(c.attrs, stringKV(observationOutput, stringValueOrNA(attr.Value)))
		case itelemetry.KeyGenAIUsageInputTokens:
			c.usage.Input = attr.Value.GetIntValue()
		case itelemetry.KeyGenAIUsageOutputTokens:
			c.usage.Output = attr.Value.GetIntValue()
		case itelemetry.KeyGenAIUsageInputTokensCached:
			c.usage.InputCached = attr.Value.GetIntValue()
		case itelemetry.KeyGenAIUsageInputTokensCacheRead:
			c.usage.InputCacheRead = attr.Value.GetIntValue()
		case itelemetry.KeyGenAIUsageInputTokensCacheCreation:
			c.usage.InputCacheCreation = attr.Value.GetIntValue()
		default:
			c.attrs = append(c.attrs, attr)
		}
	}
	return c
}

// buildLLMObservationInput constructs the Langfuse observation.input value from
// collected LLM span data, wrapping tools+messages when both are present.
func buildLLMObservationInput(c llmSpanCollected) string {
	if c.inputMessages != nil && *c.inputMessages != "" {
		return wrapWithToolsIfPresent(*c.inputMessages, c.toolDefinitions)
	}
	if c.llmRequest != nil && *c.llmRequest != "" {
		if messagesJSON, ok := extractMessagesJSONFromRequestJSON(*c.llmRequest); ok && messagesJSON != "" {
			return wrapWithToolsIfPresent(messagesJSON, c.toolDefinitions)
		}
		return *c.llmRequest
	}
	return "N/A"
}

// wrapWithToolsIfPresent returns messagesJSON as-is, or wraps it with tool
// definitions into {"tools":..., "messages":...} when toolDefs is non-empty.
func wrapWithToolsIfPresent(messagesJSON string, toolDefs *string) string {
	if toolDefs != nil && *toolDefs != "" {
		if wrapped, err := buildObservationInputPrompt(messagesJSON, *toolDefs); err == nil {
			return wrapped
		}
	}
	return messagesJSON
}

// extractModelParameters extracts "generation_config" from the LLM request JSON
// and returns it as an observation.model_parameters attribute, or nil.
func extractModelParameters(llmRequest *string) *commonpb.KeyValue {
	if llmRequest == nil || *llmRequest == "" {
		return nil
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*llmRequest), &req); err != nil {
		return nil
	}
	raw, exists := req["generation_config"]
	if !exists {
		return nil
	}
	return stringKV(observationModelParameters, string(raw))
}

// stringKV is a helper to build a string-valued KeyValue proto attribute.
func stringKV(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

// getStringPtr returns a pointer to the string value of v, or nil if v is nil.
func getStringPtr(v *commonpb.AnyValue) *string {
	if v == nil {
		return nil
	}
	s := v.GetStringValue()
	return &s
}

// stringValueOrNA returns the string value of v, or "N/A" if v is nil.
func stringValueOrNA(v *commonpb.AnyValue) string {
	if v == nil {
		return "N/A"
	}
	return v.GetStringValue()
}

func buildObservationInputPrompt(messagesJSON, toolDefsJSON string) (string, error) {
	payload := observationInputPrompt{
		Tools:    json.RawMessage([]byte(toolDefsJSON)),
		Messages: json.RawMessage([]byte(messagesJSON)),
	}
	bts, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(bts), nil
}

func extractMessagesJSONFromRequestJSON(requestJSON string) (string, bool) {
	if requestJSON == "" {
		return "", false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(requestJSON), &m); err != nil {
		return "", false
	}
	msgs, ok := m["messages"]
	if !ok || len(msgs) == 0 {
		return "", false
	}
	return string(msgs), true
}

// transformExecuteTool transforms tool execution spans for Langfuse
func transformExecuteTool(span *tracepb.Span) {
	var newAttributes []*commonpb.KeyValue

	// Add observation type
	newAttributes = append(newAttributes, &commonpb.KeyValue{
		Key: observationType,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: observationTypeTool},
		},
	})

	// Process existing attributes
	var llmSessionID *commonpb.AnyValue
	for _, attr := range span.Attributes {
		switch attr.Key {
		case itelemetry.KeyGenAIConversationID, itelemetry.KeyRunnerSessionID, traceSessionID:
			llmSessionID = attr.Value
		case itelemetry.KeyRunnerUserID:
			newAttributes = append(newAttributes, &commonpb.KeyValue{Key: traceUserID, Value: attr.Value})
		case itelemetry.KeyGenAIToolCallArguments:
			if attr.Value != nil {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationInput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value.GetStringValue()},
					},
				})
			} else {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationInput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "N/A"},
					},
				})
			}
			// Skip this attribute (delete it)
		case itelemetry.KeyGenAIToolCallResult:
			if attr.Value != nil {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationOutput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value.GetStringValue()},
					},
				})
			} else {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationOutput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "N/A"},
					},
				})
			}
			// Skip this attribute (delete it)
		default:
			// Keep other attributes
			newAttributes = append(newAttributes, attr)
		}
	}
	if llmSessionID != nil { // use post set session id
		newAttributes = append(newAttributes, &commonpb.KeyValue{Key: traceSessionID, Value: llmSessionID})
	}

	// Replace span attributes
	span.Attributes = newAttributes
}

// transformWorkflow transforms workflow spans for Langfuse.
func transformWorkflow(span *tracepb.Span) {
	var newAttributes []*commonpb.KeyValue

	// Add observation type
	newAttributes = append(newAttributes, &commonpb.KeyValue{
		Key: observationType,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: observationTypeChain},
		},
	})

	// Process existing attributes
	var llmSessionID *commonpb.AnyValue
	for _, attr := range span.Attributes {
		switch attr.Key {
		case itelemetry.KeyGenAIConversationID, itelemetry.KeyRunnerSessionID, traceSessionID:
			llmSessionID = attr.Value
		case itelemetry.KeyRunnerUserID:
			newAttributes = append(newAttributes, &commonpb.KeyValue{Key: traceUserID, Value: attr.Value})
		case itelemetry.KeyGenAIWorkflowRequest:
			if attr.Value != nil {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationInput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value.GetStringValue()},
					},
				})
			} else {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationInput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "N/A"},
					},
				})
			}
			// Skip this attribute (delete it)
		case itelemetry.KeyGenAIWorkflowResponse:
			if attr.Value != nil {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationOutput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value.GetStringValue()},
					},
				})
			} else {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationOutput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "N/A"},
					},
				})
			}
			// Skip this attribute (delete it)
		default:
			// Keep other attributes
			newAttributes = append(newAttributes, attr)
		}
	}
	if llmSessionID != nil { // use post set session id
		newAttributes = append(newAttributes, &commonpb.KeyValue{Key: traceSessionID, Value: llmSessionID})
	}

	// Replace span attributes
	span.Attributes = newAttributes
}

func (e *exporter) Shutdown(ctx context.Context) error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()

	if !started {
		return nil
	}

	var err error

	e.stopOnce.Do(func() {
		err = e.client.Stop(ctx)
		e.mu.Lock()
		e.started = false
		e.mu.Unlock()
	})

	return err
}

var errAlreadyStarted = errors.New("already started")

func (e *exporter) Start(ctx context.Context) error {
	var err = errAlreadyStarted
	e.startOnce.Do(func() {
		e.mu.Lock()
		e.started = true
		e.mu.Unlock()
		err = e.client.Start(ctx)
	})

	return err
}

// MarshalLog is the marshaling function used by the logging system to represent this exporter.
func (e *exporter) MarshalLog() any {
	return struct {
		Type   string
		Client otlptrace.Client
	}{
		Type:   "otlptrace",
		Client: e.client,
	}
}
