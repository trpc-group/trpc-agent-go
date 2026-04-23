//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use the Container (Docker) code execution
// capabilities with LLMAgent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	// Read configuration from command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("Creating LLMAgent with Container code executor:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- Code Executor: Docker container\n")
	fmt.Printf("- OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment\n")
	fmt.Println()

	// Create a model instance.
	// The OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment variables.
	modelInstance := openai.New(*modelName)

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	// Create a Container code executor.
	// This will pull the default python:3.9-slim image and launch a disposable
	// container with no network access. Make sure Docker is installed and the
	// daemon is reachable (DOCKER_HOST or /var/run/docker.sock).
	containerExecutor, err := container.New()
	if err != nil {
		log.Fatalf("Failed to create container executor: %v", err)
	}
	defer func() {
		if cerr := containerExecutor.Close(); cerr != nil {
			log.Printf("Failed to close container executor: %v", cerr)
		}
	}()

	// Alternative configurations:
	//
	// 1) Use a custom image:
	// containerExecutor, err := container.New(
	//     container.WithContainerConfig(dockercontainer.Config{
	//         Image:      "python:3.11-slim",
	//         WorkingDir: "/",
	//         Cmd:        []string{"tail", "-f", "/dev/null"},
	//         Tty:        true,
	//         OpenStdin:  true,
	//     }),
	// )
	//
	// 2) Build the image from a local Dockerfile directory:
	// containerExecutor, err := container.New(
	//     container.WithDockerFilePath("./docker"),
	// )
	//
	// 3) Bind-mount a host directory (e.g. read-only inputs):
	// containerExecutor, err := container.New(
	//     container.WithBindMount("/host/inputs", "/data/inputs", "ro"),
	// )

	name := "container_data_agent"
	// Create an LLMAgent with the Container code executor.
	// llmagent.WithCodeExecutor auto-executes a response only when the assistant
	// reply is exactly one runnable fenced code block, so the instruction below
	// asks the model to produce such a reply deterministically.
	llmAgent := llmagent.New(
		name,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("agent for data science tasks running in a sandboxed Docker container"),
		llmagent.WithInstruction(`You are a data assistant. Your code is executed inside an isolated Docker container with no network access.

Your workflow:
1. Understand the user's data analysis request.
2. When computation is needed, reply with exactly one runnable fenced `+"`python`"+` code block and no surrounding prose, explanations, or additional text. The framework will auto-execute this block via WithCodeExecutor.
3. Print the results so they appear in the execution output.
4. After the execution output is available, summarize the findings for the user.

Constraints:
- You should NEVER install any package on your own like pip install .... The container does not have internet access.
- Only use Python's standard library.
- Keep code snippets short and focused.
`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithCodeExecutor(containerExecutor),
	)

	r := runner.NewRunner(
		"container_data_agent",
		llmAgent,
	)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	query := "analyze some sample data: 5, 12, 8, 15, 7, 9, 11. " +
		"Compute the mean, median, variance and standard deviation using only Python's standard library."

	// Use a bounded context so a hanging model or container cannot block the
	// process indefinitely and so deferred cleanup runs in time.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	eventChan, err := r.Run(ctx, "user-id", "session-id", model.NewUserMessage(query))
	if err != nil {
		// Use Printf + return (not Fatalf) so the deferred r.Close() and
		// containerExecutor.Close() still run and remove the Docker container.
		log.Printf("Failed to run LLMAgent: %v", err)
		return
	}

	fmt.Println("\n=== LLMAgent with Container Execution ===")
	fmt.Println("Processing events from LLMAgent:")

	// Process events from the agent.
	eventCount := 0
	for event := range eventChan {
		eventCount++

		fmt.Printf("\n--- Event %d ---\n", eventCount)
		fmt.Printf("ID: %s\n", event.ID)
		fmt.Printf("Author: %s\n", event.Author)
		fmt.Printf("InvocationID: %s\n", event.InvocationID)
		fmt.Printf("Object: %s\n", event.Object)

		if event.Error != nil {
			fmt.Printf("Error: %s (Type: %s)\n", event.Error.Message, event.Error.Type)
		}

		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]

			if choice.Message.Content != "" {
				fmt.Printf("Message Content: %s\n", choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				fmt.Printf("Delta Content: %s\n", choice.Delta.Content)
			}
			if choice.FinishReason != nil {
				fmt.Printf("Finish Reason: %s\n", *choice.FinishReason)
			}
		}

		if event.Usage != nil {
			fmt.Printf("Token Usage - Prompt: %d, Completion: %d, Total: %d\n",
				event.Usage.PromptTokens,
				event.Usage.CompletionTokens,
				event.Usage.TotalTokens)
		}

		fmt.Printf("Done: %t\n", event.Done)

		if event.Done {
			break
		}
	}

	fmt.Printf("\n=== Execution Complete ===\n")
	fmt.Printf("Total events processed: %d\n", eventCount)

	if eventCount == 0 {
		fmt.Println("No events were generated. This might indicate:")
		fmt.Println("- Model configuration issues")
		fmt.Println("- Network connectivity problems")
		fmt.Println("- Docker daemon not reachable")
		fmt.Println("- Check the logs for more details")
	}

	fmt.Println("=== Demo Complete ===")
}

// intPtr returns a pointer to the given int value.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64 value.
func floatPtr(f float64) *float64 {
	return &f
}
