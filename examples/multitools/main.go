//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an interactive chat system using multiple tools
// including calculator, time, text processing, file operations, and DuckDuckGo search tools
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	flag.Parse()

	fmt.Printf("🚀 Multi-Tool Intelligent Assistant Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Enter 'exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, time_tool, text_tool, file_tool, duckduckgo_search\n")
	fmt.Println(strings.Repeat("=", 60))

	// Create and run chat system
	chat := &multiToolChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat system failed to run: %v", err)
	}
}

// multiToolChat manages the multi-tool conversation system
type multiToolChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session
func (c *multiToolChat) run() error {
	ctx := context.Background()

	// Setup runner
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Start interactive chat
	return c.startChat(ctx)
}

// setup creates a runner containing multiple tools
func (c *multiToolChat) setup(_ context.Context) error {
	// Create OpenAI model
	modelInstance := openai.New(c.modelName)

	// Create various tools
	tools := []tool.Tool{
		createCalculatorTool(),
		createTimeTool(),
		createTextTool(),
		createFileTool(),
		duckduckgo.NewTool(), // Original DuckDuckGo search tool
	}

	// Create LLM agent
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming response
	}

	agentName := "multi-tool-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A powerful AI assistant with multiple tools including calculator, time, text processing, file operations, and web search"),
		// llmagent.WithInstruction(`You are an intelligent assistant that can use multiple tools:
		// 1. calculator: Perform mathematical calculations, supporting basic operations, scientific calculations, etc.
		// 2. time_tool: Get current time, date, timezone information, etc.
		// 3. text_tool: Process text, including case conversion, length statistics, string operations, etc.
		// 4. file_tool: Basic file operations such as reading, writing, listing directories, etc.
		// 5. duckduckgo_search: Search web information, suitable for finding factual, encyclopedia-type information

		// Please select the appropriate tool based on user needs and provide helpful assistance.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
	)

	// Create runner
	appName := "multi-tool-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Set identifiers
	c.userID = "user"
	c.sessionID = fmt.Sprintf("multi-tool-session-%d", time.Now().Unix())

	fmt.Printf("✅ Multi-tool intelligent assistant is ready! Session ID: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop
func (c *multiToolChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Print welcome message and examples
	fmt.Println("💡 Try asking these questions:")
	fmt.Println("   [Calculator] Calculate 123 + 456 * 789")
	fmt.Println("   [Calculator] Calculate the square root of pi")
	fmt.Println("   [Time] What time is it now?")
	fmt.Println("   [Time] What day of the week is today?")
	fmt.Println("   [Text] Convert 'Hello World' to uppercase")
	fmt.Println("   [Text] Count characters in 'Hello World'")
	fmt.Println("   [File] Read the README.md file")
	fmt.Println("   [File] Create a test file in the current directory")
	fmt.Println("   [Search] Search for information about Steve Jobs")
	fmt.Println("   [Search] Find information about Tesla company")
	fmt.Println()

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
func (c *multiToolChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run agent through runner
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse processes streaming response, including tool call visualization
func (c *multiToolChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		// Handle errors
		if event.Error != nil {
			if event.Error.Type == agent.ErrorTypeStopAgentError {
				// Handle stop agent error
				fmt.Printf("\n🛑 Agent stopped: %s\n", event.Error.Message)
				log.Fatal("Agent execution stopped due to error: ", event.Error.Message)
				return agent.NewStopError(event.Error.Message)
			}
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls
		if c.handleToolCalls(event, &toolCallsDetected, &assistantStarted) {
			continue
		}

		// Detect tool responses
		if c.handleToolResponses(event) {
			continue
		}

		// Process streaming content
		c.processStreamingContent(event, &toolCallsDetected, &assistantStarted, &fullContent)

		// Check if this is the final event
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleToolCalls processes tool call events and returns true if handled
func (c *multiToolChat) handleToolCalls(event *event.Event, toolCallsDetected *bool, assistantStarted *bool) bool {
	if len(event.Response.Choices) == 0 || len(event.Response.Choices[0].Message.ToolCalls) == 0 {
		return false
	}

	*toolCallsDetected = true
	if *assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 Tool calls:\n")
	for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
		toolIcon := getToolIcon(toolCall.Function.Name)
		fmt.Printf("   %s %s (ID: %s)\n", toolIcon, toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("     Arguments: %s\n", string(toolCall.Function.Arguments))
		}
	}
	fmt.Printf("\n⚡ Executing...\n")
	return true
}

// handleToolResponses processes tool response events and returns true if handled
func (c *multiToolChat) handleToolResponses(event *event.Event) bool {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return false
	}

	hasToolResponse := false
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ Tool result (ID: %s): %s\n",
				choice.Message.ToolID,
				formatToolResult(choice.Message.Content))
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

// processStreamingContent processes streaming content events
func (c *multiToolChat) processStreamingContent(event *event.Event, toolCallsDetected *bool, assistantStarted *bool, fullContent *string) {
	if len(event.Response.Choices) == 0 {
		return
	}

	choice := event.Response.Choices[0]

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

// getToolIcon returns the corresponding icon based on tool name
func getToolIcon(toolName string) string {
	switch toolName {
	case "calculator":
		return "🧮"
	case "time_tool":
		return "⏰"
	case "text_tool":
		return "📝"
	case "file_tool":
		return "📁"
	case "duckduckgo_search":
		return "🔍"
	default:
		return "🔧"
	}
}

// formatToolResult formats the display of tool results
func formatToolResult(content string) string {
	if len(content) > 200 {
		return content[:200] + "..."
	}
	return strings.TrimSpace(content)
}

// Calculator tool related structures
type calculatorRequest struct {
	Expression string `json:"expression" jsonschema:"description=Mathematical expression to calculate. Supports basic operations (+ - * /). scientific functions (sin/cos/tan/sqrt/log/ln/abs/pow) and constants (pi/e). Examples: '2+3*4'; 'sqrt(16)'; 'sin(30*pi/180)'; 'log10(100)',required"`
}

type calculatorResponse struct {
	Expression string  `json:"expression" jsonschema:"description=The original mathematical expression that was calculated"`
	Result     float64 `json:"result" jsonschema:"description=The numerical result of the calculation"`
	Message    string  `json:"message" jsonschema:"description=Human-readable description of the calculation result"`
}

// createCalculatorTool creates a calculator tool
func createCalculatorTool() tool.CallableTool {
	return function.NewFunctionTool(
		calculateExpression,
		function.WithName("calculator"),
		function.WithDescription("Perform mathematical calculations. Supports basic operations (+, -, *, /), scientific functions (sin, cos, tan, sqrt, log, ln, abs, pow), constants (pi, e). Examples: '2+3*4', 'sqrt(16)', 'sin(30*pi/180)', 'log10(100)'"),
	)
}

// calculateExpression calculates mathematical expressions
func calculateExpression(_ context.Context, req calculatorRequest) (calculatorResponse, error) {
	if strings.TrimSpace(req.Expression) == "" {
		return calculatorResponse{
			Expression: req.Expression,
			Result:     0,
			Message:    "Error: Expression is empty",
		}, fmt.Errorf("expression is empty")
	}

	// Simple expression calculator implementation
	result, err := evaluateExpression(req.Expression)
	if err != nil {
		return calculatorResponse{
			Expression: req.Expression,
			Result:     0,
			Message:    fmt.Sprintf("Calculation error: %v", err),
		}, fmt.Errorf("calculation error: %w", err)
	}

	return calculatorResponse{
		Expression: req.Expression,
		Result:     result,
		Message:    fmt.Sprintf("Calculation result: %g", result),
	}, nil
}

// evaluateExpression simple expression evaluator
func evaluateExpression(expr string) (float64, error) {
	// Replace constants
	expr = strings.ReplaceAll(expr, "pi", fmt.Sprintf("%g", math.Pi))
	expr = strings.ReplaceAll(expr, "e", fmt.Sprintf("%g", math.E))

	// Simple implementation: support basic operations
	// This is a simplified version, real applications might need more complex expression parsers
	expr = strings.ReplaceAll(expr, " ", "")

	// Handle basic mathematical functions
	if strings.Contains(expr, "sqrt(") {
		return handleSqrt(expr)
	}
	if strings.Contains(expr, "sin(") {
		return handleSin(expr)
	}
	if strings.Contains(expr, "cos(") {
		return handleCos(expr)
	}
	if strings.Contains(expr, "abs(") {
		return handleAbs(expr)
	}

	// Handle basic operations
	return evaluateBasicExpression(expr)
}

// handleSqrt handles square root function
func handleSqrt(expr string) (float64, error) {
	if strings.HasPrefix(expr, "sqrt(") && strings.HasSuffix(expr, ")") {
		inner := expr[5 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		if val < 0 {
			return 0, fmt.Errorf("cannot calculate square root of negative number")
		}
		return math.Sqrt(val), nil
	}
	return 0, fmt.Errorf("sqrt function format error")
}

// handleSin handles sine function
func handleSin(expr string) (float64, error) {
	if strings.HasPrefix(expr, "sin(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		return math.Sin(val), nil
	}
	return 0, fmt.Errorf("sin function format error")
}

// handleCos handles cosine function
func handleCos(expr string) (float64, error) {
	if strings.HasPrefix(expr, "cos(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		return math.Cos(val), nil
	}
	return 0, fmt.Errorf("cos function format error")
}

// handleAbs handles absolute value function
func handleAbs(expr string) (float64, error) {
	if strings.HasPrefix(expr, "abs(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		return math.Abs(val), nil
	}
	return 0, fmt.Errorf("abs function format error")
}

// evaluateBasicExpression evaluates basic mathematical expressions
func evaluateBasicExpression(expr string) (float64, error) {
	// Remove all spaces
	expr = strings.ReplaceAll(expr, " ", "")

	// If it's a single number, parse directly
	if num, err := strconv.ParseFloat(expr, 64); err == nil {
		return num, nil
	}

	// Handle multiplication and division (higher priority)
	result, err := evaluateMultiplicationDivision(expr)
	if err != nil {
		return 0, err
	}

	return result, nil
}

// evaluateMultiplicationDivision handles multiplication and division, then handles addition and subtraction
func evaluateMultiplicationDivision(expr string) (float64, error) {
	// First handle addition and subtraction, as they have the lowest priority
	return evaluateAdditionSubtraction(expr)
}

// evaluateAdditionSubtraction handles addition and subtraction
func evaluateAdditionSubtraction(expr string) (float64, error) {
	// Find the last plus or minus sign (not at the beginning)
	var lastOpPos = -1
	var lastOp rune

	for i := len(expr) - 1; i >= 1; i-- { // Start from 1 to avoid handling negative sign at the beginning
		if expr[i] == '+' || expr[i] == '-' {
			lastOpPos = i
			lastOp = rune(expr[i])
			break
		}
	}

	if lastOpPos == -1 {
		// No addition or subtraction found, handle multiplication and division
		return evaluateMultiplicationDivisionOnly(expr)
	}

	// Split the expression
	left := expr[:lastOpPos]
	right := expr[lastOpPos+1:]

	// Recursively calculate left and right parts
	leftVal, err := evaluateAdditionSubtraction(left)
	if err != nil {
		return 0, err
	}

	rightVal, err := evaluateMultiplicationDivisionOnly(right)
	if err != nil {
		return 0, err
	}

	// Execute the operation
	switch lastOp {
	case '+':
		return leftVal + rightVal, nil
	case '-':
		return leftVal - rightVal, nil
	default:
		return 0, fmt.Errorf("unknown operator: %c", lastOp)
	}
}

// evaluateMultiplicationDivisionOnly handles only multiplication and division
func evaluateMultiplicationDivisionOnly(expr string) (float64, error) {
	// Find the last multiplication or division sign
	var lastOpPos = -1
	var lastOp rune

	for i := len(expr) - 1; i >= 0; i-- {
		if expr[i] == '*' || expr[i] == '/' {
			lastOpPos = i
			lastOp = rune(expr[i])
			break
		}
	}

	if lastOpPos == -1 {
		// No multiplication or division found, parse number directly
		return strconv.ParseFloat(expr, 64)
	}

	// Split the expression
	left := expr[:lastOpPos]
	right := expr[lastOpPos+1:]

	// Recursively calculate left and right parts
	leftVal, err := evaluateMultiplicationDivisionOnly(left)
	if err != nil {
		return 0, err
	}

	rightVal, err := strconv.ParseFloat(right, 64)
	if err != nil {
		return 0, err
	}

	// Execute the operation
	switch lastOp {
	case '*':
		return leftVal * rightVal, nil
	case '/':
		if rightVal == 0 {
			return 0, agent.NewStopError("division by zero")
		}
		return leftVal / rightVal, nil
	default:
		return 0, fmt.Errorf("unknown operator: %c", lastOp)
	}
}

// Time tool related structures
type timeRequest struct {
	Operation string `json:"operation" jsonschema:"description=Time operation type to perform,enum=current,enum=date,enum=weekday,enum=timestamp,required"`
}

type timeResponse struct {
	Operation string `json:"operation" jsonschema:"description=The operation that was performed"`
	Result    string `json:"result" jsonschema:"description=The result of the time operation"`
	Timestamp int64  `json:"timestamp" jsonschema:"description=Unix timestamp of when the operation was performed"`
}

// createTimeTool creates a time tool
func createTimeTool() tool.StreamableTool {
	return function.NewStreamableFunctionTool[timeRequest, timeResponse](
		getTimeInfo,
		function.WithName("time_tool"),
		function.WithDescription("Get time and date information. Supported operations: 'current'(current time), 'date'(current date), 'weekday'(day of week), 'timestamp'(Unix timestamp)"),
	)
}

func getTimeInfo(ctx context.Context, req timeRequest) (*tool.StreamReader, error) {
	stream := tool.NewStream(10)
	now := time.Now()
	var result string
	switch req.Operation {
	case "current":
		result = now.Format("2006-01-02 15:04:05")
	case "date":
		result = now.Format("2006-01-02")
	case "weekday":
		weekdays := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		result = weekdays[now.Weekday()]
	case "timestamp":
		result = fmt.Sprintf("%d", now.Unix())
	default:
		result = fmt.Sprintf("Current time: %s", now.Format("2006-01-02 15:04:05"))
	}
	output := tool.StreamChunk{
		Content: timeResponse{
			Operation: req.Operation,
			Timestamp: now.Unix(),
		},
		Metadata: tool.Metadata{CreatedAt: time.Now()},
	}
	if closed := stream.Writer.Send(output, nil); closed {
		return stream.Reader, fmt.Errorf("stream writer closed")
	}

	go func(result string) {
		for i := 0; i < len(result); i++ {
			output := tool.StreamChunk{
				Content: timeResponse{
					Result: result[i : i+1],
				},
				Metadata: tool.Metadata{CreatedAt: time.Now()},
			}
			if closed := stream.Writer.Send(output, nil); closed {
				break
			}
			time.Sleep(10 * time.Millisecond) // Simulate delay
		}
		stream.Writer.Close()
	}(result)

	return stream.Reader, nil
}

// Text tool related structures
type textRequest struct {
	Text      string `json:"text" jsonschema:"description=Text content to process,required"`
	Operation string `json:"operation" jsonschema:"description=Text operation type to perform,enum=uppercase,enum=lowercase,enum=length,enum=reverse,enum=words,required"`
}

type textResponse struct {
	OriginalText string `json:"original_text" jsonschema:"description=The original text that was processed"`
	Operation    string `json:"operation" jsonschema:"description=The operation that was performed on the text"`
	Result       string `json:"result" jsonschema:"description=The result of the text operation"`
	Info         string `json:"info" jsonschema:"description=Additional information about the operation"`
}

// createTextTool creates a text processing tool
func createTextTool() tool.CallableTool {
	return function.NewFunctionTool(
		processText,
		function.WithName("text_tool"),
		function.WithDescription("Process text content. Supported operations: 'uppercase'(to uppercase), 'lowercase'(to lowercase), 'length'(calculate length), 'reverse'(reverse text), 'words'(count words)"),
	)
}

// processText processes text
func processText(_ context.Context, req textRequest) (textResponse, error) {
	var result string
	var info string

	switch req.Operation {
	case "uppercase":
		result = strings.ToUpper(req.Text)
		info = "Text converted to uppercase"
	case "lowercase":
		result = strings.ToLower(req.Text)
		info = "Text converted to lowercase"
	case "length":
		length := len([]rune(req.Text))
		result = fmt.Sprintf("%d", length)
		info = fmt.Sprintf("Text length is %d characters", length)
	case "reverse":
		runes := []rune(req.Text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		result = string(runes)
		info = "Text reversed"
	case "words":
		words := strings.Fields(req.Text)
		result = fmt.Sprintf("%d", len(words))
		info = fmt.Sprintf("Text contains %d words", len(words))
	default:
		result = req.Text
		info = "Invalid operation type"
	}

	return textResponse{
		OriginalText: req.Text,
		Operation:    req.Operation,
		Result:       result,
		Info:         info,
	}, nil
}

// File tool related structures
type fileRequest struct {
	Path      string `json:"path" jsonschema:"description=File or directory path (relative to current working directory for security),required"`
	Operation string `json:"operation" jsonschema:"description=File operation type to perform,enum=read,enum=write,enum=list,enum=exists,required"`
	Content   string `json:"content,omitempty" jsonschema:"description=Content to write to the file (only required for write operation)"`
}

type fileResponse struct {
	Path      string `json:"path" jsonschema:"description=The file or directory path that was accessed"`
	Operation string `json:"operation" jsonschema:"description=The file operation that was performed"`
	Result    string `json:"result" jsonschema:"description=The result of the file operation (file content; directory listing; etc.)"`
	Success   bool   `json:"success" jsonschema:"description=Whether the file operation was successful"`
	Message   string `json:"message" jsonschema:"description=Human-readable message about the operation result"`
}

// createFileTool creates a file operations tool
func createFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		handleFileOperation,
		function.WithName("file_tool"),
		function.WithDescription("Perform basic file operations. Supported operations: 'read'(read file content), 'write'(write file), 'list'(list directory contents), 'exists'(check file existence). Note: For security reasons, only current working directory and subdirectories can be accessed."),
	)
}

// handleFileOperation handles file operations
func handleFileOperation(ctx context.Context, req fileRequest) (fileResponse, error) {
	// Security check: prevent path traversal attacks
	if strings.Contains(req.Path, "..") {
		return fileResponse{
			Path:      req.Path,
			Operation: req.Operation,
			Result:    "",
			Success:   false,
			Message:   "Security error: Access to parent directories is not allowed",
		}, fmt.Errorf("access to parent directories is not allowed")
	}

	switch req.Operation {
	case "read":
		return readFile(req.Path)
	case "write":
		return writeFile(req.Path, req.Content)
	case "list":
		return listDirectory(req.Path)
	case "exists":
		return checkFileExists(req.Path)
	default:
		return fileResponse{
			Path:      req.Path,
			Operation: req.Operation,
			Result:    "",
			Success:   false,
			Message:   "Unsupported file operation",
		}, nil
	}
}

// readFile reads file content
func readFile(path string) (fileResponse, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "read",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("Failed to read file: %v", err),
		}, fmt.Errorf("failed to read file: %v", err)
	}

	// Limit the length of returned content
	contentStr := string(content)
	if len(contentStr) > 1000 {
		contentStr = contentStr[:1000] + "\n... (File content too long, truncated)"
	}

	return fileResponse{
		Path:      path,
		Operation: "read",
		Result:    contentStr,
		Success:   true,
		Message:   fmt.Sprintf("Successfully read file, size: %d bytes", len(content)),
	}, nil
}

// writeFile writes file content
func writeFile(path, content string) (fileResponse, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fileResponse{
			Path:      path,
			Operation: "write",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("Failed to create directory: %v", err),
		}, fmt.Errorf("failed to create directory: %v", err)
	}

	err := os.WriteFile(path, []byte(content), 0600)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "write",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("Failed to write file: %v", err),
		}, fmt.Errorf("failed to write file: %v", err)
	}

	return fileResponse{
		Path:      path,
		Operation: "write",
		Result:    fmt.Sprintf("Wrote %d bytes", len(content)),
		Success:   true,
		Message:   "File written successfully",
	}, nil
}

