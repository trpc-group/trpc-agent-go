// Package models provides implementations of the model interface.
package models

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultGeminiTimeout = 120
	defaultGeminiModel   = "gemini-2.0-flash"
)

// GeminiModel implements the Model interface for Google's Gemini API.
type GeminiModel struct {
	name           string
	apiKey         string
	defaultOptions model.GenerationOptions
	tools          []model.ToolDefinition
	client         *genai.Client
	model          *genai.GenerativeModel
}

// GeminiModelOption is a function that configures a GeminiModel.
type GeminiModelOption func(*GeminiModel)

// WithGeminiAPIKey sets the API key for the Gemini model.
func WithGeminiAPIKey(apiKey string) GeminiModelOption {
	return func(m *GeminiModel) {
		m.apiKey = apiKey
	}
}

// WithGeminiDefaultOptions sets the default generation options for the Gemini model.
func WithGeminiDefaultOptions(options model.GenerationOptions) GeminiModelOption {
	return func(m *GeminiModel) {
		m.defaultOptions = options
	}
}

// NewGeminiModel creates a new GeminiModel with the given options.
func NewGeminiModel(name string, opts ...GeminiModelOption) (*GeminiModel, error) {
	m := &GeminiModel{
		name:           name,
		defaultOptions: model.DefaultOptions(),
	}

	for _, opt := range opts {
		opt(m)
	}

	// If no API key was provided via options, check environment variable
	if m.apiKey == "" {
		m.apiKey = os.Getenv("GOOGLE_API_KEY")
		if m.apiKey == "" {
			return nil, fmt.Errorf("Gemini API key is required (set via options or GOOGLE_API_KEY environment variable)")
		}
	}

	// Initialize the Gemini client
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(m.apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	m.client = client

	// Use the provided model name or default to gemini-pro
	modelName := defaultGeminiModel
	if m.name != "" {
		modelName = m.name
	}
	m.model = client.GenerativeModel(modelName)

	return m, nil
}

// Name returns the name of the model.
func (m *GeminiModel) Name() string {
	return m.name
}

// Provider returns the provider of the model.
func (m *GeminiModel) Provider() string {
	return "google"
}

// Generate generates a completion for the given prompt.
func (m *GeminiModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	mergedOptions := m.mergeOptions(options)

	// Apply generation parameters
	applyGenerationParams(m.model, mergedOptions)

	// Create a text-only prompt
	resp, err := m.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini API request failed: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini API")
	}

	// Extract the response text
	responseText := ""
	for _, part := range resp.Candidates[0].Content.Parts {
		if textPart, ok := part.(genai.Text); ok {
			responseText += string(textPart)
		}
	}

	// Get the finish reason
	finishReason := "stop"
	if resp.Candidates[0].FinishReason != genai.FinishReasonStop {
		finishReason = string(resp.Candidates[0].FinishReason)
	}

	// Create usage stats (Gemini doesn't provide token counts, so these are estimates)
	promptTokens := len(strings.Split(prompt, " "))
	completionTokens := len(strings.Split(responseText, " "))

	return &model.Response{
		Text: responseText,
		Usage: &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
		FinishReason: finishReason,
	}, nil
}

