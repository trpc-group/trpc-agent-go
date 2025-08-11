//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package openai provides OpenAI-compatible model implementations.
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"time"

	openai "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	functionToolType string = "function"

	defaultChannelBufferSize = 256
)

// Variant represents different model variants with specific behaviors.
type Variant string

const (
	// VariantOpenAI is the default OpenAI variant.
	VariantOpenAI Variant = "openai"
	// VariantHunyuan is the Hunyuan variant with specific file handling.
	VariantHunyuan Variant = "hunyuan"
)

// variantConfig holds configuration for different variants.
type variantConfig struct {
	// Default file upload path for this variant.
	fileUploadPath   string
	fileDeletionPath string
	// Default file purpose for this variant.
	filePurpose openai.FilePurpose
	// Default HTTP method for file deletion.
	fileDeletionMethod         string
	fileDeletionBodyConvertor  fileDeletionBodyConvertor
	fileUploadRequestConvertor fileUploadRequestConvertor
	// Whether to skip file type in content parts for this variant.
	skipFileTypeInContent bool
}

type fileDeletionBodyConvertor func(body []byte, fileID string) []byte

type fileUploadRequestConvertor func(r *http.Request, file *os.File, fileOpts *FileOptions) (*http.Request, error)

// variantConfigs maps variant names to their configurations.
var variantConfigs = map[Variant]variantConfig{
	VariantOpenAI: {
		fileUploadPath:        "/openapi/v1/files",
		filePurpose:           openai.FilePurposeUserData,
		fileDeletionMethod:    http.MethodDelete,
		skipFileTypeInContent: false,
		fileDeletionBodyConvertor: func(body []byte, fileID string) []byte {
			return body
		},
	},
	VariantHunyuan: {
		fileUploadPath:        "/openapi/v1/files/uploads",
		fileDeletionPath:      "/openapi/v1/files",
		filePurpose:           openai.FilePurpose("file-extract"),
		fileDeletionMethod:    http.MethodPost,
		skipFileTypeInContent: true,
		fileDeletionBodyConvertor: func(body []byte, fileID string) []byte {
			if body != nil {
				return body
			}
			return []byte(`{"file_id":"` + fileID + `"}`)
		},
		fileUploadRequestConvertor: func(r *http.Request, file *os.File, fileOpts *FileOptions) (*http.Request, error) {
			// Create multipart form data.
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			// Add purpose field.
			if err := writer.WriteField("purpose", string(fileOpts.Purpose)); err != nil {
				return nil, fmt.Errorf("failed to write purpose field: %w", err)
			}
			// Add file field.
			fileInfo, err := file.Stat()
			if err != nil {
				return nil, fmt.Errorf("failed to get file info: %w", err)
			}
			part, err := writer.CreateFormFile("file", fileInfo.Name())
			if err != nil {
				return nil, fmt.Errorf("failed to create form file: %w", err)
			}
			// Reset file position and copy file content.
			if _, err := file.Seek(0, 0); err != nil {
				return nil, fmt.Errorf("failed to reset file position: %w", err)
			}
			if _, err := io.Copy(part, file); err != nil {
				return nil, fmt.Errorf("failed to copy file content: %w", err)
			}
			// Close the writer to finalize the multipart data.
			if err := writer.Close(); err != nil {
				return nil, fmt.Errorf("failed to close multipart writer: %w", err)
			}
			// Set the request body and content type.
			r.Body = io.NopCloser(body)
			r.Header.Set("Content-Type", writer.FormDataContentType())
			r.ContentLength = int64(body.Len())
			return r, nil
		},
	},
}

// HTTPClient is the interface for the HTTP client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClientNewFunc is the function type for creating a new HTTP client.
type HTTPClientNewFunc func(opts ...HTTPClientOption) HTTPClient

// DefaultNewHTTPClient is the default HTTP client for OpenAI.
var DefaultNewHTTPClient HTTPClientNewFunc = func(opts ...HTTPClientOption) HTTPClient {
	options := &HTTPClientOptions{}
	for _, opt := range opts {
		opt(options)
	}
	return &http.Client{
		Transport: options.Transport,
	}
}

