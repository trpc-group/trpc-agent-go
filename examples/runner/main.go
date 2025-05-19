package main

import (
	"bufio"
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
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent/agents/react"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcptools "trpc.group/trpc-go/trpc-agent-go/tool/tools"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

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

// BasicTaskProcessor implements the taskmanager.TaskProcessor interface.
// It processes tasks by using the ReAct agent with tools.
type BasicTaskProcessor struct{}

// Process implements the TaskProcessor interface to handle task processing.
func (p *BasicTaskProcessor) Process(
	ctx context.Context,
	taskID string,
	initialMsg protocol.Message,
	handle taskmanager.TaskHandle,
) error {
	// Update task status to working
	if err := handle.UpdateStatus(protocol.TaskStateWorking, nil); err != nil {
		return fmt.Errorf("failed to update task status to working: %w", err)
	}

	// Extract the text from the message parts
	var text string
	for _, part := range initialMsg.Parts {
		if textPart, ok := part.(protocol.TextPart); ok {
			text = textPart.Text
			break
		}
	}

	// Log task start
	log.Infof("Starting to process task %s with input: %s", taskID, text)

	// Get model provider and name from environment
	provider := os.Getenv("MODEL_PROVIDER")
	modelName := os.Getenv("MODEL_NAME")

	// Use default model if not specified
	if provider == "" || modelName == "" {
		defaultModel := GetDefaultModel()
		provider = defaultModel.Provider
		modelName = defaultModel.ModelName
		log.Infof("Using default model: %s %s", provider, modelName)
	}

	// Create the model based on provider and name
	llmModel, err := createModel(provider, modelName)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to create model: %v. Make sure the %s environment variable is set.",
			err, GetModelAPIEnvVar(provider, modelName))
		log.Infof("Error:", errorMsg)

		// Update task status to failed with error message
		errMsgParts := []protocol.Part{
			protocol.NewTextPart(errorMsg),
		}
		errResponse := protocol.NewMessage(protocol.MessageRoleAgent, errMsgParts)

		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errResponse); err != nil {
			return fmt.Errorf("failed to update task status to failed: %w", err)
		}
		return fmt.Errorf(errorMsg)
	}

	// Log which model we're using
	log.Infof("Using %s model: %s", provider, modelName)

	// Create all tools (combination of traditional and MCP tools)
	calculatorTool := NewCalculatorTool()
	textAnalysisTool := NewTextAnalysisTool()

	// Add our "regular" tools
	translatorTool := NewTranslatorTool()
	unitConverterTool := NewUnitConverterTool()

	// Set up MCP tools using the existing MCPToolset
	mcpParams := mcptools.MCPServerParams{
		Type: mcptools.ConnectionTypeHTTP,
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

	// Clean up MCP toolset when done
	defer func() {
		if mcpToolset != nil {
			if err := mcpToolset.Close(); err != nil {
				log.Infof("Warning: failed to close MCP toolset: %v", err)
			} else {
				log.Infof("Successfully closed MCP toolset")
			}
		}
	}()

	// Log the tools for debugging
	log.Infof("Using %d tools in total", len(allTools))
	for _, t := range allTools {
		log.Infof("Registered tool: %s - %s", t.Name(), t.Description())
		params, err := json.Marshal(t.Parameters())
		if err != nil {
			log.Infof("Error marshaling parameters for tool %s: %v", t.Name(), err)
		} else {
			log.Infof("Tool %s parameters: %s", t.Name(), string(params))
		}
	}

	// Set up the tools in the model
	toolDefs := make([]model.ToolDefinition, 0, len(allTools))
	for _, t := range allTools {
		toolDefs = append(toolDefs, model.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}

	// Apply tools based on model type using type assertions
	if toolCallModel, ok := llmModel.(model.ToolCallSupportingModel); ok {
		if err := toolCallModel.SetTools(toolDefs); err != nil {
			log.Infof("Warning: failed to set tools on model: %v", err)
		}
	} else {
		log.Infof("Warning: model type %T doesn't support SetTools method", llmModel)
	}

	// Create the ReAct agent
	reactAgent, err := react.NewAgent(react.AgentConfig{
		Name:            fmt.Sprintf("%sAgent", provider),
		Description:     fmt.Sprintf("A ReAct agent powered by %s %s with various tools", provider, modelName),
		Model:           llmModel,
		Tools:           allTools,
		MaxIterations:   10,
		EnableStreaming: false,
	})
	if err != nil {
		log.Infof("Failed to create ReAct agent: %v", err)
		errorMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
			protocol.NewTextPart(fmt.Sprintf("Failed to create ReAct agent: %v", err)),
		})
		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errorMsg); err != nil {
			log.Infof("Failed to update task status: %v", err)
		}
		return fmt.Errorf("failed to create ReAct agent: %w", err)
	}

	// Add debug log for agent creation
	log.Infof("ReAct agent created successfully with model %s and %d tools",
		llmModel.Name(), len(allTools))

	// Status update - agent created and ready
	stageMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
		protocol.NewTextPart(fmt.Sprintf("Agent configured with %s %s and processing your request...", provider, modelName)),
	})
	if err := handle.UpdateStatus(protocol.TaskStateWorking, &stageMsg); err != nil {
		log.Infof("Failed to update status: %v", err)
	}

	// Create a user message for the ReAct agent
	userMsg := message.NewUserMessage(text)

	// Run the ReAct agent with the user message
	log.Infof("Running ReAct agent with user message: %s", text)
	response, err := reactAgent.Run(ctx, userMsg)
	if err != nil {
		log.Infof("Agent execution failed: %v", err)
		errorMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
			protocol.NewTextPart(fmt.Sprintf("Agent execution failed: %v", err)),
		})
		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errorMsg); err != nil {
			log.Infof("Failed to update task status: %v", err)
		}
		return fmt.Errorf("agent execution failed: %w", err)
	}

	// Log agent response for debugging
	log.Infof("Agent response: %s", response.Content)

	// Add agent response
	finalResponseMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
		protocol.NewTextPart(response.Content),
	})
	// Complete the task with the response message
	if err := handle.UpdateStatus(protocol.TaskStateCompleted, &finalResponseMsg); err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}
	log.Infof("Successfully completed task %s", taskID)
	return nil
}

