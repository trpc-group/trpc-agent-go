//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuseharness

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultHarnessImageURL = "https://upload.wikimedia.org/wikipedia/commons/thumb/9/99/Black_square.jpg/320px-Black_square.jpg"
	multimodalTraceName    = "agent-multimodal-otel-harness"
	toolReasoningTraceName = "agent-tool-reasoning-otel-harness"
	harnessUserID          = "observability-harness"
	harnessToolToken       = "otel-tool-token-7f3b9c1d"
)

func TestRealAgentMultimodalTraceToLangfuse(t *testing.T) {
	cfg := mustHarnessConfig(t)
	resetHarnessTracerProvider()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	clean, err := langfuse.Start(
		ctx,
		langfuse.WithPublicKey(cfg.langfusePublicKey),
		langfuse.WithSecretKey(cfg.langfuseSecretKey),
		langfuse.WithHost(cfg.langfuseHost),
	)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		require.NoError(t, clean(shutdownCtx))
		resetHarnessTracerProvider()
	}()

	message, expectedModalities := buildHarnessMessage(t, cfg)
	sessionID := fmt.Sprintf("otel-mm-%d", time.Now().UnixNano())
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())

	traceCtx := withHarnessBaggage(t, ctx, multimodalTraceName, sessionID, runID, "agent_multimodal_otel")
	traceCtx, span := atrace.Tracer.Start(
		traceCtx,
		multimodalTraceName,
		oteltrace.WithAttributes(
			attribute.String("langfuse.environment", "harness"),
			attribute.String("langfuse.observation.metadata.harness_run_id", runID),
			attribute.String("langfuse.observation.metadata.expected_modalities", strings.Join(expectedModalities, ",")),
		),
	)

	finalOutput, err := runHarnessAgent(
		traceCtx,
		newMultimodalHarnessAgent(cfg),
		"langfuse-multimodal-harness",
		sessionID,
		message,
	)
	span.SetAttributes(
		attribute.String("langfuse.trace.input", message.Content),
		attribute.String("langfuse.trace.output", finalOutput),
	)
	span.End()
	require.NoError(t, err)
	require.NotEmpty(t, finalOutput)

	t.Logf("trace_name=%s", multimodalTraceName)
	t.Logf("session_id=%s", sessionID)
	t.Logf("run_id=%s", runID)
	t.Logf("expected_modalities=%s", strings.Join(expectedModalities, ","))
	t.Logf("final_output=%s", finalOutput)

	// Give Langfuse cloud ingestion a small buffer after exporter shutdown.
	time.Sleep(8 * time.Second)
}

func TestRealAgentToolCallReasoningTraceToLangfuse(t *testing.T) {
	cfg := mustHarnessConfig(t)
	resetHarnessTracerProvider()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	clean, err := langfuse.Start(
		ctx,
		langfuse.WithPublicKey(cfg.langfusePublicKey),
		langfuse.WithSecretKey(cfg.langfuseSecretKey),
		langfuse.WithHost(cfg.langfuseHost),
	)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		require.NoError(t, clean(shutdownCtx))
		resetHarnessTracerProvider()
	}()

	message := buildHarnessToolMessage()
	sessionID := fmt.Sprintf("otel-tool-%d", time.Now().UnixNano())
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	expectedFields := []string{"tool_call", "tool_call_response", "reasoning"}

	traceCtx := withHarnessBaggage(t, ctx, toolReasoningTraceName, sessionID, runID, "agent_tool_reasoning_otel")
	traceCtx, span := atrace.Tracer.Start(
		traceCtx,
		toolReasoningTraceName,
		oteltrace.WithAttributes(
			attribute.String("langfuse.environment", "harness"),
			attribute.String("langfuse.observation.metadata.harness_run_id", runID),
			attribute.String("langfuse.observation.metadata.expected_fields", strings.Join(expectedFields, ",")),
		),
	)

	finalOutput, err := runHarnessAgent(
		traceCtx,
		newToolReasoningHarnessAgent(cfg),
		"langfuse-tool-reasoning-harness",
		sessionID,
		message,
	)
	span.SetAttributes(
		attribute.String("langfuse.trace.input", message.Content),
		attribute.String("langfuse.trace.output", finalOutput),
	)
	span.End()
	require.NoError(t, err)
	require.Contains(t, finalOutput, harnessToolToken)

	t.Logf("trace_name=%s", toolReasoningTraceName)
	t.Logf("session_id=%s", sessionID)
	t.Logf("run_id=%s", runID)
	t.Logf("expected_fields=%s", strings.Join(expectedFields, ","))
	t.Logf("final_output=%s", finalOutput)

	time.Sleep(8 * time.Second)
}