// listDirectory lists directory contents
func listDirectory(path string) (fileResponse, error) {
	if path == "" {
		path = "."
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "list",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("Failed to list directory: %v", err),
		}, fmt.Errorf("failed to list directory: %v", err)
	}

	var result strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			result.WriteString(fmt.Sprintf("[Directory] %s\n", entry.Name()))
		} else {
			info, _ := entry.Info()
			size := ""
			if info != nil {
				size = fmt.Sprintf(" (%d bytes)", info.Size())
			}
			result.WriteString(fmt.Sprintf("[File] %s%s\n", entry.Name(), size))
		}
	}

	return fileResponse{
		Path:      path,
		Operation: "list",
		Result:    result.String(),
		Success:   true,
		Message:   fmt.Sprintf("Found %d items", len(entries)),
	}, nil
}

// checkFileExists checks if file exists
func checkFileExists(path string) (fileResponse, error) {
	_, err := os.Stat(path)
	exists := err == nil

	var message string
	if exists {
		message = "File exists"
	} else {
		message = "File does not exist"
	}

	return fileResponse{
		Path:      path,
		Operation: "exists",
		Result:    fmt.Sprintf("%t", exists),
		Success:   true,
		Message:   message,
	}, nil
}

// intPtr returns a pointer to the given integer
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float
func floatPtr(f float64) *float64 {
	return &f
}