func runServer(address string) {
	// Get model provider and name
	provider := os.Getenv("MODEL_PROVIDER")
	modelName := os.Getenv("MODEL_NAME")

	// Use default model if not specified
	if provider == "" || modelName == "" {
		defaultModel := GetDefaultModel()
		provider = defaultModel.Provider
		modelName = defaultModel.ModelName
	}

	// Create an agent card with metadata about the agent
	desc := fmt.Sprintf("An agent implementing the A2A protocol with %s %s model and various local and remote tools",
		strings.ToUpper(provider), modelName)
	docURL := "https://github.com/yourusername/trpc-a2a-go/docs"

	agentCard := server.AgentCard{
		Name:             fmt.Sprintf("A2A Example Agent (%s %s)", strings.ToUpper(provider), modelName),
		Description:      &desc,
		URL:              "http://" + address + "/",
		Version:          "1.0.0",
		DocumentationURL: &docURL,
		Capabilities: server.AgentCapabilities{
			Streaming:              true,
			PushNotifications:      false,
			StateTransitionHistory: true,
		},
		Skills: []server.AgentSkill{
			{
				ID:   "text-analysis",
				Name: "Text Analysis",
				Examples: []string{
					"Analyze the word count in this text.",
					"Analyze the sentiment of this message.",
				},
			},
			{
				ID:   "calculator",
				Name: "Math Operations",
				Examples: []string{
					"Calculate the square root of 16.",
					"Add 5 and 10.",
				},
			},
			{
				ID:   "translator",
				Name: "Text Translation",
				Examples: []string{
					"Translate 'hello world' to Spanish.",
					"Translate this text to French.",
				},
			},
			{
				ID:   "unit-converter",
				Name: "Unit Conversion",
				Examples: []string{
					"Convert 10 kilometers to miles.",
					"Convert 25 celsius to fahrenheit.",
				},
			},
			{
				ID:   "mcp-weather",
				Name: "Weather Information",
				Examples: []string{
					"Look up the weather in Tokyo.",
					"What's the current weather in San Francisco?",
				},
			},
			{
				ID:   "mcp-currency",
				Name: "Currency Conversion",
				Examples: []string{
					"Convert 100 USD to EUR.",
					"What's the exchange rate from GBP to JPY?",
				},
			},
			{
				ID:   "mcp-data-analysis",
				Name: "Data Analysis",
				Examples: []string{
					"Analyze this data series: 10, 15, 12, 18, 20, 22, 19.",
					"Calculate statistics for these numbers: 5, 10, 15, 20, 25.",
				},
			},
		},
	}
	// Create a task processor that will handle the actual task processing
	taskProcessor := &BasicTaskProcessor{}

	// Create an in-memory task manager with the task processor
	taskManager, err := taskmanager.NewMemoryTaskManager(taskProcessor)
	if err != nil {
		log.Fatalf("Failed to create task manager: %v", err)
	}

	// Create the A2A server with the agent card and task manager
	a2aServer, err := server.NewA2AServer(agentCard, taskManager)
	if err != nil {
		log.Fatalf("Failed to create A2A server: %v", err)
	}

	// Start the server in a separate goroutine
	go func() {
		log.Infof("Starting A2A server on %s", address)
		if err := a2aServer.Start(address); err != nil {
			log.Fatalf("Failed to start A2A server: %v", err)
		}
	}()

	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	log.Infof("Shutting down servers...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop the A2A server
	if err := a2aServer.Stop(ctx); err != nil {
		log.Infof("Error shutting down A2A server: %v", err)
	}
}

func runClient(agentURL, message string) {
	// Create a new A2A client
	a2aClient, err := client.NewA2AClient(agentURL)
	if err != nil {
		log.Fatalf("Failed to create A2A client: %v", err)
	}

	// Create a message with the provided text
	parts := []protocol.Part{
		protocol.NewTextPart(message),
	}
	msg := protocol.NewMessage(protocol.MessageRoleUser, parts)

	// Create a task parameters object
	taskParams := protocol.SendTaskParams{
		ID:      fmt.Sprintf("task-%d", time.Now().Unix()),
		Message: msg,
	}

	// Send the task to the agent
	log.Infof("Sending task to agent...")
	task, err := a2aClient.SendTasks(context.Background(), taskParams)
	if err != nil {
		log.Fatalf("Failed to send task: %v", err)
	}

	// Print the initial task state
	log.Infof("Task ID: %s\n", task.ID)
	log.Infof("Initial state: %s\n", task.Status.State)

	// Poll for updates until the task is completed or failed
	for task.Status.State != protocol.TaskStateCompleted &&
		task.Status.State != protocol.TaskStateFailed &&
		task.Status.State != protocol.TaskStateCanceled {

		time.Sleep(500 * time.Millisecond)

		// Get the current task state
		taskQuery := protocol.TaskQueryParams{
			ID: task.ID,
		}

		task, err = a2aClient.GetTasks(context.Background(), taskQuery)
		if err != nil {
			log.Fatalf("Failed to get task status: %v", err)
		}

		log.Infof("Task state: %s\n", task.Status.State)

		// Display any message
		if task.Status.Message != nil {
			for _, part := range task.Status.Message.Parts {
				if textPart, ok := part.(protocol.TextPart); ok {
					log.Infof("  > %s\n", textPart.Text)
				}
			}
		}
	}

	// Print the final result
	fmt.Printf("\nFinal Result:")
	if task.Status.Message != nil {
		fmt.Println("Agent response:")
		for _, part := range task.Status.Message.Parts {
			if textPart, ok := part.(protocol.TextPart); ok {
				fmt.Printf("  %s\n", textPart.Text)
			}
		}
	}

	// Print any artifacts
	if len(task.Artifacts) > 0 {
		fmt.Printf("\nArtifacts:")
		for i, artifact := range task.Artifacts {
			fmt.Printf("Artifact %d: %s\n", i+1, *artifact.Name)
			fmt.Printf("Description: %s\n", *artifact.Description)
			for _, part := range artifact.Parts {
				if textPart, ok := part.(protocol.TextPart); ok {
					fmt.Printf("  %s\n", textPart.Text)
				}
			}
			fmt.Printf("--------------------------------\n")
		}
	}
}

func runStreamExample(agentURL, message string) {
	// Create a new A2A client
	a2aClient, err := client.NewA2AClient(agentURL)
	if err != nil {
		log.Fatalf("Failed to create A2A client: %v", err)
	}

	// Create a message with the provided text
	parts := []protocol.Part{
		protocol.NewTextPart(message),
	}
	msg := protocol.NewMessage(protocol.MessageRoleUser, parts)

	// Create a task parameters object
	taskParams := protocol.SendTaskParams{
		ID:      fmt.Sprintf("stream-task-%d", time.Now().Unix()),
		Message: msg,
	}

	// Send the task with streaming
	log.Infof("Sending stream task to agent...")
	eventsChan, err := a2aClient.StreamTask(context.Background(), taskParams)
	if err != nil {
		log.Fatalf("Failed to send streaming task: %v", err)
	}

	// Process events as they arrive
	log.Infof("Receiving events:")
	for event := range eventsChan {
		switch evt := event.(type) {
		case protocol.TaskStatusUpdateEvent:
			log.Infof("Status update: %s\n", evt.Status.State)
			if evt.Status.Message != nil {
				for _, part := range evt.Status.Message.Parts {
					if textPart, ok := part.(protocol.TextPart); ok {
						log.Infof("  > %s\n", textPart.Text)
					}
				}
			}
		case protocol.TaskArtifactUpdateEvent:
			log.Infof("\nArtifact update: %s\n", *evt.Artifact.Name)
			log.Infof("Description: %s\n", *evt.Artifact.Description)
			for _, part := range evt.Artifact.Parts {
				if textPart, ok := part.(protocol.TextPart); ok {
					log.Infof("%s\n", textPart.Text)
				}
			}
		}

		// Check if this is the final event
		if event.IsFinal() {
			log.Infof("\nReceived final event")
			break
		}
	}
	log.Infof("Stream completed")
}

// ModelInfo stores information about available models.
type ModelInfo struct {
	Provider  string
	ModelName string
	APIEnvVar string
	IsDefault bool
}

// GetAvailableModels returns a list of all supported models.
func GetAvailableModels() []ModelInfo {
	return []ModelInfo{
		// Gemini models
		{Provider: "gemini", ModelName: "gemini-2.0-flash", APIEnvVar: "GOOGLE_API_KEY", IsDefault: true},
		{Provider: "gemini", ModelName: "gemini-2.0-pro", APIEnvVar: "GOOGLE_API_KEY", IsDefault: false},
		{Provider: "gemini", ModelName: "gemini-1.5-pro", APIEnvVar: "GOOGLE_API_KEY", IsDefault: false},
		{Provider: "gemini", ModelName: "gemini-1.5-flash", APIEnvVar: "GOOGLE_API_KEY", IsDefault: false},

		// OpenAI models
		{Provider: "openai", ModelName: "gpt-4", APIEnvVar: "OPENAI_API_KEY", IsDefault: false},
		{Provider: "openai", ModelName: "gpt-4-turbo", APIEnvVar: "OPENAI_API_KEY", IsDefault: false},
		{Provider: "openai", ModelName: "gpt-3.5-turbo", APIEnvVar: "OPENAI_API_KEY", IsDefault: false},
	}
}

// GetDefaultModel returns the default model configuration.
func GetDefaultModel() ModelInfo {
	models := GetAvailableModels()
	for _, m := range models {
		if m.IsDefault {
			return m
		}
	}
	// Fallback if no default is marked
	return models[0]
}

// GetModelAPIEnvVar returns the API key environment variable name for a model provider.
func GetModelAPIEnvVar(provider, modelName string) string {
	for _, model := range GetAvailableModels() {
		if model.Provider == provider && model.ModelName == modelName {
			return model.APIEnvVar
		}
	}

	// Default fallbacks
	switch provider {
	case "gemini":
		return "GOOGLE_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	default:
		return "UNKNOWN_API_KEY"
	}
}

// createModel creates and configures the appropriate model based on provider and model name.
func createModel(provider, modelName string) (model.Model, error) {
	// Default options shared by all models
	defaultOptions := model.GenerationOptions{
		Temperature:      0.1,
		MaxTokens:        4096,
		TopP:             0.90,
		PresencePenalty:  0.1,
		FrequencyPenalty: 0.1,
		EnableToolCalls:  true,
	}

	switch provider {
	case "gemini":
		// Check for Gemini API key
		geminiAPIKey := os.Getenv("GOOGLE_API_KEY")
		if geminiAPIKey == "" {
			return nil, fmt.Errorf("GOOGLE_API_KEY environment variable is not set")
		}

		// Create options list
		opts := []models.GeminiModelOption{
			models.WithGeminiAPIKey(geminiAPIKey),
			models.WithGeminiDefaultOptions(defaultOptions),
		}

		// Create Gemini model
		geminiModel, err := models.NewGeminiModel(
			modelName,
			opts...,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create Gemini model: %w", err)
		}
		return geminiModel, nil

	case "openai":
		// Check for OpenAI API key
		openaiAPIKey := os.Getenv("OPENAI_API_KEY")
		if openaiAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY environment variable is not set")
		}

		// Create options list
		opts := []models.OpenAIModelOption{
			models.WithOpenAIAPIKey(openaiAPIKey),
			models.WithOpenAIDefaultOptions(defaultOptions),
		}

		// Add custom URL if provided
		openaiBaseURL := os.Getenv("OPENAI_BASE_URL")
		if openaiBaseURL != "" {
			opts = append(opts, models.WithOpenAIBaseURL(openaiBaseURL))
		}

		// Create OpenAI model
		openaiModel := models.NewOpenAIModel(
			modelName,
			opts...,
		)
		return openaiModel, nil

	default:
		return nil, fmt.Errorf("unsupported model provider: %s", provider)
	}
}

