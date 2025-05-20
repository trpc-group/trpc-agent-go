// Package main provides a simple example of a session-aware agent server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/agent/agents/react"
	"trpc.group/trpc-go/trpc-agent-go/api"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcptools "trpc.group/trpc-go/trpc-agent-go/tool/tools"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

var (
	// Command line flags
	port          = flag.Int("port", 8080, "HTTP server port")
	modelName     = flag.String("model-name", "gpt-3.5-turbo", "OpenAI model name")
	openaiBaseURL = flag.String("openai-url", "https://api.openai.com/v1", "OpenAI API base URL")
	logLevel      = flag.String("level", "debug", "Log level (debug, info, warn, error, fatal)")
	mcpPort       = flag.Int("mcp-port", 3000, "MCP server port")
)

func main() {
	// Parse command line flags
	flag.Parse()

	// Set up logging
	log.SetLevel(*logLevel)
	log.Info("Starting session server")

	// Start MCP server
	go runMCPServer(fmt.Sprintf(":%d", *mcpPort))

	// Create context that listens for interrupt signals
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Get OpenAI API key
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		log.Fatal("OpenAI API key is required. Set it with -api_key flag or OPENAI_API_KEY env var")
	}

	// Create the OpenAI streaming model
	llmModel := models.NewOpenAIStreamingModel(
		*modelName,
		models.WithOpenAIAPIKey(openAIKey),
		models.WithOpenAIBaseURL(*openaiBaseURL),
	)
	log.Infof("Using streaming model. model: %s, base_url: %s", *modelName, *openaiBaseURL)

	// Create the agent
	agentConfig := react.AgentConfig{
		Model:           llmModel,
		Tools:           getAllTools(ctx),
		EnableStreaming: true,
	}

	myAgent, err := react.NewAgent(agentConfig)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Create in-memory session manager
	sessionManager := session.NewMemoryManager(
		session.WithExpiration(24 * time.Hour),
	)
	log.Info("Using in-memory session storage")

	// Create runner configuration
	runnerConfig := runner.DefaultConfig().
		WithTimeout(2 * time.Minute).
		WithSessionExpiration(24 * time.Hour)

	// Create session-aware runner
	sessionRunner := runner.NewSessionRunner("chat-runner", myAgent, runnerConfig, sessionManager)

	// Start the runner
	if err := sessionRunner.Start(ctx); err != nil {
		log.Fatalf("Failed to start runner: %v", err)
	}
	defer sessionRunner.Stop(context.Background())

	// Create API server
	server := api.NewServer(sessionRunner)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: server.Handler(),
	}

	// Start HTTP server in a goroutine
	go func() {
		log.Infof("Starting server. port: %d", *port)
		log.Info("Example usage: curl -X POST http://localhost:8080/sessions")
		log.Info("Then: curl -X POST http://localhost:8080/sessions/YOUR_SESSION_ID/run -H \"Content-Type: application/json\" -d '{\"message\":\"Hello, AI!\"}'")
		log.Info("For streaming: curl -X POST http://localhost:8080/sessions/YOUR_SESSION_ID/run_stream -H \"Content-Type: application/json\" -d '{\"message\":\"Hello, AI!\"}'")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("Server error: %v", err)
		}
	}()

	// Wait for context cancellation (SIGINT or SIGTERM)
	<-ctx.Done()
	log.Info("Shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown server gracefully
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Errorf("Server shutdown error: %v", err)
	}

	log.Info("Server stopped")
}

func getAllTools(ctx context.Context) []tool.Tool {
	// Create all tools (combination of traditional and MCP tools)
	calculatorTool := NewCalculatorTool()
	textAnalysisTool := NewTextAnalysisTool()

	// Add our "regular" tools
	translatorTool := NewTranslatorTool()
	unitConverterTool := NewUnitConverterTool()

	// Set up MCP tools using the existing MCPToolset
	mcpParams := mcptools.MCPServerParams{
		Type: mcptools.ConnectionTypeSSE,
		URL:  "http://localhost:3000/mcp", // MCP server URL
	}

	// Create the MCP toolset with the session manager
	mcpToolset := mcptools.NewMCPToolset(mcpParams)

	// Get MCP tools
	mcpTools, err := mcpToolset.GetTools(ctx)
	if err != nil {
		log.Infof("Warning: failed to get MCP tools: %v", err)
		// Continue without MCP tools
		mcpTools = []tool.Tool{}
	} else {
		log.Infof("Retrieved %d MCP tools from toolset", len(mcpTools))
		for _, t := range mcpTools {
			log.Infof("MCP Tool: %s - %s", t.Name(), t.Description())
		}
	}

	// Combine all tools
	allTools := []tool.Tool{
		calculatorTool,
		textAnalysisTool,
		translatorTool,
		unitConverterTool,
	}

	// Add MCP tools to the list
	allTools = append(allTools, mcpTools...)

	return allTools
}

// CalculatorTool is a tool for performing basic arithmetic operations.
type CalculatorTool struct {
	tool.BaseTool
}

