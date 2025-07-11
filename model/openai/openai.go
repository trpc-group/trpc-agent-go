// Package openai provides OpenAI-compatible model implementations.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	functionToolType string = "function"

	defaultChannelBufferSize = 256
)

// Model implements the model.Model interface for OpenAI API.
type Model struct {
	client            openai.Client
	name              string
	baseURL           string
	apiKey            string
	channelBufferSize int
}

// Options contains configuration options for creating a Model.
type Options struct {
	APIKey            string
	BaseURL           string // Optional: for OpenAI-compatible APIs
	ChannelBufferSize int    // Buffer size for response channels (default: 256)
}

// New creates a new OpenAI-like model.
func New(name string, opts Options) *Model {
	var clientOpts []option.RequestOption

	if opts.APIKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(opts.APIKey))
	}

	if opts.BaseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(opts.BaseURL))
	}

	client := openai.NewClient(clientOpts...)

	// Set default channel buffer size if not specified.
	channelBufferSize := opts.ChannelBufferSize
	if channelBufferSize <= 0 {
		channelBufferSize = defaultChannelBufferSize
	}

	return &Model{
		client:            client,
		name:              name,
		baseURL:           opts.BaseURL,
		apiKey:            opts.APIKey,
		channelBufferSize: channelBufferSize,
	}
}

// Info implements the model.Model interface.
func (m *Model) Info() model.Info {
	return model.Info{
		Name: m.name,
	}
}

// GenerateContent implements the model.Model interface.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}

	responseChan := make(chan *model.Response, m.channelBufferSize)

	// Convert our request format to OpenAI format.
	chatRequest := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(m.name),
		Messages: m.convertMessages(request.Messages),
		Tools:    m.convertTools(request.Tools),
	}

	// Set optional parameters if provided.
	if request.MaxTokens != nil {
		chatRequest.MaxTokens = openai.Int(int64(*request.MaxTokens)) // Convert to int64
	}
	if request.Temperature != nil {
		chatRequest.Temperature = openai.Float(*request.Temperature)
	}
	if request.TopP != nil {
		chatRequest.TopP = openai.Float(*request.TopP)
	}
	if len(request.Stop) > 0 {
		// Use the first stop string for simplicity.
		chatRequest.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfString: openai.String(request.Stop[0]),
		}
	}
	if request.PresencePenalty != nil {
		chatRequest.PresencePenalty = openai.Float(*request.PresencePenalty)
	}
	if request.FrequencyPenalty != nil {
		chatRequest.FrequencyPenalty = openai.Float(*request.FrequencyPenalty)
	}
	if request.ReasoningEffort != nil {
		chatRequest.ReasoningEffort = shared.ReasoningEffort(*request.ReasoningEffort)
	}
	var opts []option.RequestOption
	if request.ThinkingEnabled != nil {
		opts = append(opts, option.WithJSONSet(model.ThinkingEnabledKey, *request.ThinkingEnabled))
	}
	if request.ThinkingTokens != nil {
		opts = append(opts, option.WithJSONSet(model.ThinkingTokensKey, *request.ThinkingTokens))
	}

	// Add streaming options if needed.
	if request.Stream {
		chatRequest.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}

	go func() {
		defer close(responseChan)

		if request.Stream {
			m.handleStreamingResponse(ctx, chatRequest, responseChan, opts...)
		} else {
			m.handleNonStreamingResponse(ctx, chatRequest, responseChan, opts...)
		}
	}()

	return responseChan, nil
}

// convertMessages converts our Message format to OpenAI's format.
func (m *Model) convertMessages(messages []model.Message) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, len(messages))

	for i, msg := range messages {
		switch msg.Role {
		case model.RoleSystem:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
				},
			}
		case model.RoleUser:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
				},
			}
		case model.RoleAssistant:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
					ToolCalls: m.convertToolCalls(msg.ToolCalls),
				},
			}
		case model.RoleTool:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Content: openai.ChatCompletionToolMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
					ToolCallID: msg.ToolID,
				},
			}
		default:
			// Default to user message if role is unknown.
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
				},
			}
		}
	}

	return result
}

func (m *Model) convertToolCalls(toolCalls []model.ToolCall) []openai.ChatCompletionMessageToolCallParam {
	var result []openai.ChatCompletionMessageToolCallParam
	for _, toolCall := range toolCalls {
		result = append(result, openai.ChatCompletionMessageToolCallParam{
			ID: toolCall.ID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      toolCall.Function.Name,
				Arguments: string(toolCall.Function.Arguments),
			},
		})
	}
	return result
}

func (m *Model) convertTools(tools map[string]tool.Tool) []openai.ChatCompletionToolParam {
	var result []openai.ChatCompletionToolParam
	for _, tool := range tools {
		declaration := tool.Declaration()
		// Convert the InputSchema to JSON to correctly map to OpenAI's expected format
		schemaBytes, err := json.Marshal(declaration.InputSchema)
		if err != nil {
			log.Errorf("failed to marshal tool schema for %s: %v", declaration.Name, err)
			continue
		}
		var parameters shared.FunctionParameters
		if err := json.Unmarshal(schemaBytes, &parameters); err != nil {
			log.Errorf("failed to unmarshal tool schema for %s: %v", declaration.Name, err)
			continue
		}
		result = append(result, openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        declaration.Name,
				Description: openai.String(declaration.Description),
				Parameters:  parameters,
			},
		})
	}
	return result
}

