// Package main provides a simple example of a session-aware agent server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent/react"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/rest"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

var (
	// Command line flags
	port          = flag.Int("port", 8080, "HTTP server port")
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
	modelName := os.Getenv("OPENAI_MODEL_NAME")
	if modelName == "" {
		log.Fatal("OpenAI model name is required. Set it with -model-name flag or OPENAI_MODEL_NAME env var")
	}
	openaiBaseURL := os.Getenv("OPENAI_BASE_URL")
	if openaiBaseURL == "" {
		log.Fatal("OpenAI base URL is required. Set it with -openai-url flag or OPENAI_BASE_URL env var")
	}

	// Create the OpenAI streaming model
	llmModel := model.NewOpenAIStreamingModel(
		modelName,
		model.WithOpenAIAPIKey(openAIKey),
		model.WithOpenAIBaseURL(openaiBaseURL),
	)
	log.Infof("Using streaming model. model: %s, base_url: %s", modelName, openaiBaseURL)

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
	server := rest.NewServer(sessionRunner)

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
	mcpParams := tool.MCPServerParams{
		Type: tool.ConnectionTypeSSE,
		URL:  "http://localhost:3000/mcp", // MCP server URL
	}

	// Create the MCP toolset with the session manager
	mcpToolset := tool.NewMCPToolset(mcpParams)

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
				"description": "The arithmetic operation to perform. For 'add' and 'multiply', order doesn't matter. For 'subtract', 'a' is minuend and 'b' is subtrahend. For 'divide', 'a' is dividend and 'b' is divisor. For 'power', 'a' is base and 'b' is exponent.",
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "The first operand. For sqrt, this is the number to find the square root of. For power, this is the base number.",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "The second operand. Required for all operations except sqrt. For power, this is the exponent.",
			},
		},
		"required": []string{"operation", "a"},
		"examples": []map[string]interface{}{
			{
				"operation": "add",
				"a":         5,
				"b":         3,
			},
			{
				"operation": "sqrt",
				"a":         16,
			},
			{
				"operation": "power",
				"a":         2,
				"b":         3,
			},
		},
	}

	return &CalculatorTool{
		BaseTool: *tool.NewBaseTool(
			"calculator",
			"Performs basic arithmetic operations. Supports add, subtract, multiply, divide, sqrt, and power. For sqrt, only parameter 'a' is needed. For power, 'a' is the base and 'b' is the exponent.",
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
	// Add any direct arguments not in tool_input
	for k, v := range args {
		if k != "tool_name" && k != "tool_input" && normalizedParams[strings.ToLower(k)] == nil {
			normalizedParams[strings.ToLower(k)] = v
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
						normalizedParams["b"] = *parts.b
					}
				}
			}
		}
	}
	// Extract the operation
	opInterface, ok := normalizedParams["operation"]
	if !ok {
		log.Infof("Calculator tool error: operation is required")
		return nil, fmt.Errorf("operation is required. Please specify one of: add, subtract, multiply, divide, sqrt, power")
	}
	operation, ok := opInterface.(string)
	if !ok {
		log.Infof("Calculator tool error: operation must be a string, got %T", opInterface)
		return nil, fmt.Errorf("operation must be a string, got %T. Valid operations are: add, subtract, multiply, divide, sqrt, power", opInterface)
	}

	// Normalize the operation name
	operation = strings.ToLower(operation)

	// Look for aliases of parameters 'a' and 'b'
	findNumberParam := func(aliases []string, defaultValue *float64) (float64, error) {
		for _, alias := range aliases {
			if val, exists := normalizedParams[alias]; exists && val != nil {
				switch v := val.(type) {
				case float64:
					return v, nil
				case int:
					return float64(v), nil
				case string:
					// Try to parse string as number
					if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						return parsed, nil
					}
				}
			}
		}

		if defaultValue != nil {
			return *defaultValue, nil
		}

		return 0, fmt.Errorf("required numeric parameter not found. Looked for: %v", aliases)
	}

	// Extract the first operand with aliases
	aAliases := []string{"a"}
	a, err := findNumberParam(aAliases, nil)
	if err != nil {
		log.Infof("Calculator tool error: %v", err)
		return nil, fmt.Errorf("first operand (a) is required: %v", err)
	}

	// Helper function to extract the second operand for binary operations
	extractSecondOperand := func(operation string) (float64, error) {
		bAliases := []string{"b"}
		b, err := findNumberParam(bAliases, nil)
		if err != nil {
			errMsg := fmt.Sprintf("second operand (b) is required for %s: %v", operation, err)
			log.Infof("Calculator tool error: %s", errMsg)
			return 0, fmt.Errorf(errMsg)
		}
		return b, nil
	}

	var result float64

	switch operation {
	case "sqrt":
		if a < 0 {
			return nil, fmt.Errorf("cannot calculate square root of a negative number (%f)", a)
		}
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
			return nil, fmt.Errorf("division by zero is not allowed")
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
		return nil, fmt.Errorf("unsupported operation: %s. Supported operations are: add, subtract, multiply, divide, sqrt, power", operation)
	}
	// Format result to avoid excessive decimal places
	result = math.Round(result*1000) / 1000
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
		"examples": []map[string]interface{}{
			{
				"text":            "Hello, world!",
				"target_language": "es",
			},
			{
				"text":            "Hello, world!",
				"target_language": "fr",
			},
			{
				"text":            "Hello, world!",
				"target_language": "de",
			},
		},
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
	// Add any direct arguments not in tool_input
	for k, v := range args {
		if k != "tool_name" && k != "tool_input" && normalizedParams[strings.ToLower(k)] == nil {
			normalizedParams[strings.ToLower(k)] = v
		}
	}

	// Check for other parameter name variations
	checkParamAliases := func(aliases []string) string {
		for _, alias := range aliases {
			if val, exists := normalizedParams[alias]; exists && val != nil {
				if strVal, ok := val.(string); ok && strVal != "" {
					return strVal
				}
			}
		}
		return ""
	}

	// Check for text aliases
	textAliases := []string{"text"}
	textParam := checkParamAliases(textAliases)
	if textParam != "" {
		normalizedParams["text"] = textParam
	}

	// Check for target language aliases
	langAliases := []string{"target_language"}
	langParam := checkParamAliases(langAliases)
	if langParam != "" {
		normalizedParams["target_language"] = langParam
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

	// Try to infer from context if we're missing either parameter
	if normalizedParams["text"] == nil && normalizedParams["target_language"] != nil {
		// If we have a language but no text, assume any string parameter is the text
		for _, v := range args {
			if strValue, isStr := v.(string); isStr && strValue != normalizedParams["target_language"] {
				normalizedParams["text"] = strValue
				break
			}
		}
	}

	// Extract the text
	textInterface, exists := normalizedParams["text"]
	if !exists || textInterface == nil {
		log.Infof("Translator tool error: text is required")
		return nil, fmt.Errorf("text to translate is required. Please provide text using the 'text' parameter")
	}

	var text string
	switch v := textInterface.(type) {
	case string:
		text = v
	case float64, int:
		text = fmt.Sprintf("%v", v)
	default:
		// Try to convert to JSON string if it's a complex object
		if jsonBytes, err := json.Marshal(textInterface); err == nil {
			text = string(jsonBytes)
		} else {
			text = fmt.Sprintf("%v", textInterface)
		}
	}

	if strings.TrimSpace(text) == "" {
		log.Infof("Translator tool error: text cannot be empty")
		return nil, fmt.Errorf("text to translate cannot be empty")
	}

	// Extract the target language
	targetLangInterface, exists := normalizedParams["target_language"]
	if !exists || targetLangInterface == nil {
		log.Infof("Translator tool error: target_language is required")
		return nil, fmt.Errorf("target language is required. Please specify the target language using 'target_language' parameter")
	}

	var targetLang string
	switch v := targetLangInterface.(type) {
	case string:
		targetLang = v
	default:
		targetLang = fmt.Sprintf("%v", targetLangInterface)
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
		"examples": []map[string]interface{}{
			{
				"value":     5,
				"from_unit": "km",
				"to_unit":   "miles",
			},
			{
				"value":     100,
				"from_unit": "fahrenheit",
				"to_unit":   "celsius",
			},
			{
				"value":     100,
				"from_unit": "meters",
				"to_unit":   "feet",
			},
		},
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
		if k != "tool_name" && k != "tool_input" && normalizedParams[strings.ToLower(k)] == nil {
			normalizedParams[strings.ToLower(k)] = v
		}
	}

	// Check for parameter aliases
	checkParamAliases := func(aliases []string) interface{} {
		for _, alias := range aliases {
			if val, exists := normalizedParams[alias]; exists && val != nil {
				return val
			}
		}
		return nil
	}

	// Look for value aliases
	valueAliases := []string{"value", "amount", "number", "val", "quantity", "measure", "measurement", "num", "input"}
	valueParam := checkParamAliases(valueAliases)
	if valueParam != nil {
		normalizedParams["value"] = valueParam
	}

	// Look for from_unit aliases
	fromUnitAliases := []string{"from_unit", "from", "source", "source_unit", "input_unit", "original_unit", "start_unit", "unit"}
	fromUnitParam := checkParamAliases(fromUnitAliases)
	if fromUnitParam != nil {
		normalizedParams["from_unit"] = fromUnitParam
	}

	// Look for to_unit aliases
	toUnitAliases := []string{"to_unit", "to", "target", "target_unit", "output_unit", "final_unit", "destination_unit", "dest_unit"}
	toUnitParam := checkParamAliases(toUnitAliases)
	if toUnitParam != nil {
		normalizedParams["to_unit"] = toUnitParam
	}

	// Try to extract from text if provided in one string
	// Check if we have a single text parameter that might contain a conversion request
	for _, v := range args {
		if strValue, isStr := v.(string); isStr {
			if conversionParts := extractConversionParts(strValue); conversionParts.value != 0 {
				// Only override if we don't have values yet
				if normalizedParams["value"] == nil {
					normalizedParams["value"] = conversionParts.value
				}
				if normalizedParams["from_unit"] == nil {
					normalizedParams["from_unit"] = conversionParts.fromUnit
				}
				if normalizedParams["to_unit"] == nil {
					normalizedParams["to_unit"] = conversionParts.toUnit
				}
				break
			}
		}
	}

	// Extract the value
	valueInterface, ok := normalizedParams["value"]
	if !ok {
		log.Infof("Unit converter tool error: value is required")
		return nil, fmt.Errorf("value is required. Please provide a numeric value to convert")
	}

	var value float64
	switch v := valueInterface.(type) {
	case float64:
		value = v
	case int:
		value = float64(v)
	case string:
		// Try to parse the string as a number
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			log.Infof("Unit converter tool error: could not parse value from string: %v", err)
			return nil, fmt.Errorf("could not parse value from string '%s': %v", v, err)
		}
		value = parsed
	default:
		log.Infof("Unit converter tool error: value must be a number")
		return nil, fmt.Errorf("value must be a number, got %T", valueInterface)
	}

	// Extract units
	fromUnitInterface, ok := normalizedParams["from_unit"]
	if !ok {
		log.Infof("Unit converter tool error: from_unit is required")
		return nil, fmt.Errorf("from_unit is required. Please specify the source unit")
	}
	fromUnitStr, ok := fromUnitInterface.(string)
	if !ok {
		fromUnitStr = fmt.Sprintf("%v", fromUnitInterface)
	}
	fromUnit := normalizeUnit(fromUnitStr)

	toUnitInterface, ok := normalizedParams["to_unit"]
	if !ok {
		log.Infof("Unit converter tool error: to_unit is required")
		return nil, fmt.Errorf("to_unit is required. Please specify the target unit")
	}
	toUnitStr, ok := toUnitInterface.(string)
	if !ok {
		toUnitStr = fmt.Sprintf("%v", toUnitInterface)
	}
	toUnit := normalizeUnit(toUnitStr)

	// Perform the conversion
	var result float64
	var conversionFormula string

	// Try to infer conversion types if needed
	if !isSupportedConversion(fromUnit, toUnit) {
		// Try to determine compatible units
		inferredFrom, inferredTo := inferUnitTypes(fromUnit, toUnit)
		if inferredFrom != "" && inferredTo != "" {
			log.Infof("Unit converter tool: inferred conversion from %s to %s", inferredFrom, inferredTo)
			fromUnit = inferredFrom
			toUnit = inferredTo
		}
	}

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
	} else if fromUnit == "meters" && toUnit == "feet" {
		result = value * 3.28084
		conversionFormula = "feet = meters * 3.28084"
	} else if fromUnit == "feet" && toUnit == "meters" {
		result = value * 0.3048
		conversionFormula = "meters = feet * 0.3048"
	} else if fromUnit == "kg" && toUnit == "pounds" {
		result = value * 2.20462
		conversionFormula = "pounds = kg * 2.20462"
	} else if fromUnit == "pounds" && toUnit == "kg" {
		result = value * 0.453592
		conversionFormula = "kg = pounds * 0.453592"
	} else if fromUnit == "liters" && toUnit == "gallons" {
		result = value * 0.264172
		conversionFormula = "gallons = liters * 0.264172"
	} else if fromUnit == "gallons" && toUnit == "liters" {
		result = value * 3.78541
		conversionFormula = "liters = gallons * 3.78541"
	} else {
		log.Infof("Unit converter tool error: unsupported conversion: %s to %s", fromUnit, toUnit)
		return nil, fmt.Errorf("unsupported conversion: %s to %s. Supported conversions include: km/miles, celsius/fahrenheit, meters/feet, kg/pounds, liters/gallons", fromUnit, toUnit)
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
	unit = strings.ToLower(strings.TrimSpace(unit))

	// Handle common abbreviations and variations
	unitMap := map[string]string{
		// Distance units
		"kilometer":  "km",
		"kilometers": "km",
		"klick":      "km",
		"klicks":     "km",
		"km":         "km",
		"mile":       "miles",
		"miles":      "miles",
		"mi":         "miles",
		"meter":      "meters",
		"meters":     "meters",
		"m":          "meters",
		"foot":       "feet",
		"feet":       "feet",
		"ft":         "feet",
		"yard":       "yards",
		"yards":      "yards",
		"yd":         "yards",
		"inch":       "inches",
		"inches":     "inches",
		"in":         "inches",

		// Temperature units
		"celsius":    "celsius",
		"c":          "celsius",
		"centigrade": "celsius",
		"fahrenheit": "fahrenheit",
		"f":          "fahrenheit",
		"kelvin":     "kelvin",
		"k":          "kelvin",

		// Weight/Mass units
		"kilogram":  "kg",
		"kilograms": "kg",
		"kgs":       "kg",
		"kg":        "kg",
		"pound":     "pounds",
		"pounds":    "pounds",
		"lb":        "pounds",
		"lbs":       "pounds",
		"gram":      "grams",
		"grams":     "grams",
		"g":         "grams",
		"ounce":     "ounces",
		"ounces":    "ounces",
		"oz":        "ounces",

		// Volume units
		"liter":        "liters",
		"liters":       "liters",
		"l":            "liters",
		"gallon":       "gallons",
		"gallons":      "gallons",
		"gal":          "gallons",
		"quart":        "quarts",
		"quarts":       "quarts",
		"qt":           "quarts",
		"pint":         "pints",
		"pints":        "pints",
		"pt":           "pints",
		"milliliter":   "milliliters",
		"milliliters":  "milliliters",
		"ml":           "milliliters",
		"cubic meter":  "cubic meters",
		"cubic meters": "cubic meters",
		"m3":           "cubic meters",
		"cubic foot":   "cubic feet",
		"cubic feet":   "cubic feet",
		"cu ft":        "cubic feet",
		"ft3":          "cubic feet",
	}

	if normalized, exists := unitMap[unit]; exists {
		return normalized
	}

	// Try to extract the unit from combined forms like "5km" or "10celsius"
	for key, value := range unitMap {
		if strings.HasSuffix(unit, key) {
			return value
		}
	}

	// If we couldn't normalize, return as is
	return unit
}

