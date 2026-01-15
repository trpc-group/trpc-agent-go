//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides an entrance agent for A2A (Agent-to-Agent) communication.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2a "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

var (
	host      = flag.String("host", "0.0.0.0:8087", "Host to listen on")
	modelName = flag.String("model", "deepseek-chat", "Model to use")
)

func main() {
	// Parse command-line flags.

	flag.Parse()

	numberAgent := buildNumberAgent(*modelName, "a helpful agent, I can calculate numbers")
	server, err := a2a.New(
		a2a.WithHost(*host),
		a2a.WithAgent(numberAgent, true),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a server: %v", err)
	}

	// Set up a channel to listen for termination signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine.
	go func() {
		log.Infof("Starting server on %s...", *host)
		if err := server.Start(*host); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for termination signal.
	sig := <-sigChan
	log.Infof("Received signal %v, shutting down...", sig)

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		log.Errorf("Failed to stop server gracefully: %v", err)
	}
}

func buildNumberAgent(modelName string, desc string) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(modelName)

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxCompletionTokens: intPtr(2000),
		Temperature:         floatPtr(0.7),
		Stream:              true, // Enable streaming
	}
	llmAgent := llmagent.New(
		"calculator",
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
