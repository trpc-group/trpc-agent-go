// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	inmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemeory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type generateImageInput struct {
	Prompt string `json:"prompt"`
}

type generateImageOutput struct {
	Result string `json:"result"`
}

func generateImage(ctx context.Context, input generateImageInput) (generateImageOutput, error) {
	images, err := mockLLMGenerateImage(ctx, input.Prompt)
	if err != nil {
		return generateImageOutput{}, fmt.Errorf("failed to generate image:: %w", err)
	}
	if len(images) == 0 {
		return generateImageOutput{"No images generated"}, nil
	}
	toolCtx, err := agent.NewToolContext(ctx)
	if err != nil {
		return generateImageOutput{}, fmt.Errorf("failed to create tool context: %w", err)
	}
	var output generateImageOutput

	for _, img := range images {
		output.Result += fmt.Sprintf("Image has been generated at the URL %s.", img.url)
		a := &artifact.Artifact{
			MimeType: img.mimeType,
			URL:      img.url,
			Data:     img.content,
		}
		imageID := generateRandomID()
		_, err = toolCtx.SaveArtifact(imageID, a)
		if err != nil {
			output.Result += fmt.Sprintf("Failed to save image(%s) to artifact, err: %v\n", img.url, err)
		} else {
			output.Result += fmt.Sprintf(" Saved as artifact with ID: %s\n", imageID)
		}
	}
	return output, nil
}

type image struct {
	mimeType string
	content  []byte
	url      string
}

func mockLLMGenerateImage(ctx context.Context, prompt string) ([]image, error) {
	// Get the current directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	// Resource directory path
	resourceDir := filepath.Join(currentDir, "resource")

	// Read directory contents
	files, err := os.ReadDir(resourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource directory: %w", err)
	}

	var images []image

	// Process each image file
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Check if it's an image file
		fileName := file.Name()
		if !strings.HasSuffix(strings.ToLower(fileName), ".png") &&
			!strings.HasSuffix(strings.ToLower(fileName), ".jpg") &&
			!strings.HasSuffix(strings.ToLower(fileName), ".jpeg") {
			continue
		}

		// Read file content
		filePath := filepath.Join(resourceDir, fileName)
		content, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Warning: failed to read file %s: %v\n", fileName, err)
			continue
		}

		// Determine MIME type based on file extension
		var mimeType string
		ext := strings.ToLower(filepath.Ext(fileName))
		switch ext {
		case ".png":
			mimeType = "image/png"
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		default:
			mimeType = "image/png" // default to PNG
		}

		// Create image struct
		img := image{
			mimeType: mimeType,
			content:  content,
			url:      fmt.Sprintf("file://%s", filePath),
		}

		images = append(images, img)
	}
	return images, nil
}

var generateImageTool = function.NewFunctionTool(
	generateImage,
	function.WithName("text-to-image"),
	function.WithDescription("generate image by input text"),
)

// imageGenerateAgent manages the multi-tool conversation system
type imageGenerateAgent struct {
	modelName       string
	runner          runner.Runner
	appName         string
	userID          string
	sessionID       string
	artifactService artifact.Service
}

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
		llmagent.WithInstruction(`When the user requests an image, first rewrite and optimize the prompt in English, then call text-to-image tool to generate it.`),
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

	fmt.Printf("✅ Multi-tool intelligent assistant is ready! Session ID: %s\n\n", a.sessionID)
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
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 Tool calls:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
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
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

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
		if event.Done && !a.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// isToolEvent checks if the event is a tool response (not a final response)
func (a *imageGenerateAgent) isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}
	return false
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
