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
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	a2aauth "trpc.group/trpc-go/trpc-a2a-go/v2/auth"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	memorytaskmanager "trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/memory"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const taskAuthHeader = "X-API-Key"

var (
	host = flag.String(
		"host",
		"127.0.0.1:8888",
		"A2A server listen address",
	)
	cardAddress = flag.String(
		"card-address",
		"",
		"Reachable A2A address advertised by the Agent Card (default: host)",
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
	taskAPIKeys = flag.String(
		"task-api-keys",
		os.Getenv("A2A_TASK_API_KEYS"),
		"JSON map of API keys to user IDs for retained tasks (default: A2A_TASK_API_KEYS env var)",
	)
)

func main() {
	flag.Parse()
	if *cardAddress == "" {
		*cardAddress = *host
	}
	currentTimeTool := function.NewFunctionTool(
		func(_ context.Context, args currentTimeArgs) (currentTimeResult, error) {
			location := time.Local
			if args.Timezone != "" {
				loaded, err := time.LoadLocation(args.Timezone)
				if err != nil {
					return currentTimeResult{}, fmt.Errorf("load timezone: %w", err)
				}
				location = loaded
			}
			now := time.Now().In(location)
			return currentTimeResult{
				Timezone: location.String(),
				Time:     now.Format(time.RFC3339),
			}, nil
		},
		function.WithName("current_time"),
		function.WithDescription("Get the current time in an IANA timezone"),
	)

	llmAgent := llmagent.New(
		"session_assistant",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription(
			"A session-aware assistant exposed over A2A protocol v1.0",
		),
		llmagent.WithInstruction(
			"Remember the conversation within each session. "+
				"When asked, summarize the earlier messages in that session. "+
				"Use current_time for time questions.",
		),
		llmagent.WithTools([]tool.Tool{currentTimeTool}),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: *streaming,
		}),
	)

	info := llmAgent.Info()
	card, err := a2aserver.NewAgentCard(
		info.Name,
		info.Description,
		"1.0.0",
		*cardAddress,
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
		apiKeyUsers, err := parseTaskAPIKeys(*taskAPIKeys)
		if err != nil {
			log.Fatalf("configure retained task authentication: %v", err)
		}
		// Supplying a builder is the explicit opt-in boundary for retained A2A Tasks.
		taskManagerName = "memory"
		serverOptions = append(
			serverOptions,
			a2aserver.WithExtraA2AOptions(a2aprotocolserver.WithAuthProvider(
				a2aauth.NewAPIKeyAuthProvider(apiKeyUsers, taskAuthHeader),
			)),
			a2aserver.WithTaskManagerBuilder(func(
				processor taskmanager.MessageProcessor,
			) (taskmanager.TaskManager, error) {
				return memorytaskmanager.NewTaskManager(
					processor,
					memorytaskmanager.WithOwnerResolver(func(ctx context.Context) (string, error) {
						userID, ok := a2aserver.UserIDFromContext(ctx)
						if !ok || userID == "" {
							return "", fmt.Errorf("authenticated user ID is required for retained tasks")
						}
						return userID, nil
					}),
				)
			}),
		)
	}

	server, err := a2aserver.New(serverOptions...)
	if err != nil {
		log.Fatalf("create A2A server: %v", err)
	}

	fmt.Printf("A2A protocol v1.0 server listening on %s\n", *host)
	fmt.Printf("Agent Card address: %s\n", *cardAddress)
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Task manager: %s\n", taskManagerName)
	if err := server.Start(*host); err != nil {
		log.Fatalf("run A2A server: %v", err)
	}
}

func parseTaskAPIKeys(value string) (map[string]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("task-api-keys is required when retain-tasks is enabled")
	}
	apiKeyUsers := make(map[string]string)
	if err := json.Unmarshal([]byte(value), &apiKeyUsers); err != nil {
		return nil, fmt.Errorf("parse task-api-keys JSON: %w", err)
	}
	if len(apiKeyUsers) == 0 {
		return nil, errors.New("task-api-keys must contain at least one API key")
	}
	for apiKey, userID := range apiKeyUsers {
		if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(userID) == "" {
			return nil, errors.New("task-api-keys must not contain empty API keys or user IDs")
		}
	}
	return apiKeyUsers, nil
}

type currentTimeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=IANA timezone such as Asia/Shanghai; empty means local time"`
}

type currentTimeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
}
