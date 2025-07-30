//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package main demonstrates file input processing using the Runner with
// support for text, image, audio, and file uploads.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "gpt-4o", "Model to use")
	textInput = flag.String("text", "", "Text input")
	imagePath = flag.String("image", "", "Path to image file")
	audioPath = flag.String("audio", "", "Path to audio file")
	filePath  = flag.String("file", "", "Path to file to upload")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	if *textInput == "" && *imagePath == "" && *audioPath == "" && *filePath == "" {
		log.Fatal("At least one input is required: -text, -image, -audio, or -file")
	}

	fmt.Printf("🚀 File Input Processing with Runner\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the file processor.
	processor := &fileProcessor{
		modelName: *modelName,
		streaming: *streaming,
		textInput: *textInput,
		imagePath: *imagePath,
		audioPath: *audioPath,
		filePath:  *filePath,
	}

	if err := processor.run(); err != nil {
		log.Fatalf("File processing failed: %v", err)
	}
}

// fileProcessor manages the file input processing.
type fileProcessor struct {
	modelName string
	streaming bool
	textInput string
	imagePath string
	audioPath string
	filePath  string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the file processing session.
func (p *fileProcessor) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := p.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Process the file inputs.
	return p.processInputs(ctx)
}

// setup creates the runner with LLM agent.
func (p *fileProcessor) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(p.modelName, openai.WithChannelBufferSize(512))

	// Create LLM agent.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      p.streaming,
	}

	agentName := "file-input-agent"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An AI assistant that can process text, images, audio, and files"),
		llmagent.WithInstruction("Analyze and respond to the provided content appropriately. "+
			"For images, describe what you see. For audio, transcribe and respond. "+
			"For files, analyze the content and provide insights."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
	)

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner.
	appName := "file-input-processor"
	p.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	p.userID = "user"
	p.sessionID = fmt.Sprintf("file-session-%d", time.Now().Unix())

	fmt.Printf("✅ File processor ready! Session: %s\n\n", p.sessionID)

	return nil
}

// processInputs handles the file input processing.
func (p *fileProcessor) processInputs(ctx context.Context) error {
	// Build content parts.
	var contentParts []model.ContentPart

	// Add text content if provided.
	if p.textInput != "" {
		contentParts = append(contentParts, model.NewTextContentPart(p.textInput))
		fmt.Printf("📝 Text input: %s\n", p.textInput)
	}

	// Add image content if provided.
	if p.imagePath != "" {
		imageData, err := readFileAsBase64(p.imagePath)
		if err != nil {
			return fmt.Errorf("failed to read image file: %w", err)
		}

		// Determine image format from file extension.
		ext := strings.ToLower(filepath.Ext(p.imagePath))
		var format string
		switch ext {
		case ".jpg", ".jpeg":
			format = "jpeg"
		case ".png":
			format = "png"
		case ".gif":
			format = "gif"
		case ".webp":
			format = "webp"
		default:
			return fmt.Errorf("unsupported image format: %s", ext)
		}

		contentParts = append(contentParts, model.NewImageContentPart(
			fmt.Sprintf("data:image/%s;base64,%s", format, imageData), "high"))
		fmt.Printf("🖼️  Image input: %s (%s)\n", p.imagePath, format)
	}

	// Add audio content if provided.
	if p.audioPath != "" {
		audioData, err := readFileAsBase64(p.audioPath)
		if err != nil {
			return fmt.Errorf("failed to read audio file: %w", err)
		}

		contentParts = append(contentParts, model.NewAudioContentPart(
			fmt.Sprintf("data:audio/wav;base64,%s", audioData), "wav"))
		fmt.Printf("🎵 Audio input: %s\n", p.audioPath)
	}

	// Add file content if provided.
	if p.filePath != "" {
		data, err := readFileAsBase64(p.filePath)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		contentParts = append(contentParts, model.NewFileContentPartWithData(filepath.Base(p.filePath), data))
		fmt.Printf("📄 File input: %s\n", p.filePath)
	}

	// Create user message with content parts.
	userMessage := model.NewUserMessageWithContentParts(contentParts)

	// Process the message through the runner.
	return p.processMessage(ctx, userMessage)
}

// processMessage handles a single message exchange.
func (p *fileProcessor) processMessage(ctx context.Context, userMessage model.Message) error {
	// Run the agent through the runner.
	eventChan, err := p.runner.Run(ctx, p.userID, p.sessionID, userMessage)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return p.processResponse(eventChan)
}

// processResponse handles both streaming and non-streaming responses.
func (p *fileProcessor) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent string

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Process content (streaming or non-streaming).
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

			// Handle content based on streaming mode.
			var content string
			if p.streaming {
				// Streaming mode: use delta content.
				content = choice.Delta.Content
			} else {
				// Non-streaming mode: use full message content.
				content = choice.Message.Content
			}

			if content != "" {
				fmt.Print(content)
				fullContent += content
			}
		}

		// Check if this is the final event.
		if event.Done {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// readFileAsBase64 reads a file and returns its base64 encoded content.
func readFileAsBase64(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