// NewCalculatorTool creates a new calculator tool.
func NewCalculatorTool() *CalculatorTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "subtract", "multiply", "divide", "sqrt", "power"},
				"description": "The arithmetic operation to perform",
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "The first operand (or only operand for sqrt)",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "The second operand (not used for sqrt)",
			},
		},
		"required": []string{"operation", "a"},
	}

	return &CalculatorTool{
		BaseTool: *tool.NewBaseTool(
			"calculator",
			"Performs basic arithmetic operations like add, subtract, multiply, divide, sqrt, and power",
			parameters,
		),
	}
}

// Execute performs the arithmetic operation.
func (t *CalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Log tool invocation
	log.Infof("Calculator tool called with args: %v", args)

	// Normalize parameters - handle nested tool_input structure
	normalizedParams := make(map[string]interface{})

	// Extract from tool_input if present
	if toolInput, hasToolInput := args["tool_input"]; hasToolInput {
		if inputMap, isMap := toolInput.(map[string]interface{}); isMap {
			// Map structure - copy all values
			log.Infof("Found nested tool_input structure, extracting parameters")
			for k, v := range inputMap {
				normalizedParams[k] = v
			}
		} else if inputStr, isStr := toolInput.(string); isStr && inputStr != "" {
			// String input - try to parse as JSON first
			var jsonMap map[string]interface{}
			if err := json.Unmarshal([]byte(inputStr), &jsonMap); err == nil {
				log.Infof("Parsed tool_input JSON string into parameters")
				for k, v := range jsonMap {
					normalizedParams[k] = v
				}
			} else {
				// Try to parse as a direct expression (e.g., "2+2")
				log.Infof("Treating direct string input as expression: %s", inputStr)
				normalizedParams["operation"] = "add" // Default operation

				// Try to extract operation and operands from expression
				if parts := extractExpressionParts(inputStr); parts.operation != "" {
					normalizedParams["operation"] = parts.operation
					normalizedParams["a"] = parts.a
					if parts.b != nil {
						normalizedParams["b"] = parts.b
					}
				}
			}
		}
	}

	// Add any direct arguments not in tool_input
	for k, v := range args {
		if k != "tool_name" && k != "tool_input" && normalizedParams[k] == nil {
			normalizedParams[k] = v
		}
	}

	// If still empty, check for a single parameter that might be the operation
	if len(normalizedParams) == 0 && len(args) == 1 {
		for _, v := range args {
			if strValue, isStr := v.(string); isStr {
				// Try to parse as a direct expression
				if parts := extractExpressionParts(strValue); parts.operation != "" {
					normalizedParams["operation"] = parts.operation
					normalizedParams["a"] = parts.a
					if parts.b != nil {
						normalizedParams["b"] = parts.b
					}
				}
			}
		}
	}

	// Extract the operation
	opInterface, ok := normalizedParams["operation"]
	if !ok {
		log.Infof("Calculator tool error: operation is required")
		return nil, fmt.Errorf("operation is required")
	}
	operation, ok := opInterface.(string)
	if !ok {
		log.Infof("Calculator tool error: operation must be a string, got %T", opInterface)
		return nil, fmt.Errorf("operation must be a string, got %T", opInterface)
	}

	// Extract the first operand
	aInterface, ok := normalizedParams["a"]
	if !ok {
		log.Infof("Calculator tool error: a is required")
		return nil, fmt.Errorf("a is required")
	}
	var a float64
	switch v := aInterface.(type) {
	case float64:
		a = v
	case int:
		a = float64(v)
	default:
		log.Infof("Calculator tool error: a must be a number, got %T", aInterface)
		return nil, fmt.Errorf("a must be a number, got %T", aInterface)
	}

	// Helper function to extract the second operand for binary operations
	extractSecondOperand := func(operation string) (float64, error) {
		bInterface, ok := normalizedParams["b"]
		if !ok {
			errMsg := fmt.Sprintf("b is required for %s", operation)
			log.Infof("Calculator tool error: %s", errMsg)
			return 0, fmt.Errorf(errMsg)
		}

		var b float64
		switch v := bInterface.(type) {
		case float64:
			b = v
		case int:
			b = float64(v)
		default:
			errMsg := fmt.Sprintf("b must be a number, got %T", bInterface)
			log.Infof("Calculator tool error: %s", errMsg)
			return 0, fmt.Errorf(errMsg)
		}
		return b, nil
	}

	var result float64

	switch operation {
	case "sqrt":
		result = float64(int(100*math.Sqrt(a))) / 100 // Simple square root with 2 decimal places
		log.Infof("Calculator tool: sqrt(%f) = %f", a, result)
	case "add":
		b, err := extractSecondOperand("addition")
		if err != nil {
			return nil, err
		}
		result = a + b
		log.Infof("Calculator tool: %f + %f = %f", a, b, result)
	case "subtract":
		b, err := extractSecondOperand("subtraction")
		if err != nil {
			return nil, err
		}
		result = a - b
		log.Infof("Calculator tool: %f - %f = %f", a, b, result)
	case "multiply":
		b, err := extractSecondOperand("multiplication")
		if err != nil {
			return nil, err
		}
		result = a * b
		log.Infof("Calculator tool: %f * %f = %f", a, b, result)
	case "divide":
		b, err := extractSecondOperand("division")
		if err != nil {
			return nil, err
		}
		if b == 0 {
			log.Infof("Calculator tool error: division by zero")
			return nil, fmt.Errorf("division by zero")
		}
		result = a / b
		log.Infof("Calculator tool: %f / %f = %f", a, b, result)
	case "power":
		b, err := extractSecondOperand("power operation")
		if err != nil {
			return nil, err
		}
		result = math.Pow(a, b)
		log.Infof("Calculator tool: %f ^ %f = %f", a, b, result)
	default:
		log.Infof("Calculator tool error: unsupported operation: %s", operation)
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	// Log result
	log.Infof("Calculator tool returning result: %f", result)
	return tool.NewResult(result), nil
}

// expressionParts holds the parts of a parsed expression
type expressionParts struct {
	operation string
	a         float64
	b         *float64
}

// extractExpressionParts tries to parse an expression string into operation and operands
func extractExpressionParts(expr string) expressionParts {
	// Remove all whitespace
	expr = strings.ReplaceAll(expr, " ", "")

	// Check for common operators
	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		if len(parts) == 2 {
			a, errA := strconv.ParseFloat(parts[0], 64)
			b, errB := strconv.ParseFloat(parts[1], 64)
			if errA == nil && errB == nil {
				bVal := b
				return expressionParts{operation: "add", a: a, b: &bVal}
			}
		}
	} else if strings.Contains(expr, "-") {
		parts := strings.Split(expr, "-")
		if len(parts) == 2 {
			a, errA := strconv.ParseFloat(parts[0], 64)
			b, errB := strconv.ParseFloat(parts[1], 64)
			if errA == nil && errB == nil {
				bVal := b
				return expressionParts{operation: "subtract", a: a, b: &bVal}
			}
		}
	} else if strings.Contains(expr, "*") {
		parts := strings.Split(expr, "*")
		if len(parts) == 2 {
			a, errA := strconv.ParseFloat(parts[0], 64)
			b, errB := strconv.ParseFloat(parts[1], 64)
			if errA == nil && errB == nil {
				bVal := b
				return expressionParts{operation: "multiply", a: a, b: &bVal}
			}
		}
	} else if strings.Contains(expr, "/") {
		parts := strings.Split(expr, "/")
		if len(parts) == 2 {
			a, errA := strconv.ParseFloat(parts[0], 64)
			b, errB := strconv.ParseFloat(parts[1], 64)
			if errA == nil && errB == nil {
				bVal := b
				return expressionParts{operation: "divide", a: a, b: &bVal}
			}
		}
	} else if strings.Contains(expr, "^") {
		parts := strings.Split(expr, "^")
		if len(parts) == 2 {
			a, errA := strconv.ParseFloat(parts[0], 64)
			b, errB := strconv.ParseFloat(parts[1], 64)
			if errA == nil && errB == nil {
				bVal := b
				return expressionParts{operation: "power", a: a, b: &bVal}
			}
		}
	} else if strings.HasPrefix(expr, "sqrt") {
		// Try to extract the argument from sqrt(x)
		argStart := strings.Index(expr, "(")
		argEnd := strings.LastIndex(expr, ")")
		if argStart != -1 && argEnd != -1 && argEnd > argStart {
			arg := expr[argStart+1 : argEnd]
			a, err := strconv.ParseFloat(arg, 64)
			if err == nil {
				return expressionParts{operation: "sqrt", a: a}
			}
		}
	}

	// Return empty if no match
	return expressionParts{}
}