// HTTPClientOption is the option for the HTTP client.
type HTTPClientOption func(*HTTPClientOptions)

// WithHTTPClientName is the option for the HTTP client name.
func WithHTTPClientName(name string) HTTPClientOption {
	return func(options *HTTPClientOptions) {
		options.Name = name
	}
}

// WithHTTPClientTransport is the option for the HTTP client transport.
func WithHTTPClientTransport(transport http.RoundTripper) HTTPClientOption {
	return func(options *HTTPClientOptions) {
		options.Transport = transport
	}
}

// HTTPClientOptions is the options for the HTTP client.
type HTTPClientOptions struct {
	Name      string
	Transport http.RoundTripper
}

// Model implements the model.Model interface for OpenAI API.
type Model struct {
	client               openai.Client
	name                 string
	baseURL              string
	apiKey               string
	channelBufferSize    int
	chatRequestCallback  ChatRequestCallbackFunc
	chatResponseCallback ChatResponseCallbackFunc
	chatChunkCallback    ChatChunkCallbackFunc
	extraFields          map[string]interface{}
	variant              Variant
	variantConfig        variantConfig
}

// ChatRequestCallbackFunc is the function type for the chat request callback.
type ChatRequestCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatResponse *openai.ChatCompletion,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatChunk *openai.ChatCompletionChunk,
)

// options contains configuration options for creating a Model.
type options struct {
	// API key for the OpenAI client.
	APIKey string
	// Base URL for the OpenAI client. It is optional for OpenAI-compatible APIs.
	BaseURL string
	// Buffer size for response channels (default: 256)
	ChannelBufferSize int
	// Options for the HTTP client.
	HTTPClientOptions []HTTPClientOption
	// Callback for the chat request.
	ChatRequestCallback ChatRequestCallbackFunc
	// Callback for the chat response.
	ChatResponseCallback ChatResponseCallbackFunc
	// Callback for the chat chunk.
	ChatChunkCallback ChatChunkCallbackFunc
	// Options for the OpenAI client.
	OpenAIOptions []openaiopt.RequestOption
	// Extra fields to be added to the HTTP request body.
	ExtraFields map[string]interface{}
	// Variant for model-specific behavior.
	Variant Variant
}

// Option is a function that configures an OpenAI model.
type Option func(*options)

// WithAPIKey sets the API key for the OpenAI client.
func WithAPIKey(key string) Option {
	return func(opts *options) {
		opts.APIKey = key
	}
}

// WithBaseURL sets the base URL for the OpenAI client.
func WithBaseURL(url string) Option {
	return func(opts *options) {
		opts.BaseURL = url
	}
}

// WithChannelBufferSize sets the channel buffer size for the OpenAI client.
func WithChannelBufferSize(size int) Option {
	return func(opts *options) {
		opts.ChannelBufferSize = size
	}
}

// WithChatRequestCallback sets the function to be called before sending a chat request.
func WithChatRequestCallback(fn ChatRequestCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatRequestCallback = fn
	}
}

// WithChatResponseCallback sets the function to be called after receiving a chat response.
// Used for non-streaming responses.
func WithChatResponseCallback(fn ChatResponseCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatResponseCallback = fn
	}
}

// WithChatChunkCallback sets the function to be called after receiving a chat chunk.
// Used for streaming responses.
func WithChatChunkCallback(fn ChatChunkCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatChunkCallback = fn
	}
}

// WithHTTPClientOptions sets the HTTP client options for the OpenAI client.
func WithHTTPClientOptions(httpOpts ...HTTPClientOption) Option {
	return func(opts *options) {
		opts.HTTPClientOptions = httpOpts
	}
}

