//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent is the agent for the text saving.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// logQueryInput is the input for the log query
type logQueryInput struct {
	Query string `json:"query"`
}

// logQueryOutput is the output for the log query
type logQueryOutput struct{}

// logQuery is the function to log the query
func logQuery(ctx context.Context, query logQueryInput) (logQueryOutput, error) {
	a := &artifact.Artifact{
		Data:     []byte(query.Query),
		MimeType: "text/plain",
	}
	toolCtx, err := agent.NewToolContext(ctx)
	if err != nil {
		log.Errorf("Failed to create tool context: %v", err)
		return logQueryOutput{}, err
	}

	_, err = toolCtx.SaveArtifact("query", a)
	if err != nil {
		log.Errorf("Failed to save artifact: %v", err)
	}
	return logQueryOutput{}, nil
}

var logQueryTool = function.NewFunctionTool(
	logQuery,
	function.WithName("logQuery"),
	function.WithDescription("Logs user queries"),
)

// logQueryAgent manages the text saving conversation system
type logQueryAgent struct {
	modelName       string
	runner          runner.Runner
	appName         string
	userID          string
	sessionID       string
	artifactService artifact.Service
}

// newLogQueryAgent creates a new text saving agent
func newLogQueryAgent(appName, agentName, modelName string) *logQueryAgent {
	a := &logQueryAgent{
		appName:         appName,
		modelName:       modelName,
		artifactService: inmemory.NewService(),
	}
	// Create OpenAI model
	modelInstance := openai.New(a.modelName)

	// Create various tools
	tools := []tool.Tool{
		logQueryTool,
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
		llmagent.WithDescription("Log user query"),
		llmagent.WithInstruction(`Always log the user query and reply "kk, I've logged.`),
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

	fmt.Printf("✅ Text saving agent is ready! Session ID: %s\n\n", a.sessionID)
	return a
}

// processMessage processes a single message exchange for text saving
func (a *logQueryAgent) processMessage(ctx context.Context, userMessage string) error {
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
func (a *logQueryAgent) processStreamingResponse(eventChan <-chan *event.Event) error {
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
