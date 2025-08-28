//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"go.uber.org/zap"
	a2alog "trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2a "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use")
	host      = flag.String("host", "0.0.0.0:8888", "Host to bind")
)

func main() {
	flag.Parse()

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	logger, _ := config.Build()
	a2alog.Default = logger.Sugar()
	agentlog.Default = logger.Sugar()

	go func() {
		runRemoteAgent()
	}()
	time.Sleep(1 * time.Second)
	callRemoteAgent()
}

func callRemoteAgent() {
	a2aURL := fmt.Sprintf("http://%s", *host)
	a2aAgent, err := a2aagent.New(a2aagent.WithAgentCardURL(a2aURL))
	if err != nil {
		log.Fatalf("Failed to create a2a agent: %v", err)
	}

	agentCard := a2aAgent.GetAgentCard()
	if agentCard == nil {
		log.Fatalf("Failed to get agent card")
	}
	sessionService := inmemory.NewSessionService()
	runner := runner.NewRunner(agentCard.Name, a2aAgent, runner.WithSessionService(sessionService))

	userID := "user1"
	sessionID := "session1"
	msg := "tell me a joke"

	fmt.Printf("User: %s \n", msg)
	events, err := runner.Run(context.Background(), userID, sessionID, model.Message{
		Role:    model.RoleUser,
		Content: msg,
	})
	if err != nil {
		log.Fatalf("Failed to run agent: %v", err)
	}
	fmt.Printf("Agent: ")
	hasContent := false
	for event := range events {
		if event.Response.Error != nil {
			fmt.Printf("Error: %s", event.Response.Error.Message)
			hasContent = true
			continue
		}
		if len(event.Response.Choices) == 0 {
			continue
		}
		if event.Response.Choices[0].Delta.Content != "" {
			fmt.Printf("%s", event.Response.Choices[0].Delta.Content)
			hasContent = true
		} else if event.Response.Choices[0].Message.Content != "" {
			fmt.Printf("%s", event.Response.Choices[0].Message.Content)
			hasContent = true
		}
	}
	if !hasContent {
		fmt.Printf("No response received")
	}
	fmt.Print("\n")
}

func runRemoteAgent() {
	remoteAgent := buildRemoteAgent(*modelName)
	server, err := a2a.New(
		a2a.WithHost(*host),
		a2a.WithAgent(remoteAgent),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a server: %v", err)
	}
	server.Start(*host)
}

func buildRemoteAgent(modelName string) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(modelName)

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      false, // Enable streaming
	}
	desc := "A remote agent, it will call the remote agent by a2a protocol."

	llmAgent := llmagent.New(
		"remoteAgent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
	)

	return llmAgent
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
