package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
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

	// Add imports for the ReAct agent
	"trpc.group/trpc-go/trpc-agent-go/agent/agents/react"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcptools "trpc.group/trpc-go/trpc-agent-go/tool/tools"

	// Add imports for MCP
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
	log.Printf("Calculator tool called with args: %v", args)

	// Extract the operation
	opInterface, ok := args["operation"]
	if !ok {
		log.Printf("Calculator tool error: operation is required")
		return nil, fmt.Errorf("operation is required")
	}
	operation, ok := opInterface.(string)
	if !ok {
		log.Printf("Calculator tool error: operation must be a string")
		return nil, fmt.Errorf("operation must be a string")
	}

	// Extract the first operand
	aInterface, ok := args["a"]
	if !ok {
		log.Printf("Calculator tool error: a is required")
		return nil, fmt.Errorf("a is required")
	}
	var a float64
	switch v := aInterface.(type) {
	case float64:
		a = v
	case int:
		a = float64(v)
	default:
		log.Printf("Calculator tool error: a must be a number, got %T", aInterface)
		return nil, fmt.Errorf("a must be a number")
	}

	var result float64

	switch operation {
	case "sqrt":
		result = float64(int(100*math.Sqrt(a))) / 100 // Simple square root with 2 decimal places
		log.Printf("Calculator tool: sqrt(%f) = %f", a, result)
	case "add":
		// Extract second operand
		bInterface, ok := args["b"]
		if !ok {
			log.Printf("Calculator tool error: b is required for addition")
			return nil, fmt.Errorf("b is required for addition")
		}
		var b float64
		switch v := bInterface.(type) {
		case float64:
			b = v
		case int:
			b = float64(v)
		default:
			log.Printf("Calculator tool error: b must be a number, got %T", bInterface)
			return nil, fmt.Errorf("b must be a number")
		}
		result = a + b
		log.Printf("Calculator tool: %f + %f = %f", a, b, result)
	// Simplified implementation - just showing add and sqrt
	default:
		log.Printf("Calculator tool error: unsupported operation: %s", operation)
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	// Log result
	log.Printf("Calculator tool returning result: %f", result)
	return tool.NewResult(result), nil
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
	log.Printf("Translator tool called with args: %v", args)

	// Extract the text
	text, ok := args["text"].(string)
	if !ok {
		log.Printf("Translator tool error: text is required")
		return nil, fmt.Errorf("text is required")
	}

	// Extract the target language
	targetLang, ok := args["target_language"].(string)
	if !ok {
		log.Printf("Translator tool error: target_language is required")
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
	log.Printf("Translator tool returning result: %v", result)
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
	log.Printf("Unit converter tool called with args: %v", args)

	// Extract the value
	valueInterface, ok := args["value"]
	if !ok {
		log.Printf("Unit converter tool error: value is required")
		return nil, fmt.Errorf("value is required")
	}

	var value float64
	switch v := valueInterface.(type) {
	case float64:
		value = v
	case int:
		value = float64(v)
	default:
		log.Printf("Unit converter tool error: value must be a number")
		return nil, fmt.Errorf("value must be a number")
	}

	// Extract units
	fromUnit, ok := args["from_unit"].(string)
	if !ok {
		log.Printf("Unit converter tool error: from_unit is required")
		return nil, fmt.Errorf("from_unit is required")
	}

	toUnit, ok := args["to_unit"].(string)
	if !ok {
		log.Printf("Unit converter tool error: to_unit is required")
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
		log.Printf("Unit converter tool error: unsupported conversion: %s to %s", fromUnit, toUnit)
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
	log.Printf("Unit converter tool returning result: %v", conversionResult)
	return tool.NewJSONResult(conversionResult), nil
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
	log.Printf("Text Analysis tool called with args: %v", args)

	// Extract the text
	textInterface, ok := args["text"]
	if !ok {
		log.Printf("Text Analysis tool error: text is required")
		return nil, fmt.Errorf("text is required")
	}
	text, ok := textInterface.(string)
	if !ok {
		log.Printf("Text Analysis tool error: text must be a string")
		return nil, fmt.Errorf("text must be a string")
	}

	if text == "" {
		log.Printf("Text Analysis tool error: text cannot be empty")
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Extract the analysis type
	analysisTypeInterface, ok := args["analysis_type"]
	if !ok {
		log.Printf("Text Analysis tool error: analysis_type is required")
		return nil, fmt.Errorf("analysis_type is required")
	}
	analysisType, ok := analysisTypeInterface.(string)
	if !ok {
		log.Printf("Text Analysis tool error: analysis_type must be a string")
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
		log.Printf("Text Analysis tool: word count result for text: %v", result)

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
		log.Printf("Text Analysis tool: sentiment analysis result: %v", result)

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
		log.Printf("Text Analysis tool: summary result (length %d → %d)", len(text), len(summary))

	default:
		log.Printf("Text Analysis tool error: unsupported analysis type: %s", analysisType)
		return nil, fmt.Errorf("unsupported analysis type: %s", analysisType)
	}

	// Log result
	log.Printf("Text Analysis tool returning result: %v", result)
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
	log.Printf("Starting to process task %s with input: %s", taskID, text)

	// Check for API key (necessary for Gemini)
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		errorMsg := "GOOGLE_API_KEY environment variable is not set"
		log.Println("Error:", errorMsg)

		// Update task status to failed with error message
		errMsgParts := []protocol.Part{
			protocol.NewTextPart("Failed to process task: Google API key is missing. Please set GOOGLE_API_KEY environment variable."),
		}
		errResponse := protocol.NewMessage(protocol.MessageRoleAgent, errMsgParts)

		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errResponse); err != nil {
			return fmt.Errorf("failed to update task status to failed: %w", err)
		}
		return fmt.Errorf(errorMsg)
	}

	// Create Gemini model
	geminiModel, err := models.NewGeminiModel(
		"gemini-2.0-flash",
		models.WithGeminiAPIKey(apiKey),
		models.WithGeminiDefaultOptions(model.GenerationOptions{
			Temperature:      0.1,
			MaxTokens:        4096,
			TopP:             0.90,
			TopK:             32,
			PresencePenalty:  0.1,
			FrequencyPenalty: 0.1,
			EnableToolCalls:  true,
		}),
	)
	if err != nil {
		log.Printf("Failed to create Gemini model: %v", err)
		errorMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
			protocol.NewTextPart(fmt.Sprintf("Failed to create Gemini model: %v", err)),
		})
		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errorMsg); err != nil {
			log.Printf("Failed to update task status: %v", err)
		}
		return fmt.Errorf("failed to create Gemini model: %w", err)
	}

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
		log.Printf("Warning: failed to get MCP tools: %v", err)
		// Continue without MCP tools
		mcpTools = []tool.Tool{}
	} else {
		log.Printf("Retrieved %d MCP tools from toolset", len(mcpTools))
		for _, t := range mcpTools {
			log.Printf("MCP Tool: %s - %s", t.Name(), t.Description())
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
				log.Printf("Warning: failed to close MCP toolset: %v", err)
			} else {
				log.Printf("Successfully closed MCP toolset")
			}
		}
	}()

	// Log the tools for debugging
	log.Printf("Using %d tools in total", len(allTools))
	for _, t := range allTools {
		log.Printf("Registered tool: %s - %s", t.Name(), t.Description())
		params, err := json.Marshal(t.Parameters())
		if err != nil {
			log.Printf("Error marshaling parameters for tool %s: %v", t.Name(), err)
		} else {
			log.Printf("Tool %s parameters: %s", t.Name(), string(params))
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
	if err := geminiModel.SetTools(toolDefs); err != nil {
		log.Printf("Warning: failed to set tools on model: %v", err)
		// Continue despite this warning
	}

	// Create ReAct agent components
	thoughtGenerator := react.NewLLMThoughtGenerator(
		geminiModel,
		react.NewDefaultThoughtPromptStrategy(allTools),
	)

	actionSelector := react.NewLLMActionSelector(
		geminiModel,
		react.NewDefaultActionPromptStrategy(),
	)

	responseGenerator := react.NewLLMResponseGenerator(
		geminiModel,
		react.NewDefaultResponsePromptStrategy(true),
	)

	cycleManager := react.NewInMemoryCycleManager()

	// Create the ReAct agent
	reactAgent, err := react.NewReActAgent(react.ReActAgentConfig{
		Name:              "GeminiReActAgent",
		Description:       "A ReAct agent powered by Google's Gemini model with various tools",
		Model:             geminiModel,
		Tools:             allTools,
		ThoughtGenerator:  thoughtGenerator,
		ActionSelector:    actionSelector,
		ResponseGenerator: responseGenerator,
		CycleManager:      cycleManager,
		MaxIterations:     10,
		ThoughtFormat:     react.FormatMarkdown,
		EnableStreaming:   false,
	})
	if err != nil {
		log.Printf("Failed to create ReAct agent: %v", err)
		errorMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
			protocol.NewTextPart(fmt.Sprintf("Failed to create ReAct agent: %v", err)),
		})
		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errorMsg); err != nil {
			log.Printf("Failed to update task status: %v", err)
		}
		return fmt.Errorf("failed to create ReAct agent: %w", err)
	}

	// Add debug log for agent creation
	log.Printf("ReAct agent created successfully with model %s and %d tools",
		geminiModel.Name(), len(allTools))

	// Status update - agent created and ready
	stageMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
		protocol.NewTextPart("Agent configured and processing your request..."),
	})
	if err := handle.UpdateStatus(protocol.TaskStateWorking, &stageMsg); err != nil {
		log.Printf("Failed to update status: %v", err)
	}

	// Create a user message for the ReAct agent
	userMsg := message.NewUserMessage(text)

	// Run the ReAct agent with the user message
	log.Printf("Running ReAct agent with user message: %s", text)
	response, err := reactAgent.Run(ctx, userMsg)
	if err != nil {
		log.Printf("Agent execution failed: %v", err)
		errorMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
			protocol.NewTextPart(fmt.Sprintf("Agent execution failed: %v", err)),
		})
		if err := handle.UpdateStatus(protocol.TaskStateFailed, &errorMsg); err != nil {
			log.Printf("Failed to update task status: %v", err)
		}
		return fmt.Errorf("agent execution failed: %w", err)
	}

	// Log agent response for debugging
	log.Printf("Agent response: %s", response.Content)

	// Add agent response
	finalResponseMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
		protocol.NewTextPart(response.Content),
	})

	// Get the thought process history
	cycles, err := reactAgent.GetHistory(ctx)
	if err != nil {
		log.Printf("Failed to get agent history: %v", err)
	} else {
		log.Printf("Retrieved %d cycles from agent history", len(cycles))
		for i, cycle := range cycles {
			log.Printf("Cycle %d: Thought: %s", i+1, cycle.Thought.Content)
			if cycle.Action != nil {
				log.Printf("Cycle %d: Action: %s, Parameters: %v", i+1, cycle.Action.ToolName, cycle.Action.ToolInput)
			}
			if cycle.Observation != nil {
				log.Printf("Cycle %d: Observation: Error=%v, Output=%v", i+1, cycle.Observation.IsError, cycle.Observation.ToolOutput)
			}
		}
	}

	// Add thought process as an artifact
	if len(cycles) > 0 {
		thoughtText := "Agent Thought Process:\n\n"

		for i, cycle := range cycles {
			thoughtText += fmt.Sprintf("--- Cycle %d ---\n", i+1)
			thoughtText += fmt.Sprintf("Thought: %s\n\n", cycle.Thought.Content)

			if cycle.Action != nil {
				thoughtText += fmt.Sprintf("Action: %s\n", cycle.Action.ToolName)
				thoughtText += fmt.Sprintf("Parameters: %v\n\n", cycle.Action.ToolInput)
			}

			if cycle.Observation != nil {
				thoughtText += "Observation: "
				if cycle.Observation.IsError {
					thoughtText += fmt.Sprintf("Error: %v\n", cycle.Observation.ToolOutput)
				} else if output, ok := cycle.Observation.ToolOutput["output"]; ok {
					thoughtText += fmt.Sprintf("%v\n", output)
				} else {
					thoughtText += fmt.Sprintf("%v\n", cycle.Observation.ToolOutput)
				}
			}
			thoughtText += "----------------\n\n"
		}

		// Add the thought process as an artifact
		thoughtArtifact := protocol.Artifact{
			Name:        stringPtr("agent-thought-process"),
			Description: stringPtr("Detailed record of the agent's reasoning process"),
			Parts: []protocol.Part{
				protocol.NewTextPart(thoughtText),
			},
			Index: 0,
		}
		if err := handle.AddArtifact(thoughtArtifact); err != nil {
			log.Printf("Failed to add thought artifact: %v", err)
		}
	}

	// Complete the task with the response message
	if err := handle.UpdateStatus(protocol.TaskStateCompleted, &finalResponseMsg); err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}

	log.Printf("Successfully completed task %s", taskID)
	return nil
}

