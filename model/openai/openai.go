//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	"github.com/openai/openai-go/packages/respjson"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	functionToolType string = "function"

	// defaultBatchEndpoint is the default batch endpoint.
	defaultBatchEndpoint = openai.BatchNewParamsEndpointV1ChatCompletions
	//nolint:gosec
	deepSeekAPIKeyName     string = "DEEPSEEK_API_KEY"
	defaultDeepSeekBaseURL string = "https://api.deepseek.com"

	//nolint:gosec
	qwenAPIKeyName     string = "DASHSCOPE_API_KEY"
	defaultQwenBaseURL string = "https://dashscope.aliyuncs.com/compatible-mode/v1"
)

// Variant represents different model variants with specific behaviors.
type Variant string

const (
	// VariantOpenAI is the default OpenAI variant.
	VariantOpenAI Variant = "openai"
	// VariantHunyuan is the Hunyuan variant with specific file handling.
	VariantHunyuan Variant = "hunyuan"
	// VariantDeepSeek is the DeepSeek variant with specific base_url handling.
	VariantDeepSeek Variant = "deepseek"
	// VariantQwen is the Qwen variant with specific base_url handling.
	VariantQwen Variant = "qwen"
)

// thinkingValueConvertor converts ThinkingEnabled bool to the variant-specific value.
type thinkingValueConvertor func(enabled bool) any

// defaultThinkingValueConvertor returns the bool value as-is.
var defaultThinkingValueConvertor = func(enabled bool) any {
	return enabled
}

// deepSeekThinkingValueConvertor converts to DeepSeek 3.2 format: {"type": "enabled"/"disabled"}.
var deepSeekThinkingValueConvertor = func(enabled bool) any {
	const (
		thinkingTypeEnabled  = "enabled"
		thinkingTypeDisabled = "disabled"
	)
	thinkingType := thinkingTypeDisabled
	if enabled {
		thinkingType = thinkingTypeEnabled
	}
	return map[string]string{"type": thinkingType}
}

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

	// Default base URL for this variant.
	defaultBaseURL string
	// Default API key name for this variant.
	apiKeyName string
	// Thinking key for this variant.
	thinkingEnabledKey string
	// thinkingValueConvertor converts ThinkingEnabled to variant-specific format.
	thinkingValueConvertor thinkingValueConvertor
}
type fileDeletionBodyConvertor func(body []byte, fileID string) []byte

// defaultFileDeletionBodyConvertor is the default file deletion body converter.
var defaultFileDeletionBodyConvertor = func(body []byte, fileID string) []byte {
	return body
}

type fileUploadRequestConvertor func(r *http.Request, file *os.File, fileOpts *FileOptions) (*http.Request, error)

// variantConfigs maps variant names to their configurations.
var variantConfigs = map[Variant]variantConfig{
	VariantOpenAI: {
		fileUploadPath:            "/openapi/v1/files",
		filePurpose:               openai.FilePurposeUserData,
		fileDeletionMethod:        http.MethodDelete,
		skipFileTypeInContent:     false,
		fileDeletionBodyConvertor: defaultFileDeletionBodyConvertor,
		thinkingEnabledKey:        model.ThinkingEnabledKey,
		thinkingValueConvertor:    defaultThinkingValueConvertor,
	},
	VariantDeepSeek: {
		fileUploadPath:            "/openapi/v1/files",
		filePurpose:               openai.FilePurposeUserData,
		fileDeletionMethod:        http.MethodDelete,
		skipFileTypeInContent:     false,
		fileDeletionBodyConvertor: defaultFileDeletionBodyConvertor,
		apiKeyName:                deepSeekAPIKeyName,
		defaultBaseURL:            defaultDeepSeekBaseURL,
		// DeepSeek 3.2 uses {"thinking": {"type": "enabled"}} format.
		thinkingEnabledKey:     "thinking",
		thinkingValueConvertor: deepSeekThinkingValueConvertor,
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
		thinkingEnabledKey:     model.ThinkingEnabledKey,
		thinkingValueConvertor: defaultThinkingValueConvertor,
	},
	VariantQwen: {
		fileUploadPath:            "/openapi/v1/files",
		filePurpose:               openai.FilePurposeUserData,
		fileDeletionMethod:        http.MethodDelete,
		skipFileTypeInContent:     false,
		fileDeletionBodyConvertor: defaultFileDeletionBodyConvertor,
		apiKeyName:                qwenAPIKeyName,
		defaultBaseURL:            defaultQwenBaseURL,
		// refer:https://help.aliyun.com/zh/model-studio/deep-thinking
		thinkingEnabledKey:     model.EnabledThinkingKey,
		thinkingValueConvertor: defaultThinkingValueConvertor,
	},
}