// TranslatorTool is a tool for translating text to different languages.
type TranslatorTool struct {
	tool.BaseTool
}

// NewTranslatorTool creates a new translation tool.
func NewTranslatorTool() *TranslatorTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "The text to translate",
			},
			"target_language": map[string]interface{}{
				"type":        "string",
				"description": "The target language code (e.g., 'es' for Spanish, 'fr' for French)",
			},
		},
		"required": []string{"text", "target_language"},
	}

	return &TranslatorTool{
		BaseTool: *tool.NewBaseTool(
			"translator",
			"Translates text to different languages",
			parameters,
		),
	}
}

// Execute performs the translation.
func (t *TranslatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Log tool invocation
	log.Infof("Translator tool called with args: %v", args)

	// Normalize parameters to handle various input formats
	normalizedParams := make(map[string]interface{})

	// Extract from tool_input if present
	if toolInput, hasToolInput := args["tool_input"]; hasToolInput {
		if inputMap, isMap := toolInput.(map[string]interface{}); isMap {
			// Map structure
			log.Infof("Found nested tool_input structure, extracting parameters")
			for k, v := range inputMap {
				normalizedParams[k] = v
			}
		} else if inputStr, isStr := toolInput.(string); isStr && inputStr != "" {
			// String input - try to parse as JSON first
			var jsonMap map[string]interface{}
			if err := json.Unmarshal([]byte(inputStr), &jsonMap); err == nil {
				log.Infof("Parsed tool_input JSON string into parameters")
				for k, v := range jsonMap {
					normalizedParams[k] = v
				}
			} else {
				// Direct string value - treat as text to translate
				log.Infof("Using direct string as text to translate: %s", inputStr)
				normalizedParams["text"] = inputStr

				// Look for language indicator in the original args
				for k, v := range args {
					if (k == "target_language" || k == "to" || k == "language" ||
						k == "language_code" || k == "target") && v != nil {
						if langStr, ok := v.(string); ok {
							normalizedParams["target_language"] = langStr
							break
						}
					}
				}

				// If no language found, default to Spanish
				if normalizedParams["target_language"] == nil {
					normalizedParams["target_language"] = "es"
				}
			}
		}
	}

	// Add any direct arguments not in tool_input
	for k, v := range args {
		if k != "tool_name" && k != "tool_input" && normalizedParams[k] == nil {
			normalizedParams[k] = v
		}
	}

	// If we have a single direct string parameter, treat as text to translate
	if len(normalizedParams) == 0 && len(args) == 1 {
		for _, v := range args {
			if strValue, isStr := v.(string); isStr {
				normalizedParams["text"] = strValue
				normalizedParams["target_language"] = "es" // Default to Spanish
			}
		}
	}

	// Extract the text
	text, ok := normalizedParams["text"].(string)
	if !ok {
		log.Infof("Translator tool error: text is required")
		return nil, fmt.Errorf("text is required")
	}

	// Extract the target language
	targetLang, ok := normalizedParams["target_language"].(string)
	if !ok {
		log.Infof("Translator tool error: target_language is required")
		return nil, fmt.Errorf("target_language is required")
	}

	// Simple "translation" by adding a language prefix to words
	words := strings.Fields(text)
	translatedWords := make([]string, len(words))

	prefix := ""
	switch targetLang {
	case "es":
		prefix = "es_"
	case "fr":
		prefix = "fr_"
	case "de":
		prefix = "de_"
	default:
		prefix = targetLang + "_"
	}

	for i, word := range words {
		translatedWords[i] = prefix + word
	}

	translation := strings.Join(translatedWords, " ")

	// Create and return the result
	result := map[string]interface{}{
		"original_text":   text,
		"target_language": targetLang,
		"translation":     translation,
	}

	// Log result
	log.Infof("Translator tool returning result: %v", result)
	return tool.NewJSONResult(result), nil
}