// WithOpenAIOptions sets the OpenAI options for the OpenAI client.
// E.g. use its middleware option:
//
//	import (
//		openai "github.com/openai/openai-go"
//		openaiopt "github.com/openai/openai-go/option"
//	)
//
//	WithOpenAIOptions(openaiopt.WithMiddleware(
//		func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
//			// do something
//			return next(req)
//		}
//	)))
func WithOpenAIOptions(openaiOpts ...openaiopt.RequestOption) Option {
	return func(opts *options) {
		opts.OpenAIOptions = append(opts.OpenAIOptions, openaiOpts...)
	}
}

// WithExtraFields sets extra fields to be added to the HTTP request body.
// These fields will be included in every chat completion request.
// E.g.:
//
//	WithExtraFields(map[string]interface{}{
//		"custom_metadata": map[string]string{
//			"session_id": "abc",
//		},
//	})
//
// and "session_id" : "abc" will be added to the HTTP request json body.
func WithExtraFields(extraFields map[string]interface{}) Option {
	return func(opts *options) {
		if opts.ExtraFields == nil {
			opts.ExtraFields = make(map[string]interface{})
		}
		for k, v := range extraFields {
			opts.ExtraFields[k] = v
		}
	}
}

// WithVariant sets the model variant for specific behavior.
// The default variant is VariantOpenAI.
// Optional variants are:
// - VariantHunyuan: Hunyuan variant with specific file handling.
func WithVariant(variant Variant) Option {
	return func(opts *options) {
		opts.Variant = variant
	}
}

// New creates a new OpenAI-like model.
func New(name string, opts ...Option) *Model {
	o := &options{
		Variant: VariantOpenAI, // The default variant is VariantOpenAI.
	}
	for _, opt := range opts {
		opt(o)
	}
	var clientOpts []openaiopt.RequestOption

	if o.APIKey != "" {
		clientOpts = append(clientOpts, openaiopt.WithAPIKey(o.APIKey))
	}

	if o.BaseURL != "" {
		clientOpts = append(clientOpts, openaiopt.WithBaseURL(o.BaseURL))
	}

	clientOpts = append(clientOpts, openaiopt.WithHTTPClient(DefaultNewHTTPClient(o.HTTPClientOptions...)))
	clientOpts = append(clientOpts, o.OpenAIOptions...)

	client := openai.NewClient(clientOpts...)

	// Set default channel buffer size if not specified.
	channelBufferSize := o.ChannelBufferSize
	if channelBufferSize <= 0 {
		channelBufferSize = defaultChannelBufferSize
	}
	return &Model{
		client:               client,
		name:                 name,
		baseURL:              o.BaseURL,
		apiKey:               o.APIKey,
		channelBufferSize:    channelBufferSize,
		chatRequestCallback:  o.ChatRequestCallback,
		chatResponseCallback: o.ChatResponseCallback,
		chatChunkCallback:    o.ChatChunkCallback,
		extraFields:          o.ExtraFields,
		variant:              o.Variant,
		variantConfig:        variantConfigs[o.Variant],
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
	var opts []openaiopt.RequestOption
	if request.ThinkingEnabled != nil {
		opts = append(opts, openaiopt.WithJSONSet(model.ThinkingEnabledKey, *request.ThinkingEnabled))
	}
	if request.ThinkingTokens != nil {
		opts = append(opts, openaiopt.WithJSONSet(model.ThinkingTokensKey, *request.ThinkingTokens))
	}

	// Add extra fields to the request
	for key, value := range m.extraFields {
		opts = append(opts, openaiopt.WithJSONSet(key, value))
	}

	// Add streaming options if needed.
	if request.Stream {
		chatRequest.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}

	go func() {
		defer close(responseChan)

		if m.chatRequestCallback != nil {
			m.chatRequestCallback(ctx, &chatRequest)
		}

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
		toUserMessage := func() openai.ChatCompletionMessageParamUnion {
			content, extraFields := m.convertUserMessageContent(msg)
			userMessage := &openai.ChatCompletionUserMessageParam{
				Content: content,
			}
			if m.variantConfig.skipFileTypeInContent {
				userMessage.SetExtraFields(extraFields)
			}
			return openai.ChatCompletionMessageParamUnion{
				OfUser: userMessage,
			}
		}
		switch msg.Role {
		case model.RoleSystem:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: m.convertSystemMessageContent(msg),
				},
			}
		case model.RoleAssistant:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content:   m.convertAssistantMessageContent(msg),
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
		case model.RoleUser:
			result[i] = toUserMessage()
		default: // Default to user message if role is unknown.
			result[i] = toUserMessage()
		}
	}

	return result
}

