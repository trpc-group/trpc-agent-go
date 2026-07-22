//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main exposes a session-aware LLM agent over A2A protocol v1.0.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	memorytaskmanager "trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/memory"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	host = flag.String(
		"host",
		"127.0.0.1:8888",
		"A2A server address",
	)
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Name of the model to use (default: MODEL_NAME env var)",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming responses",
	)
	retainTasks = flag.Bool(
		"retain-tasks",
		false,
		"Retain A2A tasks in memory and enable retained task management",
	)
)

func main() {
	flag.Parse()

	llmAgent := llmagent.New(
		"session_assistant",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription(
			"A session-aware assistant exposed over A2A protocol v1.0",
		),
		llmagent.WithInstruction(
			"Remember the conversation within each session. "+
				"When asked, summarize the earlier messages in that session.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: *streaming,
		}),
	)

	info := llmAgent.Info()
	card, err := a2aserver.NewAgentCard(
		info.Name,
		info.Description,
		*host,
		*streaming,
		a2aserver.WithCardTools(llmAgent.Tools()...),
	)
	if err != nil {
		log.Fatalf("create Agent Card: %v", err)
	}

	agentRunner := runner.NewRunner(
		info.Name,
		llmAgent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer func() {
		if err := agentRunner.Close(); err != nil {
			log.Printf("close Runner: %v", err)
		}
	}()

	serverOptions := []a2aserver.Option{
		a2aserver.WithRunner(agentRunner),
		a2aserver.WithAgentCard(card),
	}
	taskManagerName := "stateless"
	if *retainTasks {
		// Supplying a builder is the explicit opt-in boundary for retained A2A Tasks.
		taskManagerName = "memory"
		serverOptions = append(serverOptions, a2aserver.WithTaskManagerBuilder(func(
			processor taskmanager.MessageProcessor,
		) (taskmanager.TaskManager, error) {
			return memorytaskmanager.NewTaskManager(processor)
		}))
	}

	server, err := a2aserver.New(serverOptions...)
	if err != nil {
		log.Fatalf("create A2A server: %v", err)
	}

	fmt.Printf("A2A protocol v1.0 server listening on %s\n", *host)
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Task manager: %s\n", taskManagerName)
	if err := server.Start(*host); err != nil {
		log.Fatalf("run A2A server: %v", err)
	}
}
