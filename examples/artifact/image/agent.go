//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent is the agent for the image generation.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// imageGenerateAgent manages the image generation conversation system
type imageGenerateAgent struct {
	modelName       string
	runner          runner.Runner
	appName         string
	userID          string
	sessionID       string
	artifactService artifact.Service
}

// newImageGenerateAgent creates a new image generation agent
func newImageGenerateAgent(appName, agentName, modelName string) *imageGenerateAgent {
	a := &imageGenerateAgent{
		appName:         appName,
		modelName:       modelName,
		artifactService: inmemory.NewService(),
	}
	// Create OpenAI model
	modelInstance := openai.New(a.modelName)

	// Create various tools
	tools := []tool.Tool{
		generateImageTool,
		displayImageTool,
	}

	// Create LLM agent
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming response
	}
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("You are an AI artist skilled at turning text into images"),
		llmagent.WithInstruction(`When the user requests an image,
first rewrite and optimize the prompt in English, 
then call text-to-image tool to generate it, 
finally call display-image tool to display it.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
	)

	a.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithArtifactService(a.artifactService),
	)

	// Set identifiers
	a.userID = "user"
	a.sessionID = fmt.Sprintf("multi-tool-session-%d", time.Now().Unix())

	fmt.Printf("✅ Image generation agent is ready! Session ID: %s\n\n", a.sessionID)
	return a
}

// processMessage processes a single message exchange
func (a *imageGenerateAgent) processMessage(ctx context.Context, userMessage string) error {
	fmt.Printf("👤 User message: %s\n", userMessage)
	message := model.NewUserMessage(userMessage)

	// Run agent through runner
	eventChan, err := a.runner.Run(ctx, a.userID, a.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response
	return a.processStreamingResponse(eventChan)
}

// processStreamingResponse processes streaming response, including tool call visualization
func (a *imageGenerateAgent) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		// Handle errors
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 Tool calls:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				toolIcon := "🔧"
				fmt.Printf("   %s %s (ID: %s)\n", toolIcon, toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Arguments: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n⚡ Executing...\n")
		}

		// Detect tool responses
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ Tool result (ID: %s): %s\n",
						choice.Message.ToolID,
						formatToolResult(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]

			// Process streaming delta content
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		// Check if this is the final event
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// formatToolResult formats the display of tool results
func formatToolResult(content string) string {
	if len(content) > 200 {
		return content[:200] + "..."
	}
	return strings.TrimSpace(content)
}

// intPtr returns a pointer to the given integer
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float
func floatPtr(f float64) *float64 {
	return &f
}

// generateRandomID generates a random ID for artifacts
func generateRandomID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 8

	b := make([]byte, length)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to timestamp-based ID if random generation fails
		return fmt.Sprintf("img_%d", time.Now().UnixNano())
	}

	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}

	return string(b)
}