// convertSystemMessageContent converts message content to system message content union.
func (m *Model) convertSystemMessageContent(msg model.Message) openai.ChatCompletionSystemMessageParamContentUnion {
	if len(msg.ContentParts) == 0 && msg.Content != "" {
		return openai.ChatCompletionSystemMessageParamContentUnion{
			OfString: openai.String(msg.Content),
		}
	}
	// Convert content parts to OpenAI content parts.
	var contentParts []openai.ChatCompletionContentPartTextParam
	if msg.Content != "" {
		contentParts = append(contentParts, openai.ChatCompletionContentPartTextParam{
			Text: msg.Content,
		})
	}
	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			contentParts = append(contentParts, openai.ChatCompletionContentPartTextParam{
				Text: *part.Text,
			})
		}
	}
	return openai.ChatCompletionSystemMessageParamContentUnion{
		OfArrayOfContentParts: contentParts,
	}
}

// convertUserMessageContent converts message content to user message content union.
func (m *Model) convertUserMessageContent(
	msg model.Message,
) (openai.ChatCompletionUserMessageParamContentUnion, map[string]any) {
	// If there are no content parts and Content is not empty, return as string.
	if len(msg.ContentParts) == 0 && msg.Content != "" {
		return openai.ChatCompletionUserMessageParamContentUnion{
			OfString: openai.String(msg.Content),
		}, nil
	}
	var (
		contentParts []openai.ChatCompletionContentPartUnionParam
		extraFields  = make(map[string]any)
	)
	// Add Content as a text part if present.
	if msg.Content != "" {
		contentParts = append(
			contentParts,
			openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{
					Text: msg.Content,
				},
			},
		)
	}
	for _, part := range msg.ContentParts {
		contentPart := m.convertContentPart(part)
		if contentPart == nil {
			continue
		}
		// Handle file content parts based on variant configuration.
		if part.Type == model.ContentTypeFile && m.variantConfig.skipFileTypeInContent {
			const fileIDsKey = "file_ids"
			// Collect file IDs in extraFields under "file_ids".
			fileIDs, ok := extraFields[fileIDsKey].([]string)
			if !ok {
				fileIDs = []string{}
			}
			fileIDs = append(fileIDs, part.File.FileID)
			extraFields[fileIDsKey] = fileIDs
			continue
		}
		// For non-file or non-skipped file types, add to contentParts.
		contentParts = append(contentParts, *contentPart)
	}
	return openai.ChatCompletionUserMessageParamContentUnion{
		OfArrayOfContentParts: contentParts,
	}, extraFields
}

// convertAssistantMessageContent converts message content to assistant message content union.
func (m *Model) convertAssistantMessageContent(
	msg model.Message,
) openai.ChatCompletionAssistantMessageParamContentUnion {
	if len(msg.ContentParts) == 0 && msg.Content != "" {
		return openai.ChatCompletionAssistantMessageParamContentUnion{
			OfString: openai.String(msg.Content),
		}
	}
	// Convert content parts to OpenAI content parts.
	var contentParts []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion
	if msg.Content != "" {
		contentParts = append(contentParts, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
			OfText: &openai.ChatCompletionContentPartTextParam{
				Text: msg.Content,
			},
		})
	}
	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			contentParts = append(contentParts,
				openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
					OfText: &openai.ChatCompletionContentPartTextParam{
						Text: *part.Text,
					},
				})
		}
	}
	return openai.ChatCompletionAssistantMessageParamContentUnion{
		OfArrayOfContentParts: contentParts,
	}
}

