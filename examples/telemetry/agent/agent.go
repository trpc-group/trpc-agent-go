//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent demonstrates a multi-tool chat agent with various tools and tool selection.
package agent

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// MultiToolChatAgent manages the multi-tool conversation system
type MultiToolChatAgent struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// NewMultiToolChatAgent creates a new multi-tool chat agent.
func NewMultiToolChatAgent(agentName, modelName string) *MultiToolChatAgent {
	a := &MultiToolChatAgent{modelName: modelName}
	// Create OpenAI model
	modelInstance := openai.New(a.modelName)

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
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A powerful AI assistant with multiple tools including calculator, time, text processing, file operations, and web search"),
		// llmagent.WithInstruction(`You are an intelligent assistant that can use multiple tools:
		// 1. calculator: Perform mathematical calculations, supporting basic operations, scientific calculations, etc.
		// 2. time_tool: Get current time, date, timezone information, etc.
		// 3. text_tool: Process text, including case conversion, length statistics, string operations, etc.
		// 4. file_tool: Basic file operations such as reading, writing, listing directories, etc.
		//5. duckduckgo_search: Search web information, suitable for finding factual, encyclopedia-type information

		//Please select the appropriate tool based on user needs and provide helpful assistance.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
	)

	// Create runner
	appName := "multi-tool-chat"
	a.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Set identifiers
	a.userID = "user"
	a.sessionID = fmt.Sprintf("multi-tool-session-%d", time.Now().Unix())

	fmt.Printf("✅ Multi-tool intelligent assistant is ready! Session ID: %s\n\n", a.sessionID)
	return a
}

// Close closes the agent and releases owned resources.
func (c *MultiToolChatAgent) Close() error {
	return c.runner.Close()
}

// ProcessMessage processes a single message exchange
func (c *MultiToolChatAgent) ProcessMessage(ctx context.Context, userMessage string) (string, error) {
	var output strings.Builder
	fmt.Printf("👤 User message: %s\n", userMessage)
	message := model.NewUserMessage(userMessage)

	// Run agent through runner
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return output.String(), fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response
	streamOutput, err := c.processStreamingResponse(eventChan)
	if err != nil {
		return output.String(), err
	}

	output.WriteString(streamOutput)
	return output.String(), nil
}

// processStreamingResponse processes streaming response, including tool call visualization
func (c *MultiToolChatAgent) processStreamingResponse(eventChan <-chan *event.Event) (string, error) {
	var output strings.Builder

	assistantPrefix := "🤖 Assistant: "
	fmt.Print(assistantPrefix)
	output.WriteString(assistantPrefix)

	var (
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {

		// Handle errors
		if event.Error != nil {
			errorMsg := fmt.Sprintf("\n❌ Error: %s\n", event.Error.Message)
			fmt.Print(errorMsg)
			output.WriteString(errorMsg)
			continue
		}

		// Detect and display tool calls
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				newline := "\n"
				fmt.Print(newline)
				output.WriteString(newline)
			}
			toolCallsHeader := "🔧 Tool calls:\n"
			fmt.Print(toolCallsHeader)
			output.WriteString(toolCallsHeader)

			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				toolIcon := getToolIcon(toolCall.Function.Name)
				toolCallMsg := fmt.Sprintf("   %s %s (ID: %s)\n", toolIcon, toolCall.Function.Name, toolCall.ID)
				fmt.Print(toolCallMsg)
				output.WriteString(toolCallMsg)

				if len(toolCall.Function.Arguments) > 0 {
					argsMsg := fmt.Sprintf("     Arguments: %s\n", string(toolCall.Function.Arguments))
					fmt.Print(argsMsg)
					output.WriteString(argsMsg)
				}
			}
			executingMsg := "\n⚡ Executing...\n"
			fmt.Print(executingMsg)
			output.WriteString(executingMsg)
		}

		// Detect tool responses
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					toolResultMsg := fmt.Sprintf("✅ Tool result (ID: %s): %s\n",
						choice.Message.ToolID,
						formatToolResult(choice.Message.Content))
					fmt.Print(toolResultMsg)
					output.WriteString(toolResultMsg)
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
						assistantRestart := "\n🤖 Assistant: "
						fmt.Print(assistantRestart)
						output.WriteString(assistantRestart)
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				output.WriteString(choice.Delta.Content)
			}
		}

		// Check if this is the final event
		if event.IsFinalResponse() {
			finalNewline := "\n"
			fmt.Print(finalNewline)
			output.WriteString(finalNewline)
			break
		}
	}

	return output.String(), nil
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
	Expression string `json:"expression" jsonschema:"description=Mathematical expression to calculate. e.g. '2+3*4'; 'sqrt(16)'; 'sin(30*pi/180)'"`
}

