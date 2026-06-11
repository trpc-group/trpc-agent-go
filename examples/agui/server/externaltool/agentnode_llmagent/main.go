//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates AG-UI GraphAgent resume after an AgentNode LLMAgent external tool call.
package main

import (
	"flag"
	"net/http"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName   = "agui-agentnode-llmagent-externaltool-demo"
	agentName = "agui-agentnode-llmagent-externaltool"
)

var (
	modelName = flag.String("model", "deepseek-v4-flash", "OpenAI-compatible model name.")
	baseURL   = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL.")
	apiKey    = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key for the model service.")
	isStream  = flag.Bool("stream", true, "Whether to stream the response.")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path      = flag.String("path", "/agui", "HTTP path.")
)

func main() {
	flag.Parse()
	modelInstance := openai.New(*modelName, openAIOptions(*baseURL, *apiKey)...)
	generationConfig := newGenerationConfig(*isStream)
	sessionService := sessioninmemory.NewSessionService()
	ga, err := newGraphAgent(modelInstance, generationConfig)
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}
	r := runner.NewRunner(appName, ga, runner.WithSessionService(sessionService))
	defer r.Close()
	server, err := newAGUIServer(r, sessionService)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}
	if strings.TrimSpace(*apiKey) == "" {
		log.Warnf("OPENAI_API_KEY is empty; set it with -api-key or the OPENAI_API_KEY environment variable.")
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", ga.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func openAIOptions(baseURL string, apiKey string) []openai.Option {
	var opts []openai.Option
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		opts = append(opts, openai.WithAPIKey(apiKey))
	}
	return opts
}

func newGenerationConfig(stream bool) model.GenerationConfig {
	return model.GenerationConfig{
		MaxTokens:   intPtr(1024),
		Temperature: floatPtr(0),
		Stream:      stream,
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