func main() {
	// Command line flags
	serverMode := flag.Bool("server", true, "Run in server mode")
	address := flag.String("address", "localhost:8080", "Server address (host:port)")
	mcpAddress := flag.String("mcp-address", "localhost:3000", "MCP server address (host:port)")
	message := flag.String("message", "Hello, A2A agent! Can you translate this to Spanish and calculate the square root of 25?", "Message to send in client mode")
	modelName := flag.String("model-name", "gemini-2.0-flash", "Model name to use")
	modelProvider := flag.String("model-provider", "gemini", "Model provider (gemini, openai)")
	openaiBaseURL := flag.String("openai-url", "https://api.openai.com/v1", "Base URL for OpenAI API")
	useStreaming := flag.Bool("stream", false, "Use streaming API in client mode")
	listModels := flag.Bool("list-models", false, "List all available models and exit")
	debug := flag.Bool("debug", false, "Enable debug logging")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error, fatal)")
	useRunner := flag.Bool("runner", false, "Use direct runner mode instead of A2A server")
	runnerAsync := flag.Bool("runner-async", false, "Use async mode with the runner")
	runnerTimeout := flag.Int("runner-timeout", 30, "Timeout in seconds for the runner")
	multiTurnChat := flag.Bool("chat", false, "Enable multi-turn interactive chat with the agent")
	flag.Parse()

	// Configure logging
	if *debug {
		// Debug flag takes precedence over log-level
		log.SetLevel(log.LevelDebug)
		log.Infof("Debug logging enabled")
	} else {
		// Otherwise use the specified log level
		log.SetLevel(*logLevel)
		log.Infof("Log level set to %s", *logLevel)
	}

	// List models if requested
	if *listModels {
		log.Infof("Available models:")
		log.Infof("-----------------------------")
		log.Infof("%-10s %-30s %-20s %s\n", "PROVIDER", "MODEL", "API ENV VAR", "DEFAULT")
		log.Infof("-----------------------------")

		for _, m := range GetAvailableModels() {
			defaultStr := ""
			if m.IsDefault {
				defaultStr = "✓"
			}
			log.Infof("%-10s %-30s %-20s %s\n", m.Provider, m.ModelName, m.APIEnvVar, defaultStr)
		}
		log.Infof("\nTo use a specific model: --model-provider=<provider> --model-name=<model-name>")
		return
	}

	// Set model selection in environment for the process to access
	os.Setenv("MODEL_PROVIDER", *modelProvider)
	os.Setenv("MODEL_NAME", *modelName)

	// Set OpenAI URL in environment
	os.Setenv("OPENAI_BASE_URL", *openaiBaseURL)

	if *useRunner {
		// Use direct runner mode
		runRunnerExample(*address, *modelProvider, *modelName, *runnerAsync, *runnerTimeout)
	} else if *serverMode {
		log.Infof("Using model provider: %s, model: %s", *modelProvider, *modelName)
		if *modelProvider == "openai" && *openaiBaseURL != "https://api.openai.com/v1" {
			log.Infof("Using custom OpenAI URL: %s", *openaiBaseURL)
		}
		// Start the MCP server in a separate goroutine
		go runMCPServer(*mcpAddress)

		// Run the main A2A server
		runServer(*address)
	} else {
		// Run the client
		agentURL := fmt.Sprintf("http://%s/", *address)

		if *multiTurnChat {
			// Run multi-turn interactive chat client
			runMultiTurnClient(agentURL, *message, *useStreaming)
		} else if *useStreaming {
			// Run single-turn streaming client
			runStreamExample(agentURL, *message)
		} else {
			// Run single-turn synchronous client
			runClient(agentURL, *message)
		}
	}
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