// Model implements the model.Model interface for OpenAI API.
type Model struct {
	client                     openai.Client
	name                       string
	baseURL                    string
	apiKey                     string
	showToolCallDelta          bool
	channelBufferSize          int
	chatRequestCallback        ChatRequestCallbackFunc
	chatResponseCallback       ChatResponseCallbackFunc
	chatChunkCallback          ChatChunkCallbackFunc
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	extraFields                map[string]any
	variant                    Variant
	variantConfig              variantConfig
	batchCompletionWindow      openai.BatchNewParamsCompletionWindow
	batchMetadata              map[string]string
	batchBaseURL               string
	enableTokenTailoring       bool                    // Enable automatic token tailoring.
	maxInputTokens             int                     // Max input tokens for token tailoring.
	tokenCounter               model.TokenCounter      // Token counter for token tailoring.
	tailoringStrategy          model.TailoringStrategy // Tailoring strategy for token tailoring.
	// Token tailoring budget parameters (instance-level overrides).
	protocolOverheadTokens int
	reserveOutputTokens    int
	inputTokensFloor       int
	outputTokensFloor      int
	safetyMarginRatio      float64
	maxInputTokensRatio    float64

	accumulateChunkUsage AccumulateChunkUsage
}

// New creates a new OpenAI-like model.
func New(name string, opts ...Option) *Model {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	// Set default API key and base URL if not specified.
	if cfg, ok := variantConfigs[o.Variant]; ok {
		if val, ok := os.LookupEnv(cfg.apiKeyName); ok && o.APIKey == "" {
			o.APIKey = val
		}
		if cfg.defaultBaseURL != "" && o.BaseURL == "" {
			o.BaseURL = cfg.defaultBaseURL
		}
	}

	var clientOpts []openaiopt.RequestOption

	if o.APIKey != "" {
		clientOpts = append(clientOpts, openaiopt.WithAPIKey(o.APIKey))
	}

	if o.BaseURL != "" {
		clientOpts = append(clientOpts, openaiopt.WithBaseURL(o.BaseURL))
	}

	clientOpts = append(clientOpts, openaiopt.WithHTTPClient(model.DefaultNewHTTPClient(o.HTTPClientOptions...)))
	clientOpts = append(clientOpts, o.OpenAIOptions...)

	client := openai.NewClient(clientOpts...)

	if o.TailoringStrategy == nil {
		o.TailoringStrategy = model.NewMiddleOutStrategy(o.TokenCounter)
	}

	return &Model{
		client:                     client,
		name:                       name,
		baseURL:                    o.BaseURL,
		apiKey:                     o.APIKey,
		showToolCallDelta:          o.ShowToolCallDelta,
		channelBufferSize:          o.ChannelBufferSize,
		chatRequestCallback:        o.ChatRequestCallback,
		chatResponseCallback:       o.ChatResponseCallback,
		chatChunkCallback:          o.ChatChunkCallback,
		chatStreamCompleteCallback: o.ChatStreamCompleteCallback,
		extraFields:                o.ExtraFields,
		variant:                    o.Variant,
		variantConfig:              variantConfigs[o.Variant],
		batchCompletionWindow:      o.BatchCompletionWindow,
		batchMetadata:              o.BatchMetadata,
		batchBaseURL:               o.BatchBaseURL,
		enableTokenTailoring:       o.EnableTokenTailoring,
		tokenCounter:               o.TokenCounter,
		tailoringStrategy:          o.TailoringStrategy,
		maxInputTokens:             o.MaxInputTokens,
		protocolOverheadTokens:     o.TokenTailoringConfig.ProtocolOverheadTokens,
		reserveOutputTokens:        o.TokenTailoringConfig.ReserveOutputTokens,
		inputTokensFloor:           o.TokenTailoringConfig.InputTokensFloor,
		outputTokensFloor:          o.TokenTailoringConfig.OutputTokensFloor,
		safetyMarginRatio:          o.TokenTailoringConfig.SafetyMarginRatio,
		maxInputTokensRatio:        o.TokenTailoringConfig.MaxInputTokensRatio,
		accumulateChunkUsage:       o.accumulateChunkUsage,
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

	// Apply token tailoring if configured.
	m.applyTokenTailoring(ctx, request)

	responseChan := make(chan *model.Response, m.channelBufferSize)

	chatRequest, opts := m.buildChatRequest(request)

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

// applyTokenTailoring performs best-effort token tailoring if configured.
// It uses the token tailoring strategy defined in imodel package.
func (m *Model) applyTokenTailoring(ctx context.Context, request *model.Request) {
	// Early return if token tailoring is disabled or no messages to process.
	if !m.enableTokenTailoring || len(request.Messages) == 0 {
		return
	}

	// Determine max input tokens using priority: user config > auto calculation > default.
	maxInputTokens := m.maxInputTokens
	if maxInputTokens <= 0 {
		// Auto-calculate based on model context window with custom or default parameters.
		contextWindow := imodel.ResolveContextWindow(m.name)
		if m.protocolOverheadTokens > 0 || m.reserveOutputTokens > 0 {
			// Use custom parameters if any are set.
			maxInputTokens = imodel.CalculateMaxInputTokensWithParams(
				contextWindow,
				m.protocolOverheadTokens,
				m.reserveOutputTokens,
				m.inputTokensFloor,
				m.safetyMarginRatio,
				m.maxInputTokensRatio,
			)
		} else {
			// Use default parameters.
			maxInputTokens = imodel.CalculateMaxInputTokens(contextWindow)
		}
		log.DebugfContext(
			ctx,
			"auto-calculated max input tokens: model=%s, "+
				"contextWindow=%d, maxInputTokens=%d",
			m.name,
			contextWindow,
			maxInputTokens,
		)
	}

	// Apply token tailoring.
	tailored, err := m.tailoringStrategy.TailorMessages(ctx, request.Messages, maxInputTokens)
	if err != nil {
		log.WarnContext(
			ctx,
			"token tailoring failed in openai.Model",
			err,
		)
		return
	}

	request.Messages = tailored

	// Calculate remaining tokens for output based on context window.
	usedTokens, err := m.tokenCounter.CountTokensRange(ctx, request.Messages, 0, len(request.Messages))
	if err != nil {
		log.WarnContext(
			ctx,
			"failed to count tokens after tailoring",
			err,
		)
		return
	}

	// Set max output tokens only if user hasn't specified it.
	// This respects user's explicit configuration while providing a safe default.
	if request.GenerationConfig.MaxTokens == nil {
		contextWindow := imodel.ResolveContextWindow(m.name)
		var maxOutputTokens int
		if m.protocolOverheadTokens > 0 || m.outputTokensFloor > 0 {
			// Use custom parameters if any are set.
			maxOutputTokens = imodel.CalculateMaxOutputTokensWithParams(
				contextWindow,
				usedTokens,
				m.protocolOverheadTokens,
				m.outputTokensFloor,
				m.safetyMarginRatio,
			)
		} else {
			// Use default parameters.
			maxOutputTokens = imodel.CalculateMaxOutputTokens(contextWindow, usedTokens)
		}
		if maxOutputTokens > 0 {
			request.GenerationConfig.MaxTokens = &maxOutputTokens
			log.DebugfContext(
				ctx,
				"token tailoring: contextWindow=%d, usedTokens=%d, "+
					"maxOutputTokens=%d",
				contextWindow,
				usedTokens,
				maxOutputTokens,
			)
		}
	}
}

// buildChatRequest converts our Request to OpenAI request params and options.
func (m *Model) buildChatRequest(request *model.Request) (openai.ChatCompletionNewParams, []openaiopt.RequestOption) {
	chatRequest := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(m.name),
		Messages: m.convertMessages(request.Messages),
		Tools:    m.convertTools(request.Tools),
	}

	// Set response_format for native structured outputs when requested.
	if request.StructuredOutput != nil &&
		request.StructuredOutput.Type == model.StructuredOutputJSONSchema &&
		request.StructuredOutput.JSONSchema != nil {
		js := request.StructuredOutput.JSONSchema
		chatRequest.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        js.Name,
					Schema:      js.Schema,
					Strict:      openai.Bool(js.Strict),
					Description: openai.String(js.Description),
				},
			},
		}
	}

	// MaxTokens is deprecated and not compatible with o-series models.
	// Use MaxCompletionTokens instead.
	if request.MaxTokens != nil {
		chatRequest.MaxCompletionTokens = openai.Int(int64(*request.MaxTokens))
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
	opts := m.buildThinkingOption(request)
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
	return chatRequest, opts
}