// conversionParts holds the parts of a parsed conversion
type conversionParts struct {
	value    float64
	fromUnit string
	toUnit   string
}

// extractConversionParts tries to parse a conversion string
// Examples: "5 km to miles", "convert 100F to C", "32 celsius in fahrenheit"
func extractConversionParts(text string) conversionParts {
	text = strings.ToLower(strings.TrimSpace(text))

	// Remove common filler words
	text = strings.ReplaceAll(text, "convert", "")
	text = strings.ReplaceAll(text, "change", "")
	text = strings.ReplaceAll(text, "from", "")

	// Normalize separators
	for _, sep := range []string{" to ", " in ", " into ", " as ", " -> "} {
		if strings.Contains(text, sep) {
			text = strings.ReplaceAll(text, sep, "|")
		}
	}

	if !strings.Contains(text, "|") {
		return conversionParts{} // No conversion separators found
	}

	parts := strings.Split(text, "|")
	if len(parts) != 2 {
		return conversionParts{} // Unexpected format
	}

	// Extract value and from_unit from first part
	fromPart := strings.TrimSpace(parts[0])
	var valueStr string
	var fromUnit string

	// Try to extract numeric value
	var value float64
	var err error

	// Find where the numeric part ends
	i := 0
	for i < len(fromPart) {
		if (fromPart[i] >= '0' && fromPart[i] <= '9') || fromPart[i] == '.' || fromPart[i] == '-' {
			i++
		} else {
			break
		}
	}

	if i > 0 {
		valueStr = fromPart[:i]
		fromUnit = strings.TrimSpace(fromPart[i:])

		value, err = strconv.ParseFloat(valueStr, 64)
		if err != nil {
			return conversionParts{} // Could not parse value
		}
	} else {
		// Try looking for the number elsewhere in the string
		numberRegex := regexp.MustCompile(`[-+]?\d*\.?\d+`)
		match := numberRegex.FindString(fromPart)
		if match != "" {
			value, err = strconv.ParseFloat(match, 64)
			if err != nil {
				return conversionParts{} // Could not parse value
			}

			// Remove the number to get the unit
			fromUnit = strings.TrimSpace(strings.ReplaceAll(fromPart, match, ""))
		} else {
			return conversionParts{} // No number found
		}
	}

	// Extract to_unit from second part
	toUnit := strings.TrimSpace(parts[1])

	// Normalize units
	fromUnit = normalizeUnit(fromUnit)
	toUnit = normalizeUnit(toUnit)

	return conversionParts{
		value:    value,
		fromUnit: fromUnit,
		toUnit:   toUnit,
	}
}