// runRunnerExample demonstrates using the runner package directly without A2A.
func runRunnerExample(address, modelProvider, modelName string, useAsync bool, timeoutSeconds int) {
	log.Infof("Running direct runner example")
	log.Infof("  Model provider: %s", modelProvider)
	log.Infof("  Model name: %s", modelName)
	log.Infof("  Async mode: %t", useAsync)
	log.Infof("  Timeout: %d seconds", timeoutSeconds)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	go func() {
		sig := <-signalCh
		log.Infof("Received signal: %v", sig)
		cancel()
	}()

	// Create agent with tools
	model, err := createModel(modelProvider, modelName)
	if err != nil {
		log.Fatalf("Failed to create model: %v", err)
	}

	// Create tools
	calculatorTool := NewCalculatorTool()
	translatorTool := NewTranslatorTool()
	unitConverterTool := NewUnitConverterTool()
	textAnalysisTool := NewTextAnalysisTool()

	var tools []tool.Tool
	tools = append(tools, calculatorTool, translatorTool, unitConverterTool, textAnalysisTool)

	// Start MCP server in the background
	go func() {
		runMCPServer("localhost:3000")
	}()

	// Create a ReAct agent
	reactAgent, err := react.NewAgent(react.AgentConfig{
		Name:            "RunnerExampleAgent",
		Description:     "A ReAct agent that showcases the runner package",
		Model:           model,
		Tools:           tools,
		MaxIterations:   10,
		EnableStreaming: useAsync,
	})
	if err != nil {
		log.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Configure the runner
	config := runner.Config{
		MaxConcurrent: 5,
		Timeout:       time.Duration(timeoutSeconds) * time.Second,
		RetryCount:    1,
		BufferSize:    10,
	}

	// Create and start the runner
	baseRunner := runner.NewBaseRunner("example-runner", reactAgent, config, nil)
	if err := baseRunner.Start(ctx); err != nil {
		log.Fatalf("Failed to start runner: %v", err)
	}
	defer func() {
		if err := baseRunner.Stop(ctx); err != nil {
			log.Errorf("Failed to stop runner: %v", err)
		}
	}()

	log.Infof("Runner started successfully")

	runRunnerWithA2A(ctx, baseRunner, address, modelProvider, modelName)
}

// RunnerTaskProcessor is a task processor that uses a runner
type RunnerTaskProcessor struct {
	runner *runner.BaseRunner
}

// Process implements the TaskProcessor interface
func (p *RunnerTaskProcessor) Process(
	ctx context.Context,
	taskID string,
	initialMsg protocol.Message,
	handle taskmanager.TaskHandle,
) error {
	// Update task status to working
	if err := handle.UpdateStatus(protocol.TaskStateWorking, nil); err != nil {
		return fmt.Errorf("failed to update task status to working: %w", err)
	}

	// Extract text from message parts
	var text string
	for _, part := range initialMsg.Parts {
		if textPart, ok := part.(protocol.TextPart); ok {
			text = textPart.Text
			break
		}
	}

	log.Infof("Processing task %s with runner: %s", taskID, text)

	// Since the TaskHandle interface doesn't provide a way to get conversation history,
	// we'll implement a simpler approach using message context markers

	// Check if the message contains a conversation marker
	var userMsg *message.Message
	if strings.Contains(text, "Previously in our conversation:") ||
		strings.Contains(text, "Conversation history:") {
		// Message already contains history context
		userMsg = message.NewUserMessage(text)
		log.Infof("Message appears to contain conversation history")
	} else {
		// No history, just use the message directly
		userMsg = message.NewUserMessage(text)
	}

	// Send status update
	statusMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
		protocol.NewTextPart("Processing your request with the runner..."),
	})
	if err := handle.UpdateStatus(protocol.TaskStateWorking, &statusMsg); err != nil {
		log.Infof("Failed to update status: %v", err)
	}

	// Run asynchronously to support streaming
	eventCh, err := p.runner.RunAsync(ctx, *userMsg)
	if err != nil {
		log.Infof("Runner execution failed: %v", err)
		errorMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
			protocol.NewTextPart(fmt.Sprintf("Runner execution failed: %v", err)),
		})
		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errorMsg); err != nil {
			log.Infof("Failed to update task status: %v", err)
		}
		return fmt.Errorf("runner execution failed: %w", err)
	}

	// Create a buffer for the full response
	var responseBuilder strings.Builder

	// Process events
	for event := range eventCh {
		if event.Type == "message" {
			if msg, ok := event.Data.(*message.Message); ok && msg != nil {
				log.Infof("Received message event: %s", msg.Content)

				// Append to the full response
				responseBuilder.WriteString(msg.Content)

				// Send intermediate update for streaming
				updateMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
					protocol.NewTextPart(msg.Content),
				})
				if err := handle.UpdateStatus(protocol.TaskStateWorking, &updateMsg); err != nil {
					log.Infof("Failed to send streaming update: %v", err)
				}
			}
		} else if event.Type == "error" {
			errStr, _ := event.GetMetadata("error")
			log.Infof("Received error event: %v", errStr)

			// Send error to client
			errMsg := fmt.Sprintf("Error during processing: %v", errStr)
			errorMessage := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
				protocol.NewTextPart(errMsg),
			})
			if err := handle.UpdateStatus(protocol.TaskStateWorking, &errorMessage); err != nil {
				log.Infof("Failed to update error status: %v", err)
			}
		}
	}

	// Send the final response
	finalResponse := responseBuilder.String()
	if finalResponse == "" {
		finalResponse = "No response generated by the runner."
	}

	finalMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
		protocol.NewTextPart(finalResponse),
	})

	if err := handle.UpdateStatus(protocol.TaskStateCompleted, &finalMsg); err != nil {
		log.Infof("Failed to send final response: %v", err)
		return fmt.Errorf("failed to complete task: %w", err)
	}

	log.Infof("Successfully completed task %s", taskID)
	return nil
}