// UnitConverterTool is a tool for converting between different units.
type UnitConverterTool struct {
	tool.BaseTool
}

// NewUnitConverterTool creates a new unit conversion tool.
func NewUnitConverterTool() *UnitConverterTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": map[string]interface{}{
				"type":        "number",
				"description": "The value to convert",
			},
			"from_unit": map[string]interface{}{
				"type":        "string",
				"description": "The source unit (e.g., 'km', 'miles', 'celsius')",
			},
			"to_unit": map[string]interface{}{
				"type":        "string",
				"description": "The target unit (e.g., 'miles', 'km', 'fahrenheit')",
			},
		},
		"required": []string{"value", "from_unit", "to_unit"},
	}

	return &UnitConverterTool{
		BaseTool: *tool.NewBaseTool(
			"unit_converter",
			"Converts values between different units",
			parameters,
		),
	}
}

// Execute performs the unit conversion.
func (t *UnitConverterTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Log tool invocation
	log.Infof("Unit converter tool called with args: %v", args)

	// Normalize parameters to handle various input formats
	normalizedParams := make(map[string]interface{})

	// Add any direct arguments not in tool_input
	for k, v := range args {
		normalizedParams[k] = v
		if k != "value" {
			v = normalizeUnit(v.(string))
		}
	}

	// Extract the value
	valueInterface, ok := normalizedParams["value"]
	if !ok {
		log.Infof("Unit converter tool error: value is required")
		return nil, fmt.Errorf("value is required")
	}

	var value float64
	switch v := valueInterface.(type) {
	case float64:
		value = v
	case int:
		value = float64(v)
	default:
		log.Infof("Unit converter tool error: value must be a number")
		return nil, fmt.Errorf("value must be a number")
	}

	// Extract units
	fromUnit, ok := normalizedParams["from_unit"].(string)
	if !ok {
		log.Infof("Unit converter tool error: from_unit is required")
		return nil, fmt.Errorf("from_unit is required")
	}

	toUnit, ok := normalizedParams["to_unit"].(string)
	if !ok {
		log.Infof("Unit converter tool error: to_unit is required")
		return nil, fmt.Errorf("to_unit is required")
	}

	// Perform the conversion
	var result float64
	var conversionFormula string

	// Handle different conversion types
	if fromUnit == "km" && toUnit == "miles" {
		result = value * 0.621371
		conversionFormula = "miles = km * 0.621371"
	} else if fromUnit == "miles" && toUnit == "km" {
		result = value * 1.60934
		conversionFormula = "km = miles * 1.60934"
	} else if fromUnit == "celsius" && toUnit == "fahrenheit" {
		result = (value * 9 / 5) + 32
		conversionFormula = "fahrenheit = (celsius * 9/5) + 32"
	} else if fromUnit == "fahrenheit" && toUnit == "celsius" {
		result = (value - 32) * 5 / 9
		conversionFormula = "celsius = (fahrenheit - 32) * 5/9"
	} else {
		log.Infof("Unit converter tool error: unsupported conversion: %s to %s", fromUnit, toUnit)
		return nil, fmt.Errorf("unsupported conversion: %s to %s", fromUnit, toUnit)
	}

	// Round to 2 decimal places
	result = math.Round(result*100) / 100

	// Create and return the result
	conversionResult := map[string]interface{}{
		"original_value":  value,
		"original_unit":   fromUnit,
		"converted_value": result,
		"target_unit":     toUnit,
		"formula":         conversionFormula,
	}

	// Log result
	log.Infof("Unit converter tool returning result: %v", conversionResult)
	return tool.NewJSONResult(conversionResult), nil
}

