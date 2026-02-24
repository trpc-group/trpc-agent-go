//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides a runnable example for the gateway server.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/gateway"
)

const (
	defaultListenAddr = ":8080"
	defaultMode       = "mock"

	modeMock   = "mock"
	modeOpenAI = "openai"

	mockModelName = "mock-echo"

	csvDelimiter = ","

	defaultOpenAIModel = "deepseek-chat"

	openAIVariantAuto = "auto"

	defaultOpenAIVariant = openAIVariantAuto

	deepSeekModelHint = "deepseek"
	qwenModelHint     = "qwen"
	hunyuanModelHint  = "hunyuan"

	openAIBaseURLEnvName = "OPENAI_BASE_URL"
)

func main() {
	addr := flag.String(
		"addr",
		defaultListenAddr,
		"Listen address",
	)
	mode := flag.String(
		"mode",
		defaultMode,
		"Model mode: mock or openai",
	)
	openAIModel := flag.String(
		"model",
		defaultOpenAIModel,
		"OpenAI model name (mode=openai)",
	)
	openAIVariant := flag.String(
		"openai-variant",
		defaultOpenAIVariant,
		"OpenAI variant: auto, openai, deepseek, qwen, hunyuan",
	)
	mockDelay := flag.Duration(
		"mock-delay",
		0,
		"Delay for mock model responses",
	)
	allowUsers := flag.String(
		"allow-users",
		"",
		"Comma-separated allowlist; empty allows all",
	)
	requireMention := flag.Bool(
		"require-mention",
		false,
		"Require mention in thread messages",
	)
	mention := flag.String(
		"mention",
		"",
		"Comma-separated mention patterns",
	)
	flag.Parse()

	modelInstance, err := newModel(
		*mode,
		*openAIModel,
		*openAIVariant,
		*mockDelay,
	)
	if err != nil {
		log.Fatalf("create model failed: %v", err)
	}

	llm := llmagent.New(
		"assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(
			"You are a helpful assistant. Keep replies concise.",
		),
	)

	r := runner.NewRunner("gateway-demo", llm)

	var gwOpts []gateway.Option
	if strings.TrimSpace(*allowUsers) != "" {
		users := splitCSV(*allowUsers)
		gwOpts = append(gwOpts, gateway.WithAllowUsers(users...))
	}
	if *requireMention {
		patterns := splitCSV(*mention)
		if len(patterns) == 0 {
			log.Fatalf(
				"`-require-mention` requires `-mention` patterns",
			)
		}
		gwOpts = append(
			gwOpts,
			gateway.WithRequireMentionInThreads(true),
		)
		gwOpts = append(
			gwOpts,
			gateway.WithMentionPatterns(patterns...),
		)
	}

	srv, err := gateway.New(r, gwOpts...)
	if err != nil {
		log.Fatalf("create gateway server failed: %v", err)
	}

	log.Infof("Gateway listening on %s", *addr)
	log.Infof("Health:   GET  %s", srv.HealthPath())
	log.Infof("Messages: POST %s", srv.MessagesPath())
	log.Infof("Status:   GET  %s?request_id=...", srv.StatusPath())
	log.Infof("Cancel:   POST %s", srv.CancelPath())

	//nolint:gosec
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func newModel(
	mode string,
	openAIModel string,
	openAIVariant string,
	mockDelay time.Duration,
) (model.Model, error) {
	switch strings.TrimSpace(mode) {
	case modeMock:
		return &echoModel{name: mockModelName, delay: mockDelay}, nil
	case modeOpenAI:
		variant, err := parseOpenAIVariant(openAIVariant, openAIModel)
		if err != nil {
			return nil, err
		}
		opts := []openai.Option{openai.WithVariant(variant)}
		baseURL := strings.TrimSpace(os.Getenv(openAIBaseURLEnvName))
		if baseURL != "" {
			opts = append(opts, openai.WithBaseURL(baseURL))
		}
		return openai.New(openAIModel, opts...), nil
	default:
		return nil, fmt.Errorf("unsupported mode: %s", mode)
	}
}

func parseOpenAIVariant(
	raw string,
	modelName string,
) (openai.Variant, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == openAIVariantAuto {
		return inferOpenAIVariant(modelName), nil
	}

	variant := openai.Variant(v)
	switch variant {
	case openai.VariantOpenAI,
		openai.VariantDeepSeek,
		openai.VariantHunyuan,
		openai.VariantQwen:
		return variant, nil
	default:
		return "", fmt.Errorf("unsupported openai variant: %s", raw)
	}
}

func inferOpenAIVariant(modelName string) openai.Variant {
	name := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.Contains(name, deepSeekModelHint):
		return openai.VariantDeepSeek
	case strings.Contains(name, qwenModelHint):
		return openai.VariantQwen
	case strings.Contains(name, hunyuanModelHint):
		return openai.VariantHunyuan
	default:
		return openai.VariantOpenAI
	}
}

func splitCSV(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, csvDelimiter)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

type echoModel struct {
	name  string
	delay time.Duration
}

func (m *echoModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *echoModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}

	ch := make(chan *model.Response, 1)
	if err := m.wait(ctx); err != nil {
		close(ch)
		return ch, err
	}

	text := lastUserText(req)
	reply := fmt.Sprintf("Echo: %s", text)
	ch <- &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Model:  m.name,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage(reply)},
		},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (m *echoModel) wait(ctx context.Context) error {
	if m.delay <= 0 {
		return nil
	}

	timer := time.NewTimer(m.delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func lastUserText(req *model.Request) string {
	if req == nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != model.RoleUser {
			continue
		}
		if msg.Content != "" {
			return msg.Content
		}
		return ""
	}
	return ""
}