// buildThinkingOption converts our Request to OpenAI request RequestOption.
func (m *Model) buildThinkingOption(request *model.Request) []openaiopt.RequestOption {
	var opts []openaiopt.RequestOption
	if request.ThinkingTokens != nil {
		opts = append(opts, openaiopt.WithJSONSet(model.ThinkingTokensKey, *request.ThinkingTokens))
	}
	if request.ThinkingEnabled == nil {
		return opts
	}
	// Use variant-specific key and value convertor.
	cfg := m.variantConfig
	key := cfg.thinkingEnabledKey
	if key == "" {
		key = model.ThinkingEnabledKey
	}
	convertor := cfg.thinkingValueConvertor
	if convertor == nil {
		convertor = defaultThinkingValueConvertor
	}
	opts = append(opts, openaiopt.WithJSONSet(key, convertor(*request.ThinkingEnabled)))
	return opts
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
			assistantMsg := &openai.ChatCompletionAssistantMessageParam{
				Content:   m.convertAssistantMessageContent(msg),
				ToolCalls: m.convertToolCalls(msg.ToolCalls),
			}
			// Pass reasoning_content to API if present (required by DeepSeek for
			// tool call scenarios within the same request turn).
			if msg.ReasoningContent != "" {
				assistantMsg.SetExtraFields(map[string]any{
					model.ReasoningContentKey: msg.ReasoningContent,
				})
			}
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfAssistant: assistantMsg,
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
				Description: openai.String(buildToolDescription(declaration)),
				Parameters:  parameters,
			},
		})
	}
	return result
}