// convertContentPart converts a single content part to OpenAI format.
func (m *Model) convertContentPart(part model.ContentPart) *openai.ChatCompletionContentPartUnionParam {
	switch part.Type {
	case model.ContentTypeText:
		if part.Text != nil {
			return &openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{
					Text: *part.Text,
				},
			}
		}
	case model.ContentTypeImage:
		if part.Image != nil {
			return &openai.ChatCompletionContentPartUnionParam{
				OfImageURL: &openai.ChatCompletionContentPartImageParam{
					ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
						// The URL from openai-go can be used either as a URL or as a base64-encoded string.
						URL:    imageToURLOrBase64(part.Image),
						Detail: part.Image.Detail,
					},
				},
			}
		}
	case model.ContentTypeAudio:
		if part.Audio != nil {
			return &openai.ChatCompletionContentPartUnionParam{
				OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
					InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
						Data:   audioToBase64(part.Audio),
						Format: part.Audio.Format,
					},
				},
			}
		}
	case model.ContentTypeFile:
		if part.File != nil {
			return &openai.ChatCompletionContentPartUnionParam{
				OfFile: &openai.ChatCompletionContentPartFileParam{
					File: fileToParams(part.File),
				},
			}
		}
	}
	return nil
}

func imageToURLOrBase64(image *model.Image) string {
	if image.URL != "" {
		return image.URL
	}
	return "data:image/" + image.Format + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

func fileToParams(file *model.File) openai.ChatCompletionContentPartFileFileParam {
	if file.FileID != "" {
		return openai.ChatCompletionContentPartFileFileParam{
			FileID: openai.String(file.FileID),
		}
	}
	return openai.ChatCompletionContentPartFileFileParam{
		FileData: openai.String("data:" + file.MimeType + ";base64," + base64.StdEncoding.EncodeToString(file.Data)),
		Filename: openai.String(file.Name),
	}
}

func audioToBase64(audio *model.Audio) string {
	return "data:" + audio.Format + ";base64," + base64.StdEncoding.EncodeToString(audio.Data)
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
	opts ...openaiopt.RequestOption,
) {
	stream := m.client.Chat.Completions.NewStreaming(
		ctx, chatRequest, opts...)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}
	// Track ID -> Index mapping.
	idToIndexMap := make(map[string]int)

	for stream.Next() {
		chunk := stream.Current()

		// Track ID -> Index mapping when ID is present (first chunk of each tool call).
		m.updateToolCallIndexMapping(chunk, idToIndexMap)

		// Always accumulate for correctness (tool call deltas are assembled later),
		// but we may suppress emitting a partial event for noise reduction.
		acc.AddChunk(chunk)

		// Suppress chunks that carry no meaningful visible delta (including
		// tool_call deltas, which we'll surface only in the final response).
		if m.shouldSuppressChunk(chunk) {
			continue
		}

		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, &chatRequest, &chunk)
		}

		response := m.createPartialResponse(chunk)

		select {
		case responseChan <- response:
		case <-ctx.Done():
			return
		}
	}

	// Send final response with usage information if available.
	m.sendFinalResponse(ctx, stream, acc, idToIndexMap, responseChan)
}

// updateToolCallIndexMapping updates the tool call index mapping.
func (m *Model) updateToolCallIndexMapping(chunk openai.ChatCompletionChunk, idToIndexMap map[string]int) {
	if len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta.ToolCalls) > 0 {
		toolCall := chunk.Choices[0].Delta.ToolCalls[0]
		index := int(toolCall.Index)
		if toolCall.ID != "" {
			idToIndexMap[toolCall.ID] = index
		}
	}
}

