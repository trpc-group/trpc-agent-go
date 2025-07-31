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

// Package main demonstrates file input processing using the OpenAI model with
// support for text, image, audio, and file uploads.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
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

	fmt.Printf("🚀 File Input Processing with OpenAI Model\n")
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
	apiKey    string
	model     *openai.Model
}

// run starts the file processing session.
func (p *fileProcessor) run() error {
	ctx := context.Background()

	// Setup the model.
	if err := p.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Process the file inputs.
	return p.processInputs(ctx)
}

// setup creates the OpenAI model.
func (p *fileProcessor) setup() error {
	// Create OpenAI model.
	p.model = openai.New(p.modelName,
		openai.WithAPIKey(p.apiKey),
		openai.WithChannelBufferSize(512),
	)

	fmt.Printf("✅ File processor ready!\n\n")
	return nil
}

// processInputs handles the file input processing.
func (p *fileProcessor) processInputs(ctx context.Context) error {
	// Create user message.
	userMessage := model.NewUserMessage("")

	// Add text content if provided.
	if p.textInput != "" {
		userMessage.Content = p.textInput
		fmt.Printf("📝 Text input: %s\n", p.textInput)
	}

	// Add image content if provided.
	if p.imagePath != "" {
		if err := userMessage.AddImageFilePath(p.imagePath, "auto"); err != nil {
			return fmt.Errorf("failed to add image: %w", err)
		}
		fmt.Printf("🖼️  Image input: %s\n", p.imagePath)
	}

	// Add audio content if provided.
	if p.audioPath != "" {
		if err := userMessage.AddAudioFilePath(p.audioPath); err != nil {
			return fmt.Errorf("failed to add audio: %w", err)
		}
		fmt.Printf("🎵 Audio input: %s\n", p.audioPath)
	}

	// Add file content if provided.
	if p.filePath != "" {
		if err := userMessage.AddFilePath(p.filePath); err != nil {
			return fmt.Errorf("failed to add file: %w", err)
		}
		fmt.Printf("📄 File input: %s\n", p.filePath)
	}

	// Process the message through the model.
	return p.processMessage(ctx, userMessage)
}

// processMessage handles a single message exchange.
func (p *fileProcessor) processMessage(ctx context.Context, userMessage model.Message) error {
	// Create request.
	request := &model.Request{
		Messages: []model.Message{userMessage},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   intPtr(2000),
			Temperature: floatPtr(0.7),
			Stream:      p.streaming,
		},
	}

	// Generate content.
	responseChan, err := p.model.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	// Process response.
	return p.processResponse(responseChan)
}

// processResponse handles both streaming and non-streaming responses.
func (p *fileProcessor) processResponse(responseChan <-chan *model.Response) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent string

	for response := range responseChan {
		// Handle errors.
		if response.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", response.Error.Message)
			continue
		}

		// Process content (streaming or non-streaming).
		if len(response.Choices) > 0 {
			choice := response.Choices[0]

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

		// Check if this is the final response.
		if response.Done {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