// isSupportedConversion checks if a conversion between two units is supported
func isSupportedConversion(fromUnit, toUnit string) bool {
	supportedPairs := map[string]bool{
		"km|miles":           true,
		"miles|km":           true,
		"celsius|fahrenheit": true,
		"fahrenheit|celsius": true,
		"meters|feet":        true,
		"feet|meters":        true,
		"kg|pounds":          true,
		"pounds|kg":          true,
		"liters|gallons":     true,
		"gallons|liters":     true,
	}

	key := fromUnit + "|" + toUnit
	return supportedPairs[key]
}

// inferUnitTypes tries to determine compatible unit types
func inferUnitTypes(fromUnit, toUnit string) (string, string) {
	// Define unit categories
	distanceUnits := map[string]bool{"km": true, "miles": true, "meters": true, "feet": true, "yards": true, "inches": true}
	temperatureUnits := map[string]bool{"celsius": true, "fahrenheit": true, "kelvin": true}
	weightUnits := map[string]bool{"kg": true, "pounds": true, "grams": true, "ounces": true}
	volumeUnits := map[string]bool{"liters": true, "gallons": true, "quarts": true, "pints": true, "milliliters": true, "cubic meters": true, "cubic feet": true}

	// Check if both are in the same category, but we just need to normalize
	if distanceUnits[fromUnit] && distanceUnits[toUnit] {
		if fromUnit == "km" && toUnit != "miles" {
			return "km", "miles"
		}
		if fromUnit == "miles" && toUnit != "km" {
			return "miles", "km"
		}
		if fromUnit == "meters" && toUnit != "feet" {
			return "meters", "feet"
		}
		if fromUnit == "feet" && toUnit != "meters" {
			return "feet", "meters"
		}
	}

	if temperatureUnits[fromUnit] && temperatureUnits[toUnit] {
		if fromUnit == "celsius" && toUnit != "fahrenheit" {
			return "celsius", "fahrenheit"
		}
		if fromUnit == "fahrenheit" && toUnit != "celsius" {
			return "fahrenheit", "celsius"
		}
	}

	if weightUnits[fromUnit] && weightUnits[toUnit] {
		if fromUnit == "kg" && toUnit != "pounds" {
			return "kg", "pounds"
		}
		if fromUnit == "pounds" && toUnit != "kg" {
			return "pounds", "kg"
		}
	}

	if volumeUnits[fromUnit] && volumeUnits[toUnit] {
		if fromUnit == "liters" && toUnit != "gallons" {
			return "liters", "gallons"
		}
		if fromUnit == "gallons" && toUnit != "liters" {
			return "gallons", "liters"
		}
	}

	// No inference possible
	return "", ""
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
				normalizedParams[strings.ToLower(k)] = v
			}
		} else if inputStr, isStr := toolInput.(string); isStr && inputStr != "" {
			// String input - try to parse as JSON first
			var jsonMap map[string]interface{}
			if err := json.Unmarshal([]byte(inputStr), &jsonMap); err == nil {
				log.Infof("Parsed tool_input JSON string into parameters")
				for k, v := range jsonMap {
					normalizedParams[strings.ToLower(k)] = v
				}
			} else {
				// Direct string value - treat as text to analyze
				log.Infof("Using direct string as text to analyze: %s", inputStr)
				normalizedParams["text"] = inputStr

				// Try to infer analysis type from the original args
				for k, v := range args {
					lowerK := strings.ToLower(k)
					if (lowerK == "analysis_type" || lowerK == "type" || lowerK == "analyze_for" ||
						lowerK == "analysis" || lowerK == "operation" || lowerK == "mode" ||
						lowerK == "task") && v != nil {
						if typeStr, ok := v.(string); ok {
							normalizedParams["analysis_type"] = normalizeAnalysisType(typeStr)
							break
						}
					}
				}

				// If no analysis type specified, try to infer from the content
				if normalizedParams["analysis_type"] == nil {
					inferredType := inferAnalysisTypeFromText(inputStr)
					if inferredType != "" {
						normalizedParams["analysis_type"] = inferredType
						log.Infof("Inferred analysis type: %s", inferredType)
					} else {
						// Default to word_count if we can't infer
						normalizedParams["analysis_type"] = "word_count"
					}
				}
			}
		}
	}

	// Add any direct arguments not in tool_input
	for k, v := range args {
		if k != "tool_name" && k != "tool_input" && normalizedParams[strings.ToLower(k)] == nil {
			normalizedParams[strings.ToLower(k)] = v
		}
	}

	// Check for parameter aliases
	checkParamAliases := func(aliases []string) interface{} {
		for _, alias := range aliases {
			if val, exists := normalizedParams[alias]; exists && val != nil {
				return val
			}
		}
		return nil
	}

	// Check for text aliases
	textAliases := []string{"text", "input", "content", "document", "string", "passage", "paragraph", "source", "analyze", "body", "data"}
	textParam := checkParamAliases(textAliases)
	if textParam != nil {
		normalizedParams["text"] = textParam
	}

	// Check for analysis_type aliases
	typeAliases := []string{"analysis_type", "type", "analyze_for", "analysis", "operation", "mode", "task", "method", "function"}
	typeParam := checkParamAliases(typeAliases)
	if typeParam != nil {
		if strType, ok := typeParam.(string); ok {
			normalizedParams["analysis_type"] = normalizeAnalysisType(strType)
		}
	}

	// If we have a single direct string parameter, treat as text to analyze
	if len(normalizedParams) == 0 && len(args) == 1 {
		for _, v := range args {
			if strValue, isStr := v.(string); isStr {
				normalizedParams["text"] = strValue

				// Try to infer analysis type from content
				inferredType := inferAnalysisTypeFromText(strValue)
				if inferredType != "" {
					normalizedParams["analysis_type"] = inferredType
				} else {
					normalizedParams["analysis_type"] = "word_count" // Default type
				}
			}
		}
	}

	// Extract the text
	textInterface, ok := normalizedParams["text"]
	if !ok {
		log.Infof("Text Analysis tool error: text is required")
		return nil, fmt.Errorf("text to analyze is required. Please provide text using the 'text' parameter")
	}

	var text string
	switch v := textInterface.(type) {
	case string:
		text = v
	case float64, int:
		text = fmt.Sprintf("%v", v)
	default:
		// Try to convert to JSON string if it's a complex object
		if jsonBytes, err := json.Marshal(textInterface); err == nil {
			text = string(jsonBytes)
		} else {
			text = fmt.Sprintf("%v", textInterface)
		}
	}

	if text == "" {
		log.Infof("Text Analysis tool error: text cannot be empty")
		return nil, fmt.Errorf("text to analyze cannot be empty")
	}

	// Extract the analysis type
	analysisTypeInterface, ok := normalizedParams["analysis_type"]
	if !ok {
		// Try to infer the analysis type from the text content
		inferredType := inferAnalysisTypeFromText(text)
		if inferredType != "" {
			analysisTypeInterface = inferredType
		} else {
			// Default to word_count if still not specified
			analysisTypeInterface = "word_count"
		}
	}

	analysisType, ok := analysisTypeInterface.(string)
	if !ok {
		analysisType = fmt.Sprintf("%v", analysisTypeInterface)
	}

	// Normalize the analysis type
	analysisType = normalizeAnalysisType(analysisType)

	result := map[string]interface{}{}

	switch analysisType {
	case "word_count":
		words := strings.Fields(text)
		sentences := strings.Count(text, ".") + strings.Count(text, "!") + strings.Count(text, "?")
		if sentences == 0 && len(text) > 0 {
			sentences = 1
		}

		// Count paragraphs (non-empty lines)
		paragraphs := 0
		lines := strings.Split(text, "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				paragraphs++
			}
		}

		result = map[string]interface{}{
			"word_count":      len(words),
			"character_count": len(text),
			"sentence_count":  sentences,
			"paragraph_count": paragraphs,
			"line_count":      len(lines),
			"unique_words":    countUniqueWords(words),
		}
		log.Infof("Text Analysis tool: word count result for text: %v", result)

	case "sentiment":
		// Simple sentiment analysis
		positiveWords := []string{
			"good", "great", "excellent", "happy", "love", "best", "beautiful", "joy", "wonderful",
			"superb", "amazing", "fantastic", "perfect", "glad", "pleased", "delighted", "awesome",
			"positive", "nice", "brilliant", "splendid", "superior", "terrific", "outstanding",
		}
		negativeWords := []string{
			"bad", "terrible", "awful", "hate", "worst", "poor", "horrible", "sad", "angry",
			"upset", "disappointed", "mediocre", "miserable", "inferior", "negative", "dreadful",
			"ugly", "painful", "distressing", "troubling", "annoying", "frustrating", "grim",
		}

		lowerText := strings.ToLower(text)
		positiveCount := 0
		negativeCount := 0

		for _, word := range positiveWords {
			positiveCount += strings.Count(lowerText, word)
		}

		for _, word := range negativeWords {
			negativeCount += strings.Count(lowerText, word)
		}

		// Calculate sentiment score from -1 (negative) to +1 (positive)
		totalSentimentWords := positiveCount + negativeCount
		var sentimentScore float64 = 0
		if totalSentimentWords > 0 {
			sentimentScore = float64(positiveCount-negativeCount) / float64(totalSentimentWords)
			// Round to 2 decimal places
			sentimentScore = math.Round(sentimentScore*100) / 100
		}

		sentiment := "Neutral"
		if sentimentScore > 0.2 {
			sentiment = "Positive"
		} else if sentimentScore < -0.2 {
			sentiment = "Negative"
		}

		result = map[string]interface{}{
			"sentiment":       sentiment,
			"sentiment_score": sentimentScore,
			"positive_words":  positiveCount,
			"negative_words":  negativeCount,
			"sentiment_words": totalSentimentWords,
			"total_words":     len(strings.Fields(text)),
		}
		log.Infof("Text Analysis tool: sentiment analysis result: %v", result)

	case "summary":
		var summary string

		// Try a simple sentence-based summarization
		sentences := extractSentences(text)

		if len(sentences) <= 1 {
			// Just return the first 100 chars if only one sentence
			if len(text) > 100 {
				summary = text[:100] + "..."
			} else {
				summary = text
			}
		} else if len(sentences) <= 3 {
			// For very short texts, just return the first sentence
			summary = sentences[0]
		} else {
			// For longer texts, return first and last sentence with an ellipsis
			summary = sentences[0] + " ... " + sentences[len(sentences)-1]
		}

		result = map[string]interface{}{
			"summary":         summary,
			"original_length": len(text),
			"summary_length":  len(summary),
			"compression":     fmt.Sprintf("%.1f%%", 100.0*(1.0-float64(len(summary))/float64(len(text)))),
			"sentence_count":  len(sentences),
		}
		log.Infof("Text Analysis tool: summary result (length %d → %d)", len(text), len(summary))

	default:
		log.Infof("Text Analysis tool error: unsupported analysis type: %s", analysisType)
		return nil, fmt.Errorf("unsupported analysis type: %s. Supported types are: word_count, sentiment, summary", analysisType)
	}

	// Add metadata to all results
	result["analysis_type"] = analysisType
	result["timestamp"] = time.Now().Format(time.RFC3339)

	// Log result
	log.Infof("Text Analysis tool returning result: %v", result)
	return tool.NewJSONResult(result), nil
}