// shouldSuppressChunk returns true when the chunk contains no meaningful delta
// (no content, no refusal, no non-empty tool calls, and no finish reason).
// This filters out completely empty streaming events that cause noisy logs.
func (m *Model) shouldSuppressChunk(chunk openai.ChatCompletionChunk) bool {
	if len(chunk.Choices) == 0 {
		return true
	}
	choice := chunk.Choices[0]
	delta := choice.Delta

	// Any meaningful payload disables suppression.
	if delta.Content != "" {
		return false
	}

	// think model reasoning content
	if _, ok := delta.JSON.ExtraFields["reasoning_content"]; ok {
		return false
	}

	// If this chunk is a tool_calls delta, suppress emission. We'll only expose
	// tool calls in the final aggregated response to avoid noisy blank chunks.
	if delta.JSON.ToolCalls.Valid() {
		return true
	}
	if choice.FinishReason != "" {
		return false
	}
	return true
}

// createPartialResponse creates a partial response from a chunk.
func (m *Model) createPartialResponse(chunk openai.ChatCompletionChunk) *model.Response {
	response := &model.Response{
		ID: chunk.ID,
		// Normalize object for chunks; upstream may emit empty object for toolcall deltas.
		Object: func() string {
			if chunk.Object != "" {
				return string(chunk.Object)
			}
			return model.ObjectTypeChatCompletionChunk
		}(),
		Created:   chunk.Created,
		Model:     chunk.Model,
		Timestamp: time.Now(),
		Done:      false,
		IsPartial: true,
	}

	// Convert choices for partial responses (content streaming).
	if len(chunk.Choices) > 0 {
		if response.Choices == nil {
			response.Choices = make([]model.Choice, 1)
		}

		reasoningContent, err := strconv.Unquote(chunk.Choices[0].Delta.JSON.ExtraFields["reasoning_content"].Raw())
		if err != nil {
			reasoningContent = ""
		}

		response.Choices[0].Delta = model.Message{
			Role:             model.RoleAssistant,
			Content:          chunk.Choices[0].Delta.Content,
			ReasoningContent: reasoningContent,
		}

		// Handle finish reason - FinishReason is a plain string.
		if chunk.Choices[0].FinishReason != "" {
			finishReason := chunk.Choices[0].FinishReason
			response.Choices[0].FinishReason = &finishReason
		}
	}

	return response
}

// sendFinalResponse sends the final response with accumulated data.
func (m *Model) sendFinalResponse(
	ctx context.Context,
	stream *ssestream.Stream[openai.ChatCompletionChunk],
	acc openai.ChatCompletionAccumulator,
	idToIndexMap map[string]int,
	responseChan chan<- *model.Response,
) {
	if stream.Err() == nil {
		// Check accumulated tool calls (batch processing after streaming is complete).
		var hasToolCall bool
		var accumulatedToolCalls []model.ToolCall

		if len(acc.Choices) > 0 && len(acc.Choices[0].Message.ToolCalls) > 0 {
			hasToolCall = true
			accumulatedToolCalls = m.processAccumulatedToolCalls(acc, idToIndexMap)
		}

		finalResponse := m.createFinalResponse(acc, hasToolCall, accumulatedToolCalls)

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

// processAccumulatedToolCalls processes accumulated tool calls.
func (m *Model) processAccumulatedToolCalls(
	acc openai.ChatCompletionAccumulator,
	idToIndexMap map[string]int,
) []model.ToolCall {
	accumulatedToolCalls := make([]model.ToolCall, 0, len(acc.Choices[0].Message.ToolCalls))

	for i, toolCall := range acc.Choices[0].Message.ToolCalls {
		// if openai return function tool call start with index 1 or more
		// ChatCompletionAccumulator will return empty tool call for index like 0, skip it.
		if toolCall.Function.Name == "" && toolCall.ID == "" {
			continue
		}

		// Use the original index from ID->Index mapping if available, otherwise use loop index.
		originalIndex := i
		if toolCall.ID != "" {
			if mappedIndex, exists := idToIndexMap[toolCall.ID]; exists {
				originalIndex = mappedIndex
			}
		}

		accumulatedToolCalls = append(accumulatedToolCalls, model.ToolCall{
			Index: func() *int { idx := originalIndex; return &idx }(),
			ID:    toolCall.ID,
			Type:  functionToolType, // openapi only supports a function type for now.
			Function: model.FunctionDefinitionParam{
				Name:      toolCall.Function.Name,
				Arguments: []byte(toolCall.Function.Arguments),
			},
		})
	}

	return accumulatedToolCalls
}

// createFinalResponse creates the final response with accumulated data.
func (m *Model) createFinalResponse(
	acc openai.ChatCompletionAccumulator,
	hasToolCall bool,
	accumulatedToolCalls []model.ToolCall,
) *model.Response {
	finalResponse := &model.Response{
		Object:  model.ObjectTypeChatCompletion,
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

		// If there are tool calls, add them to the final response.
		if hasToolCall && i == 0 { // Usually only the first choice contains tool calls.
			finalResponse.Choices[i].Message.ToolCalls = accumulatedToolCalls
		}
	}

	return finalResponse
}

// handleNonStreamingResponse handles non-streaming chat completion responses.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	responseChan chan<- *model.Response,
	opts ...openaiopt.RequestOption,
) {
	chatCompletion, err := m.client.Chat.Completions.New(
		ctx, chatRequest, opts...)
	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, &chatRequest, chatCompletion)
	}
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
			response.Choices[i] = model.Choice{
				Index: int(choice.Index),
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: choice.Message.Content,
				},
			}

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

