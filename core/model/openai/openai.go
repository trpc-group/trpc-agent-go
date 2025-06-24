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

	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const defaultChannelBufferSize = 256

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

	// Add streaming options if needed.
	if request.Stream {
		chatRequest.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}

	go func() {
		defer close(responseChan)

		if request.Stream {
			m.handleStreamingResponse(ctx, chatRequest, responseChan)
		} else {
			m.handleNonStreamingResponse(ctx, chatRequest, responseChan)
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
) {
	stream := m.client.Chat.Completions.NewStreaming(ctx, chatRequest)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}
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
		}

		if tool, ok := acc.JustFinishedToolCall(); ok {
			response.SetToolCalls([]model.ToolCall{
				{
					Index: &tool.Index,
					ID:    tool.ID,
					Type:  "function", // openapi only supports function type for now
					Function: model.FunctionDefinitionParam{
						Name:      tool.Name,
						Arguments: []byte(tool.Arguments),
					},
				},
			})
		}

		// Convert choices.
		if len(chunk.Choices) > 0 {
			response.Choices = make([]model.Choice, len(chunk.Choices))
			for i, choice := range chunk.Choices {
				response.Choices[i] = model.Choice{
					Index: int(choice.Index),
				}

				toolCallsConverted := make([]model.ToolCall, len(choice.Delta.ToolCalls))
				for j, toolCall := range choice.Delta.ToolCalls {
					toolCallsConverted[j] = model.ToolCall{
						ID:   toolCall.ID,
						Type: string(toolCall.Type), // Convert constant to string
						Function: model.FunctionDefinitionParam{
							Name:      toolCall.Function.Name,
							Arguments: []byte(toolCall.Function.Arguments),
						},
					}
					index := int(toolCall.Index)
					toolCallsConverted[j].Index = &index
				}

				// Handle delta content - Content is a plain string.
				if choice.Delta.Content != "" || len(toolCallsConverted) > 0 {
					response.Choices[i].Delta = model.Message{
						Role:      model.RoleAssistant,
						Content:   choice.Delta.Content,
						ToolCalls: toolCallsConverted,
					}
				}

				// Handle finish reason - FinishReason is a plain string.
				if choice.FinishReason != "" {
					finishReason := choice.FinishReason
					response.Choices[i].FinishReason = &finishReason
				}
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
			Timestamp: time.Now(),
			Done:      true,
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
) {
	chatCompletion, err := m.client.Chat.Completions.New(ctx, chatRequest)
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
			toolCallsConverted := make([]model.ToolCall, len(choice.Message.ToolCalls))
			for j, toolCall := range choice.Message.ToolCalls {
				toolCallsConverted[j] = model.ToolCall{
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
					Role:      model.RoleAssistant,
					Content:   choice.Message.Content,
					ToolCalls: toolCallsConverted,
				},
			}
			response.SetToolCalls(toolCallsConverted)

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
