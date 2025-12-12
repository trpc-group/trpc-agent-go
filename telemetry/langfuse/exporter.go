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
			// Skip this attribute (delete it)
		default:
			newAttributes = append(newAttributes, attr)
		}
	}
	span.Attributes = newAttributes
}

// transformCallLLM transforms LLM call spans for Langfuse
func transformCallLLM(span *tracepb.Span) {
	var newAttributes []*commonpb.KeyValue

	// Add observation type
	newAttributes = append(newAttributes, &commonpb.KeyValue{
		Key: observationType,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: observationTypeGeneration},
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
		case itelemetry.KeyLLMRequest:
			if attr.Value != nil {
				request := attr.Value.GetStringValue()
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationInput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: request},
					},
				})

				// Extract generation_config if exists
				var req map[string]any
				if err := json.Unmarshal([]byte(request), &req); err == nil {
					if genConfig, exists := req["generation_config"]; exists {
						if jsonConfig, err := json.Marshal(genConfig); err == nil {
							newAttributes = append(newAttributes, &commonpb.KeyValue{
								Key: observationModelParameters,
								Value: &commonpb.AnyValue{
									Value: &commonpb.AnyValue_StringValue{StringValue: string(jsonConfig)},
								},
							})
						}
					}
				}
			} else {
				newAttributes = append(newAttributes, &commonpb.KeyValue{
					Key: observationInput,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "N/A"},
					},
				})
			}

			// Skip this attribute (delete it)
		case itelemetry.KeyLLMResponse:
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