// FileOptions is the options for file operations.
type FileOptions struct {
	// Path for file operations (default: /openapi/v1/files).
	Path string
	// Purpose for file upload (default: openai.FilePurposeUserData).
	Purpose openai.FilePurpose
	// Method for HTTP request (default: based on operation).
	Method string
	// Body for HTTP request (default: auto-generated based on operation).
	Body []byte
}

// FileOption is the option for file operations.
type FileOption func(*FileOptions)

// WithPath is the option for setting the file operation path.
func WithPath(path string) FileOption {
	return func(options *FileOptions) {
		options.Path = path
	}
}

// WithPurpose is the option for setting the file upload purpose.
func WithPurpose(purpose openai.FilePurpose) FileOption {
	return func(options *FileOptions) {
		options.Purpose = purpose
	}
}

// WithMethod is the option for setting the HTTP method.
func WithMethod(method string) FileOption {
	return func(options *FileOptions) {
		options.Method = method
	}
}

// WithBody is the option for setting the HTTP request body.
func WithBody(body []byte) FileOption {
	return func(options *FileOptions) {
		options.Body = body
	}
}

// UploadFile uploads a file to OpenAI and returns the file ID.
// The file can then be referenced in messages using AddFileID().
func (m *Model) UploadFile(ctx context.Context, filePath string, opts ...FileOption) (string, error) {
	fileOpts := &FileOptions{
		Path:    m.variantConfig.fileUploadPath,
		Purpose: m.variantConfig.filePurpose,
	}
	for _, opt := range opts {
		opt(fileOpts)
	}

	// Open the file.
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create middleware to construct multipart form data request.
	middlewareOpt := openaiopt.WithMiddleware(
		func(r *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
			// Set the correct path.
			r.URL.Path = fileOpts.Path

			// Set custom HTTP method if specified.
			if fileOpts.Method != "" {
				r.Method = fileOpts.Method
			}

			// Use custom body if specified, otherwise create multipart form data.
			if fileOpts.Body != nil {
				r.Body = io.NopCloser(bytes.NewReader(fileOpts.Body))
				r.ContentLength = int64(len(fileOpts.Body))
			} else if m.variantConfig.fileUploadRequestConvertor != nil {
				r, err = m.variantConfig.fileUploadRequestConvertor(r, file, fileOpts)
				if err != nil {
					return nil, fmt.Errorf("failed to convert request: %w", err)
				}
			}
			// Continue with the modified request.
			return next(r)
		})

	// Create empty file params since we're handling the file in middleware.
	fileParams := openai.FileNewParams{
		File:    file,
		Purpose: fileOpts.Purpose,
	}

	// Upload the file.
	fileObj, err := m.client.Files.New(ctx, fileParams, middlewareOpt)
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}
	return fileObj.ID, nil
}

