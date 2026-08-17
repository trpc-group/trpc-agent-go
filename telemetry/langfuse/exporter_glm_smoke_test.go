//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/tracetransform"
)

const glmSmokePrompt = "1+1等于几"

func TestGLMLangfuseObservationSmoke(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping real API test")
	}

	t.Run("defaultWritesOTel", func(t *testing.T) {
		input, output := runGLMLangfuseObservation(t, atrace.SpanAttributePolicy{})
		require.NotEqual(t, "N/A", input)
		require.Contains(t, input, glmSmokePrompt)
		require.NotEqual(t, "N/A", output)
		require.NotEmpty(t, output)
	})

	t.Run("DropOTelKeepsLegacyFallback", func(t *testing.T) {
		policy := atrace.SpanAttributePolicy{}
		atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrInputMessagesOTel, atrace.Drop())(&policy)
		atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrOutputMessagesOTel, atrace.Drop())(&policy)
		input, output := runGLMLangfuseObservation(t, policy)
		require.NotEqual(t, "N/A", input)
		require.Contains(t, input, glmSmokePrompt)
		require.NotEqual(t, "N/A", output)
		require.NotEmpty(t, output)
	})
}

func runGLMLangfuseObservation(t *testing.T, policy atrace.SpanAttributePolicy) (string, string) {
	t.Helper()

	atrace.SetSpanAttributePolicy(policy)
	t.Cleanup(func() { atrace.SetSpanAttributePolicy(atrace.SpanAttributePolicy{}) })

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := atrace.TracerProvider
	originalTracer := atrace.Tracer
	atrace.TracerProvider = provider
	atrace.Tracer = provider.Tracer(itelemetry.InstrumentName)
	t.Cleanup(func() {
		atrace.TracerProvider = originalProvider
		atrace.Tracer = originalTracer
		_ = provider.Shutdown(context.Background())
	})

	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	modelName := os.Getenv("MODEL_NAME")
	if strings.TrimSpace(modelName) == "" {
		modelName = "glm-5.0-w4afp8"
	}
	opts := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	maxTokens := 64
	ag := llmagent.New(
		"glm-langfuse-smoke",
		llmagent.WithModel(openai.New(modelName, opts...)),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: &maxTokens,
			Stream:    true,
		}),
	)
	r := runner.NewRunner(
		"glm-langfuse-smoke-app",
		ag,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	eventCh, err := r.Run(
		ctx,
		"user-glm-smoke",
		"session-glm-smoke-"+t.Name(),
		model.NewUserMessage(glmSmokePrompt),
	)
	require.NoError(t, err)
	for evt := range eventCh {
		if evt != nil && evt.Error != nil {
			t.Fatalf("runner event error: %+v", evt.Error)
		}
	}

	chatSpan := findChatProtoSpan(t, recorder.Ended())
	transformCallLLM(chatSpan)
	attrMap := map[string]string{}
	for _, attr := range chatSpan.Attributes {
		if attr.Value != nil {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}
	}
	return attrMap[observationInput], attrMap[observationOutput]
}

func findChatProtoSpan(t *testing.T, ended []sdktrace.ReadOnlySpan) *tracepb.Span {
	t.Helper()
	readonly := make([]sdktrace.ReadOnlySpan, len(ended))
	copy(readonly, ended)
	for _, rs := range tracetransform.Spans(readonly) {
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
				for _, attr := range span.Attributes {
					if attr.Key == semconvtrace.KeyGenAIOperationName &&
						attr.Value.GetStringValue() == itelemetry.OperationChat {
						return span
					}
				}
			}
		}
	}
	t.Fatal("expected a chat span in the recorded trace")
	return nil
}