// handleStreamingResponse handles streaming chat completion responses.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	responseChan chan<- *model.Response,
	opts ...option.RequestOption,
) {
	stream := m.client.Chat.Completions.NewStreaming(
		ctx, chatRequest, opts...)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}
	var hasToolCall bool
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		response := &model.Response{
			ID:        chunk.ID,
			Object:    string(chunk.Object), // Convert constant to string
			Created:   chunk.Created,
			Model:     chunk.Model,
			Timestamp: time.Now(),
			Done:      false,
			IsPartial: true,
		}

		if t, ok := acc.JustFinishedToolCall(); ok {
			hasToolCall = true
			response.IsPartial = false
			if response.Choices == nil {
				response.Choices = make([]model.Choice, 1)
			}
			response.Choices[0].Message = model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						Index: &t.Index,
						ID:    t.ID,
						Type:  functionToolType, // openapi only supports a function type for now
						Function: model.FunctionDefinitionParam{
							Name:      t.Name,
							Arguments: []byte(t.Arguments),
						},
					},
				},
			}
		}

		// Convert choices.
		if len(chunk.Choices) > 0 {
			if response.Choices == nil {
				response.Choices = make([]model.Choice, 1)
			}
			response.Choices[0].Delta = model.Message{
				Role:    model.RoleAssistant,
				Content: chunk.Choices[0].Delta.Content,
			}

			// Handle finish reason - FinishReason is a plain string.
			if chunk.Choices[0].FinishReason != "" {
				finishReason := chunk.Choices[0].FinishReason
				response.Choices[0].FinishReason = &finishReason
			}
		}

		select {
		case responseChan <- response:
		case <-ctx.Done():
			return
		}
	}

	// Send final response with usage information if available.
	if stream.Err() == nil {
		finalResponse := &model.Response{
			ID:      acc.ID,
			Created: acc.Created,
			Model:   acc.Model,
			Choices: make([]model.Choice, len(acc.Choices)),
			Usage: &model.Usage{
				PromptTokens:     int(acc.Usage.PromptTokens),
				CompletionTokens: int(acc.Usage.CompletionTokens),
				TotalTokens:      int(acc.Usage.TotalTokens),
			},
			Timestamp: time.Now(),
			Done:      !hasToolCall,
			IsPartial: false,
		}
		for i, choice := range acc.Choices {
			finalResponse.Choices[i] = model.Choice{
				Index: int(choice.Index),
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: choice.Message.Content,
				},
			}
		}

		select {
		case responseChan <- finalResponse:
		case <-ctx.Done():
		}
	} else {
		// Send error response.
		errorResponse := &model.Response{
			Error: &model.ResponseError{
				Message: stream.Err().Error(),
				Type:    model.ErrorTypeStreamError,
			},
			Timestamp: time.Now(),
			Done:      true,
		}

		select {
		case responseChan <- errorResponse:
		case <-ctx.Done():
		}
	}
}

// handleNonStreamingResponse handles non-streaming chat completion responses.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	responseChan chan<- *model.Response,
	opts ...option.RequestOption,
) {
	chatCompletion, err := m.client.Chat.Completions.New(
		ctx, chatRequest, opts...)
	if err != nil {
		errorResponse := &model.Response{
			Error: &model.ResponseError{
				Message: err.Error(),
				Type:    model.ErrorTypeAPIError,
			},
			Timestamp: time.Now(),
			Done:      true,
		}

		select {
		case responseChan <- errorResponse:
		case <-ctx.Done():
		}
		return
	}

	response := &model.Response{
		ID:        chatCompletion.ID,
		Object:    string(chatCompletion.Object), // Convert constant to string
		Created:   chatCompletion.Created,
		Model:     chatCompletion.Model,
		Timestamp: time.Now(),
		Done:      true,
	}

	// Convert choices.
	if len(chatCompletion.Choices) > 0 {
		response.Choices = make([]model.Choice, len(chatCompletion.Choices))
		for i, choice := range chatCompletion.Choices {
			response.Choices[i].Message.ToolCalls = make([]model.ToolCall, len(choice.Message.ToolCalls))
			for j, toolCall := range choice.Message.ToolCalls {
				response.Choices[i].Message.ToolCalls[j] = model.ToolCall{
					ID:   toolCall.ID,
					Type: string(toolCall.Type),
					Function: model.FunctionDefinitionParam{
						Name:      toolCall.Function.Name,
						Arguments: []byte(toolCall.Function.Arguments),
					},
				}
			}

			response.Choices[i] = model.Choice{
				Index: int(choice.Index),
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: choice.Message.Content,
				},
			}

			// Handle finish reason - FinishReason is a plain string.
			if choice.FinishReason != "" {
				finishReason := choice.FinishReason
				response.Choices[i].FinishReason = &finishReason
			}
		}
	}

	// Convert usage information.
	if chatCompletion.Usage.PromptTokens > 0 || chatCompletion.Usage.CompletionTokens > 0 {
		response.Usage = &model.Usage{
			PromptTokens:     int(chatCompletion.Usage.PromptTokens),
			CompletionTokens: int(chatCompletion.Usage.CompletionTokens),
			TotalTokens:      int(chatCompletion.Usage.TotalTokens),
		}
	}

	// Set system fingerprint if available.
	if chatCompletion.SystemFingerprint != "" {
		response.SystemFingerprint = &chatCompletion.SystemFingerprint
	}

	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}
