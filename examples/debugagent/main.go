//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
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
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
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

	fmt.Printf("🐞 Debug Agent Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Base Directory: %s\n", *baseDir)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: save_file, read_file, read_multiple_files, list_file, search_file, search_content, replace_content\n")
	fmt.Println(strings.Repeat("=", 50))
	// Create and run the chat.
	chat := &debugChat{
		modelName: *modelName,
		baseDir:   *baseDir,
	}
	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// debugChat manages the conversation with file operation tools.
type debugChat struct {
	modelName string
	baseDir   string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *debugChat) run() error {
	ctx := context.Background()
	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and file operation tools.
func (c *debugChat) setup(_ context.Context) error {
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
		Temperature: floatPtr(0.2),
		Stream:      true, // Enable streaming
	}
	agentName := "debug agent"
	// Execution-first instruction: allow direct bash or write+run script; never write without running
	instruction := strings.Join([]string{
		"You are a debugging assistant with file operation tools and a local CodeExecutor.",
		"",
		"Primary Goal:",
		"- Help users debug, run, build, and fix code in the current project safely and directly.",
		"",
		"Execution Behavior (Two Accepted Modes):",
		"- When the user asks to run/compile/test/debug or check a project, choose ONE of the following and follow it strictly:",
		"  1) Direct Run: Your next message is exactly ONE fenced bash code block (```bash) with NOTHING before or after it, containing the commands to run.",
		"  2) Write + Run Script: First, use save_file/replace_content to write a minimal script (e.g., run.sh) in the working directory. Immediately after the tool result, send a new message that is exactly ONE fenced bash code block (```bash) that runs the script, e.g., bash ./run.sh. Do not add text outside the block.",
		"- Never write a script without running it in the same turn sequence.",
		"- Inside any bash block, run only the commands needed for the task (e.g., go list ./..., go build ./..., optionally go test ./... -count=1; if asked to run the program, prefer go run . or a specific main package like ./cmd/...).",
		"- Do NOT install packages or access the network. Keep execution non-interactive and concise.",
		"- Do NOT emit multiple code blocks. The language must be exactly: bash (lowercase). No text outside the block.",
		"",
		"Working Directory and Scope:",
		"- Work in the current working directory (the host-provided base directory). Do NOT cd out of this directory unless explicitly instructed by the user.",
		"- Use ./... patterns to restrict list/build/test to the current subtree so you only operate on this project, not the entire monorepo.",
		"- When running a saved script, prefer 'bash ./run.sh' over './run.sh' to avoid permission issues; keep paths relative.",
		"",
		"After Execution:",
		"- Summarize results briefly and propose precise next steps (e.g., which files to read or edits to apply). Use tools only when necessary.",
		"",
		"File Operation Rules:",
		"- read_file, list_files, search_files, search_content: can run without confirmation.",
		"- read_multiple_files: Prefer this when you need multiple files in one call to reduce round-trips.",
		"  Use read_file when you need partial ranges of a single file (start_line/num_lines).",
		"- save_file, replace_content: MUST ask for user confirmation before overwriting/creating files.",
		"- Prefer replace_content over save_file for small edits; be precise and explain planned changes.",
		"",
		"Available Tools:",
		"- save_file: Save content to files (confirmation required).",
		"- replace_content: Replace specific strings (confirmation required). Prefer this for small edits.",
		"- read_file: Read file contents.",
		"- read_multiple_files: Read multiple files in a single call.",
		"- list_files: List files and directories.",
		"- search_files: Search for files by pattern.",
		"- search_content: Search for content by pattern.",
		"",
		"General Style:",
		"- Be explicit about what you're doing and why.",
		"- Keep changes minimal and focused; avoid unrelated refactors.",
	}, "\n")
	// Create LLM agent with file operation tools and code executor.
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant for debugging and code maintenance"),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithToolSets([]tool.ToolSet{fileToolSet}),
		llmagent.WithCodeExecutor(local.New(
			local.WithWorkDir(c.baseDir),
			local.WithTimeout(30*time.Second),
			local.WithCleanTempFiles(true),
		)),
	)
	// Create runner.
	appName := "debug-agent"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)
	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("debug-session-%d", time.Now().Unix())
	fmt.Printf("✅ Debug agent ready! Session: %s\n\n", c.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
func (c *debugChat) startChat(ctx context.Context) error {
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
func (c *debugChat) processMessage(ctx context.Context, userMessage string) error {
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
func (c *debugChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	var (
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
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("\n🔧 Tool call initiated:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Arguments: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("🔄 Executing tools...\n")
		}
		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ Tool result (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				fmt.Printf("\n")
				continue
			}
		}
		if event.Response != nil && event.Response.Object == model.ObjectTypePostprocessingCodeExecution {
			for _, choice := range event.Response.Choices {
				content := strings.TrimSpace(choice.Message.Content)
				if content == "" {
					continue
				}
				fmt.Printf("\n✅ Code execution:\n %s\n", content)
			}
			fmt.Printf("\n")
			continue
		}
		// Process streaming content.
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Printf("%s", choice.Delta.Content)
			}
		}
		// Only break on runner completion to ensure we consume all postprocessing events
		if event.Done && event.Response != nil && event.Response.Object == model.ObjectTypeRunnerCompletion {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

// intPtr returns a pointer to the given int.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64.
func floatPtr(f float64) *float64 {
	return &f
}