// normalizeAnalysisType standardizes the analysis type
func normalizeAnalysisType(analysisType string) string {
	analysisType = strings.ToLower(strings.TrimSpace(analysisType))

	// Map of analysis type aliases
	typeMap := map[string]string{
		// Word count aliases
		"word_count": "word_count",
		"wordcount":  "word_count",
		"words":      "word_count",
		"count":      "word_count",
		"stats":      "word_count",
		"statistics": "word_count",
		"length":     "word_count",
		"analyze":    "word_count",

		// Sentiment aliases
		"sentiment":          "sentiment",
		"emotion":            "sentiment",
		"mood":               "sentiment",
		"feeling":            "sentiment",
		"tone":               "sentiment",
		"sentiment_analysis": "sentiment",
		"emotional_analysis": "sentiment",
		"polarity":           "sentiment",

		// Summary aliases
		"summary":       "summary",
		"summarize":     "summary",
		"summarization": "summary",
		"extract":       "summary",
		"shorten":       "summary",
		"brief":         "summary",
		"tldr":          "summary",
		"abstract":      "summary",
	}

	if normalized, exists := typeMap[analysisType]; exists {
		return normalized
	}

	// Try to match partial names
	for key, value := range typeMap {
		if strings.Contains(analysisType, key) {
			return value
		}
	}

	return analysisType
}

// inferAnalysisTypeFromText tries to determine the most appropriate analysis type
// based on the text content or any hints in the text
func inferAnalysisTypeFromText(text string) string {
	text = strings.ToLower(text)

	// Check for explicit indicators in the text
	if strings.Contains(text, "sentiment") || strings.Contains(text, "emotion") ||
		strings.Contains(text, "feeling") || strings.Contains(text, "tone") ||
		strings.Contains(text, "positive") || strings.Contains(text, "negative") {
		return "sentiment"
	}

	if strings.Contains(text, "summary") || strings.Contains(text, "summarize") ||
		strings.Contains(text, "tldr") || strings.Contains(text, "shorten") ||
		strings.Contains(text, "extract") || strings.Contains(text, "brief") {
		return "summary"
	}

	if strings.Contains(text, "word count") || strings.Contains(text, "character count") ||
		strings.Contains(text, "length") || strings.Contains(text, "stats") ||
		strings.Contains(text, "statistics") {
		return "word_count"
	}

	// If no clear indicators, use length as a heuristic
	if len(text) > 500 {
		// Longer texts are often better for summarization
		return "summary"
	}

	// Default to word count for short texts with no clear indicators
	return "word_count"
}

// countUniqueWords returns the count of unique words in the given slice
func countUniqueWords(words []string) int {
	uniqueWords := make(map[string]bool)
	for _, word := range words {
		word = strings.ToLower(strings.Trim(word, ".,!?;:\"'()[]{}"))
		if word != "" {
			uniqueWords[word] = true
		}
	}
	return len(uniqueWords)
}