// runRunnerWithA2A wraps the runner in an A2A server
func runRunnerWithA2A(ctx context.Context, baseRunner *runner.BaseRunner, address string, modelProvider, modelName string) {
	// Create an agent card with metadata
	desc := fmt.Sprintf("A runner-backed agent using %s %s model with various tools",
		strings.ToUpper(modelProvider), modelName)

	agentCard := server.AgentCard{
		Name:        fmt.Sprintf("Runner Agent (%s %s)", strings.ToUpper(modelProvider), modelName),
		Description: &desc,
		URL:         "http://" + address + "/",
		Version:     "1.0.0",
		Capabilities: server.AgentCapabilities{
			Streaming:              true,
			PushNotifications:      false,
			StateTransitionHistory: true,
		},
		Skills: []server.AgentSkill{
			{
				ID:   "calculator",
				Name: "Math Operations",
				Examples: []string{
					"Calculate the square root of 16",
					"Add 5 and 10",
				},
			},
			{
				ID:   "translator",
				Name: "Text Translation",
				Examples: []string{
					"Translate 'hello world' to Spanish",
					"Translate this text to French",
				},
			},
			{
				ID:   "unit-converter",
				Name: "Unit Conversion",
				Examples: []string{
					"Convert 10 kilometers to miles",
					"Convert 25 celsius to fahrenheit",
				},
			},
			{
				ID:   "text-analysis",
				Name: "Text Analysis",
				Examples: []string{
					"Analyze the word count in this text",
					"Analyze the sentiment of this message",
				},
			},
		},
	}

	// Create task processor that uses our runner
	taskProcessor := &RunnerTaskProcessor{runner: baseRunner}

	// Create an in-memory task manager
	taskManager, err := taskmanager.NewMemoryTaskManager(taskProcessor)
	if err != nil {
		log.Fatalf("Failed to create task manager: %v", err)
	}

	// Create the A2A server
	a2aServer, err := server.NewA2AServer(agentCard, taskManager)
	if err != nil {
		log.Fatalf("Failed to create A2A server: %v", err)
	}

	// Start the server
	log.Infof("Starting Runner A2A server on %s", address)
	log.Infof("You can connect to this server using the A2A client")
	log.Infof("Example: ./a2a_example -server=false -address=%s -message=\"Your message here\"", address)
	log.Infof("Example with streaming: ./a2a_example -server=false -stream -address=%s -message=\"Your message here\"", address)

	// Start the server in a separate goroutine
	go func() {
		if err := a2aServer.Start(address); err != nil {
			log.Fatalf("Failed to start A2A server: %v", err)
		}
	}()

	// Wait for context to be done (e.g., SIGINT)
	<-ctx.Done()
	log.Infof("Shutting down Runner A2A server...")

	// Create a context with timeout for graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// Stop the A2A server
	if err := a2aServer.Stop(shutdownCtx); err != nil {
		log.Infof("Error shutting down A2A server: %v", err)
	}
}

