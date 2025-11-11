//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use Jupyter code execution capabilities with LLMAgent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/jupyter"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	// Read configuration from command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use (agent mode only)")
	flag.Parse()

	fmt.Printf("Creating LLMAgent with Jupyter code executor:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- Code Executor: Jupyter\n")
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

	// Create Jupyter executor
	jupyterExecutor, err := jupyter.New()
	if err != nil {
		log.Fatalf("Failed to create Jupyter executor: %v", err)
	}
	defer jupyterExecutor.Close()

	// if you have a jupyter server running, you can use the client mode.
	// jupyter kernelgateway --KernelGatewayApp.auth_token <TOKEN> --JupyterApp.answer_yes true
	//cli, err := jupyter.NewClient(jupyter.ConnectionInfo{
	//	Host:       "127.0.0.1",
	//	Port:       8888,
	//	Token:      "<TOKEN>",
	//	KernelName: "python3",
	//})
	//if err != nil {
	//	log.Fatalf("Failed to create Jupyter client: %v", err)
	//}
	// llmagent.WithCodeExecutor(cli)

	name := "jupyter_data_agent"
	// Create an LLMAgent with Jupyter code executor.
	llmAgent := llmagent.New(
		name,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("agent for data science tasks using Jupyter kernel"),
		llmagent.WithInstruction(`You are a data assistant, executing code using the Jupyter kernel.
One core feature of Jupyter is handling multiple code executions while maintaining state. 
You need to analyze the user's requests, generate multiple code segments, 
where each segment can use variables defined in the previous segment, thus achieving code reuse and state preservation.
When generating multiple code segments, explain their usage and print the output.

Your workflow:
1. Understand the user's data analysis request
2. Write appropriate Python code to analyze the data
3. Execute the code using Jupyter kernel
4. Execute the code multiple times as much as possible, because the Jupyter kernel maintains the state, and this feature can be utilized.
5. Present the results clearly

You should include all relevant data and visualizations in your response.
If you cannot answer the question directly, explain why and suggest alternatives.

You have access to Jupyter kernel with Python 3.x and standard data science libraries.
`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithCodeExecutor(jupyterExecutor),
	)

	r := runner.NewRunner(
		"jupyter_data_agent",
		llmAgent,
	)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	// Example query for data analysis
	//query := "analyze the following dataset and provide descriptive statistics: 5, 12, 8, 15, 7, 9, 11"
	query := "Randomly generate two matrices and then calculate their product."

	eventChan, err := r.Run(context.Background(), "user-id", "session-id", model.NewUserMessage(query))
	if err != nil {
		log.Fatalf("Failed to run LLMAgent: %v", err)
	}

	fmt.Println("\n=== LLMAgent with Jupyter Execution ===")
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
