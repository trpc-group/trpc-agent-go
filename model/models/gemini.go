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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultGeminiTimeout = 120
	defaultGeminiModel   = "gemini-2.0-flash"
)

// GeminiModel implements the Model interface for Google's Gemini API.
type GeminiModel struct {
	*model.BaseModel
	apiKey   string
	client   *genai.Client
	genModel *genai.GenerativeModel
	tools    []*tool.ToolDefinition
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
		// Use the MergeOptions method instead of direct field access
		m.BaseModel = model.NewBaseModel(m.BaseModel.Name(), m.BaseModel.Provider(), options)
	}
}

// NewGeminiModel creates a new GeminiModel with the given options.
func NewGeminiModel(name string, opts ...GeminiModelOption) (*GeminiModel, error) {
	m := &GeminiModel{
		BaseModel: model.NewBaseModel(name, "google", model.DefaultOptions()),
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
	if name != "" {
		modelName = name
	}
	m.genModel = client.GenerativeModel(modelName)

	// Set tool call support flag
	m.SetSupportsToolCalls(true)

	return m, nil
}

// Name returns the name of the model.
func (m *GeminiModel) Name() string {
	return m.BaseModel.Name()
}

// Provider returns the provider of the model.
func (m *GeminiModel) Provider() string {
	return "google"
}

// Generate generates a completion for the given prompt.
func (m *GeminiModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	mergedOptions := m.MergeOptions(options)

	// Apply generation parameters
	applyGenerationParams(m.genModel, mergedOptions)

	// Create a text-only prompt
	resp, err := m.genModel.GenerateContent(ctx, genai.Text(prompt))
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

	// Check for function calls
	var toolCalls []model.ToolCall
	if resp.Candidates[0].Content.Parts != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if funcPart, ok := part.(*genai.FunctionCall); ok {
				// Convert the arguments map to a JSON string
				argsJSON, err := json.Marshal(funcPart.Args)
				if err != nil {
					argsJSON = []byte("{}")
				}

				toolCall := model.ToolCall{
					ID:   fmt.Sprintf("call_%d", time.Now().UnixNano()),
					Type: "function",
					Function: model.FunctionCall{
						Name:      funcPart.Name,
						Arguments: string(argsJSON),
					},
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}

	return &model.Response{
		Text: responseText,
		Usage: &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
		FinishReason: finishReason,
		ToolCalls:    toolCalls,
	}, nil
}

// GenerateWithMessages generates a completion for the given messages.
func (m *GeminiModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	mergedOptions := m.MergeOptions(options)

	// Apply generation parameters
	applyGenerationParams(m.genModel, mergedOptions)

	// Create a chat session
	cs := m.genModel.StartChat()

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
		m.genModel.Tools = []*genai.Tool{
			{
				FunctionDeclarations: convertToolsToGeminiFunctions(m.tools),
			},
		}
	} else {
		m.genModel.Tools = nil
	}

	// Send the last message to generate a response
	var prompt genai.Text
	if len(nonSystemMessages) > 0 {
		prompt = genai.Text(nonSystemMessages[len(nonSystemMessages)-1].Content)
	} else {
		prompt = genai.Text("Hello")
	}

	// Set function calling mode if specified
	// Note: Gemini API may not support this configuration directly
	// We'll use a simplified approach based on the requested mode
	if mergedOptions.FunctionCallingMode != "" {
		switch mergedOptions.FunctionCallingMode {
		case "auto":
			// Default behavior
		case "required":
			// Force tools to be available
			if len(m.tools) > 0 && m.genModel.Tools == nil {
				m.genModel.Tools = []*genai.Tool{
					{
						FunctionDeclarations: convertToolsToGeminiFunctions(m.tools),
					},
				}
			}
		case "none":
			// Disable tools
			m.genModel.Tools = nil
		}
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
	responseMsg := message.NewAssistantMessage(responseText)

	// Check for function calls
	var toolCalls []model.ToolCall
	for _, part := range resp.Candidates[0].Content.Parts {
		if funcPart, ok := part.(*genai.FunctionCall); ok {
			// Convert the arguments map to a JSON string
			argsJSON, err := json.Marshal(funcPart.Args)
			if err != nil {
				argsJSON = []byte("{}")
			}

			toolCall := model.ToolCall{
				ID:   fmt.Sprintf("call_%d", time.Now().UnixNano()),
				Type: "function",
				Function: model.FunctionCall{
					Name:      funcPart.Name,
					Arguments: string(argsJSON),
				},
			}
			toolCalls = append(toolCalls, toolCall)
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
		Text:     responseText,
		Messages: []*message.Message{responseMsg},
		Usage: &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
		FinishReason: finishReason,
		ToolCalls:    toolCalls,
	}, nil
}

// SetTools implements the model.ToolCallSupportingModel interface.
func (m *GeminiModel) SetTools(tools []model.ToolDefinition) error {
	// Convert model.ToolDefinition to tool.ToolDefinition
	var toolDefs []*tool.ToolDefinition
	for _, t := range tools {
		def := tool.NewToolDefinition(t.Name, t.Description)

		// Convert parameters to Properties
		for name, paramSchema := range t.Parameters {
			if propObj, ok := paramSchema.(map[string]interface{}); ok {
				prop := propertyFromMap(propObj)

				// Check if this parameter is required
				required := false
				if reqList, ok := t.Parameters["required"].([]interface{}); ok {
					for _, req := range reqList {
						if reqStr, ok := req.(string); ok && reqStr == name {
							required = true
							break
						}
					}
				}

				def.AddParameter(name, prop, required)
			}
		}

		toolDefs = append(toolDefs, def)
	}

	return m.RegisterTools(toolDefs)
}

// RegisterTools implements the model.ToolCallSupportingModel interface.
func (m *GeminiModel) RegisterTools(tools []*tool.ToolDefinition) error {
	m.tools = tools

	if m.genModel != nil && len(tools) > 0 {
		m.genModel.Tools = []*genai.Tool{
			{
				FunctionDeclarations: convertToolsToGeminiFunctions(tools),
			},
		}
	}

	return nil
}

// propertyFromMap creates a Property from a map representation.
func propertyFromMap(propMap map[string]interface{}) *tool.Property {
	prop := &tool.Property{}

	// Set type
	if typeStr, ok := propMap["type"].(string); ok {
		prop.Type = typeStr
	} else {
		prop.Type = "string" // Default to string
	}

	// Set description
	if desc, ok := propMap["description"].(string); ok {
		prop.Description = desc
	}

	// Set default
	if def, ok := propMap["default"]; ok {
		prop.Default = def
	}

	// Set enum values
	if enum, ok := propMap["enum"].([]interface{}); ok {
		prop.Enum = enum
	}

	// Handle array items
	if prop.Type == "array" {
		if items, ok := propMap["items"].(map[string]interface{}); ok {
			prop.Items = propertyFromMap(items)
		}
	}

	// Handle object properties
	if prop.Type == "object" {
		if nestedProps, ok := propMap["properties"].(map[string]interface{}); ok {
			prop.Properties = make(map[string]*tool.Property)
			for k, v := range nestedProps {
				if propDef, ok := v.(map[string]interface{}); ok {
					prop.Properties[k] = propertyFromMap(propDef)
				}
			}
		}

		// Handle additionalProperties
		if addProps, ok := propMap["additionalProperties"].(bool); ok {
			prop.AdditionalProperties = addProps
		}
	}

	return prop
}

// convertToolsToGeminiFunctions converts the tool definitions to Gemini function declarations.
func convertToolsToGeminiFunctions(tools []*tool.ToolDefinition) []*genai.FunctionDeclaration {
	funcs := make([]*genai.FunctionDeclaration, 0, len(tools))

	for _, tool := range tools {
		// Convert tool parameters to properly formatted schema for Gemini
		schema := &genai.Schema{
			Type:       genai.TypeObject, // Use the Type enum constant
			Properties: make(map[string]*genai.Schema),
			Required:   tool.RequiredParameters(),
		}

		// Add each parameter to the schema
		for name, prop := range tool.Parameters {
			schema.Properties[name] = convertPropertyToGeminiSchema(prop)
		}

		funcs = append(funcs, &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
		})
	}

	return funcs
}

// convertPropertyToGeminiSchema converts a tool.Property to a genai.Schema.
func convertPropertyToGeminiSchema(prop *tool.Property) *genai.Schema {
	schema := &genai.Schema{}

	// Set the type using the correct enum values
	switch prop.Type {
	case "string":
		schema.Type = genai.TypeString
	case "number":
		schema.Type = genai.TypeNumber
	case "integer":
		schema.Type = genai.TypeInteger
	case "boolean":
		schema.Type = genai.TypeBoolean
	case "array":
		schema.Type = genai.TypeArray
		if prop.Items != nil {
			schema.Items = convertPropertyToGeminiSchema(prop.Items)
		}
	case "object":
		schema.Type = genai.TypeObject
		schema.Properties = make(map[string]*genai.Schema)
		for name, p := range prop.Properties {
			schema.Properties[name] = convertPropertyToGeminiSchema(p)
		}
		schema.Required = getRequiredProperties(prop)
	default:
		schema.Type = genai.TypeString // Default to string
	}

	// Set description
	schema.Description = prop.Description

	// Set enum values if any
	if len(prop.Enum) > 0 {
		for _, v := range prop.Enum {
			schema.Enum = append(schema.Enum, fmt.Sprintf("%v", v))
		}
	}

	return schema
}

// getRequiredProperties gets the required properties from an object Property.
func getRequiredProperties(prop *tool.Property) []string {
	var required []string

	// Check each property to see if it's required
	for name, p := range prop.Properties {
		if p.Required {
			required = append(required, name)
		}
	}

	return required
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

// SupportsGeminiFormat indicates if the model supports Gemini content format.
func (m *GeminiModel) SupportsGeminiFormat() bool {
	return true
}

// GenerateWithGeminiMessages generates a response for messages in Gemini format.
func (m *GeminiModel) GenerateWithGeminiMessages(ctx context.Context, geminiContents []*message.GeminiContent, options model.GenerationOptions) (model.Response, error) {
	mergedOptions := m.MergeOptions(options)

	// Apply generation parameters
	applyGenerationParams(m.genModel, mergedOptions)

	// Convert to Gemini content format and accumulate for single API call
	parts := []genai.Part{}
	for _, gc := range geminiContents {
		// Create text content from each Gemini content part
		for _, part := range gc.Parts {
			if part.Text != "" {
				parts = append(parts, genai.Text(part.Text))
			} else if part.InlineData != nil {
				// Handle image, file, and JSON parts based on MIME type
				if strings.HasPrefix(part.MimeType, "image/") && part.InlineData.ImageURL != "" {
					// TODO: Add image handling with proper mime type detection
					// For now, we just handle it as text
					parts = append(parts, genai.Text("[Image: "+part.InlineData.ImageURL+"]"))
				} else if part.MimeType == "application/json" && len(part.InlineData.JSON) > 0 {
					jsonStr := string(part.InlineData.JSON)
					parts = append(parts, genai.Text(jsonStr))
				} else if part.InlineData.FileURL != "" {
					parts = append(parts, genai.Text("[File: "+part.InlineData.FileURL+"]"))
				}
			}
		}
	}

	// Enable tools if requested
	if mergedOptions.EnableToolCalls && len(m.tools) > 0 {
		m.genModel.Tools = []*genai.Tool{
			{
				FunctionDeclarations: convertToolsToGeminiFunctions(m.tools),
			},
		}
	} else {
		m.genModel.Tools = nil
	}

	// Generate response
	resp, err := m.genModel.GenerateContent(ctx, parts...)
	if err != nil {
		return model.Response{}, fmt.Errorf("Gemini API request failed: %w", err)
	}

	// Process response and convert to non-pointer response
	result, err := processGeminiResponse(resp)
	if err != nil {
		return model.Response{}, err
	}
	return *result, nil
}

// GenerateStreamWithGeminiMessages streams a completion for messages in Gemini format.
func (m *GeminiModel) GenerateStreamWithGeminiMessages(ctx context.Context, geminiContents []*message.GeminiContent, options model.GenerationOptions) (<-chan model.Response, error) {
	// Create response channel - for non-pointer responses
	responseCh := make(chan model.Response, 10)

	mergedOptions := m.MergeOptions(options)

	// Apply generation parameters
	applyGenerationParams(m.genModel, mergedOptions)

	// Convert to Gemini content format and accumulate for single API call
	parts := []genai.Part{}
	for _, gc := range geminiContents {
		// Create text content from each Gemini content part
		for _, part := range gc.Parts {
			if part.Text != "" {
				parts = append(parts, genai.Text(part.Text))
			} else if part.InlineData != nil {
				// Handle image, file, and JSON parts based on MIME type
				if strings.HasPrefix(part.MimeType, "image/") && part.InlineData.ImageURL != "" {
					// TODO: Add image handling with proper mime type detection
					// For now, we just handle it as text
					parts = append(parts, genai.Text("[Image: "+part.InlineData.ImageURL+"]"))
				} else if part.MimeType == "application/json" && len(part.InlineData.JSON) > 0 {
					jsonStr := string(part.InlineData.JSON)
					parts = append(parts, genai.Text(jsonStr))
				} else if part.InlineData.FileURL != "" {
					parts = append(parts, genai.Text("[File: "+part.InlineData.FileURL+"]"))
				}
			}
		}
	}

	// Enable tools if requested
	if mergedOptions.EnableToolCalls && len(m.tools) > 0 {
		m.genModel.Tools = []*genai.Tool{
			{
				FunctionDeclarations: convertToolsToGeminiFunctions(m.tools),
			},
		}
	} else {
		m.genModel.Tools = nil
	}

	// Start stream in goroutine
	go func() {
		defer close(responseCh)

		// Generate streaming response
		iter := m.genModel.GenerateContentStream(ctx, parts...)
		for {
			resp, err := iter.Next()
			if err != nil {
				if err.Error() == "genai: stream is complete" {
					break
				}
				// Send error response
				errResp := model.Response{
					Text: fmt.Sprintf("Error: %v", err),
				}
				responseCh <- errResp
				return
			}

			// Process response chunks
			response, err := processGeminiResponse(resp)
			if err != nil {
				errResp := model.Response{
					Text: fmt.Sprintf("Error processing response: %v", err),
				}
				responseCh <- errResp
				continue
			}

			// Add Gemini-specific content
			if response != nil && response.Text != "" {
				response.GeminiContent = &message.GeminiContent{
					Role: "assistant",
					Parts: []message.GeminiPart{
						{
							MimeType: "text/plain",
							Text:     response.Text,
						},
					},
				}
			}

			// Send response as a non-pointer type
			if response != nil {
				responseCh <- *response
			} else {
				// In case of nil response, send empty response
				responseCh <- model.Response{}
			}
		}
	}()

	return responseCh, nil
}

// Helper function to process Gemini API responses
func processGeminiResponse(resp *genai.GenerateContentResponse) (*model.Response, error) {
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

	// Create usage stats (Gemini doesn't provide token counts in streaming, so these are estimates)
	completionTokens := len(strings.Split(responseText, " "))

	// Check for function calls
	var toolCalls []model.ToolCall
	if resp.Candidates[0].Content.Parts != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if funcPart, ok := part.(*genai.FunctionCall); ok {
				// Convert the arguments map to a JSON string
				argsJSON, err := json.Marshal(funcPart.Args)
				if err != nil {
					argsJSON = []byte("{}")
				}

				toolCall := model.ToolCall{
					ID:   fmt.Sprintf("call_%d", time.Now().UnixNano()),
					Type: "function",
					Function: model.FunctionCall{
						Name:      funcPart.Name,
						Arguments: string(argsJSON),
					},
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}

	// Create assistant message
	assistantMsg := message.NewAssistantMessage(responseText)

	// Create response object
	response := &model.Response{
		Text:     responseText,
		Messages: []*message.Message{assistantMsg},
		Usage: &model.Usage{
			CompletionTokens: completionTokens,
			TotalTokens:      completionTokens, // Prompt tokens unknown in streaming
		},
		FinishReason: finishReason,
		ToolCalls:    toolCalls,
		GeminiContent: &message.GeminiContent{
			Role: "assistant",
			Parts: []message.GeminiPart{
				{
					MimeType: "text/plain",
					Text:     responseText,
				},
			},
		},
	}

	return response, nil
}