// runMultiTurnClient runs an interactive chat session with the agent
func runMultiTurnClient(agentURL string, initialMessage string, useStreaming bool) {
	// Create a new A2A client
	a2aClient, err := client.NewA2AClient(agentURL)
	if err != nil {
		log.Fatalf("Failed to create A2A client: %v", err)
	}

	// Maintain conversation history
	var conversationHistory []string

	// Add initial user message if provided
	if initialMessage != "" {
		conversationHistory = append(conversationHistory, fmt.Sprintf("User: %s", initialMessage))
	}

	// Interactive chat loop
	fmt.Println("\n=== Multi-turn Chat with A2A Agent ===")
	fmt.Printf("Connected to: %s\n", agentURL)
	fmt.Println("Type 'exit' or 'quit' to end the conversation.")

	// Input reader
	reader := bufio.NewReader(os.Stdin)

	for {
		// If we have no messages yet, prompt for the first one
		if len(conversationHistory) == 0 {
			fmt.Print("\nYou: ")
			input, err := reader.ReadString('\n')
			if err != nil {
				log.Fatalf("Error reading input: %v", err)
			}

			// Trim whitespace
			input = strings.TrimSpace(input)

			// Check for exit commands
			if strings.ToLower(input) == "exit" || strings.ToLower(input) == "quit" {
				fmt.Println("Goodbye!")
				return
			}

			// Add user message to history
			conversationHistory = append(conversationHistory, fmt.Sprintf("User: %s", input))
		}

		// Get the latest user input (should be at the end of the history)
		latestUserInput := ""
		for i := len(conversationHistory) - 1; i >= 0; i-- {
			if strings.HasPrefix(conversationHistory[i], "User: ") {
				latestUserInput = strings.TrimPrefix(conversationHistory[i], "User: ")
				break
			}
		}

		// Create the message with conversation history included in the text
		var messageText string
		if len(conversationHistory) > 1 {
			messageText = "Conversation history:\n"
			// Add all but the latest message to the history
			for i := 0; i < len(conversationHistory)-1; i++ {
				messageText += conversationHistory[i] + "\n"
			}
			messageText += "\nCurrent message: " + latestUserInput
		} else {
			// First message, no history
			messageText = latestUserInput
		}

		// Create the message for the A2A protocol
		msg := protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{
			protocol.NewTextPart(messageText),
		})

		// Create task parameters
		taskParams := protocol.SendTaskParams{
			ID:      fmt.Sprintf("chat-task-%d", time.Now().UnixNano()),
			Message: msg,
		}

		// Process the user message - either streaming or regular
		var agentResponse string

		if useStreaming {
			agentResponse = handleStreamingChatWithHistory(a2aClient, taskParams)
		} else {
			agentResponse = handleSyncChatWithHistory(a2aClient, taskParams)
		}

		// Add agent response to conversation history
		if agentResponse != "" {
			conversationHistory = append(conversationHistory, fmt.Sprintf("Agent: %s", agentResponse))
		}

		// Prompt for next message
		fmt.Print("\nYou: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatalf("Error reading input: %v", err)
		}

		// Trim whitespace
		input = strings.TrimSpace(input)

		// Check for exit commands
		if strings.ToLower(input) == "exit" || strings.ToLower(input) == "quit" {
			fmt.Println("Goodbye!")
			return
		}

		// Add new user message to history
		conversationHistory = append(conversationHistory, fmt.Sprintf("User: %s", input))
	}
}

