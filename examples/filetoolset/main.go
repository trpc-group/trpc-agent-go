//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates interactive chat using file operation tools.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/file"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	baseDir := flag.String("base-dir", ".", "Base directory for file operations")
	flag.Parse()

	fmt.Printf("📁 File Operations Chat Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Base Directory: %s\n", *baseDir)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: save_file, read_file, list_file, search_file, search_content, replace_content\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &fileChat{
		modelName: *modelName,
		baseDir:   *baseDir,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// fileChat manages the conversation with file operation tools.
type fileChat struct {
	modelName string
	baseDir   string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *fileChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and file operation tools.
func (c *fileChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.WithChannelBufferSize(512))

	// Create file operation tools.
	fileToolSet, err := file.NewToolSet(
		file.WithBaseDir(c.baseDir),
	)
	if err != nil {
		return fmt.Errorf("create file tool set: %w", err)
	}

	// Create LLM agent with file operation tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "file-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant for debugging and code maintenance"),
		llmagent.WithInstruction("You are a debugging assistant with file operation capabilities. "+
			"Your primary goal is to help users debug and fix code issues. "+
			""+
			"DEBUGGING APPROACH: "+
			"1. If the user doesn't explicitly specify a bug or issue, proactively read project files to understand "+
			"the project structure and identify potential problems. "+
			"2. When debugging, first read the relevant files to understand the code structure and identify potential "+
			"issues. "+
			"3. Look for common issues such as concurrency problems, logic errors, file I/O issues, or syntax "+
			"problems. "+
			"4. After identifying a bug, explain the problem clearly and provide a corrected version of the code. "+
			""+
			"FILE OPERATION RULES: "+
			"- READ/LIST/SEARCH operations: Can run silently without user confirmation "+
			"- SAVE/REPLACE operations: Must ask for user confirmation before overwriting or creating files "+
			"- Always be careful with file operations and explain what you're doing "+
			""+
			"AVAILABLE TOOLS: "+
			"- save_file: Save content to files (requires confirmation) "+
			"- replace_content: Replace a specific string in a file to a new string (requires confirmation), "+
			"prefer to use this tool to edit content instead of save_file when modifying small content"+
			"- read_file: Read file contents "+
			"- list_files: List files and directories "+
			"- search_files: Search for files using patterns "+
			"- search_content: Search for content in files using patterns "+
			""+
			"Use the file operation tools to read existing code, analyze it, and save the fixed version when confirmed"+
			" by the user."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithToolSets([]tool.ToolSet{fileToolSet}),
	)

	// Create runner.
	appName := "file-operations-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("file-session-%d", time.Now().Unix())

	fmt.Printf("✅ File operations chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *fileChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Print welcome message with examples.
	fmt.Println("💡 Try asking questions like:")
	fmt.Println("   - Save a file called 'hello.txt' with content 'Hello, World!'")
	fmt.Println("   - Read the file 'hello.txt'")
	fmt.Println("   - List all files in the directory")
	fmt.Println("   - Search for files with pattern '*.txt'")
	fmt.Println("   - Create a file called 'data.json' with JSON content")
	fmt.Println("   - The current directory is a code project. Can you help me fix the bug?")
	fmt.Println()
	fmt.Println("ℹ️  Note: All file operations will be performed in the base directory")
	fmt.Println()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle exit command.
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Process the user message.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *fileChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with file tool visualization.
func (c *fileChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {

		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("📁 File operation initiated:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Arguments: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Processing file operation...\n")
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ File operation result (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

			// Handle streaming delta content.
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

		// Check if this is the final event.
		// Don't break on tool response events (Done=true but not final assistant response).
		if event.Done && !c.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// isToolEvent checks if an event is a tool response (not a final response).
func (c *fileChat) isToolEvent(event *event.Event) bool {
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

// intPtr returns a pointer to the given int.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64.
func floatPtr(f float64) *float64 {
	return &f
}