// normalizeUnit converts abbreviations to full unit names
func normalizeUnit(unit string) string {
	unit = strings.TrimSpace(unit)

	// Handle common abbreviations
	switch unit {
	case "km", "kilometer", "kilometers":
		return "km"
	case "mi", "mile", "miles":
		return "miles"
	case "c", "celsius", "celcius":
		return "celsius"
	case "f", "fahrenheit":
		return "fahrenheit"
	default:
		return unit
	}
}

// TextAnalysisTool provides text analysis capabilities.
type TextAnalysisTool struct {
	tool.BaseTool
}

// NewTextAnalysisTool creates a new text analysis tool.
func NewTextAnalysisTool() *TextAnalysisTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "The text to analyze",
			},
			"analysis_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"word_count", "sentiment", "summary"},
				"description": "The type of analysis to perform",
			},
		},
		"required": []string{"text", "analysis_type"},
	}

	return &TextAnalysisTool{
		BaseTool: *tool.NewBaseTool(
			"text_analysis",
			"Analyzes text for word count, sentiment, or summarization",
			parameters,
		),
	}
}

// Execute performs the text analysis.
func (t *TextAnalysisTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Log tool invocation
	log.Infof("Text Analysis tool called with args: %v", args)

	// Normalize parameters to handle various input formats
	normalizedParams := make(map[string]interface{})

	// Extract from tool_input if present
	if toolInput, hasToolInput := args["tool_input"]; hasToolInput {
		if inputMap, isMap := toolInput.(map[string]interface{}); isMap {
			// Map structure
			log.Infof("Found nested tool_input structure, extracting parameters")
			for k, v := range inputMap {
				normalizedParams[k] = v
			}
		} else if inputStr, isStr := toolInput.(string); isStr && inputStr != "" {
			// String input - try to parse as JSON first
			var jsonMap map[string]interface{}
			if err := json.Unmarshal([]byte(inputStr), &jsonMap); err == nil {
				log.Infof("Parsed tool_input JSON string into parameters")
				for k, v := range jsonMap {
					normalizedParams[k] = v
				}
			} else {
				// Direct string value - treat as text to analyze
				log.Infof("Using direct string as text to analyze: %s", inputStr)
				normalizedParams["text"] = inputStr

				// Try to infer analysis type from the original args
				for k, v := range args {
					if (k == "analysis_type" || k == "type" || k == "analyze_for") && v != nil {
						if typeStr, ok := v.(string); ok {
							normalizedParams["analysis_type"] = typeStr
							break
						}
					}
				}

				// If no analysis type specified, default to word_count
				if normalizedParams["analysis_type"] == nil {
					normalizedParams["analysis_type"] = "word_count"
				}
			}
		}
	}

	// Add any direct arguments not in tool_input
	for k, v := range args {
		if k != "tool_name" && k != "tool_input" && normalizedParams[k] == nil {
			normalizedParams[k] = v
		}
	}

	// If we have a single direct string parameter, treat as text to analyze
	if len(normalizedParams) == 0 && len(args) == 1 {
		for _, v := range args {
			if strValue, isStr := v.(string); isStr {
				normalizedParams["text"] = strValue
				normalizedParams["analysis_type"] = "word_count" // Default type
			}
		}
	}

	// Extract the text
	textInterface, ok := normalizedParams["text"]
	if !ok {
		log.Infof("Text Analysis tool error: text is required")
		return nil, fmt.Errorf("text is required")
	}
	text, ok := textInterface.(string)
	if !ok {
		log.Infof("Text Analysis tool error: text must be a string")
		return nil, fmt.Errorf("text must be a string")
	}

	if text == "" {
		log.Infof("Text Analysis tool error: text cannot be empty")
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Extract the analysis type
	analysisTypeInterface, ok := normalizedParams["analysis_type"]
	if !ok {
		log.Infof("Text Analysis tool error: analysis_type is required")
		return nil, fmt.Errorf("analysis_type is required")
	}
	analysisType, ok := analysisTypeInterface.(string)
	if !ok {
		log.Infof("Text Analysis tool error: analysis_type must be a string")
		return nil, fmt.Errorf("analysis_type must be a string")
	}

	result := map[string]interface{}{}

	switch analysisType {
	case "word_count":
		words := strings.Fields(text)
		sentences := strings.Count(text, ".") + strings.Count(text, "!") + strings.Count(text, "?")
		if sentences == 0 && len(text) > 0 {
			sentences = 1
		}

		result = map[string]interface{}{
			"word_count":      len(words),
			"character_count": len(text),
			"sentence_count":  sentences,
		}
		log.Infof("Text Analysis tool: word count result for text: %v", result)

	case "sentiment":
		// Simple sentiment analysis
		positiveWords := []string{"good", "great", "excellent", "happy", "love", "best"}
		negativeWords := []string{"bad", "terrible", "awful", "hate", "worst", "poor"}

		lowerText := strings.ToLower(text)
		positiveCount := 0
		negativeCount := 0

		for _, word := range positiveWords {
			positiveCount += strings.Count(lowerText, word)
		}

		for _, word := range negativeWords {
			negativeCount += strings.Count(lowerText, word)
		}

		sentiment := "Neutral"
		if positiveCount > negativeCount {
			sentiment = "Positive"
		} else if negativeCount > positiveCount {
			sentiment = "Negative"
		}

		result = map[string]interface{}{
			"sentiment":      sentiment,
			"positive_words": positiveCount,
			"negative_words": negativeCount,
		}
		log.Infof("Text Analysis tool: sentiment analysis result: %v", result)

	case "summary":
		var summary string
		sentenceEnd := strings.IndexAny(text, ".!?")
		if sentenceEnd > 0 {
			summary = text[:sentenceEnd+1]
		} else if len(text) > 100 {
			summary = text[:100] + "..."
		} else {
			summary = text
		}

		result = map[string]interface{}{
			"summary":         summary,
			"original_length": len(text),
			"summary_length":  len(summary),
		}
		log.Infof("Text Analysis tool: summary result (length %d → %d)", len(text), len(summary))

	default:
		log.Infof("Text Analysis tool error: unsupported analysis type: %s", analysisType)
		return nil, fmt.Errorf("unsupported analysis type: %s", analysisType)
	}

	// Log result
	log.Infof("Text Analysis tool returning result: %v", result)
	return tool.NewJSONResult(result), nil
}

// runMCPServer starts an MCP server on the specified address.
func runMCPServer(address string) {
	// Create MCP server with basic implementation info
	mcpServer := mcp.NewServer(address, mcp.Implementation{
		Name:    "A2A-MCP-Server",
		Version: "1.0.0",
	}, mcp.WithPathPrefix("/mcp"))

	// Register more useful MCP tools

	// 1. Weather lookup tool with geolocation
	weatherTool := mcp.NewTool("mcp_weather_lookup",
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract parameters
			location, ok := req.Params.Arguments["location"].(string)
			if !ok || location == "" {
				return nil, fmt.Errorf("location is required")
			}

			// Simulate detailed weather data with geolocation
			locationHash := 0
			for _, c := range location {
				locationHash += int(c)
			}

			// Generate simulated coordinates based on location string
			lat := 35.0 + (float64(locationHash%10) - 5.0)
			long := 120.0 + (float64(locationHash%15) - 7.5)

			// Simulate weather information
			temperature := 15 + float64(locationHash%25)
			humidity := 30 + float64(locationHash%60)
			windSpeed := 2 + float64(locationHash%20)
			rainChance := float64(locationHash % 100)

			// Determine conditions based on temp and rain chance
			conditions := "Sunny"
			forecast := "Clear skies expected."
			if rainChance > 70 {
				conditions = "Rainy"
				forecast = "High chance of precipitation, bring an umbrella."
			} else if rainChance > 40 {
				conditions = "Cloudy"
				forecast = "Overcast conditions with potential showers."
			} else if temperature < 20 {
				conditions = "Cool and Clear"
				forecast = "Cool temperature with clear skies."
			} else if temperature >= 30 {
				conditions = "Hot"
				forecast = "Hot conditions expected. Stay hydrated."
			}

			// Create a structured response
			result := map[string]interface{}{
				"location": map[string]interface{}{
					"name":      location,
					"latitude":  fmt.Sprintf("%.4f°", lat),
					"longitude": fmt.Sprintf("%.4f°", long),
				},
				"current": map[string]interface{}{
					"temperature":   fmt.Sprintf("%.1f°C", temperature),
					"humidity":      fmt.Sprintf("%.1f%%", humidity),
					"wind_speed":    fmt.Sprintf("%.1f km/h", windSpeed),
					"conditions":    conditions,
					"precipitation": fmt.Sprintf("%.1f%%", rainChance),
				},
				"forecast": forecast,
				"source":   "MCP Weather API (simulated)",
				"updated":  time.Now().Format(time.RFC3339),
			}

			// Convert to JSON
			resultJSON, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal weather data: %w", err)
			}

			// Return result
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(resultJSON))}}, nil
		},
		mcp.WithDescription("Gets detailed weather information for a specified location."),
		mcp.WithString("location",
			mcp.Description("The location name to get weather information for (e.g., 'Tokyo', 'New York', 'London')"),
		),
	)
	if err := mcpServer.RegisterTool(weatherTool); err != nil {
		log.Fatalf("Failed to register MCP weather tool: %v", err)
	}

	// 2. Currency conversion tool
	currencyTool := mcp.NewTool("mcp_currency_converter",
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract parameters
			amountInterface, ok := req.Params.Arguments["amount"]
			if !ok {
				return nil, fmt.Errorf("amount is required")
			}

			var amount float64
			switch v := amountInterface.(type) {
			case float64:
				amount = v
			case int:
				amount = float64(v)
			default:
				return nil, fmt.Errorf("amount must be a number")
			}

			fromCurrency, ok := req.Params.Arguments["from_currency"].(string)
			if !ok || fromCurrency == "" {
				return nil, fmt.Errorf("from_currency is required")
			}

			toCurrency, ok := req.Params.Arguments["to_currency"].(string)
			if !ok || toCurrency == "" {
				return nil, fmt.Errorf("to_currency is required")
			}

			// Simulate currency exchange rates (deterministic based on currency codes)
			rates := map[string]float64{
				"USD": 1.0,
				"EUR": 0.92,
				"GBP": 0.78,
				"JPY": 151.35,
				"CNY": 7.23,
				"CAD": 1.37,
				"AUD": 1.52,
				"CHF": 0.90,
				"HKD": 7.82,
				"SGD": 1.35,
			}

			// Validate currencies
			fromRate, fromExists := rates[strings.ToUpper(fromCurrency)]
			toRate, toExists := rates[strings.ToUpper(toCurrency)]

			if !fromExists {
				return nil, fmt.Errorf("unsupported source currency: %s", fromCurrency)
			}
			if !toExists {
				return nil, fmt.Errorf("unsupported target currency: %s", toCurrency)
			}

			// Calculate conversion
			// Convert source to USD, then to target
			converted := amount * (toRate / fromRate)

			// Round to 4 decimal places for most currencies
			precision := 4
			if strings.ToUpper(toCurrency) == "JPY" {
				precision = 2 // JPY typically shown with fewer decimals
			}
			factor := math.Pow(10, float64(precision))
			converted = math.Round(converted*factor) / factor

			// Add some market volatility (simulated)
			marketTrend := []string{"up", "down", "stable"}[time.Now().Second()%3]
			volatility := (float64(time.Now().Second() % 10)) / 100

			// Create result
			result := map[string]interface{}{
				"from": map[string]interface{}{
					"currency": strings.ToUpper(fromCurrency),
					"amount":   amount,
				},
				"to": map[string]interface{}{
					"currency": strings.ToUpper(toCurrency),
					"amount":   converted,
				},
				"exchange_rate": toRate / fromRate,
				"market_info": map[string]interface{}{
					"trend":       marketTrend,
					"volatility":  fmt.Sprintf("%.2f%%", volatility*100),
					"last_update": time.Now().Format("2006-01-02 15:04:05"),
				},
				"source": "MCP Exchange Rate API (simulated)",
			}

			// Convert to JSON
			resultJSON, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal currency data: %w", err)
			}

			// Return result
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(resultJSON))}}, nil
		},
		mcp.WithDescription("Converts amounts between different currencies using current exchange rates. Supports USD, EUR, GBP, JPY, CNY, CAD, AUD, CHF, HKD, SGD."),
		mcp.WithNumber("amount",
			mcp.Description("The amount of money to convert (numeric value)"),
		),
		mcp.WithString("from_currency",
			mcp.Description("The source currency code (e.g., USD, EUR, GBP) - must be one of: USD, EUR, GBP, JPY, CNY, CAD, AUD, CHF, HKD, SGD"),
		),
		mcp.WithString("to_currency",
			mcp.Description("The target currency code (e.g., USD, EUR, GBP) - must be one of: USD, EUR, GBP, JPY, CNY, CAD, AUD, CHF, HKD, SGD"),
		),
	)
	if err := mcpServer.RegisterTool(currencyTool); err != nil {
		log.Fatalf("Failed to register MCP currency tool: %v", err)
	}

	// 3. Data analysis tool
	dataTool := mcp.NewTool("mcp_data_analyzer",
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract data series as a string of comma-separated numbers
			dataSeriesStr, ok := req.Params.Arguments["data_series"].(string)
			if !ok || dataSeriesStr == "" {
				return nil, fmt.Errorf("data_series is required")
			}

			analysisType, ok := req.Params.Arguments["analysis_type"].(string)
			if !ok || analysisType == "" {
				return nil, fmt.Errorf("analysis_type is required")
			}

			// Parse data series
			parts := strings.Split(dataSeriesStr, ",")
			dataSeries := make([]float64, 0, len(parts))

			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}

				val, err := strconv.ParseFloat(part, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid data point '%s': %w", part, err)
				}

				dataSeries = append(dataSeries, val)
			}

			if len(dataSeries) == 0 {
				return nil, fmt.Errorf("data series must contain at least one valid number")
			}

			// Perform requested analysis
			result := map[string]interface{}{
				"data_points": len(dataSeries),
				"raw_data":    dataSeries,
			}

			switch analysisType {
			case "statistics":
				// Calculate basic statistics
				sum := 0.0
				min := dataSeries[0]
				max := dataSeries[0]

				for _, val := range dataSeries {
					sum += val
					if val < min {
						min = val
					}
					if val > max {
						max = val
					}
				}

				mean := sum / float64(len(dataSeries))

				// Calculate variance and std deviation
				variance := 0.0
				for _, val := range dataSeries {
					variance += math.Pow(val-mean, 2)
				}
				variance /= float64(len(dataSeries))
				stdDev := math.Sqrt(variance)

				// Calculate median
				sortedData := make([]float64, len(dataSeries))
				copy(sortedData, dataSeries)
				sort.Float64s(sortedData)

				median := 0.0
				if len(sortedData)%2 == 0 {
					median = (sortedData[len(sortedData)/2-1] + sortedData[len(sortedData)/2]) / 2
				} else {
					median = sortedData[len(sortedData)/2]
				}

				stats := map[string]interface{}{
					"min":                min,
					"max":                max,
					"range":              max - min,
					"sum":                sum,
					"mean":               mean,
					"median":             median,
					"variance":           variance,
					"standard_deviation": stdDev,
				}

				result["statistics"] = stats

			case "trend":
				// Calculate linear regression
				sumX := 0.0
				sumY := 0.0
				sumXY := 0.0
				sumXX := 0.0

				for i, y := range dataSeries {
					x := float64(i)
					sumX += x
					sumY += y
					sumXY += x * y
					sumXX += x * x
				}

				n := float64(len(dataSeries))
				slope := (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
				intercept := (sumY - slope*sumX) / n

				// Calculate trend line points
				trendLine := make([]float64, len(dataSeries))
				for i := range trendLine {
					trendLine[i] = slope*float64(i) + intercept
				}

				// Calculate R-squared
				meanY := sumY / n
				totalSS := 0.0
				residualSS := 0.0

				for i, y := range dataSeries {
					predicted := slope*float64(i) + intercept
					totalSS += math.Pow(y-meanY, 2)
					residualSS += math.Pow(y-predicted, 2)
				}

				rSquared := 1 - (residualSS / totalSS)

				trendDirection := "stable"
				if slope > 0.1 {
					trendDirection = "strongly increasing"
				} else if slope > 0.01 {
					trendDirection = "slightly increasing"
				} else if slope < -0.1 {
					trendDirection = "strongly decreasing"
				} else if slope < -0.01 {
					trendDirection = "slightly decreasing"
				}

				result["trend_analysis"] = map[string]interface{}{
					"slope":           slope,
					"intercept":       intercept,
					"r_squared":       rSquared,
					"trend_line":      trendLine,
					"trend_direction": trendDirection,
					"fit_quality":     getCorrelationDescription(rSquared),
				}

			case "forecast":
				// Simple forecasting based on moving average
				if len(dataSeries) < 3 {
					return nil, fmt.Errorf("forecasting requires at least 3 data points")
				}

				// Calculate trend
				sumX := 0.0
				sumY := 0.0
				sumXY := 0.0
				sumXX := 0.0

				for i, y := range dataSeries {
					x := float64(i)
					sumX += x
					sumY += y
					sumXY += x * y
					sumXX += x * x
				}

				n := float64(len(dataSeries))
				slope := (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
				intercept := (sumY - slope*sumX) / n

				// Generate next 3 points forecast
				forecast := make([]float64, 3)
				for i := range forecast {
					nextX := float64(len(dataSeries) + i)
					forecast[i] = slope*nextX + intercept
					// Add some randomness based on data variance
					variance := 0.0
					for _, val := range dataSeries {
						variance += math.Pow(val-(sumY/n), 2)
					}
					variance /= n

					// Add controlled randomness
					randomFactor := (math.Sin(float64(i)*0.7) * math.Sqrt(variance) * 0.3)
					forecast[i] += randomFactor
				}

				result["forecast"] = map[string]interface{}{
					"method":      "linear regression with seasonal adjustment",
					"next_points": forecast,
					"confidence":  "medium",
					"model_params": map[string]interface{}{
						"slope":     slope,
						"intercept": intercept,
					},
				}

			default:
				return nil, fmt.Errorf("unsupported analysis type: %s", analysisType)
			}

			// Convert to JSON
			resultJSON, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal analysis data: %w", err)
			}

			// Return result
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(resultJSON))}}, nil
		},
		mcp.WithDescription("Analyzes numeric data series for statistical insights. Provide a comma-separated list of numbers to analyze."),
		mcp.WithString("data_series",
			mcp.Description("Comma-separated list of numeric values to analyze (e.g., '10, 15, 20, 25, 30')"),
		),
		mcp.WithString("analysis_type",
			mcp.Description("Type of analysis to perform. Choose one of: statistics (basic stats), trend (linear regression), forecast (future prediction)"),
		),
	)
	if err := mcpServer.RegisterTool(dataTool); err != nil {
		log.Fatalf("Failed to register MCP data analyzer tool: %v", err)
	}

	// Start the server
	log.Infof("Starting MCP server on %s", address)
	if err := mcpServer.Start(); err != nil {
		log.Fatalf("MCP server failed: %v", err)
	}
}

// Helper function for data analysis tool
func getCorrelationDescription(rSquared float64) string {
	if rSquared > 0.9 {
		return "very strong fit"
	} else if rSquared > 0.7 {
		return "strong fit"
	} else if rSquared > 0.5 {
		return "moderate fit"
	} else if rSquared > 0.3 {
		return "weak fit"
	}
	return "very weak fit"
}
