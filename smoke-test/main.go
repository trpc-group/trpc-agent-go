//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main runs real LLM smoke tests for chat telemetry.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/baggage"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

func main() {
	mode := flag.String("mode", "direct", "smoke mode: direct or llmflow")
	modelName := flag.String("model", getenv("MODEL_NAME", "gpt-4o-mini"), "model name")
	baseURL := flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL")
	apiKey := flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "OpenAI-compatible API key")
	prompt := flag.String("prompt", "Reply with exactly: chat telemetry smoke ok", "prompt")
	smokeID := flag.String("smoke-id", "", "Langfuse metadata smoke id")
	stream := flag.Bool("stream", true, "enable streaming")
	flag.Parse()

	if *smokeID == "" {
		*smokeID = fmt.Sprintf("chat-telemetry-%d", time.Now().UnixNano())
	}
	if err := ensureLangfuseHost(); err != nil {
		log.Fatalf("prepare Langfuse env: %v", err)
	}
	clean, err := langfuse.Start(context.Background())
	if err != nil {
		log.Fatalf("start Langfuse telemetry: %v", err)
	}
	defer func() {
		if err := clean(context.Background()); err != nil {
			log.Printf("shutdown Langfuse telemetry: %v", err)
		}
	}()

	ctx, err := smokeContext(context.Background(), *smokeID, *mode)
	if err != nil {
		log.Fatalf("prepare smoke context: %v", err)
	}

	var output string
	switch *mode {
	case "direct":
		output, err = runDirectModel(ctx, *modelName, *baseURL, *apiKey, *prompt, *stream)
	case "llmflow":
		output, err = runLLMFlow(ctx, *modelName, *baseURL, *apiKey, *prompt, *stream, *smokeID)
	default:
		err = fmt.Errorf("unknown mode %q", *mode)
	}
	if err != nil {
		log.Fatalf("smoke failed: %v", err)
	}
	fmt.Printf("smoke_id=%s mode=%s output=%q\n", *smokeID, *mode, output)
}

func runDirectModel(
	ctx context.Context,
	modelName string,
	baseURL string,
	apiKey string,
	prompt string,
	stream bool,
) (string, error) {
	llm := openai.New(
		modelName,
		openai.WithBaseURL(baseURL),
		openai.WithAPIKey(apiKey),
		openai.WithChatTelemetry(true),
	)
	ch, err := llm.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{model.NewUserMessage(prompt)},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: intPtr(128),
			Stream:    stream,
		},
	})
	if err != nil {
		return "", err
	}
	return collectModelOutput(ch)
}

func runLLMFlow(
	ctx context.Context,
	modelName string,
	baseURL string,
	apiKey string,
	prompt string,
	stream bool,
	smokeID string,
) (string, error) {
	llm := openai.New(
		modelName,
		openai.WithBaseURL(baseURL),
		openai.WithAPIKey(apiKey),
	)
	agent := llmagent.New(
		"chat-telemetry-smoke-agent",
		llmagent.WithModel(llm),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(128),
			Stream:    stream,
		}),
	)
	r := runner.NewRunner("chat-telemetry-smoke", agent)
	defer func() {
		if err := r.Close(); err != nil {
			log.Printf("close runner: %v", err)
		}
	}()
	events, err := r.Run(ctx, "smoke-user", "smoke-session-"+smokeID, model.NewUserMessage(prompt))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Error != nil {
			return b.String(), fmt.Errorf("model response error: %s", evt.Error.Message)
		}
		for _, choice := range evt.Response.Choices {
			b.WriteString(choice.Delta.Content)
			b.WriteString(choice.Message.Content)
		}
	}
	return b.String(), nil
}

func collectModelOutput(ch <-chan *model.Response) (string, error) {
	var b strings.Builder
	for resp := range ch {
		if resp == nil {
			continue
		}
		if resp.Error != nil {
			return b.String(), fmt.Errorf("model response error: %s", resp.Error.Message)
		}
		for _, choice := range resp.Choices {
			b.WriteString(choice.Delta.Content)
			b.WriteString(choice.Message.Content)
		}
	}
	return b.String(), nil
}

func smokeContext(ctx context.Context, smokeID string, mode string) (context.Context, error) {
	members := make([]baggage.Member, 0, 5)
	for _, item := range []struct {
		key   string
		value string
	}{
		{key: "langfuse.user.id", value: "chat-telemetry-smoke-user"},
		{key: "langfuse.session.id", value: "chat-telemetry-smoke-" + smokeID},
		{key: "langfuse.trace.metadata.smoke_id", value: smokeID},
		{key: "langfuse.trace.metadata.mode", value: mode},
		{key: "langfuse.trace.tags", value: "chat-telemetry-smoke"},
	} {
		member, err := baggage.NewMemberRaw(item.key, item.value)
		if err != nil {
			return ctx, err
		}
		members = append(members, member)
	}
	bag, err := baggage.New(members...)
	if err != nil {
		return ctx, err
	}
	return baggage.ContextWithBaggage(ctx, bag), nil
}

func ensureLangfuseHost() error {
	if os.Getenv("LANGFUSE_HOST") != "" {
		return nil
	}
	baseURL := os.Getenv("LANGFUSE_BASE_URL")
	if baseURL == "" {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	host := parsed.Host
	if host == "" {
		return fmt.Errorf("LANGFUSE_BASE_URL has no host")
	}
	if !strings.Contains(host, ":") {
		switch parsed.Scheme {
		case "https":
			host += ":443"
		case "http":
			host += ":80"
			if os.Getenv("LANGFUSE_INSECURE") == "" {
				if err := os.Setenv("LANGFUSE_INSECURE", "true"); err != nil {
					return err
				}
			}
		}
	}
	return os.Setenv("LANGFUSE_HOST", host)
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intPtr(value int) *int {
	return &value
}