// GenerateWithMessages generates a completion for the given messages.
func (m *GeminiModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	mergedOptions := m.mergeOptions(options)

	// Apply generation parameters
	applyGenerationParams(m.model, mergedOptions)

	// Create a chat session
	cs := m.model.StartChat()

	// Extract system messages
	var systemInstructions string
	var nonSystemMessages []*message.Message

	// Separate system messages from other messages
	for _, msg := range messages {
		if msg.Role == message.RoleSystem {
			// Accumulate system message content
			if systemInstructions != "" {
				systemInstructions += "\n"
			}
			systemInstructions += msg.Content
		} else {
			nonSystemMessages = append(nonSystemMessages, msg)
		}
	}

	// If we have system instructions but no user messages, create a minimal user message
	if systemInstructions != "" && len(nonSystemMessages) == 0 {
		nonSystemMessages = append(nonSystemMessages, message.NewUserMessage("Hello"))
	}

	// Process non-system messages
	for i, msg := range nonSystemMessages {
		var role string
		switch msg.Role {
		case message.RoleUser:
			role = "user"
		case message.RoleAssistant:
			role = "model"
		default:
			role = "user"
		}

		// For the first user message, prepend system instructions if any
		content := msg.Content
		if i == 0 && msg.Role == message.RoleUser && systemInstructions != "" {
			content = "System instructions: " + systemInstructions + "\n\nUser: " + content
		}

		// Create the content object
		contentObj := &genai.Content{
			Role:  role,
			Parts: []genai.Part{genai.Text(content)},
		}

		// Add any parts if they exist
		for _, part := range msg.Parts {
			switch part.Type {
			case message.ContentTypeText:
				contentObj.Parts = append(contentObj.Parts, genai.Text(part.Text))
			}
		}

		// Add to history
		cs.History = append(cs.History, contentObj)
	}

	// Enable tools if requested
	if mergedOptions.EnableToolCalls && len(m.tools) > 0 {
		funcs := make([]*genai.FunctionDeclaration, 0, len(m.tools))
		for _, tool := range m.tools {
			// Convert tool parameters to properly formatted schema for Gemini
			schema := convertParametersToGeminiSchema(tool.Parameters)

			funcs = append(funcs, &genai.FunctionDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
			})
		}

		m.model.Tools = []*genai.Tool{
			{
				FunctionDeclarations: funcs,
			},
		}
	} else {
		m.model.Tools = nil
	}

	// Send the last message to generate a response
	var prompt genai.Text
	if len(nonSystemMessages) > 0 {
		prompt = genai.Text(nonSystemMessages[len(nonSystemMessages)-1].Content)
	} else {
		prompt = genai.Text("Hello")
	}

	// Generate response
	resp, err := cs.SendMessage(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("Gemini API request failed: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini API")
	}

	// Extract the response text
	responseText := ""
	for _, part := range resp.Candidates[0].Content.Parts {
		if textPart, ok := part.(genai.Text); ok {
			responseText += string(textPart)
		}
	}

	// Create the response message
	responseMessage := message.NewAssistantMessage(responseText)

	// Process function calls from Gemini response
	var toolCalls []model.ToolCall
	for _, part := range resp.Candidates[0].Content.Parts {
		if fc, ok := part.(*genai.FunctionCall); ok {
			// Convert the args map to a JSON string
			argsJSON, err := json.Marshal(fc.Args)
			if err != nil {
				fmt.Printf("Warning: failed to marshal function arguments: %v\n", err)
				argsJSON = []byte("{}")
			}

			// Create a structured tool call
			toolCall := model.ToolCall{
				ID:   fmt.Sprintf("call_%d", time.Now().UnixNano()),
				Type: "function",
				Function: model.FunctionCall{
					Name:      fc.Name,
					Arguments: string(argsJSON),
				},
			}
			toolCalls = append(toolCalls, toolCall)

			// Also store for backward compatibility
			toolCallMeta := map[string]interface{}{
				"name":      fc.Name,
				"arguments": fc.Args,
			}
			responseMessage.SetMetadata("tool_calls", []map[string]interface{}{toolCallMeta})
		}
	}

	// Get the finish reason
	finishReason := "stop"
	if resp.Candidates[0].FinishReason != genai.FinishReasonStop {
		finishReason = string(resp.Candidates[0].FinishReason)
	}

	// Create usage stats (Gemini doesn't provide token counts, so these are estimates)
	promptTokens := 0
	for _, msg := range messages {
		promptTokens += len(strings.Split(msg.Content, " "))
	}
	completionTokens := len(strings.Split(responseText, " "))

	return &model.Response{
		Text: responseText,
		Messages: []*message.Message{
			responseMessage,
		},
		ToolCalls: toolCalls,
		Usage: &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
		FinishReason: finishReason,
	}, nil
}