type harnessConfig struct {
	modelName         string
	openAIBaseURL     string
	openAIAPIKey      string
	langfusePublicKey string
	langfuseSecretKey string
	langfuseBaseURL   string
	langfuseHost      string
	includeFile       bool
	imageURL          string
}

func mustHarnessConfig(t *testing.T) harnessConfig {
	t.Helper()

	cfg := harnessConfig{
		modelName:         strings.TrimSpace(os.Getenv("MODEL_NAME")),
		openAIBaseURL:     strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")),
		openAIAPIKey:      strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		langfusePublicKey: strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY")),
		langfuseSecretKey: strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY")),
		langfuseBaseURL:   strings.TrimSpace(os.Getenv("LANGFUSE_BASE_URL")),
		imageURL:          strings.TrimSpace(os.Getenv("HARNESS_IMAGE_URL")),
		includeFile:       strings.TrimSpace(os.Getenv("HARNESS_INCLUDE_FILE")) == "1",
	}

	if cfg.imageURL == "" {
		cfg.imageURL = defaultHarnessImageURL
	}
	require.NotEmpty(t, cfg.modelName, "MODEL_NAME must be set")
	require.NotEmpty(t, cfg.openAIBaseURL, "OPENAI_BASE_URL must be set")
	require.NotEmpty(t, cfg.openAIAPIKey, "OPENAI_API_KEY must be set")
	require.NotEmpty(t, cfg.langfusePublicKey, "LANGFUSE_PUBLIC_KEY must be set")
	require.NotEmpty(t, cfg.langfuseSecretKey, "LANGFUSE_SECRET_KEY must be set")
	require.NotEmpty(t, cfg.langfuseBaseURL, "LANGFUSE_BASE_URL must be set")

	cfg.langfuseHost = hostFromBaseURL(t, cfg.langfuseBaseURL)
	return cfg
}

func buildHarnessMessage(t *testing.T, cfg harnessConfig) (model.Message, []string) {
	t.Helper()

	msg := model.NewUserMessage(
		"Describe the image in one concise sentence. " +
			"Also mention that this request came from the telemetry multimodal harness.",
	)
	msg.AddImageURL(cfg.imageURL, "low")

	expectedModalities := []string{"text", "image"}
	if cfg.includeFile {
		notePath := filepath.Join("testdata", "note.txt")
		require.NoError(t, msg.AddFilePath(notePath))
		expectedModalities = append(expectedModalities, "file")
	}
	return msg, expectedModalities
}

func buildHarnessToolMessage() model.Message {
	return model.NewUserMessage(
		"Use the harness_lookup tool exactly once to fetch the token for key 'otel'. " +
			"Then answer in one short sentence that includes the returned token and mention that this request came from the telemetry tool harness.",
	)
}

func newMultimodalHarnessAgent(cfg harnessConfig) *llmagent.LLMAgent {
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(256),
		Temperature: floatPtr(0.1),
		Stream:      false,
	}

	modelInstance := openai.New(
		cfg.modelName,
		openai.WithAPIKey(cfg.openAIAPIKey),
		openai.WithBaseURL(cfg.openAIBaseURL),
	)
	return llmagent.New(
		"langfuse-multimodal-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A small harness agent for verifying multimodal telemetry."),
		llmagent.WithInstruction(
			"You are a concise assistant. Respond in one short sentence. "+
				"If you can inspect the image, describe it. If a file is attached and readable, mention that too.",
		),
		llmagent.WithGenerationConfig(genConfig),
	)
}