// UploadFileData uploads file data to OpenAI and returns the file ID.
// This is useful when you have file data in memory rather than a file path.
func (m *Model) UploadFileData(
	ctx context.Context,
	filename string,
	data []byte,
	opts ...FileOption,
) (string, error) {
	// Apply default options based on variant.
	fileOpts := &FileOptions{
		Path:    m.variantConfig.fileUploadPath,
		Purpose: m.variantConfig.filePurpose,
	}
	for _, opt := range opts {
		opt(fileOpts)
	}

	// Create file upload parameters with data reader.
	fileParams := openai.FileNewParams{
		File:    bytes.NewReader(data),
		Purpose: fileOpts.Purpose,
	}

	// Create middleware to handle custom options.
	middlewareOpt := openaiopt.WithMiddleware(
		func(r *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
			// Set the correct path.
			r.URL.Path = fileOpts.Path
			// Set custom HTTP method if specified.
			if fileOpts.Method != "" {
				r.Method = fileOpts.Method
			}
			// Use custom body if specified.
			if fileOpts.Body != nil {
				r.Body = io.NopCloser(bytes.NewReader(fileOpts.Body))
				r.ContentLength = int64(len(fileOpts.Body))
			}
			return next(r)
		})

	// Upload the file.
	fileObj, err := m.client.Files.New(ctx, fileParams, middlewareOpt)
	if err != nil {
		return "", fmt.Errorf("failed to upload file data: %w", err)
	}
	return fileObj.ID, nil
}

// DeleteFile deletes a file from OpenAI.
func (m *Model) DeleteFile(ctx context.Context, fileID string, opts ...FileOption) error {
	fileOpts := &FileOptions{
		Path:   m.variantConfig.fileDeletionPath,
		Method: m.variantConfig.fileDeletionMethod,
	}
	for _, opt := range opts {
		opt(fileOpts)
	}
	fileOpts.Body = m.variantConfig.fileDeletionBodyConvertor(fileOpts.Body, fileID)
	// Create middleware to handle custom options.
	middlewareOpt := openaiopt.WithMiddleware(
		func(r *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
			if fileOpts.Path != "" {
				r.URL.Path = fileOpts.Path
			}
			// Set custom HTTP method if specified.
			if fileOpts.Method != "" {
				r.Method = fileOpts.Method
			}
			// Use custom body if specified.
			if fileOpts.Body != nil {
				r.Body = io.NopCloser(bytes.NewReader(fileOpts.Body))
				r.ContentLength = int64(len(fileOpts.Body))
			}
			return next(r)
		})

	_, err := m.client.Files.Delete(ctx, fileID, middlewareOpt)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

// GetFile retrieves file information from OpenAI.
func (m *Model) GetFile(
	ctx context.Context,
	fileID string,
	opts ...FileOption,
) (*openai.FileObject, error) {
	fileOpts := &FileOptions{
		Path: m.variantConfig.fileUploadPath,
	}
	for _, opt := range opts {
		opt(fileOpts)
	}
	// Create middleware to handle custom options.
	middlewareOpt := openaiopt.WithMiddleware(
		func(r *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
			// Set the correct path.
			r.URL.Path = fileOpts.Path
			// Set custom HTTP method if specified.
			if fileOpts.Method != "" {
				r.Method = fileOpts.Method
			}
			// Use custom body if specified.
			if fileOpts.Body != nil {
				r.Body = io.NopCloser(bytes.NewReader(fileOpts.Body))
				r.ContentLength = int64(len(fileOpts.Body))
			}
			return next(r)
		})
	fileObj, err := m.client.Files.Get(ctx, fileID, middlewareOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}
	return fileObj, nil
}
