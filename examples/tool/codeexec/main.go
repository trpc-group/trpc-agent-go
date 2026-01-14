//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates code execution tool usage with LLM agent
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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/jupyter"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
)

func main() {
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	executorKind := flag.String("executor", "local", "Code executor backend: local or jupyter")
	flag.Parse()

	fmt.Printf("🚀 Code Execution Tool Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Executor: %s\n", *executorKind)
	fmt.Printf("Enter 'exit' to end the conversation\n")
	fmt.Println(strings.Repeat("=", 60))

	// Create and run chat system
	chat := &codeExecChat{
		modelName:    *modelName,
		executorKind: *executorKind,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat system failed to run: %v", err)
	}
}

// codeExecChat manages the code execution conversation system
type codeExecChat struct {
	modelName    string
	executorKind string
	runner       runner.Runner
	userID       string
	sessionID    string
	cleanup      func() error
}

// run starts the interactive chat session
func (c *codeExecChat) run() error {
	ctx := context.Background()

	// Setup runner
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up
	defer c.runner.Close()
	defer func() {
		if c.cleanup != nil {
			if err := c.cleanup(); err != nil {
				log.Printf("cleanup failed: %v", err)
			}
		}
	}()

	// Start interactive chat
	return c.startChat(ctx)
}

// setup creates a runner with code execution tool
func (c *codeExecChat) setup(_ context.Context) error {
	// Create OpenAI model
	modelInstance := openai.New(c.modelName)

	// Create code executor
	var executor codeexecutor.CodeExecutor
	switch strings.ToLower(strings.TrimSpace(c.executorKind)) {
	case "local":
		executor = local.New(
			local.WithTimeout(30 * time.Second),
		)
	case "jupyter":
		je, err := jupyter.New(
			jupyter.WithStartTimeout(30*time.Second),
			jupyter.WithWaitReadyTimeout(30*time.Second),
		)
		if err != nil {
			return fmt.Errorf("create jupyter executor: %w", err)
		}
		executor = je
		c.cleanup = je.Close
	default:
		return fmt.Errorf("unknown -executor=%q (supported: local, jupyter)", c.executorKind)
	}

	// Create code execution tool
	codeExecTool := codeexec.NewTool(executor,
		codeexec.WithDescription("Execute Python or Bash code and return the result. "+
			"Use this when you need to run code for computation, data analysis, or logic verification."),
	)

	// Create LLM agent
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	agentName := "code-exec-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An AI assistant that can execute Python and Bash code"),
		llmagent.WithInstruction(`You are an intelligent assistant that can execute code.
When users ask you to perform calculations, data analysis, or any task that requires code execution,
use the execute_code tool to run Python or Bash code and return the results.

Examples of when to use the tool:
- Mathematical calculations: "Calculate the factorial of 10"
- Data processing: "Generate a list of prime numbers under 100"
- System information: "Show current directory contents"
- Text processing: "Count words in a given text"

Always explain what the code does before executing it.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{codeExecTool}),
	)

	// Create runner
	appName := "code-exec-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Set identifiers
	c.userID = "user"
	c.sessionID = fmt.Sprintf("codeexec-session-%d", time.Now().Unix())

	fmt.Printf("✅ Code execution assistant is ready! Session ID: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop
func (c *codeExecChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Print welcome message and examples
	printExamples()

	for {
		fmt.Print("👤 User: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle exit command
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Process user message
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add blank line between conversation rounds
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage processes a single message exchange
func (c *codeExecChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run agent through runner
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse processes streaming response
func (c *codeExecChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for evt := range eventChan {
		// Handle errors
		if evt.Error != nil {
			if evt.Error.Type == agent.ErrorTypeStopAgentError {
				fmt.Printf("\n🛑 Agent stopped: %s\n", evt.Error.Message)
				return agent.NewStopError(evt.Error.Message)
			}
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}

		// Detect and display tool calls
		if c.handleToolCalls(evt, &toolCallsDetected, &assistantStarted) {
			continue
		}

		// Detect tool responses
		if c.handleToolResponses(evt) {
			continue
		}

		// Process streaming content
		c.processStreamingContent(evt, &toolCallsDetected, &assistantStarted, &fullContent)

		// Check if this is the final event
		if evt.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleToolCalls processes tool call events
func (c *codeExecChat) handleToolCalls(evt *event.Event, toolCallsDetected *bool, assistantStarted *bool) bool {
	if len(evt.Response.Choices) == 0 || len(evt.Response.Choices[0].Message.ToolCalls) == 0 {
		return false
	}

	*toolCallsDetected = true
	if *assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 Tool calls:\n")
	for _, toolCall := range evt.Response.Choices[0].Message.ToolCalls {
		fmt.Printf("   💻 %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			// Parse and display code nicely
			args := string(toolCall.Function.Arguments)
			fmt.Printf("     Arguments: %s\n", truncateString(args, 200))
		}
	}
	fmt.Printf("\n⚡ Executing code...\n")
	return true
}

// handleToolResponses processes tool response events
func (c *codeExecChat) handleToolResponses(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}

	hasToolResponse := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ Execution result (ID: %s):\n%s\n",
				choice.Message.ToolID,
				formatCodeResult(choice.Message.Content))
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

// processStreamingContent processes streaming content events
func (c *codeExecChat) processStreamingContent(evt *event.Event, toolCallsDetected *bool, assistantStarted *bool, fullContent *string) {
	if len(evt.Response.Choices) == 0 {
		return
	}

	choice := evt.Response.Choices[0]

	// Process streaming delta content
	if choice.Delta.Content != "" {
		if !*assistantStarted {
			if *toolCallsDetected {
				fmt.Printf("\n🤖 Assistant: ")
			}
			*assistantStarted = true
		}
		fmt.Print(choice.Delta.Content)
		*fullContent += choice.Delta.Content
	}
}

// truncateString truncates a string to the specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatCodeResult formats code execution result for display
func formatCodeResult(content string) string {
	content = strings.TrimSpace(content)
	if len(content) > 500 {
		return content[:500] + "\n... (output truncated)"
	}
	return content
}

// intPtr returns a pointer to the given integer
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float
func floatPtr(f float64) *float64 {
	return &f
}

// printExamples prints example questions for users
func printExamples() {
	fmt.Println("💡 Example questions you can try:")
	fmt.Println()
	fmt.Println("   📊 Math & Computation:")
	fmt.Println("      • Calculate the factorial of 10")
	fmt.Println("      • What is 123 * 456 + 789?")
	fmt.Println("      • Generate first 20 Fibonacci numbers")
	fmt.Println("      • Find all prime numbers under 100")
	fmt.Println()
	fmt.Println("   🔐 Security & Random:")
	fmt.Println("      • Generate a random 16-character password with letters, numbers and symbols")
	fmt.Println("      • Generate a UUID")
	fmt.Println("      • Calculate the MD5 hash of 'hello world'")
	fmt.Println()
	fmt.Println("   📈 Data Analysis:")
	fmt.Println("      • Calculate mean, median, and std of [1,2,3,4,5,6,7,8,9,10]")
	fmt.Println("      • Sort the list [64, 34, 25, 12, 22, 11, 90] using quicksort")
	fmt.Println()
	fmt.Println("   🎨 Fun & Creative:")
	fmt.Println("      • Create an ASCII art of a cat")
	fmt.Println("      • Print a multiplication table from 1 to 9")
	fmt.Println("      • Draw a simple bar chart for data [5, 3, 8, 2, 7]")
	fmt.Println()
	fmt.Println("   💻 System (Bash):")
	fmt.Println("      • Show current date and time")
	fmt.Println("      • List files in current directory with sizes")
	fmt.Println("      • Show system information (uname -a)")
	fmt.Println("      • Display disk usage")
	fmt.Println()
}