func newToolReasoningHarnessAgent(cfg harnessConfig) *llmagent.LLMAgent {
	thinkingEnabled := true
	genConfig := model.GenerationConfig{
		MaxTokens:       intPtr(256),
		Temperature:     floatPtr(0.1),
		Stream:          false,
		ThinkingEnabled: &thinkingEnabled,
	}

	modelInstance := openai.New(
		cfg.modelName,
		openai.WithAPIKey(cfg.openAIAPIKey),
		openai.WithBaseURL(cfg.openAIBaseURL),
	)
	lookupTool := function.NewFunctionTool(
		harnessLookup,
		function.WithName("harness_lookup"),
		function.WithDescription("Returns a verification token for a supplied key. You must call this tool to know the exact token value."),
	)

	return llmagent.New(
		"langfuse-tool-reasoning-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A small harness agent for verifying tool-call and reasoning telemetry."),
		llmagent.WithInstruction(
			"You are a concise assistant. You MUST call harness_lookup exactly once before answering. "+
				"Do not guess or fabricate the token. After the tool returns, answer in one short sentence that includes the token and mention the telemetry tool harness.",
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{lookupTool}),
	)
}

func runHarnessAgent(
	ctx context.Context,
	agentInstance *llmagent.LLMAgent,
	appName, sessionID string,
	message model.Message,
) (string, error) {
	r := runner.NewRunner(appName, agentInstance)
	defer r.Close()

	eventCh, err := r.Run(ctx, harnessUserID, sessionID, message)
	if err != nil {
		return "", err
	}

	var finalText strings.Builder
	for evt := range eventCh {
		if err := eventError(evt); err != nil {
			return finalText.String(), err
		}
		appendEventText(&finalText, evt)
	}

	return strings.TrimSpace(finalText.String()), nil
}

func eventError(evt *event.Event) error {
	if evt == nil {
		return nil
	}
	if evt.Error != nil {
		return fmt.Errorf("%s: %s", evt.Error.Type, evt.Error.Message)
	}
	if evt.Response != nil && evt.Response.Error != nil {
		return fmt.Errorf("%s: %s", evt.Response.Error.Type, evt.Response.Error.Message)
	}
	return nil
}

func appendEventText(dst *strings.Builder, evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for _, choice := range evt.Response.Choices {
		if choice.Delta.Content != "" {
			dst.WriteString(choice.Delta.Content)
			continue
		}
		if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
			if dst.Len() == 0 || evt.IsFinalResponse() {
				dst.Reset()
				dst.WriteString(choice.Message.Content)
			}
		}
	}
}

func withHarnessBaggage(
	t *testing.T,
	ctx context.Context,
	traceName, sessionID, runID, harnessCase string,
) context.Context {
	t.Helper()

	members := []baggage.Member{
		mustBaggageMember(t, "langfuse.trace.name", traceName),
		mustBaggageMember(t, "langfuse.user.id", harnessUserID),
		mustBaggageMember(t, "langfuse.session.id", sessionID),
		mustBaggageMember(t, "langfuse.trace.metadata.harness_run_id", runID),
		mustBaggageMember(t, "langfuse.trace.metadata.harness_case", harnessCase),
	}
	bag, err := baggage.New(members...)
	require.NoError(t, err)
	return baggage.ContextWithBaggage(ctx, bag)
}

func mustBaggageMember(t *testing.T, key, value string) baggage.Member {
	t.Helper()
	member, err := baggage.NewMemberRaw(key, value)
	require.NoError(t, err)
	return member
}

func hostFromBaseURL(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Host, "LANGFUSE_BASE_URL must include a host")
	return parsed.Host
}

type harnessLookupRequest struct {
	Key string `json:"key"`
}

type harnessLookupResponse struct {
	Key     string `json:"key"`
	Token   string `json:"token"`
	Message string `json:"message"`
}

func harnessLookup(_ context.Context, req harnessLookupRequest) (harnessLookupResponse, error) {
	key := strings.TrimSpace(req.Key)
	return harnessLookupResponse{
		Key:     key,
		Token:   harnessToolToken,
		Message: fmt.Sprintf("verification token for %s", key),
	}, nil
}

func resetHarnessTracerProvider() {
	atrace.TracerProvider = nooptrace.NewTracerProvider()
	atrace.Tracer = atrace.TracerProvider.Tracer("")
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
