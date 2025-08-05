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
// support for text, image, audio, and file uploads using both file data (base64)
// and file_ids approaches.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName  = flag.String("model", "gpt-4o", "Model to use")
	textInput  = flag.String("text", "", "Text input")
	imagePath  = flag.String("image", "", "Path to image file")
	audioPath  = flag.String("audio", "", "Path to audio file")
	filePath   = flag.String("file", "", "Path to file to upload")
	variant    = flag.String("variant", "openai", "Model variant (openai, hunyuan)")
	streaming  = flag.Bool("streaming", true, "Enable streaming mode for responses")
	useFileIDs = flag.Bool("file-ids", true, "Use file_ids instead of file data (base64)")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	if *textInput == "" && *imagePath == "" && *audioPath == "" && *filePath == "" {
		log.Fatal("At least one input is required: -text, -image, -audio, or -file")
	}

	fmt.Printf("🚀 File Input Processing with OpenAI Model\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Variant: %s\n", *variant)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("File Mode: %s\n", func() string {
		if *useFileIDs {
			return "file_ids (recommended for Hunyuan/Gemini)"
		}
		return "file data (base64)"
	}())
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the file processor.
	processor := &fileProcessor{
		modelName:  *modelName,
		variant:    *variant,
		streaming:  *streaming,
		textInput:  *textInput,
		imagePath:  *imagePath,
		audioPath:  *audioPath,
		filePath:   *filePath,
		useFileIDs: *useFileIDs,
	}

	if err := processor.run(); err != nil {
		log.Fatalf("File processing failed: %v", err)
	}
}

// fileProcessor manages the file input processing using runner pattern.
type fileProcessor struct {
	modelName      string
	modelInstance  *openaimodel.Model
	variant        string
	streaming      bool
	textInput      string
	imagePath      string
	audioPath      string
	filePath       string
	useFileIDs     bool
	uploadedFileID string
	runner         runner.Runner
	userID         string
	sessionID      string
}

// run starts the file processing session.
func (p *fileProcessor) run() error {
	ctx := context.Background()

	// Setup the model.
	if err := p.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Process the file inputs.
	if err := p.processInputs(ctx); err != nil {
		return err
	}

	// Cleanup uploaded file.
	return p.cleanup(ctx)
}

// setup creates the runner with LLM agent for file processing.
func (p *fileProcessor) setup() error {
	// Convert variant string to Variant type.
	var variant openaimodel.Variant
	switch p.variant {
	case "hunyuan":
		variant = openaimodel.VariantHunyuan
	default:
		variant = openaimodel.VariantOpenAI
	}

	// Create OpenAI model.
	p.modelInstance = openaimodel.New(p.modelName,
		openaimodel.WithVariant(variant),
	)

	// Create LLM agent for file processing.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      p.streaming,
	}

	agentName := "file-processor"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(p.modelInstance),
		llmagent.WithDescription("An AI assistant that can process and analyze files, images, audio, and text"),
		llmagent.WithInstruction("Analyze the provided content and provide helpful insights. "+
			"If files are uploaded, examine their content and explain what you find."),
		llmagent.WithGenerationConfig(genConfig),
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
	p.sessionID = "file-session"

	fmt.Printf("✅ File processor ready!\n\n")
	return nil
}

// processInputs handles the file input processing.
func (p *fileProcessor) processInputs(ctx context.Context) error {
	// Create user message.
	userMessage := model.NewUserMessage("What is the content of the file?")

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
		if p.useFileIDs {
			if err := p.addFileWithID(ctx, &userMessage, p.filePath); err != nil {
				return fmt.Errorf("failed to add file with ID: %w", err)
			}
		} else {
			if err := userMessage.AddFilePath(p.filePath); err != nil {
				return fmt.Errorf("failed to add file: %w", err)
			}
		}
		fmt.Printf("📄 File input: %s (mode: %s)\n", p.filePath, func() string {
			if p.useFileIDs {
				return "file_ids"
			}
			return "file data"
		}())
	}

	// Process the message through the model.
	return p.processMessage(ctx, userMessage)
}

// addFileWithID uploads a file to OpenAI and adds it using file_id.
func (p *fileProcessor) addFileWithID(ctx context.Context, userMessage *model.Message, filePath string) error {
	// Convert variant string to Variant type.
	var variant openaimodel.Variant
	switch p.variant {
	case "hunyuan":
		variant = openaimodel.VariantHunyuan
	default:
		variant = openaimodel.VariantOpenAI
	}

	// Create a temporary model instance for file upload.
	modelInstance := openaimodel.New(p.modelName, openaimodel.WithVariant(variant))

	// Upload file to OpenAI with variant-specific defaults.
	fileID, err := modelInstance.UploadFile(ctx, filePath)
	if err != nil {
		return fmt.Errorf("failed to upload file to OpenAI: %w", err)
	}

	// Store the file ID for cleanup.
	p.uploadedFileID = fileID

	// Add file ID to message.
	userMessage.AddFileID(fileID)

	fmt.Printf("📤 File uploaded with ID: %s (variant: %s)\n", fileID, p.variant)
	return nil
}

// cleanup deletes the uploaded file after processing is complete.
func (p *fileProcessor) cleanup(ctx context.Context) error {
	if p.uploadedFileID == "" {
		return nil // No file was uploaded.
	}
	fmt.Printf("🧹 Cleaning up uploaded file: %s\n", p.uploadedFileID)
	if err := p.modelInstance.DeleteFile(ctx, p.uploadedFileID); err != nil {
		return fmt.Errorf("failed to delete uploaded file: %w", err)
	}
	fmt.Printf("✅ File deleted successfully: %s\n", p.uploadedFileID)
	return nil
}

// processMessage handles a single message exchange using runner.
func (p *fileProcessor) processMessage(ctx context.Context, userMessage model.Message) error {
	// Run the agent through the runner.
	eventChan, err := p.runner.Run(ctx, p.userID, p.sessionID, userMessage)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	// Process response.
	return p.processResponse(eventChan)
}

// processResponse handles both streaming and non-streaming responses with events.
func (p *fileProcessor) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent string

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Process content from choices.
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

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