// Helper function to get a string pointer
func stringPtr(s string) *string {
	return &s
}

func runServer(address string) {
	// Create an agent card with metadata about the agent
	desc := "An agent implementing the A2A protocol with various local tools and remote MCP tools"
	docURL := "https://github.com/yourusername/trpc-a2a-go/docs"

	agentCard := server.AgentCard{
		Name:             "A2A Example Agent with Multiple Tools and MCP Integration",
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
		log.Printf("Starting A2A server on %s", address)
		if err := a2aServer.Start(address); err != nil {
			log.Fatalf("Failed to start A2A server: %v", err)
		}
	}()

	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	log.Println("Shutting down servers...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop the A2A server
	if err := a2aServer.Stop(ctx); err != nil {
		log.Printf("Error shutting down A2A server: %v", err)
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
	fmt.Println("Sending task to agent...")
	task, err := a2aClient.SendTasks(context.Background(), taskParams)
	if err != nil {
		log.Fatalf("Failed to send task: %v", err)
	}

	// Print the initial task state
	fmt.Printf("Task ID: %s\n", task.ID)
	fmt.Printf("Initial state: %s\n", task.Status.State)

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

		fmt.Printf("Task state: %s\n", task.Status.State)

		// Display any message
		if task.Status.Message != nil {
			for _, part := range task.Status.Message.Parts {
				if textPart, ok := part.(protocol.TextPart); ok {
					fmt.Printf("  > %s\n", textPart.Text)
				}
			}
		}
	}

	// Print the final result
	fmt.Println("\nFinal Result:")
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
		fmt.Println("\nArtifacts:")
		for i, artifact := range task.Artifacts {
			fmt.Printf("Artifact %d: %s\n", i+1, *artifact.Name)
			fmt.Printf("Description: %s\n", *artifact.Description)
			for _, part := range artifact.Parts {
				if textPart, ok := part.(protocol.TextPart); ok {
					fmt.Printf("  %s\n", textPart.Text)
				}
			}
			fmt.Println()
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
	fmt.Println("Sending stream task to agent...")
	eventsChan, err := a2aClient.StreamTask(context.Background(), taskParams)
	if err != nil {
		log.Fatalf("Failed to send streaming task: %v", err)
	}

	// Process events as they arrive
	fmt.Println("Receiving events:")
	for event := range eventsChan {
		switch evt := event.(type) {
		case protocol.TaskStatusUpdateEvent:
			fmt.Printf("Status update: %s\n", evt.Status.State)
			if evt.Status.Message != nil {
				for _, part := range evt.Status.Message.Parts {
					if textPart, ok := part.(protocol.TextPart); ok {
						fmt.Printf("  > %s\n", textPart.Text)
					}
				}
			}
		case protocol.TaskArtifactUpdateEvent:
			fmt.Printf("\nArtifact update: %s\n", *evt.Artifact.Name)
			fmt.Printf("Description: %s\n", *evt.Artifact.Description)
			for _, part := range evt.Artifact.Parts {
				if textPart, ok := part.(protocol.TextPart); ok {
					fmt.Printf("%s\n", textPart.Text)
				}
			}
		}

		// Check if this is the final event
		if event.IsFinal() {
			fmt.Println("\nReceived final event")
			break
		}
	}
	fmt.Println("Stream completed")
}

func main() {
	// Parse command-line flags
	serverMode := flag.Bool("server", true, "Run in server mode")
	streamMode := flag.Bool("stream", false, "Use streaming API in client mode")
	address := flag.String("address", "localhost:8080", "Server address (host:port)")
	message := flag.String("message", "Hello, A2A agent! Can you translate this to Spanish and calculate the square root of 25?", "Message to send in client mode")
	mcpAddress := flag.String("mcp-address", "localhost:3000", "MCP server address (host:port)")

	flag.Parse()

	if *serverMode {
		// Start the MCP server in a separate goroutine
		go runMCPServer(*mcpAddress)

		// Run the main A2A server
		runServer(*address)
	} else {
		// Run the client
		agentURL := fmt.Sprintf("http://%s/", *address)
		if *streamMode {
			runStreamExample(agentURL, *message)
		} else {
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
		mcp.WithDescription("Gets detailed weather information for a specified location. Just provide the city name as input - no coordinates needed."),
		mcp.WithString("location",
			mcp.Description("The city or location name to get weather information for (e.g., 'Tokyo', 'New York', 'London')"),
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
	log.Printf("Starting MCP server on %s", address)
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