type calculatorResponse struct {
	Expression string  `json:"expression"`
	Result     float64 `json:"result"`
	Message    string  `json:"message"`
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
		}, nil
	}

	// Simple expression calculator implementation
	result, err := evaluateExpression(req.Expression)
	if err != nil {
		return calculatorResponse{
			Expression: req.Expression,
			Result:     0,
			Message:    fmt.Sprintf("Calculation error: %v", err),
		}, nil
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
			return 0, fmt.Errorf("division by zero")
		}
		return leftVal / rightVal, nil
	default:
		return 0, fmt.Errorf("unknown operator: %c", lastOp)
	}
}

// Time tool related structures
type timeRequest struct {
	Operation string `json:"operation" jsonschema:"description=Time operation type, enum=current,enum=date,enum=weekday,enum=timestamp"`
}

type timeResponse struct {
	Operation string `json:"operation"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
}

// createTimeTool creates a time tool
func createTimeTool() tool.CallableTool {
	return function.NewFunctionTool(
		getTimeInfo,
		function.WithName("time_tool"),
		function.WithDescription("Get time and date information. Supported operations: 'current'(current time), 'date'(current date), 'weekday'(day of week), 'timestamp'(Unix timestamp)"),
	)
}

// getTimeInfo gets time information
func getTimeInfo(_ context.Context, req timeRequest) (timeResponse, error) {
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

	return timeResponse{
		Operation: req.Operation,
		Result:    result,
		Timestamp: now.Unix(),
	}, nil
}

// Text tool related structures
type textRequest struct {
	Text      string `json:"text" jsonschema:"description=Text content to process"`
	Operation string `json:"operation" jsonschema:"description=Text operation type, enum=uppercase,enum=lowercase,enum=length,enum=reverse,enum=words"`
}

type textResponse struct {
	OriginalText string `json:"original_text"`
	Operation    string `json:"operation"`
	Result       string `json:"result"`
	Info         string `json:"info"`
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
	Path      string `json:"path" jsonschema:"description=File or directory path"`
	Operation string `json:"operation" jsonschema:"description=File operation type, enum=read,enum=write,enum=list,enum=exists"`
	Content   string `json:"content,omitempty" jsonschema:"description=Content to write when writing files (only for write operation)"`
}

type fileResponse struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Result    string `json:"result"`
	Success   bool   `json:"success"`
	Message   string `json:"message"`
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
func handleFileOperation(_ context.Context, req fileRequest) (fileResponse, error) {
	// Security check: prevent path traversal attacks
	if strings.Contains(req.Path, "..") {
		return fileResponse{
			Path:      req.Path,
			Operation: req.Operation,
			Result:    "",
			Success:   false,
			Message:   "Security error: Access to parent directories is not allowed",
		}, nil
	}

	switch req.Operation {
	case "read":
		return readFile(req.Path), nil
	case "write":
		return writeFile(req.Path, req.Content), nil
	case "list":
		return listDirectory(req.Path), nil
	case "exists":
		return checkFileExists(req.Path), nil
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
func readFile(path string) fileResponse {
	content, err := os.ReadFile(path)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "read",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("Failed to read file: %v", err),
		}
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
	}
}

// writeFile writes file content
func writeFile(path, content string) fileResponse {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fileResponse{
			Path:      path,
			Operation: "write",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("Failed to create directory: %v", err),
		}
	}

	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "write",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("Failed to write file: %v", err),
		}
	}

	return fileResponse{
		Path:      path,
		Operation: "write",
		Result:    fmt.Sprintf("Wrote %d bytes", len(content)),
		Success:   true,
		Message:   "File written successfully",
	}
}

// listDirectory lists directory contents
func listDirectory(path string) fileResponse {
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
		}
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
	}
}

// checkFileExists checks if file exists
func checkFileExists(path string) fileResponse {
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
	}
}

// intPtr returns a pointer to the given integer
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float
func floatPtr(f float64) *float64 {
	return &f
}