// convertParametersToGeminiSchema converts JSON Schema parameters to Gemini-compatible schema.
// It properly handles enum fields to ensure they're only used with string types.
func convertParametersToGeminiSchema(parameters map[string]interface{}) *genai.Schema {
	// Create a base schema object
	schema := &genai.Schema{
		Type: genai.TypeObject,
	}

	// Marshal the original parameters to JSON
	paramsJSON, err := json.Marshal(parameters)
	if err != nil {
		// If marshaling fails, return a basic schema
		return schema
	}

	// Unmarshal into a map for processing
	var paramsMap map[string]interface{}
	if err := json.Unmarshal(paramsJSON, &paramsMap); err != nil {
		return schema
	}

	// Process properties to ensure they're Gemini-compatible
	if properties, ok := paramsMap["properties"].(map[string]interface{}); ok {
		// Iterate through each property
		for propName, propValue := range properties {
			if propMap, ok := propValue.(map[string]interface{}); ok {
				// Check if the property has an enum field
				if enum, hasEnum := propMap["enum"]; hasEnum {
					// Check if the property's type is not a string
					propType, hasType := propMap["type"]
					if !hasType || propType != "string" {
						// Remove enum for non-string types to avoid Gemini API errors
						delete(propMap, "enum")
						properties[propName] = propMap
					} else if enumArray, ok := enum.([]interface{}); ok {
						// For string types, ensure all enum values are strings
						stringEnums := make([]string, 0, len(enumArray))
						for _, val := range enumArray {
							if strVal, ok := val.(string); ok {
								stringEnums = append(stringEnums, strVal)
							} else {
								// Convert non-string values to strings
								stringEnums = append(stringEnums, fmt.Sprintf("%v", val))
							}
						}
						propMap["enum"] = stringEnums
						properties[propName] = propMap
					}
				}
			}
		}
		paramsMap["properties"] = properties
	}

	// Re-marshal the modified parameters
	cleanJSON, err := json.Marshal(paramsMap)
	if err != nil {
		return schema
	}

	// Unmarshal into the genai.Schema
	if err := json.Unmarshal(cleanJSON, schema); err != nil {
		return &genai.Schema{Type: genai.TypeObject}
	}

	return schema
}

// SetTools implements the ToolCallSupportingModel interface.
func (m *GeminiModel) SetTools(tools []model.ToolDefinition) error {
	m.tools = tools
	return nil
}

// applyGenerationParams applies the generation parameters to the model.
func applyGenerationParams(model *genai.GenerativeModel, options model.GenerationOptions) {
	// Set temperature
	temp := float32(options.Temperature)
	model.Temperature = &temp

	// Set max output tokens
	if options.MaxTokens > 0 {
		maxTokens := int32(options.MaxTokens)
		model.MaxOutputTokens = &maxTokens
	}

	// Set top-p sampling
	if options.TopP > 0 {
		topP := float32(options.TopP)
		model.TopP = &topP
	}

	// Set top-k sampling
	if options.TopK > 0 {
		topK := int32(options.TopK)
		model.TopK = &topK
	}

	// Note: Gemini doesn't directly support presence penalty or frequency penalty
	// We can't set StopSequences directly as the Gemini API has a different approach
}

// mergeOptions merges the provided options with the default options.
func (m *GeminiModel) mergeOptions(options model.GenerationOptions) model.GenerationOptions {
	result := m.defaultOptions

	// Only override non-zero values
	if options.Temperature != 0 {
		result.Temperature = options.Temperature
	}
	if options.MaxTokens != 0 {
		result.MaxTokens = options.MaxTokens
	}
	if options.TopP != 0 {
		result.TopP = options.TopP
	}
	if options.TopK != 0 {
		result.TopK = options.TopK
	}
	if options.PresencePenalty != 0 {
		result.PresencePenalty = options.PresencePenalty
	}
	if options.FrequencyPenalty != 0 {
		result.FrequencyPenalty = options.FrequencyPenalty
	}
	if len(options.StopSequences) > 0 {
		result.StopSequences = options.StopSequences
	}
	if options.EnableToolCalls {
		result.EnableToolCalls = true
	}

	return result
}