// extractSentences splits text into sentences
func extractSentences(text string) []string {
	// Replace common end-of-sentence punctuation with a unique marker
	marker := "||SENTENCE_END||"
	text = strings.ReplaceAll(text, ". ", marker)
	text = strings.ReplaceAll(text, "! ", marker)
	text = strings.ReplaceAll(text, "? ", marker)

	// Handle cases where sentences might end with punctuation but without a space
	text = strings.ReplaceAll(text, ".\n", marker)
	text = strings.ReplaceAll(text, "!\n", marker)
	text = strings.ReplaceAll(text, "?\n", marker)

	// Split by marker
	parts := strings.Split(text, marker)

	// Clean up and filter empty parts
	sentences := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			sentences = append(sentences, part)
		}
	}

	return sentences
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
			// Extract parameters with better error handling and flexibility
			log.Infof("MCP weather tool called with params: %v", req.Params.Arguments)

			// Normalize parameters
			normalizedParams := make(map[string]interface{})
			for k, v := range req.Params.Arguments {
				normalizedParams[strings.ToLower(k)] = v
			}

			// Check for location aliases
			locationAliases := []string{"location", "city", "place", "area", "region", "town", "query", "q", "where", "loc"}
			location := ""

			// Try to find the location from all possible parameter names
			for _, alias := range locationAliases {
				if val, exists := normalizedParams[alias]; exists && val != nil {
					switch v := val.(type) {
					case string:
						if strings.TrimSpace(v) != "" {
							location = strings.TrimSpace(v)
							break
						}
					default:
						// Try to convert non-string values to string
						location = fmt.Sprintf("%v", val)
						if strings.TrimSpace(location) != "" {
							break
						}
					}
				}
			}

			// Check if we found a location
			if location == "" {
				// Try to infer from any string parameters if no explicit location found
				for _, v := range normalizedParams {
					if strVal, isStr := v.(string); isStr && strings.TrimSpace(strVal) != "" {
						// Check if this looks like a location (not a date or other format)
						if !strings.ContainsAny(strVal, "{}[]()=+*&^%$#@!~`|\\<>") {
							location = strings.TrimSpace(strVal)
							break
						}
					}
				}
			}

			// Last resort: check if there's any string parameter called "text" or "__text" (sometimes used for direct input)
			if location == "" {
				for k, v := range normalizedParams {
					if (k == "text" || k == "__text") && v != nil {
						if strVal, isStr := v.(string); isStr && strings.TrimSpace(strVal) != "" {
							// Try to extract potential location from this text
							words := strings.Fields(strVal)
							for _, word := range words {
								// Skip common words and keep only potential locations
								if len(word) > 2 && !strings.ContainsAny(word, "{}[]()=+*&^%$#@!~`|\\<>") {
									// Clean up any punctuation
									word = strings.Trim(word, ".,!?;:")
									if len(word) > 2 {
										location = word
										break
									}
								}
							}
						}
					}
				}
			}

			// If still no location, use a default
			if location == "" {
				log.Infof("MCP weather tool: no location provided, using default")
				location = "San Francisco" // Default location
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

			// Add more details to make the response more useful
			var feelsLike float64
			if humidity > 70 && temperature > 25 {
				feelsLike = temperature + 3 // Humid conditions feel hotter
			} else if windSpeed > 15 && temperature < 20 {
				feelsLike = temperature - 2 // Windy conditions feel colder
			} else {
				feelsLike = temperature
			}

			// Add time of day variation
			timeOfDay := []string{"Morning", "Afternoon", "Evening", "Night"}[locationHash%4]

			// Format values with proper units
			temperatureFormatted := fmt.Sprintf("%.1f°C (%.1f°F)", temperature, (temperature*9/5)+32)
			feelsLikeFormatted := fmt.Sprintf("%.1f°C (%.1f°F)", feelsLike, (feelsLike*9/5)+32)

			// Create a structured response
			result := map[string]interface{}{
				"location": map[string]interface{}{
					"name":      location,
					"latitude":  fmt.Sprintf("%.4f°", lat),
					"longitude": fmt.Sprintf("%.4f°", long),
				},
				"current": map[string]interface{}{
					"temperature":   temperatureFormatted,
					"feels_like":    feelsLikeFormatted,
					"humidity":      fmt.Sprintf("%.1f%%", humidity),
					"wind_speed":    fmt.Sprintf("%.1f km/h", windSpeed),
					"conditions":    conditions,
					"precipitation": fmt.Sprintf("%.1f%%", rainChance),
					"time_of_day":   timeOfDay,
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
		mcp.WithDescription("Gets detailed weather information for a specified location. You can provide the location through parameters like 'location', 'city', 'place', etc."),
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
			// Log the request for debugging
			log.Infof("MCP currency converter called with params: %v", req.Params.Arguments)

			// Normalize parameters
			normalizedParams := make(map[string]interface{})
			for k, v := range req.Params.Arguments {
				normalizedParams[strings.ToLower(k)] = v
			}

			// Try to extract the amount with various parameter names
			amountAliases := []string{"amount", "value", "sum", "money", "quantity", "num", "number"}
			var amount float64
			var amountFound bool

			// Check all amount aliases
			for _, alias := range amountAliases {
				if amountInterface, exists := normalizedParams[alias]; exists && amountInterface != nil {
					switch v := amountInterface.(type) {
					case float64:
						amount = v
						amountFound = true
						break
					case int:
						amount = float64(v)
						amountFound = true
						break
					case string:
						// Try to parse the string as a number
						parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
						if err == nil {
							amount = parsed
							amountFound = true
							break
						}
					}
				}
			}

			// If amount not found, look for any numeric parameter
			if !amountFound {
				for _, v := range normalizedParams {
					switch val := v.(type) {
					case float64:
						amount = val
						amountFound = true
						break
					case int:
						amount = float64(val)
						amountFound = true
						break
					case string:
						// Try to parse numeric string values
						if num, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
							amount = num
							amountFound = true
							break
						}
					}
				}
			}

			// If still no amount, use default
			if !amountFound {
				amount = 100.0 // Default amount
				log.Infof("MCP currency converter: no amount provided, using default value of %f", amount)
			}

			// Try to extract currency codes with various parameter names
			fromCurrencyAliases := []string{"from_currency", "from", "source", "source_currency", "base", "base_currency", "original"}
			toCurrencyAliases := []string{"to_currency", "to", "target", "target_currency", "destination", "dest", "convert_to"}

			var fromCurrency, toCurrency string

			// Check from currency aliases
			for _, alias := range fromCurrencyAliases {
				if currencyInterface, exists := normalizedParams[alias]; exists && currencyInterface != nil {
					if code, ok := currencyInterface.(string); ok && strings.TrimSpace(code) != "" {
						fromCurrency = normalizeCurrencyCode(strings.TrimSpace(code))
						break
					}
				}
			}

			// Check to currency aliases
			for _, alias := range toCurrencyAliases {
				if currencyInterface, exists := normalizedParams[alias]; exists && currencyInterface != nil {
					if code, ok := currencyInterface.(string); ok && strings.TrimSpace(code) != "" {
						toCurrency = normalizeCurrencyCode(strings.TrimSpace(code))
						break
					}
				}
			}

			// If we don't have both currencies yet, try to extract from direct text
			if fromCurrency == "" || toCurrency == "" {
				// Check any string parameters for currency codes
				for _, v := range normalizedParams {
					if strVal, isStr := v.(string); isStr {
						extractedFrom, extractedTo := extractCurrencyPair(strVal)
						if fromCurrency == "" && extractedFrom != "" {
							fromCurrency = extractedFrom
						}
						if toCurrency == "" && extractedTo != "" {
							toCurrency = extractedTo
						}

						// If we found both, we can stop
						if fromCurrency != "" && toCurrency != "" {
							break
						}
					}
				}
			}

			// Set defaults if we still don't have values
			if fromCurrency == "" {
				fromCurrency = "USD" // Default source
				log.Infof("MCP currency converter: no source currency provided, using default %s", fromCurrency)
			}

			if toCurrency == "" {
				if fromCurrency == "USD" {
					toCurrency = "EUR" // Default target if source is USD
				} else {
					toCurrency = "USD" // Default to USD for other source currencies
				}
				log.Infof("MCP currency converter: no target currency provided, using default %s", toCurrency)
			}

			// Normalize currency codes to ensure they're valid
			fromCurrency = normalizeCurrencyCode(fromCurrency)
			toCurrency = normalizeCurrencyCode(toCurrency)

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
			fromRate, fromExists := rates[fromCurrency]
			toRate, toExists := rates[toCurrency]

			if !fromExists {
				log.Infof("MCP currency converter error: unsupported source currency: %s", fromCurrency)
				return nil, fmt.Errorf("unsupported source currency: %s. Supported currencies: USD, EUR, GBP, JPY, CNY, CAD, AUD, CHF, HKD, SGD", fromCurrency)
			}
			if !toExists {
				log.Infof("MCP currency converter error: unsupported target currency: %s", toCurrency)
				return nil, fmt.Errorf("unsupported target currency: %s. Supported currencies: USD, EUR, GBP, JPY, CNY, CAD, AUD, CHF, HKD, SGD", toCurrency)
			}

			// Calculate conversion
			// Convert source to USD, then to target
			converted := amount * (toRate / fromRate)

			// Round to 4 decimal places for most currencies
			precision := 4
			if toCurrency == "JPY" {
				precision = 2 // JPY typically shown with fewer decimals
			}
			factor := math.Pow(10, float64(precision))
			converted = math.Round(converted*factor) / factor

			// Add some market volatility (simulated)
			marketTrend := []string{"up", "down", "stable"}[time.Now().Second()%3]
			volatility := (float64(time.Now().Second() % 10)) / 100

			// Add currency symbols and names for better user experience
			currencySymbols := map[string]string{
				"USD": "$",
				"EUR": "€",
				"GBP": "£",
				"JPY": "¥",
				"CNY": "¥",
				"CAD": "C$",
				"AUD": "A$",
				"CHF": "Fr",
				"HKD": "HK$",
				"SGD": "S$",
			}

			currencyNames := map[string]string{
				"USD": "US Dollar",
				"EUR": "Euro",
				"GBP": "British Pound",
				"JPY": "Japanese Yen",
				"CNY": "Chinese Yuan",
				"CAD": "Canadian Dollar",
				"AUD": "Australian Dollar",
				"CHF": "Swiss Franc",
				"HKD": "Hong Kong Dollar",
				"SGD": "Singapore Dollar",
			}

			// Get symbols and names
			fromSymbol := currencySymbols[fromCurrency]
			toSymbol := currencySymbols[toCurrency]
			fromName := currencyNames[fromCurrency]
			toName := currencyNames[toCurrency]

			// Create result with formatted values for better readability
			result := map[string]interface{}{
				"from": map[string]interface{}{
					"currency":      fromCurrency,
					"currency_name": fromName,
					"symbol":        fromSymbol,
					"amount":        amount,
					"formatted":     formatCurrencyAmount(amount, fromCurrency, fromSymbol),
				},
				"to": map[string]interface{}{
					"currency":      toCurrency,
					"currency_name": toName,
					"symbol":        toSymbol,
					"amount":        converted,
					"formatted":     formatCurrencyAmount(converted, toCurrency, toSymbol),
				},
				"exchange_rate": map[string]interface{}{
					"rate":       toRate / fromRate,
					"expression": fmt.Sprintf("1 %s = %.4f %s", fromCurrency, toRate/fromRate, toCurrency),
					"inverse":    fmt.Sprintf("1 %s = %.4f %s", toCurrency, fromRate/toRate, fromCurrency),
				},
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
			// Log the request for debugging
			log.Infof("MCP data analyzer called with params: %v", req.Params.Arguments)

			// Normalize parameters
			normalizedParams := make(map[string]interface{})
			for k, v := range req.Params.Arguments {
				normalizedParams[strings.ToLower(k)] = v
			}

			// Extract data series with various parameter names
			dataSeriesAliases := []string{"data_series", "data", "series", "values", "numbers", "dataset", "points", "sample"}
			var dataSeriesStr string

			// Try to find the data series from all possible parameter names
			for _, alias := range dataSeriesAliases {
				if val, exists := normalizedParams[alias]; exists && val != nil {
					switch v := val.(type) {
					case string:
						dataSeriesStr = strings.TrimSpace(v)
						if dataSeriesStr != "" {
							break
						}
					case []interface{}:
						// Handle array input directly
						dataSeries := make([]float64, 0, len(v))
						valid := true

						for _, item := range v {
							switch num := item.(type) {
							case float64:
								dataSeries = append(dataSeries, num)
							case int:
								dataSeries = append(dataSeries, float64(num))
							case string:
								parsed, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
								if err == nil {
									dataSeries = append(dataSeries, parsed)
								} else {
									valid = false
									break
								}
							default:
								valid = false
								break
							}
						}

						if valid && len(dataSeries) > 0 {
							// Convert array to comma-separated string
							parts := make([]string, len(dataSeries))
							for i, num := range dataSeries {
								parts[i] = strconv.FormatFloat(num, 'f', -1, 64)
							}
							dataSeriesStr = strings.Join(parts, ",")
							break
						}
					default:
						// Try to convert to string
						dataSeriesStr = fmt.Sprintf("%v", val)
						if strings.TrimSpace(dataSeriesStr) != "" {
							break
						}
					}
				}
			}

			// If we still don't have a data series, try to extract from any parameter
			if dataSeriesStr == "" {
				for _, v := range normalizedParams {
					if arr, isArr := v.([]interface{}); isArr {
						// Handle array input
						dataSeries := make([]float64, 0, len(arr))
						valid := true

						for _, item := range arr {
							switch num := item.(type) {
							case float64:
								dataSeries = append(dataSeries, num)
							case int:
								dataSeries = append(dataSeries, float64(num))
							case string:
								parsed, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
								if err == nil {
									dataSeries = append(dataSeries, parsed)
								} else {
									valid = false
									break
								}
							default:
								valid = false
								break
							}
						}

						if valid && len(dataSeries) > 0 {
							// Convert array to comma-separated string
							parts := make([]string, len(dataSeries))
							for i, num := range dataSeries {
								parts[i] = strconv.FormatFloat(num, 'f', -1, 64)
							}
							dataSeriesStr = strings.Join(parts, ",")
							break
						}
					} else if str, isStr := v.(string); isStr {
						// Check if the string looks like a comma-separated list of numbers
						parts := strings.Split(str, ",")
						if len(parts) > 1 {
							allNumbers := true
							for _, part := range parts {
								if _, err := strconv.ParseFloat(strings.TrimSpace(part), 64); err != nil {
									allNumbers = false
									break
								}
							}

							if allNumbers {
								dataSeriesStr = str
								break
							}
						}
					}
				}
			}

			// Check if we have a data series
			if dataSeriesStr == "" {
				// Generate sample data for demonstration if no data provided
				log.Infof("MCP data analyzer: no data series provided, using sample data")
				sampleData := []float64{10, 15, 20, 25, 30, 35, 40}
				parts := make([]string, len(sampleData))
				for i, num := range sampleData {
					parts[i] = strconv.FormatFloat(num, 'f', -1, 64)
				}
				dataSeriesStr = strings.Join(parts, ",")
			}

			// Extract analysis type with various parameter names
			analysisTypeAliases := []string{"analysis_type", "type", "analyze", "operation", "function", "task", "method", "calculate"}
			var analysisType string

			// Try to find the analysis type from all possible parameter names
			for _, alias := range analysisTypeAliases {
				if val, exists := normalizedParams[alias]; exists && val != nil {
					if typeStr, isStr := val.(string); isStr {
						analysisType = strings.ToLower(strings.TrimSpace(typeStr))
						break
					}
				}
			}

			// If no analysis type specified, try to infer from keywords in parameters
			if analysisType == "" {
				for k, v := range normalizedParams {
					k = strings.ToLower(k)
					if strings.Contains(k, "stat") || strings.Contains(k, "basic") || strings.Contains(k, "summary") {
						analysisType = "statistics"
						break
					} else if strings.Contains(k, "trend") || strings.Contains(k, "regression") || strings.Contains(k, "line") {
						analysisType = "trend"
						break
					} else if strings.Contains(k, "forecast") || strings.Contains(k, "predict") || strings.Contains(k, "future") {
						analysisType = "forecast"
						break
					}

					// Also check string values
					if strVal, isStr := v.(string); isStr {
						strVal = strings.ToLower(strVal)
						if strings.Contains(strVal, "stat") || strings.Contains(strVal, "mean") || strings.Contains(strVal, "average") {
							analysisType = "statistics"
							break
						} else if strings.Contains(strVal, "trend") || strings.Contains(strVal, "line") {
							analysisType = "trend"
							break
						} else if strings.Contains(strVal, "forecast") || strings.Contains(strVal, "predict") {
							analysisType = "forecast"
							break
						}
					}
				}
			}

			// Default to statistics if still not specified
			if analysisType == "" {
				analysisType = "statistics"
				log.Infof("MCP data analyzer: no analysis type provided, using default type: %s", analysisType)
			}

			// Normalize analysis type for known variations
			switch analysisType {
			case "stats", "basic", "summary", "descriptive", "mean", "average", "std", "min", "max":
				analysisType = "statistics"
			case "regression", "line", "linear", "correlation", "r2", "slope", "best-fit":
				analysisType = "trend"
			case "prediction", "future", "extrapolate", "next", "predict":
				analysisType = "forecast"
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
					log.Infof("MCP data analyzer error: invalid data point '%s': %v", part, err)
					return nil, fmt.Errorf("invalid data point '%s'. Please provide a comma-separated list of numbers", part)
				}

				dataSeries = append(dataSeries, val)
			}

			if len(dataSeries) == 0 {
				log.Infof("MCP data analyzer error: data series must contain at least one valid number")
				return nil, fmt.Errorf("data series must contain at least one valid number")
			}

			// Perform requested analysis
			result := map[string]interface{}{
				"data_points":   len(dataSeries),
				"raw_data":      dataSeries,
				"analysis_type": analysisType,
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

				// Calculate additional statistics
				// Range
				dataRange := max - min

				// Mode (most frequent value)
				frequency := make(map[float64]int)
				for _, val := range dataSeries {
					frequency[val]++
				}

				mode := dataSeries[0]
				maxFreq := 0
				for val, freq := range frequency {
					if freq > maxFreq {
						maxFreq = freq
						mode = val
					}
				}

				// Quartiles
				var q1, q3 float64
				if len(sortedData) >= 4 {
					// Calculate Q1 (first quartile)
					q1Idx := len(sortedData) / 4
					if len(sortedData)%4 == 0 {
						q1 = (sortedData[q1Idx-1] + sortedData[q1Idx]) / 2
					} else {
						q1 = sortedData[q1Idx]
					}

					// Calculate Q3 (third quartile)
					q3Idx := (3 * len(sortedData)) / 4
					if len(sortedData)%4 == 0 {
						q3 = (sortedData[q3Idx-1] + sortedData[q3Idx]) / 2
					} else {
						q3 = sortedData[q3Idx]
					}
				} else {
					// For very small datasets, use simple approximations
					q1 = min + (median-min)/2
					q3 = median + (max-median)/2
				}

				// Interquartile range
				iqr := q3 - q1

				// Format values to reasonable precision for readability
				stats := map[string]interface{}{
					"min":                roundToSignificantDigits(min, 4),
					"max":                roundToSignificantDigits(max, 4),
					"range":              roundToSignificantDigits(dataRange, 4),
					"sum":                roundToSignificantDigits(sum, 4),
					"mean":               roundToSignificantDigits(mean, 4),
					"median":             roundToSignificantDigits(median, 4),
					"mode":               roundToSignificantDigits(mode, 4),
					"variance":           roundToSignificantDigits(variance, 4),
					"standard_deviation": roundToSignificantDigits(stdDev, 4),
					"quartiles": map[string]interface{}{
						"q1":  roundToSignificantDigits(q1, 4),
						"q2":  roundToSignificantDigits(median, 4), // Q2 is the median
						"q3":  roundToSignificantDigits(q3, 4),
						"iqr": roundToSignificantDigits(iqr, 4),
					},
					"summary": fmt.Sprintf("The dataset has %d points with values ranging from %.2f to %.2f. The mean is %.2f and the median is %.2f.",
						len(dataSeries), min, max, mean, median),
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

				// Avoid division by zero
				var slope, intercept float64
				if n*sumXX-sumX*sumX != 0 {
					slope = (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
					intercept = (sumY - slope*sumX) / n
				} else {
					// If we can't calculate slope (all x values are the same), use the mean as a flat line
					slope = 0
					intercept = sumY / n
				}

				// Calculate trend line points
				trendLine := make([]float64, len(dataSeries))
				for i := range trendLine {
					trendLine[i] = roundToSignificantDigits(slope*float64(i)+intercept, 4)
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

				var rSquared float64
				if totalSS != 0 {
					rSquared = 1 - (residualSS / totalSS)
				} else {
					rSquared = 0 // If all y values are identical
				}

				// Determine trend direction with more detailed categories
				trendDirection := "stable"
				if slope > 0.5 {
					trendDirection = "rapidly increasing"
				} else if slope > 0.1 {
					trendDirection = "steadily increasing"
				} else if slope > 0.01 {
					trendDirection = "slightly increasing"
				} else if slope < -0.5 {
					trendDirection = "rapidly decreasing"
				} else if slope < -0.1 {
					trendDirection = "steadily decreasing"
				} else if slope < -0.01 {
					trendDirection = "slightly decreasing"
				}

				// Generate a natural language description
				var trendDescription string
				if rSquared > 0.7 {
					trendDescription = fmt.Sprintf("There is a strong %s trend in the data (R² = %.2f).",
						trendDirection, rSquared)
				} else if rSquared > 0.4 {
					trendDescription = fmt.Sprintf("There is a moderate %s trend in the data (R² = %.2f).",
						trendDirection, rSquared)
				} else {
					trendDescription = fmt.Sprintf("There is a weak %s trend in the data (R² = %.2f).",
						trendDirection, rSquared)
				}

				result["trend_analysis"] = map[string]interface{}{
					"slope":           roundToSignificantDigits(slope, 4),
					"intercept":       roundToSignificantDigits(intercept, 4),
					"r_squared":       roundToSignificantDigits(rSquared, 4),
					"trend_line":      trendLine,
					"trend_direction": trendDirection,
					"fit_quality":     getCorrelationDescription(rSquared),
					"equation":        fmt.Sprintf("y = %.4fx + %.4f", slope, intercept),
					"description":     trendDescription,
				}

			case "forecast":
				// Simple forecasting based on moving average and trend
				if len(dataSeries) < 3 {
					log.Infof("MCP data analyzer error: forecasting requires at least 3 data points")
					return nil, fmt.Errorf("forecasting requires at least 3 data points, but only %d provided", len(dataSeries))
				}

				// Calculate trend (linear regression)
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

				// Avoid division by zero
				var slope, intercept float64
				if n*sumXX-sumX*sumX != 0 {
					slope = (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
					intercept = (sumY - slope*sumX) / n
				} else {
					// If we can't calculate slope (all x values are the same), use the mean as a flat line
					slope = 0
					intercept = sumY / n
				}

				// Generate next 5 points forecast (or fewer if the original dataset is very small)
				forecastPoints := 5
				if len(dataSeries) < 5 {
					forecastPoints = len(dataSeries)
				}

				forecast := make([]float64, forecastPoints)
				forecastDates := make([]string, forecastPoints)

				for i := range forecast {
					nextX := float64(len(dataSeries) + i)
					linearForecast := slope*nextX + intercept

					// Add some randomness based on data variance
					variance := 0.0
					for _, val := range dataSeries {
						variance += math.Pow(val-(sumY/n), 2)
					}
					variance /= n

					// Use noise that's proportional to the data variability
					// More variable data -> more noise in the forecast
					noiseLevel := math.Sqrt(variance) * 0.2 // 20% of std deviation

					// Add controlled randomness (using sine for smooth variations)
					randomFactor := (math.Sin(float64(i)*0.7) * noiseLevel)

					// Combine linear forecast with randomness
					forecast[i] = roundToSignificantDigits(linearForecast+randomFactor, 4)

					// Generate "dates" for the forecast points
					forecastDates[i] = fmt.Sprintf("t+%d", i+1)
				}

				// Calculate mean absolute percentage error (MAPE) as a simple confidence measure
				// We'll use the model to "predict" the last few known points and see how good it was
				var mape float64
				if len(dataSeries) >= 6 {
					// Use the last 30% of points for validation
					validationSize := len(dataSeries) / 3
					if validationSize < 2 {
						validationSize = 2
					}

					// Build a model using all but the validation points
					trainingSize := len(dataSeries) - validationSize

					// Calculate regression on training data
					trainSumX := 0.0
					trainSumY := 0.0
					trainSumXY := 0.0
					trainSumXX := 0.0

					for i := 0; i < trainingSize; i++ {
						x := float64(i)
						y := dataSeries[i]
						trainSumX += x
						trainSumY += y
						trainSumXY += x * y
						trainSumXX += x * x
					}

					trainN := float64(trainingSize)
					var trainSlope, trainIntercept float64

					if trainN*trainSumXX-trainSumX*trainSumX != 0 {
						trainSlope = (trainN*trainSumXY - trainSumX*trainSumY) / (trainN*trainSumXX - trainSumX*trainSumX)
						trainIntercept = (trainSumY - trainSlope*trainSumX) / trainN
					} else {
						trainSlope = 0
						trainIntercept = trainSumY / trainN
					}

					// Calculate errors on validation data
					totalAPE := 0.0
					for i := trainingSize; i < len(dataSeries); i++ {
						actual := dataSeries[i]
						predicted := trainSlope*float64(i) + trainIntercept

						// Avoid division by zero
						if actual != 0 {
							ape := math.Abs((actual - predicted) / actual)
							totalAPE += ape
						}
					}

					// Calculate mean APE
					mape = totalAPE / float64(validationSize)

					// Convert to a confidence percentage
					confidence := 100 * (1 - math.Min(mape, 1))
					if confidence < 0 {
						confidence = 0
					}

					// Provide a confidence level description
					var confidenceLevel string
					if confidence > 90 {
						confidenceLevel = "very high"
					} else if confidence > 70 {
						confidenceLevel = "high"
					} else if confidence > 50 {
						confidenceLevel = "moderate"
					} else if confidence > 30 {
						confidenceLevel = "low"
					} else {
						confidenceLevel = "very low"
					}

					// Generate a human-readable description of the forecast
					var forecastDescription string
					if slope > 0.1 {
						forecastDescription = fmt.Sprintf("The forecast predicts an upward trend, with values expected to increase by approximately %.2f units per period.", slope)
					} else if slope < -0.1 {
						forecastDescription = fmt.Sprintf("The forecast predicts a downward trend, with values expected to decrease by approximately %.2f units per period.", -slope)
					} else {
						forecastDescription = "The forecast predicts a relatively stable trend with minor fluctuations."
					}

					result["forecast"] = map[string]interface{}{
						"method":      "linear regression with noise adjustment",
						"next_points": forecast,
						"time_points": forecastDates,
						"confidence": map[string]interface{}{
							"level":      confidenceLevel,
							"percentage": roundToSignificantDigits(confidence, 2),
							"mape":       roundToSignificantDigits(mape, 4),
						},
						"model_params": map[string]interface{}{
							"slope":     roundToSignificantDigits(slope, 4),
							"intercept": roundToSignificantDigits(intercept, 4),
							"equation":  fmt.Sprintf("y = %.4fx + %.4f", slope, intercept),
						},
						"description": forecastDescription,
					}
				} else {
					// For very small datasets, provide a simpler forecast without confidence measures
					result["forecast"] = map[string]interface{}{
						"method":      "linear extrapolation",
						"next_points": forecast,
						"time_points": forecastDates,
						"confidence":  "undetermined (insufficient data for validation)",
						"model_params": map[string]interface{}{
							"slope":     roundToSignificantDigits(slope, 4),
							"intercept": roundToSignificantDigits(intercept, 4),
							"equation":  fmt.Sprintf("y = %.4fx + %.4f", slope, intercept),
						},
						"note": "This forecast is based on limited data. Exercise caution when interpreting these results.",
					}
				}

			default:
				log.Infof("MCP data analyzer error: unsupported analysis type: %s", analysisType)
				return nil, fmt.Errorf("unsupported analysis type: %s. Supported types are: statistics, trend, forecast", analysisType)
			}

			// Add timestamp and metadata
			result["timestamp"] = time.Now().Format(time.RFC3339)
			result["note"] = "This is a simulated analysis for demonstration purposes."

			// Convert to JSON
			resultJSON, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal analysis data: %w", err)
			}

			// Return result
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(resultJSON))}}, nil
		},
		mcp.WithDescription("Analyzes numeric data series for statistical insights, trends, and forecasts. Provide a comma-separated list of numbers or an array."),
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

// normalizeCurrencyCode standardizes currency codes
func normalizeCurrencyCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))

	// Common currency name mappings
	currencyMap := map[string]string{
		// US Dollar variations
		"DOLLAR":    "USD",
		"DOLLARS":   "USD",
		"US":        "USD",
		"USDOLLAR":  "USD",
		"USDOLLARS": "USD",
		"$":         "USD",

		// Euro variations
		"EURO":  "EUR",
		"EUROS": "EUR",
		"€":     "EUR",

		// British Pound variations
		"POUND":    "GBP",
		"POUNDS":   "GBP",
		"GBP":      "GBP",
		"STERLING": "GBP",
		"UK":       "GBP",
		"£":        "GBP",

		// Japanese Yen variations
		"YEN":   "JPY",
		"JAPAN": "JPY",
		"¥":     "JPY",

		// Chinese Yuan variations
		"YUAN":     "CNY",
		"CHINA":    "CNY",
		"RMB":      "CNY",
		"RENMINBI": "CNY",

		// Canadian Dollar variations
		"CAD":            "CAD",
		"CANADIAN":       "CAD",
		"CANADIANDOLLAR": "CAD",
		"C$":             "CAD",

		// Australian Dollar variations
		"AUD":              "AUD",
		"AUSTRALIAN":       "AUD",
		"AUSTRALIANDOLLAR": "AUD",
		"A$":               "AUD",

		// Swiss Franc variations
		"FRANC":      "CHF",
		"FRANCS":     "CHF",
		"SWISS":      "CHF",
		"SWISSFRANC": "CHF",

		// Hong Kong Dollar variations
		"HKD":      "HKD",
		"HONGKONG": "HKD",
		"HK$":      "HKD",

		// Singapore Dollar variations
		"SGD":       "SGD",
		"SINGAPORE": "SGD",
		"S$":        "SGD",
	}

	// Try direct match
	if len(code) == 3 {
		return code // Already a 3-letter currency code
	}

	// Try to match from the map
	if normalized, exists := currencyMap[code]; exists {
		return normalized
	}

	// If it's a single character (likely a symbol), try to match
	if len(code) == 1 {
		switch code {
		case "$":
			return "USD"
		case "€":
			return "EUR"
		case "£":
			return "GBP"
		case "¥":
			if strings.Contains(strings.ToLower(strings.Join(symbolsToSlice(normalizedParams), " ")), "japan") {
				return "JPY"
			}
			return "CNY" // Default to CNY for ¥ unless context suggests JPY
		}
	}

	// Return original if no match found
	return code
}