// handleStreamingChatWithHistory processes a chat message with streaming, returns the agent's response as string
func handleStreamingChatWithHistory(a2aClient *client.A2AClient, taskParams protocol.SendTaskParams) string {
	// Send the streaming request
	eventsChan, err := a2aClient.StreamTask(context.Background(), taskParams)
	if err != nil {
		log.Fatalf("Failed to send streaming task: %v", err)
	}

	// Variables to track streaming state
	var responseBuilder strings.Builder
	fmt.Print("\nAgent: ")

	// Process events as they arrive
	for event := range eventsChan {
		switch evt := event.(type) {
		case protocol.TaskStatusUpdateEvent:
			if evt.Status.Message != nil {
				for _, part := range evt.Status.Message.Parts {
					if textPart, ok := part.(protocol.TextPart); ok {
						// Only print and collect new content (to avoid duplicates in streaming)
						responseText := textPart.Text

						// Print each chunk as it arrives
						fmt.Print(responseText)

						// Add to the total response
						responseBuilder.WriteString(responseText)
					}
				}
			}
		case protocol.TaskArtifactUpdateEvent:
			// If there are artifacts, we'll just note this in the console for now
			fmt.Print("\n[Received artifact: " + *evt.Artifact.Name + "]")
		}
	}

	// Return the final response
	return responseBuilder.String()
}

