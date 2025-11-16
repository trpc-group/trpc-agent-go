//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use the code execution capabilities of the LLMAgent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	// Read configuration from command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("Creating LLMAgent with configuration:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment\n")
	fmt.Println()

	// Create a model instance.
	// The OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment variables.
	modelInstance := openai.New(*modelName) // Larger buffer for agent use.

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	name := "data_science_agent"
	// Create an LLMAgent with configuration.
	llmAgent := llmagent.New(
		name,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("agent for data science tasks"),
		llmagent.WithInstruction(baseSystemInstruction()+
			`You need to assist the user with their queries by looking at the data and the context in the conversation.
You final answer should summarize the code and code execution relavant to the user query.

You should include all pieces of data to answer the user query, such as the table from code execution results.
If you cannot answer the question directly, you should follow the guidelines above to generate the next step.
If the question can be answered directly with writing any code, you should do that.
If you doesn't have enough data to answer the question, you should ask for clarification from the user.

You should NEVER install any package on your own like pip install ....
	`,
		),
		llmagent.WithGenerationConfig(genConfig),
		// codeexecutor.NewContainerCodeExecutor() is also available.
		// can use llmagent.WithCodeExecutor(codeexecutor.NewContainerCodeExecutor()),
		llmagent.WithCodeExecutor(local.New()),
	)

	r := runner.NewRunner(
		"data_science_agent",
		llmAgent,
	)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	eventChan, err := r.Run(context.Background(), "user-id", "session-id", model.NewUserMessage("analyze some sample data: 5, 12, 8, 15, 7, 9, 11"))
	if err != nil {
		log.Fatalf("Failed to run LLMAgent: %v", err)
	}

	fmt.Println("\n=== LLMAgent Execution ===")
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

func baseSystemInstruction() string {
	// Read content from instruction.md file.
	content, err := os.ReadFile("instruction.md")
	if err != nil {
		log.Printf("Failed to read instruction.md: %v", err)
		return ""
	}
	return string(content)
}