// formatCurrencyAmount formats a currency amount with the appropriate symbol and format
func formatCurrencyAmount(amount float64, currencyCode string, symbol string) string {
	// Format with appropriate decimal places
	var formatted string

	if currencyCode == "JPY" || currencyCode == "CNY" {
		// Yen and Yuan are typically shown without decimal places
		formatted = fmt.Sprintf("%.0f", amount)
	} else {
		formatted = fmt.Sprintf("%.2f", amount)
	}

	// Add the symbol in the correct position
	if symbol != "" {
		// Most currencies show symbol first, but some like EUR often show it after
		if currencyCode == "EUR" {
			return formatted + symbol
		} else {
			return symbol + formatted
		}
	}

	// If no symbol, use the code
	return formatted + " " + currencyCode
}

// extractCurrencyPair tries to identify currency codes in a string
// Example inputs: "usd to eur", "convert 100 dollars to euros", etc.
func extractCurrencyPair(text string) (fromCurrency, toCurrency string) {
	text = strings.ToLower(strings.TrimSpace(text))

	// Common currency indicators
	currencyIndicators := map[string]string{
		"dollar":   "USD",
		"$":        "USD",
		"usd":      "USD",
		"euro":     "EUR",
		"€":        "EUR",
		"eur":      "EUR",
		"pound":    "GBP",
		"£":        "GBP",
		"gbp":      "GBP",
		"yen":      "JPY",
		"jpy":      "JPY",
		"yuan":     "CNY",
		"cny":      "CNY",
		"rmb":      "CNY",
		"cad":      "CAD",
		"canadian": "CAD",
		"aud":      "AUD",
		"aussie":   "AUD",
		"chf":      "CHF",
		"franc":    "CHF",
		"hkd":      "HKD",
		"sgd":      "SGD",
	}

	// Try to find source and target currencies in the text
	fromCode := ""
	toCode := ""

	// Check for separator phrases
	separators := []string{" to ", " in ", " into ", "->", "=>"}
	for _, sep := range separators {
		if strings.Contains(text, sep) {
			parts := strings.Split(text, sep)
			if len(parts) >= 2 {
				// Check the parts for currency indicators
				for indicator, code := range currencyIndicators {
					if strings.Contains(parts[0], indicator) && fromCode == "" {
						fromCode = code
					}
					if strings.Contains(parts[1], indicator) && toCode == "" {
						toCode = code
					}
				}

				// If we found both currencies, we can stop
				if fromCode != "" && toCode != "" {
					break
				}
			}
		}
	}

	// If separator phrases didn't work, look for currency codes or symbols directly
	if fromCode == "" || toCode == "" {
		words := strings.Fields(text)

		// First pass: look for exact 3-letter currency codes
		for _, word := range words {
			word = strings.ToUpper(strings.Trim(word, ",.?!;:()[]{}"))
			if len(word) == 3 && word != "TO" && word != "FROM" {
				// Check if it's likely a currency code (all letters)
				isCode := true
				for _, c := range word {
					if c < 'A' || c > 'Z' {
						isCode = false
						break
					}
				}

				if isCode {
					if fromCode == "" {
						fromCode = word
					} else if toCode == "" && word != fromCode {
						toCode = word
					}
				}
			}
		}

		// Second pass: look for currency indicators
		if fromCode == "" || toCode == "" {
			for _, word := range words {
				lowerWord := strings.ToLower(word)
				if code, exists := currencyIndicators[lowerWord]; exists {
					if fromCode == "" {
						fromCode = code
					} else if toCode == "" && code != fromCode {
						toCode = code
					}
				}
			}
		}
	}

	return fromCode, toCode
}

// symbolsToSlice converts a map to a slice of string values (helper for context)
func symbolsToSlice(m map[string]interface{}) []string {
	result := make([]string, 0, len(m))
	for _, v := range m {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// Helper variable for context in normalizing currency codes
var normalizedParams map[string]interface{}

// roundToSignificantDigits rounds a number to the specified number of significant digits
func roundToSignificantDigits(num float64, sigDigits int) float64 {
	if num == 0 {
		return 0 // Avoid log10 of zero
	}

	// Determine the order of magnitude of the number
	orderOfMagnitude := math.Floor(math.Log10(math.Abs(num)))

	// Calculate the scaling factor to round to significant digits
	scale := math.Pow(10, float64(sigDigits)-orderOfMagnitude-1)

	// Round the number using the scaling factor
	return math.Round(num*scale) / scale
}