// handleSyncChatWithHistory processes a chat message synchronously, returns the agent's response as string
func handleSyncChatWithHistory(a2aClient *client.A2AClient, taskParams protocol.SendTaskParams) string {
	// Send the task to the agent
	task, err := a2aClient.SendTasks(context.Background(), taskParams)
	if err != nil {
		log.Fatalf("Failed to send task: %v", err)
	}

	fmt.Print("\nAgent: ")

	// Poll for updates until the task is completed or failed
	for task.Status.State != protocol.TaskStateCompleted &&
		task.Status.State != protocol.TaskStateFailed &&
		task.Status.State != protocol.TaskStateCanceled {

		time.Sleep(500 * time.Millisecond)

		// Get the current task state
		taskQuery := protocol.TaskQueryParams{
			ID: task.ID,
		}

		task, err = a2aClient.GetTasks(context.Background(), taskQuery)
		if err != nil {
			log.Fatalf("Failed to get task status: %v", err)
		}
	}

	// Print and collect the agent's response
	var responseBuilder strings.Builder

	if task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			if textPart, ok := part.(protocol.TextPart); ok {
				// Print to console
				fmt.Print(textPart.Text)

				// Add to response builder
				responseBuilder.WriteString(textPart.Text)
			}
		}
	}

	return responseBuilder.String()
}