// buildToolDescription builds the description for a tool.
// It appends the output schema to the description.
func buildToolDescription(declaration *tool.Declaration) string {
	desc := declaration.Description
	if declaration.OutputSchema == nil {
		return desc
	}
	schemaJSON, err := json.Marshal(declaration.OutputSchema)
	if err != nil {
		log.Errorf("marshal output schema for tool %s: %v", declaration.Name, err)
		return desc
	}
	desc += "\nOutput schema: " + string(schemaJSON)
	return desc
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
	// Aggregate reasoning deltas for final message fallback (some providers don't retain it in accumulator).
	var reasoningBuf bytes.Buffer

	for stream.Next() {
		chunk := stream.Current()

		// Skip empty chunks.
		if m.shouldSkipEmptyChunk(chunk) {
			continue
		}

		// Track ID -> Index mapping when ID is present (first chunk of each tool call).
		m.updateToolCallIndexMapping(chunk, idToIndexMap)

		// Always accumulate for correctness (tool call deltas are assembled later),
		// but skip chunks with reasoning content that would cause the SDK accumulator to panic.
		if !m.hasReasoningContent(chunk.Choices) {
			// Sanitize chunks before feeding them into the upstream accumulator to
			// avoid known panics when JSON.ToolCalls is marked present but the
			// typed ToolCalls slice is empty, especially on finish_reason chunks.
			sanitizedChunk := sanitizeChunkForAccumulator(chunk)

			acc.AddChunk(sanitizedChunk)
			if m.accumulateChunkUsage != nil {
				accUsage, chunkUsage := completionUsageToModelUsage(acc.Usage), completionUsageToModelUsage(chunk.Usage)
				usage := inverseOPENAISKDAddChunkUsage(accUsage, chunkUsage)
				usage = m.accumulateChunkUsage(usage, chunkUsage)
				acc.Usage = modelUsageToCompletionUsage(usage)

			}
		}

		// Aggregate reasoning delta (if any) for final response fallback.
		if len(chunk.Choices) > 0 {
			if reasoningContent := extractReasoningContent(chunk.Choices[0].Delta.JSON.ExtraFields); reasoningContent != "" {
				reasoningBuf.WriteString(reasoningContent)
			}
		}

		// Suppress chunks that carry no meaningful visible delta (including
		// tool_call deltas, which we'll surface only in the final response).
		// Note: reasoning content chunks are not suppressed even if they have no other content.
		if m.shouldSuppressChunk(chunk) {
			if !m.hasReasoningContent(chunk.Choices) {
				continue
			}
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
	m.sendFinalResponse(ctx, stream, acc, idToIndexMap, reasoningBuf.String(), responseChan)

	// Call the stream complete callback after final response is sent
	if m.chatStreamCompleteCallback != nil {
		var callbackAcc *openai.ChatCompletionAccumulator
		if stream.Err() == nil {
			callbackAcc = &acc
		}
		m.chatStreamCompleteCallback(ctx, &chatRequest, callbackAcc, stream.Err())
	}
}

// sanitizeChunkForAccumulator returns a defensive copy of the given chunk that
// avoids structures known to cause panics in the upstream OpenAI SDK
// accumulator. In particular, it clears JSON.ToolCalls metadata when it is
// marked present but the typed ToolCalls slice is empty on a finish_reason
// chunk, which would otherwise lead to an out-of-range access in
// chatCompletionResponseState.update.
func sanitizeChunkForAccumulator(chunk openai.ChatCompletionChunk) openai.ChatCompletionChunk {
	if len(chunk.Choices) == 0 {
		return chunk
	}

	choice := chunk.Choices[0]
	delta := choice.Delta

	// Only sanitize the specific pattern that is known to be unsafe for the
	// accumulator:
	//   - finish_reason is set (e.g. "tool_calls" or "stop")
	//   - JSON.ToolCalls is marked present
	//   - but the typed ToolCalls slice is empty
	if choice.FinishReason == "" ||
		!delta.JSON.ToolCalls.Valid() ||
		len(delta.ToolCalls) != 0 {
		return chunk
	}

	sanitized := chunk
	sanitized.Choices = make([]openai.ChatCompletionChunkChoice, len(chunk.Choices))
	copy(sanitized.Choices, chunk.Choices)

	// Clear the JSON metadata for ToolCalls on the first choice only. This
	// preserves finish_reason and usage semantics while preventing the
	// accumulator from treating this as a tool-call delta that must have at
	// least one element.
	sanitized.Choices[0].Delta.JSON.ToolCalls = respjson.Field{}

	return sanitized
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
	// Check for reasoning content - if present, don't suppress.
	if m.hasReasoningContent(chunk.Choices) {
		return false
	}

	choice := chunk.Choices[0]
	delta := choice.Delta

	// Any meaningful payload disables suppression.
	if delta.Content != "" {
		return false
	}

	// If this chunk is a tool_calls delta, optionally suppress emission.
	// By default we only expose tool calls in the final aggregated response
	// to avoid noisy blank chunks. When showToolCallDelta is enabled, treat
	// tool_call chunks as meaningful streaming payload.
	hasToolCall := delta.JSON.ToolCalls.Valid() ||
		len(delta.ToolCalls) > 0
	if hasToolCall {
		return !m.showToolCallDelta
	}

	if choice.FinishReason != "" {
		return false
	}
	return true
}

// shouldSkipEmptyChunk returns true when the chunk contains no meaningful delta.
// This is a defensive check against malformed responses from certain providers
// that may return chunks with valid JSON fields but empty actual content.
//
// The order of checks matters:
// 1. Check reasoning content first - if present, don't skip
// 2. Check content - if valid, don't skip (even if empty string)
// 3. Check refusal - if valid, don't skip
// 4. Check toolcalls - if valid but array is empty, skip (defensive against panic)
// 5. Check usage - if valid, don't skip
// 6. Otherwise, skip
func (m *Model) shouldSkipEmptyChunk(chunk openai.ChatCompletionChunk) bool {
	// Chunks that carry a finish reason are meaningful and should not be
	// skipped, even if they have no content or usage. This ensures that
	// streaming clients can observe termination semantics.
	if len(chunk.Choices) > 0 &&
		chunk.Choices[0].FinishReason != "" {
		return false
	}

	// No choices available, don't skip (let it be processed normally).
	if len(chunk.Choices) == 0 {
		return false
	}

	// Reasoning content is meaningful even if other fields are empty.
	if m.hasReasoningContent(chunk.Choices) {
		return false
	}

	// Extract delta for inspection.
	delta := chunk.Choices[0].Delta

	// Content or refusal indicates meaningful output.
	if delta.JSON.Content.Valid() || delta.JSON.Refusal.Valid() {
		return false
	}

	// Tool calls are only meaningful when the array is non-empty.
	if delta.JSON.ToolCalls.Valid() {
		return len(delta.ToolCalls) == 0
	}

	if chunk.Usage.CompletionTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.TotalTokens > 0 {
		return false
	}

	// Otherwise there is no meaningful delta, skip the chunk.
	return true
}

// hasReasoningContent checks if the choices contains reasoning content.
func (m *Model) hasReasoningContent(choices []openai.ChatCompletionChunkChoice) bool {
	if len(choices) == 0 {
		return false
	}
	return extractReasoningContent(choices[0].Delta.JSON.ExtraFields) != ""
}

// extractReasoningContent extracts reasoning content from ExtraFields.
// The extraFields parameter should be a map with values that have a Raw() method.
func extractReasoningContent(extraFields map[string]respjson.Field) string {
	if extraFields == nil {
		return ""
	}
	reasoningField, ok := extraFields[model.ReasoningContentKey]
	if !ok {
		return ""
	}
	reasoningStr, err := strconv.Unquote(reasoningField.Raw())
	if err == nil {
		return reasoningStr
	}
	return ""
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

		reasoningContent := extractReasoningContent(
			chunk.Choices[0].Delta.JSON.ExtraFields)
		var toolCalls []model.ToolCall
		if m.showToolCallDelta &&
			len(chunk.Choices[0].Delta.ToolCalls) > 0 {
			toolCalls = make(
				[]model.ToolCall, 0,
				len(chunk.Choices[0].Delta.ToolCalls))
			for _, toolCall := range chunk.Choices[0].Delta.ToolCalls {
				var indexPtr *int
				if toolCall.Index != 0 {
					index := int(toolCall.Index)
					indexPtr = &index
				}
				toolCalls = append(toolCalls, model.ToolCall{
					Type: string(toolCall.Type),
					Function: model.FunctionDefinitionParam{
						Name:      toolCall.Function.Name,
						Arguments: []byte(toolCall.Function.Arguments),
					},
					ID:    toolCall.ID,
					Index: indexPtr,
				})
			}
		}

		response.Choices[0].Delta = model.Message{
			Role:             model.RoleAssistant,
			Content:          chunk.Choices[0].Delta.Content,
			ReasoningContent: reasoningContent,
			ToolCalls:        toolCalls,
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
	aggregatedReasoning string,
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

		// If accumulator is empty but we have aggregated reasoning, create a response with it.
		if len(acc.Choices) == 0 && aggregatedReasoning != "" {
			finalResponse := &model.Response{
				Object:    model.ObjectTypeChatCompletion,
				ID:        acc.ID,
				Created:   acc.Created,
				Model:     acc.Model,
				Timestamp: time.Now(),
				Done:      true,
				IsPartial: false,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:             model.RoleAssistant,
							ReasoningContent: aggregatedReasoning,
						},
					},
				},
			}
			select {
			case responseChan <- finalResponse:
			case <-ctx.Done():
			}
			return
		}

		finalResponse := m.createFinalResponse(acc, hasToolCall, accumulatedToolCalls, aggregatedReasoning)

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

		// Some providers (e.g., gpt-5-nano) may omit the tool_call ID.
		// Synthesize a stable ID from the index to ensure proper pairing.
		synthesizedID := toolCall.ID
		if synthesizedID == "" {
			synthesizedID = fmt.Sprintf("auto_call_%d", originalIndex)
		}

		accumulatedToolCalls = append(accumulatedToolCalls, model.ToolCall{
			Index: func() *int { idx := originalIndex; return &idx }(),
			ID:    synthesizedID,
			Type:  functionToolType, // OpenAI supports function tools for now.
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
	aggregatedReasoning string,
) *model.Response {
	usage := completionUsageToModelUsage(acc.Usage)
	finalResponse := &model.Response{
		Object:    model.ObjectTypeChatCompletion,
		ID:        acc.ID,
		Created:   acc.Created,
		Model:     acc.Model,
		Choices:   make([]model.Choice, len(acc.Choices)),
		Usage:     &usage,
		Timestamp: time.Now(),
		Done:      !hasToolCall,
		IsPartial: false,
	}

	for i, choice := range acc.Choices {
		// Extract reasoning content from the accumulated message if available.
		reasoningContent := extractReasoningContent(choice.Message.JSON.ExtraFields)
		// Fallback to aggregated streaming deltas if accumulator didn't retain reasoning.
		if reasoningContent == "" && i == 0 && aggregatedReasoning != "" {
			reasoningContent = aggregatedReasoning
		}

		finalResponse.Choices[i] = model.Choice{
			Index: int(choice.Index),
			Message: model.Message{
				Role:             model.RoleAssistant,
				Content:          choice.Message.Content,
				ReasoningContent: reasoningContent,
			},
		}

		// If there are tool calls, add them to the final response.
		if hasToolCall && i == 0 { // Usually only the first choice contains tool calls.
			finalResponse.Choices[i].Message.ToolCalls = accumulatedToolCalls
		}

		// Propagate finish reason from the accumulated choice so that the final
		// aggregated response exposes the same termination semantics as the
		// underlying provider.
		if choice.FinishReason != "" {
			finishReason := choice.FinishReason
			finalResponse.Choices[i].FinishReason = &finishReason
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
	// Call response callback on successful completion.
	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, &chatRequest, chatCompletion)
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
			// Extract reasoning content from the message if available.
			reasoningContent := extractReasoningContent(choice.Message.JSON.ExtraFields)

			response.Choices[i] = model.Choice{
				Index: int(choice.Index),
				Message: model.Message{
					Role:             model.RoleAssistant,
					Content:          choice.Message.Content,
					ReasoningContent: reasoningContent,
				},
			}

			response.Choices[i].Message.ToolCalls = make([]model.ToolCall, len(choice.Message.ToolCalls))
			for j, toolCall := range choice.Message.ToolCalls {
				synthesizedID := toolCall.ID
				if synthesizedID == "" {
					// Synthesize ID for providers that omit it (e.g., gpt-5-nano).
					synthesizedID = fmt.Sprintf("auto_call_%d", j)
				}
				response.Choices[i].Message.ToolCalls[j] = model.ToolCall{
					ID:   synthesizedID,
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
		usage := completionUsageToModelUsage(chatCompletion.Usage)
		response.Usage = &usage
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
	// BaseURL override for this file request.
	BaseURL string
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

// WithFileBaseURL sets a per-request base URL override for file operations.
func WithFileBaseURL(url string) FileOption {
	return func(options *FileOptions) {
		options.BaseURL = url
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
			if fileOpts.Path != "" {
				r.URL.Path = fileOpts.Path
			}

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
	if fileOpts.BaseURL != "" {
		fileObj, err := m.client.Files.New(ctx, fileParams, middlewareOpt, openaiopt.WithBaseURL(fileOpts.BaseURL))
		if err != nil {
			return "", fmt.Errorf("failed to upload file: %w", err)
		}
		return fileObj.ID, nil
	}
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
		File: nil, // Set to nil to avoid duplicate multipart form construction by SDK.
		// The middleware will handle all request body construction to ensure proper
		// filename preservation and field ordering required by Venus platform.
		Purpose: fileOpts.Purpose,
	}

	// Create middleware to handle custom options.
	middlewareOpt := openaiopt.WithMiddleware(
		func(r *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
			// Set the correct path.
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
			} else {
				// Build multipart form to ensure filename suffix is preserved.
				buf := &bytes.Buffer{}
				w := multipart.NewWriter(buf)
				// purpose.
				if err := w.WriteField("purpose", string(fileOpts.Purpose)); err != nil {
					return nil, fmt.Errorf("failed to write purpose field: %w", err)
				}
				// file.
				part, err := w.CreateFormFile("file", filename)
				if err != nil {
					return nil, fmt.Errorf("failed to create form file: %w", err)
				}
				if _, err := part.Write(data); err != nil {
					return nil, fmt.Errorf("failed to write file data: %w", err)
				}
				if err := w.Close(); err != nil {
					return nil, fmt.Errorf("failed to close multipart writer: %w", err)
				}
				r.Body = io.NopCloser(buf)
				r.Header.Set("Content-Type", w.FormDataContentType())
				r.ContentLength = int64(buf.Len())
			}
			return next(r)
		})

	// Upload the file.
	if fileOpts.BaseURL != "" {
		fileObj, err := m.client.Files.New(ctx, fileParams, middlewareOpt, openaiopt.WithBaseURL(fileOpts.BaseURL))
		if err != nil {
			return "", fmt.Errorf("failed to upload file data: %w", err)
		}
		return fileObj.ID, nil
	}
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
	fileObj, err := m.client.Files.Get(ctx, fileID, middlewareOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}
	return fileObj, nil
}
