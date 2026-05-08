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
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/respjson"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
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
	deepSeekAPIHost        string = "api.deepseek.com"

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
	// It backfills empty reasoning_content for assistant history messages by
	// default, including when inferred from the default DeepSeek base URL. Use
	// WithReasoningContentBackfill(false) to disable this behavior.
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

// deepSeekThinkingValueConvertor converts to the DeepSeek thinking-toggle
// format introduced in v3.2 and reused by v4 (e.g. deepseek-v4-pro /
// deepseek-v4-flash): {"type": "enabled"/"disabled"}.
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
	// Whether user message content must be reduced to text only.
	textOnlyMessageContent bool

	// Default base URL for this variant.
	defaultBaseURL string
	// Default API key name for this variant.
	apiKeyName string
	// Thinking key for this variant.
	thinkingEnabledKey string
	// thinkingValueConvertor converts ThinkingEnabled to variant-specific format.
	thinkingValueConvertor thinkingValueConvertor

	// defaultOptimizeForCache controls the default value for cache optimization
	// when WithOptimizeForCache is not explicitly set.
	defaultOptimizeForCache bool
	// defaultReasoningContentBackfill controls replay-time empty
	// reasoning_content backfill for assistant messages.
	defaultReasoningContentBackfill bool
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
		defaultOptimizeForCache:   true,
	},
	VariantDeepSeek: {
		fileUploadPath:            "/openapi/v1/files",
		filePurpose:               openai.FilePurposeUserData,
		fileDeletionMethod:        http.MethodDelete,
		skipFileTypeInContent:     false,
		textOnlyMessageContent:    true,
		fileDeletionBodyConvertor: defaultFileDeletionBodyConvertor,
		apiKeyName:                deepSeekAPIKeyName,
		defaultBaseURL:            defaultDeepSeekBaseURL,
		// DeepSeek v3.2+ (incl. v4-pro / v4-flash) uses
		// {"thinking": {"type": "enabled"/"disabled"}} format.
		thinkingEnabledKey:              "thinking",
		thinkingValueConvertor:          deepSeekThinkingValueConvertor,
		defaultReasoningContentBackfill: true,
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
	chatRequestJSONCallback    ChatRequestJSONCallbackFunc
	chatResponseCallback       ChatResponseCallbackFunc
	chatChunkCallback          ChatChunkCallbackFunc
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	extraFields                map[string]any
	variant                    Variant
	variantConfig              variantConfig
	reasoningContentBackfill   bool
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
	optimizeForCache     bool // Optimize message structure for prompt caching
	omitFileContentParts bool
}

// New creates a new OpenAI-like model.
func New(name string, opts ...Option) *Model {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}
	if !o.variantSet {
		o.Variant = inferVariant(o.BaseURL)
	}

	cfg, cfgOK := variantConfigs[o.Variant]
	if !o.optimizeForCacheSet {
		o.OptimizeForCache = cfgOK && cfg.defaultOptimizeForCache
	}
	if !o.reasoningContentBackfillSet {
		o.ReasoningContentBackfill = cfgOK && cfg.defaultReasoningContentBackfill
	}

	// Set default API key and base URL if not specified.
	if cfgOK {
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
		chatRequestJSONCallback:    o.ChatRequestJSONCallback,
		chatResponseCallback:       o.ChatResponseCallback,
		chatChunkCallback:          o.ChatChunkCallback,
		chatStreamCompleteCallback: o.ChatStreamCompleteCallback,
		extraFields:                o.ExtraFields,
		variant:                    o.Variant,
		variantConfig:              variantConfigs[o.Variant],
		reasoningContentBackfill:   o.ReasoningContentBackfill,
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
		optimizeForCache:           o.OptimizeForCache,
		omitFileContentParts:       o.OmitFileContentParts,
	}
}

func inferVariant(baseURL string) Variant {
	if isDeepSeekBaseURL(baseURL) {
		return VariantDeepSeek
	}
	return VariantOpenAI
}

func isDeepSeekBaseURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), deepSeekAPIHost)
}

// Info implements the model.Model interface.
func (m *Model) Info() model.Info {
	return model.Info{
		Name: m.name,
	}
}

func (m *Model) runChatRequestCallback(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
) {
	if m.chatRequestCallback == nil {
		return
	}
	defer imodel.RecoverCallbackPanic(ctx, "chat request callback")
	m.chatRequestCallback(ctx, chatRequest)
}

func (m *Model) runChatRequestJSONCallback(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
) {
	if m.chatRequestJSONCallback == nil {
		return
	}

	var (
		raw []byte
		err error
	)
	if chatRequest != nil {
		raw, err = chatRequest.MarshalJSON()
	}

	defer imodel.RecoverCallbackPanic(
		ctx,
		"chat request json callback",
	)
	m.chatRequestJSONCallback(ctx, raw, err)
}

func (m *Model) runChatResponseCallback(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatResponse *openai.ChatCompletion,
) {
	if m.chatResponseCallback == nil {
		return
	}
	defer imodel.RecoverCallbackPanic(ctx, "chat response callback")
	m.chatResponseCallback(ctx, chatRequest, chatResponse)
}

func (m *Model) runChatChunkCallback(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatChunk *openai.ChatCompletionChunk,
) {
	if m.chatChunkCallback == nil {
		return
	}
	defer imodel.RecoverCallbackPanic(ctx, "chat chunk callback")
	m.chatChunkCallback(ctx, chatRequest, chatChunk)
}

// prepareChatRequest validates and mutates the request in-place before sending it to the provider.
func (m *Model) prepareChatRequest(
	ctx context.Context,
	request *model.Request,
) (*openai.ChatCompletionNewParams, []openaiopt.RequestOption, error) {
	if request == nil {
		return nil, nil, errors.New("request cannot be nil")
	}
	// Optimize message structure for cache if enabled.
	if m.optimizeForCache {
		request.Messages = m.optimizeMessagesForCache(request.Messages)
	}
	// Apply token tailoring if configured.
	m.applyTokenTailoring(ctx, request)
	chatRequest, opts := m.buildChatRequest(request)
	return chatRequest, opts, nil
}

// GenerateContent implements the model.Model interface.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	chatRequest, opts, err := m.prepareChatRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	// Execute callback synchronously before starting the goroutine
	// to avoid a race where the runner and HTTP handler finish
	// (closing the SSE writer) while the callback is still running.
	m.runChatRequestCallback(ctx, chatRequest)
	m.runChatRequestJSONCallback(ctx, chatRequest)
	responseChan := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responseChan)
		if request.Stream {
			m.handleStreamingResponse(ctx, *chatRequest, responseChan, opts...)
		} else {
			m.handleNonStreamingResponse(ctx, *chatRequest, responseChan, opts...)
		}
	}()
	return responseChan, nil
}

// GenerateContentIter implements the model.IterModel interface.
func (m *Model) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	chatRequest, opts, err := m.prepareChatRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	return func(yield func(*model.Response) bool) {
		m.runChatRequestCallback(ctx, chatRequest)
		m.runChatRequestJSONCallback(ctx, chatRequest)
		emit := func(resp *model.Response) bool {
			if ctx.Err() != nil {
				return false
			}
			return yield(resp)
		}
		if request.Stream {
			m.handleStreamingResponseWithEmitter(ctx, *chatRequest, emit, opts...)
			return
		}
		m.handleNonStreamingResponseWithEmitter(ctx, *chatRequest, emit, opts...)
	}, nil
}

// optimizeMessagesForCache reorders messages to improve cache hit rates.
// System messages are moved to the front as they are most likely to be cached.
func (m *Model) optimizeMessagesForCache(messages []model.Message) []model.Message {
	if len(messages) == 0 {
		return messages
	}

	var systemMsgs, otherMsgs []model.Message

	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			systemMsgs = append(systemMsgs, msg)
		} else {
			otherMsgs = append(otherMsgs, msg)
		}
	}

	// If no reordering needed, return original
	if len(systemMsgs) == 0 {
		return messages
	}

	// System messages first, then other messages
	return append(systemMsgs, otherMsgs...)
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
	outputReserveTokens := m.effectiveOutputReserveTokens(request)
	contextWindow := imodel.ResolveContextWindow(m.name)
	autoBudget := maxInputTokens <= 0
	if autoBudget {
		// Auto-calculate based on model context window with custom or default parameters.
		if m.protocolOverheadTokens > 0 || m.reserveOutputTokens > 0 {
			// Use custom parameters if any are set.
			maxInputTokens = imodel.CalculateMaxInputTokensWithParams(
				contextWindow,
				m.protocolOverheadTokens,
				outputReserveTokens,
				m.inputTokensFloor,
				m.safetyMarginRatio,
				m.maxInputTokensRatio,
			)
		} else {
			// Use default parameters.
			maxInputTokens = imodel.CalculateMaxInputTokensWithParams(
				contextWindow,
				imodel.DefaultProtocolOverheadTokens,
				outputReserveTokens,
				imodel.DefaultInputTokensFloor,
				imodel.DefaultSafetyMarginRatio,
				imodel.DefaultMaxInputTokensRatio,
			)
		}
	}

	maxInputTokens = min(maxInputTokens, m.hardInputBudget(contextWindow, outputReserveTokens))
	if autoBudget {
		log.DebugfContext(
			ctx,
			"auto-calculated max input tokens: model=%s, "+
				"contextWindow=%d, reserveOutputTokens=%d, maxInputTokens=%d",
			m.name,
			contextWindow,
			outputReserveTokens,
			maxInputTokens,
		)
		toolsTokens := m.estimateToolsTokens(ctx, request.Tools)
		if toolsTokens > 0 {
			maxInputTokens = max(maxInputTokens-toolsTokens, 0)
			log.DebugfContext(
				ctx,
				"adjusted max input tokens after tools budget: model=%s, "+
					"toolsTokens=%d, maxInputTokens=%d",
				m.name,
				toolsTokens,
				maxInputTokens,
			)
		}
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
}

func (m *Model) effectiveOutputReserveTokens(request *model.Request) int {
	reserve := m.reserveOutputTokens
	if reserve <= 0 {
		reserve = imodel.DefaultReserveOutputTokens
	}
	if request == nil {
		return reserve
	}
	if request.MaxTokens != nil && *request.MaxTokens > reserve {
		reserve = *request.MaxTokens
	}
	if request.ThinkingTokens != nil && *request.ThinkingTokens > reserve {
		reserve = *request.ThinkingTokens
	}
	return reserve
}

func (m *Model) hardInputBudget(contextWindow, outputReserveTokens int) int {
	protocolOverheadTokens := m.protocolOverheadTokens
	if protocolOverheadTokens <= 0 {
		protocolOverheadTokens = imodel.DefaultProtocolOverheadTokens
	}
	safetyMarginRatio := m.safetyMarginRatio
	if safetyMarginRatio <= 0 {
		safetyMarginRatio = imodel.DefaultSafetyMarginRatio
	}
	safetyMargin := int(float64(contextWindow) * safetyMarginRatio)
	return max(contextWindow-outputReserveTokens-protocolOverheadTokens-safetyMargin, 0)
}

func (m *Model) estimateToolsTokens(
	ctx context.Context,
	tools map[string]tool.Tool,
) int {
	if len(tools) == 0 || m.tokenCounter == nil {
		return 0
	}
	converted := m.convertTools(tools)
	if len(converted) == 0 {
		return 0
	}
	raw, err := json.Marshal(converted)
	if err != nil {
		log.WarnContext(ctx, "failed to marshal tools for token tailoring", err)
		return 0
	}
	tokens, err := m.tokenCounter.CountTokens(ctx, model.Message{
		Role:    model.RoleSystem,
		Content: string(raw),
	})
	if err != nil {
		log.WarnContext(ctx, "failed to count tools tokens for token tailoring", err)
		return 0
	}
	return tokens
}

// buildChatRequest converts our Request to OpenAI request params and options.
func (m *Model) buildChatRequest(request *model.Request) (*openai.ChatCompletionNewParams, []openaiopt.RequestOption) {
	chatRequest := &openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(m.name),
		Messages: m.convertMessages(request.Messages),
		Tools:    m.convertTools(request.Tools),
	}

	// Set response_format for native structured outputs when requested.
	if request.StructuredOutput != nil &&
		request.StructuredOutput.Type == model.StructuredOutputJSONSchema &&
		request.StructuredOutput.JSONSchema != nil {
		js := request.StructuredOutput.JSONSchema
		if m.variant == VariantDeepSeek {
			jsonObject := shared.NewResponseFormatJSONObjectParam()
			chatRequest.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
				OfJSONObject: &jsonObject,
			}
		} else {
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
		if len(request.Tools) > 0 {
			// Parallel tool calls can interfere with strict JSON schema
			// output.
			chatRequest.ParallelToolCalls = openai.Bool(false)
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
	// Add model-level extra fields to the request.
	for key, value := range m.extraFields {
		opts = append(opts, openaiopt.WithJSONSet(key, value))
	}
	// Add request-level extra fields after model-level fields so they take precedence.
	for key, value := range request.ExtraFields {
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
//
// Note on default behavior: when request.ThinkingEnabled is nil, this function
// does not emit any thinking-toggle field in the outgoing request. The
// upstream provider then applies its server-side default (e.g. DeepSeek v4
// defaults to thinking "enabled"). Callers that want a deterministic on/off
// behavior must set ThinkingEnabled explicitly.
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

// shouldBackfillReasoningContent reports whether replay should emit
// model.ReasoningContentKey as an empty string for assistant messages. Some
// providers require the key to be present for every assistant message in
// thinking mode even when no reasoning text was returned.
func (m *Model) shouldBackfillReasoningContent(
	msg model.Message,
) bool {
	return m.reasoningContentBackfill &&
		msg.Role == model.RoleAssistant &&
		msg.ReasoningContent == "" &&
		(msg.Content != "" || len(msg.ContentParts) > 0 || len(msg.ToolCalls) > 0)
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
			// Pass reasoning_content to API if present, or when provider replay
			// requires the field for assistant history in thinking mode.
			if msg.ReasoningContent != "" ||
				m.shouldBackfillReasoningContent(msg) {
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

// convertUserMessageContent converts a message into an OpenAI user
// message content union.
func (m *Model) convertUserMessageContent(
	msg model.Message,
) (openai.ChatCompletionUserMessageParamContentUnion, map[string]any) {
	// If there are no content parts and Content is not empty, return as string.
	if len(msg.ContentParts) == 0 && msg.Content != "" {
		return openai.ChatCompletionUserMessageParamContentUnion{
			OfString: openai.String(msg.Content),
		}, nil
	}

	fileHint := m.userFileHint(msg)
	contentParts := make(
		[]openai.ChatCompletionContentPartUnionParam,
		0,
		len(msg.ContentParts)+2,
	)
	if msg.Content != "" {
		contentParts = append(contentParts, userTextPart(msg.Content))
	}
	if fileHint != "" {
		contentParts = append(contentParts, userTextPart(fileHint))
	}
	if omittedHint := m.omittedContentHint(msg.ContentParts); omittedHint != "" {
		contentParts = append(contentParts, userTextPart(omittedHint))
	}
	extraFields := m.appendUserContentParts(&contentParts, msg.ContentParts)

	if strings.TrimSpace(msg.Content) == "" &&
		fileHint != "" &&
		onlyFileContentParts(msg.ContentParts) {
		return openai.ChatCompletionUserMessageParamContentUnion{
			OfArrayOfContentParts: contentParts,
		}, extraFields
	}

	if content, ok := singleUserContentString(
		msg.Content,
		fileHint,
		contentParts,
		extraFields,
	); ok {
		return openai.ChatCompletionUserMessageParamContentUnion{
			OfString: openai.String(content),
		}, nil
	}

	return openai.ChatCompletionUserMessageParamContentUnion{
		OfArrayOfContentParts: contentParts,
	}, extraFields
}

func (m *Model) userFileHint(msg model.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return ""
	}
	if !m.omitFileContentParts &&
		!m.variantConfig.skipFileTypeInContent &&
		!onlyInternalFileContentParts(msg.ContentParts) {
		return ""
	}
	if !onlyFileContentParts(msg.ContentParts) {
		return ""
	}
	return fileHintForContentParts(msg.ContentParts)
}

func onlyFileContentParts(parts []model.ContentPart) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part.Type != model.ContentTypeFile {
			return false
		}
	}
	return true
}

func onlyInternalFileContentParts(parts []model.ContentPart) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part.Type != model.ContentTypeFile || part.File == nil {
			return false
		}
		if !isInternalOnlyFile(part.File) {
			return false
		}
	}
	return true
}

func userTextPart(text string) openai.ChatCompletionContentPartUnionParam {
	return openai.ChatCompletionContentPartUnionParam{
		OfText: &openai.ChatCompletionContentPartTextParam{
			Text: text,
		},
	}
}

func (m *Model) appendUserContentParts(
	dst *[]openai.ChatCompletionContentPartUnionParam,
	parts []model.ContentPart,
) map[string]any {
	var extraFields map[string]any
	for _, part := range parts {
		if m.variantConfig.textOnlyMessageContent &&
			part.Type != model.ContentTypeText {
			continue
		}
		if part.Type == model.ContentTypeFile &&
			m.omitFileContentParts {
			continue
		}
		if part.Type == model.ContentTypeFile &&
			part.File != nil &&
			isInternalOnlyFile(part.File) {
			continue
		}
		if part.Type == model.ContentTypeFile &&
			m.variantConfig.skipFileTypeInContent {
			extraFields = appendFileID(extraFields, part)
			continue
		}
		contentPart := m.convertContentPart(part)
		if contentPart == nil {
			continue
		}
		*dst = append(*dst, *contentPart)
	}
	return extraFields
}

func (m *Model) omittedContentHint(parts []model.ContentPart) string {
	if !m.variantConfig.textOnlyMessageContent {
		return ""
	}

	var imageCount, audioCount, fileCount int
	for _, part := range parts {
		switch part.Type {
		case model.ContentTypeImage:
			imageCount++
		case model.ContentTypeAudio:
			audioCount++
		case model.ContentTypeFile:
			fileCount++
		}
	}
	return omittedAttachmentHint(imageCount, audioCount, fileCount)
}

func omittedAttachmentHint(
	imageCount int,
	audioCount int,
	fileCount int,
) string {
	const (
		omittedHintPrefix  = "Omitted non-text attachments for this provider: "
		omittedHintSuffix  = "."
		omittedImageSingle = "1 image"
		omittedImagePlural = "%d images"
		omittedAudioSingle = "1 audio clip"
		omittedAudioPlural = "%d audio clips"
		omittedFileSingle  = "1 file"
		omittedFilePlural  = "%d files"
	)

	parts := make([]string, 0, 3)
	if imageCount == 1 {
		parts = append(parts, omittedImageSingle)
	} else if imageCount > 1 {
		parts = append(parts, fmt.Sprintf(omittedImagePlural, imageCount))
	}
	if audioCount == 1 {
		parts = append(parts, omittedAudioSingle)
	} else if audioCount > 1 {
		parts = append(parts, fmt.Sprintf(omittedAudioPlural, audioCount))
	}
	if fileCount == 1 {
		parts = append(parts, omittedFileSingle)
	} else if fileCount > 1 {
		parts = append(parts, fmt.Sprintf(omittedFilePlural, fileCount))
	}
	if len(parts) == 0 {
		return ""
	}
	return omittedHintPrefix + strings.Join(parts, ", ") + omittedHintSuffix
}

func appendFileID(
	extraFields map[string]any,
	part model.ContentPart,
) map[string]any {
	if part.File == nil || !isProviderFileID(part.File.FileID) {
		return extraFields
	}
	const fileIDsKey = "file_ids"
	if extraFields == nil {
		extraFields = make(map[string]any)
	}
	fileIDs, ok := extraFields[fileIDsKey].([]string)
	if !ok {
		fileIDs = []string{}
	}
	fileIDs = append(fileIDs, part.File.FileID)
	extraFields[fileIDsKey] = fileIDs
	return extraFields
}

func singleUserContentString(
	mainText string,
	fileHint string,
	contentParts []openai.ChatCompletionContentPartUnionParam,
	extraFields map[string]any,
) (string, bool) {
	if extraFields != nil {
		return "", false
	}
	if len(contentParts) != 1 {
		return "", false
	}
	if contentParts[0].OfText == nil {
		return "", false
	}

	text := contentParts[0].OfText.Text
	if mainText != "" && text == mainText {
		return mainText, true
	}
	if mainText == "" && fileHint != "" && text == fileHint {
		return fileHint, true
	}
	return "", false
}

func fileHintForContentParts(parts []model.ContentPart) string {
	const (
		fileHintDefaultName = "attachment"
		fileHintSingleFmt   = "Uploaded file: %s (available to tools)."
		fileHintMultiFmt    = "Uploaded files: %s (available to tools)."
	)

	var names []string
	for _, part := range parts {
		if part.Type != model.ContentTypeFile || part.File == nil {
			continue
		}
		name := safeFileHintName(part.File)
		if name == "" {
			name = fileHintDefaultName
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return fmt.Sprintf(fileHintSingleFmt, names[0])
	}
	return fmt.Sprintf(fileHintMultiFmt, strings.Join(names, ", "))
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
			params, ok := fileToParamsOK(part.File)
			if !ok {
				return nil
			}
			return &openai.ChatCompletionContentPartUnionParam{
				OfFile: &openai.ChatCompletionContentPartFileParam{
					File: params,
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

func isProviderFileID(fileID string) bool {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return false
	}
	return !fileref.IsInternalFileRef(id)
}

func isInternalOnlyFile(file *model.File) bool {
	if file == nil {
		return false
	}
	return fileref.IsInternalFileRef(file.FileID) &&
		len(file.Data) == 0
}

func safeFileHintName(file *model.File) string {
	if file == nil {
		return ""
	}
	name := strings.TrimSpace(file.Name)
	if name != "" {
		return name
	}
	if display := fileref.DisplayName(file.FileID); display != "" {
		return display
	}
	if isProviderFileID(file.FileID) {
		return strings.TrimSpace(file.FileID)
	}
	return ""
}

func fileToParamsOK(
	file *model.File,
) (openai.ChatCompletionContentPartFileFileParam, bool) {
	if file == nil {
		return openai.ChatCompletionContentPartFileFileParam{}, false
	}
	if isProviderFileID(file.FileID) {
		return openai.ChatCompletionContentPartFileFileParam{
			FileID: openai.String(file.FileID),
		}, true
	}
	if len(file.Data) == 0 {
		return openai.ChatCompletionContentPartFileFileParam{}, false
	}
	const (
		fileDataPrefix = "data:"
		fileDataBase64 = ";base64,"
	)
	encoded := base64.StdEncoding.EncodeToString(file.Data)
	fileData := fileDataPrefix + file.MimeType + fileDataBase64 +
		encoded
	return openai.ChatCompletionContentPartFileFileParam{
		FileData: openai.String(fileData),
		Filename: openai.String(file.Name),
	}, true
}

func fileToParams(file *model.File) openai.ChatCompletionContentPartFileFileParam {
	params, _ := fileToParamsOK(file)
	return params
}

func audioToBase64(audio *model.Audio) string {
	return "data:" + audio.Format + ";base64," + base64.StdEncoding.EncodeToString(audio.Data)
}

func (m *Model) convertToolCalls(toolCalls []model.ToolCall) []openai.ChatCompletionMessageToolCallParam {
	var result []openai.ChatCompletionMessageToolCallParam
	for _, toolCall := range toolCalls {
		param := openai.ChatCompletionMessageToolCallParam{
			ID: toolCall.ID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      toolCall.Function.Name,
				Arguments: string(toolCall.Function.Arguments),
			},
		}
		// Pass through ExtraFields transparently (e.g., Gemini 3's thought_signature).
		if len(toolCall.ExtraFields) > 0 {
			param.SetExtraFields(toolCall.ExtraFields)
		}
		result = append(result, param)
	}
	return result
}

func (m *Model) convertTools(tools map[string]tool.Tool) []openai.ChatCompletionToolParam {
	// Extract and sort tool names for stable ordering to improve cache hit rate
	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	// Build tools in sorted order
	var result []openai.ChatCompletionToolParam
	for _, name := range toolNames {
		tool := tools[name]
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
		// Some OpenAI-compatible proxies require object schemas to include
		// a `properties` key, even when the tool takes no arguments.
		if typ, ok := parameters["type"].(string); ok && typ == "object" {
			if props, exists := parameters["properties"]; !exists || props == nil {
				parameters["properties"] = map[string]any{}
			}
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
	emitter := func(resp *model.Response) bool {
		select {
		case responseChan <- resp:
			return true
		case <-ctx.Done():
			return false
		}
	}
	m.handleStreamingResponseWithEmitter(ctx, chatRequest, emitter, opts...)
}

// responseEmitter emits a response and returns false to stop streaming.
type responseEmitter func(*model.Response) bool

// handleStreamingResponseWithEmitter handles streaming chat completion responses.
// It returns early when emit returns false.
func (m *Model) handleStreamingResponseWithEmitter(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	emit responseEmitter,
	opts ...openaiopt.RequestOption,
) {
	stream := m.client.Chat.Completions.NewStreaming(ctx, chatRequest, opts...)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}
	// Track ID -> Index mapping.
	idToIndexMap := make(map[string]int)
	// Track ExtraFields by tool call ID (SDK accumulator doesn't preserve ExtraFields).
	extraFieldsMap := make(map[string]map[string]any)
	// Aggregate reasoning deltas for final message fallback (some providers don't retain it in accumulator).
	var reasoningBuf bytes.Buffer
	// Track next available index for tool calls (for providers that don't set correct indices).
	nextToolCallIndex := 0

	for stream.Next() {
		chunk := stream.Current()

		// Skip empty chunks.
		if m.shouldSkipEmptyChunk(chunk) {
			continue
		}

		// Fix tool call indices for providers that return all indices as 0.
		// This must be done before updateToolCallIndexMapping and accumulation.
		chunk = fixToolCallIndices(chunk, idToIndexMap, &nextToolCallIndex)

		// Collect ExtraFields from chunk tool_calls (SDK accumulator doesn't preserve ExtraFields).
		m.collectExtraFieldsFromChunk(chunk, extraFieldsMap)

		// Track ID -> Index mapping when ID is present (first chunk of each tool call).
		m.updateToolCallIndexMapping(chunk, idToIndexMap)

		// Accumulate chunk for correctness. When a chunk mixes reasoning with
		// content or tool-call deltas, strip only the reasoning metadata before
		// passing it to the SDK accumulator.
		m.accumulateChunk(chunk, &acc, &reasoningBuf)

		// Suppress chunks that carry no meaningful visible delta (including
		// tool_call deltas, which we'll surface only in the final response).
		// Note: reasoning content chunks are not suppressed even if they have no other content.
		if m.shouldSuppressChunk(chunk) {
			if !m.hasReasoningContent(chunk.Choices) {
				continue
			}
		}

		m.runChatChunkCallback(ctx, &chatRequest, &chunk)

		if !emit(m.createPartialResponse(chunk)) {
			if err := ctx.Err(); err != nil {
				m.handleStreamCompleteCallback(ctx, chatRequest, acc, err)
			}
			return
		}
	}

	// Call the stream complete callback before the final response is emitted.
	m.handleStreamCompleteCallback(ctx, chatRequest, acc, stream.Err())

	m.emitStreamingFinalResponse(ctx, stream, acc, idToIndexMap, extraFieldsMap, reasoningBuf.String(), emit)
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

// stripReasoningFromChunkForAccumulator returns a defensive copy of the chunk
// with reasoning-only ExtraFields removed from the first choice delta. This
// lets the upstream accumulator keep non-reasoning payloads from mixed chunks
// without mutating the original chunk.
func stripReasoningFromChunkForAccumulator(
	chunk openai.ChatCompletionChunk,
) (openai.ChatCompletionChunk, bool) {
	if len(chunk.Choices) == 0 {
		return chunk, false
	}

	extraFields := chunk.Choices[0].Delta.JSON.ExtraFields
	if extractReasoningContent(extraFields) == "" {
		return chunk, false
	}

	stripped := chunk
	stripped.Choices = make([]openai.ChatCompletionChunkChoice, len(chunk.Choices))
	copy(stripped.Choices, chunk.Choices)
	stripped.Choices[0].Delta.JSON.ExtraFields =
		cloneRespJSONFieldMap(extraFields)
	delete(
		stripped.Choices[0].Delta.JSON.ExtraFields,
		model.ReasoningContentKey,
	)
	delete(
		stripped.Choices[0].Delta.JSON.ExtraFields,
		model.ReasoningContentKeyAlt,
	)
	return stripped, true
}

// hasAccumulatorPayloadBeyondReasoning reports whether the stripped chunk still
// contains payload the SDK accumulator must keep, such as content, refusal,
// tool calls, finish reasons, or usage. Pure reasoning-only chunks
// intentionally keep the old behavior and stay out of the accumulator.
func hasAccumulatorPayloadBeyondReasoning(
	chunk openai.ChatCompletionChunk,
) bool {
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			return true
		}

		delta := choice.Delta
		if delta.JSON.Content.Valid() || delta.JSON.Refusal.Valid() {
			return true
		}
		if len(delta.ToolCalls) > 0 {
			return true
		}
	}

	return chunk.Usage.CompletionTokens > 0 ||
		chunk.Usage.PromptTokens > 0 ||
		chunk.Usage.TotalTokens > 0
}

type toolCallIndexState struct {
	idToIndexMap map[string]int
	indexToID    map[int64]string
	nextIndex    *int
}

func buildIndexToIDMap(
	idToIndexMap map[string]int,
	toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall,
) map[int64]string {
	indexToID := make(map[int64]string, len(toolCalls)+len(idToIndexMap))
	for id, idx := range idToIndexMap {
		indexToID[int64(idx)] = id
	}
	return indexToID
}

func checkIfIndexFixNeeded(
	toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall,
	idToIndexMap map[string]int,
	indexToID map[int64]string,
) bool {
	for _, tc := range toolCalls {
		if tc.ID == "" {
			continue
		}
		if existingIndex, exists := idToIndexMap[tc.ID]; exists {
			if tc.Index != int64(existingIndex) {
				return true
			}
			indexToID[tc.Index] = tc.ID
			continue
		}
		if existingID, exists := indexToID[tc.Index]; exists && existingID != tc.ID {
			return true
		}
		indexToID[tc.Index] = tc.ID
	}
	return false
}

func updateIDToIndexMapFromToolCalls(
	toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall,
	idToIndexMap map[string]int,
	nextIndex *int,
) {
	for _, tc := range toolCalls {
		if tc.ID == "" {
			continue
		}
		if _, exists := idToIndexMap[tc.ID]; !exists {
			idToIndexMap[tc.ID] = int(tc.Index)
			if int(tc.Index) >= *nextIndex {
				*nextIndex = int(tc.Index) + 1
			}
		}
	}
}

func createDeepCopyOfChunkForFix(
	chunk openai.ChatCompletionChunk,
	delta openai.ChatCompletionChunkChoiceDelta,
) openai.ChatCompletionChunk {
	fixedChunk := chunk
	fixedChunk.Choices = make([]openai.ChatCompletionChunkChoice, len(chunk.Choices))
	copy(fixedChunk.Choices, chunk.Choices)
	fixedChunk.Choices[0].Delta.ToolCalls = make(
		[]openai.ChatCompletionChunkChoiceDeltaToolCall,
		len(delta.ToolCalls),
	)
	copy(fixedChunk.Choices[0].Delta.ToolCalls, delta.ToolCalls)
	return fixedChunk
}

func buildUsedIndicesSet(idToIndexMap map[string]int) map[int64]struct{} {
	usedIndices := make(map[int64]struct{}, len(idToIndexMap))
	for _, idx := range idToIndexMap {
		usedIndices[int64(idx)] = struct{}{}
	}
	return usedIndices
}

func findNextAvailableIndex(usedIndices map[int64]struct{}, startFrom int) int64 {
	candidate := int64(startFrom)
	for {
		if _, used := usedIndices[candidate]; !used {
			return candidate
		}
		candidate++
	}
}

func applyToolCallIndexFixes(
	fixedChunk *openai.ChatCompletionChunk,
	state *toolCallIndexState,
	usedIndices map[int64]struct{},
) {
	for i := range fixedChunk.Choices[0].Delta.ToolCalls {
		tc := &fixedChunk.Choices[0].Delta.ToolCalls[i]
		if tc.ID == "" {
			continue
		}
		if existingIndex, exists := state.idToIndexMap[tc.ID]; exists {
			tc.Index = int64(existingIndex)
			continue
		}
		if _, used := usedIndices[tc.Index]; used {
			tc.Index = findNextAvailableIndex(usedIndices, *state.nextIndex)
		}
		state.idToIndexMap[tc.ID] = int(tc.Index)
		usedIndices[tc.Index] = struct{}{}
		if int(tc.Index) >= *state.nextIndex {
			*state.nextIndex = int(tc.Index) + 1
		}
	}
}

// fixToolCallIndices normalizes tool call indices in streaming chunks.
// Some providers incorrectly set ToolCalls[].Index to 0 for every tool call.
// The upstream openai-go accumulator uses ToolCalls[].Index as the slice position.
// When indices are wrong, different tool calls get merged by concatenating Name and Arguments.
// This function uses ToolCalls[].ID as the stable identity and rewrites indices to be consistent.
// This function also handles the case where a single chunk contains multiple tool calls sharing the same index.
// The idToIndexMap stores the canonical index for each tool call ID.
// The nextIndex points to the next available canonical index and is advanced monotonically.
func fixToolCallIndices(
	chunk openai.ChatCompletionChunk,
	idToIndexMap map[string]int,
	nextIndex *int,
) openai.ChatCompletionChunk {
	if len(chunk.Choices) == 0 {
		return chunk
	}
	delta := chunk.Choices[0].Delta
	if len(delta.ToolCalls) == 0 {
		return chunk
	}

	indexToID := buildIndexToIDMap(idToIndexMap, delta.ToolCalls)
	needsFix := checkIfIndexFixNeeded(delta.ToolCalls, idToIndexMap, indexToID)

	if !needsFix {
		updateIDToIndexMapFromToolCalls(delta.ToolCalls, idToIndexMap, nextIndex)
		return chunk
	}

	fixedChunk := createDeepCopyOfChunkForFix(chunk, delta)
	usedIndices := buildUsedIndicesSet(idToIndexMap)
	state := &toolCallIndexState{
		idToIndexMap: idToIndexMap,
		indexToID:    indexToID,
		nextIndex:    nextIndex,
	}
	applyToolCallIndexFixes(&fixedChunk, state, usedIndices)
	return fixedChunk
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

// collectExtraFieldsFromChunk collects ExtraFields from chunk tool_calls.
func (m *Model) collectExtraFieldsFromChunk(
	chunk openai.ChatCompletionChunk,
	extraFieldsMap map[string]map[string]any,
) {
	if len(chunk.Choices) == 0 || len(chunk.Choices[0].Delta.ToolCalls) == 0 {
		return
	}
	for _, tc := range chunk.Choices[0].Delta.ToolCalls {
		extraFields := convertExtraFields(tc.JSON.ExtraFields)
		if len(extraFields) == 0 {
			continue
		}
		// Use ID if available, otherwise use index as key.
		key := tc.ID
		if key == "" && tc.Index != 0 {
			key = fmt.Sprintf("index_%d", tc.Index)
		}
		if key != "" {
			extraFieldsMap[key] = extraFields
		}
	}
}

func applyOpenAISDKTokenDetailsAccumulationFix(
	acc *openai.ChatCompletionAccumulator,
	chunk openai.ChatCompletionChunk,
) {
	// Temporary workaround for token details accumulation.
	// See https://github.com/trpc-group/trpc-agent-go/issues/1270.
	// Remove this after upgrading openai-go to v3.10.0.
	acc.Usage.CompletionTokensDetails.AcceptedPredictionTokens += chunk.Usage.CompletionTokensDetails.AcceptedPredictionTokens
	acc.Usage.CompletionTokensDetails.AudioTokens += chunk.Usage.CompletionTokensDetails.AudioTokens
	acc.Usage.CompletionTokensDetails.ReasoningTokens += chunk.Usage.CompletionTokensDetails.ReasoningTokens
	acc.Usage.CompletionTokensDetails.RejectedPredictionTokens += chunk.Usage.CompletionTokensDetails.RejectedPredictionTokens
	acc.Usage.PromptTokensDetails.AudioTokens += chunk.Usage.PromptTokensDetails.AudioTokens
	acc.Usage.PromptTokensDetails.CachedTokens += chunk.Usage.PromptTokensDetails.CachedTokens
}

// accumulateChunk accumulates non-reasoning deltas into the SDK accumulator and
// always appends reasoning deltas to the reasoning buffer.
func (m *Model) accumulateChunk(
	chunk openai.ChatCompletionChunk,
	acc *openai.ChatCompletionAccumulator,
	reasoningBuf *bytes.Buffer,
) {
	chunkForAccumulator := chunk
	shouldAccumulate := true
	if strippedChunk, stripped := stripReasoningFromChunkForAccumulator(chunk); stripped {
		if hasAccumulatorPayloadBeyondReasoning(strippedChunk) {
			chunkForAccumulator = strippedChunk
		} else {
			shouldAccumulate = false
		}
	}

	if shouldAccumulate {
		// Sanitize chunks before feeding them into the upstream accumulator to
		// avoid known panics when JSON.ToolCalls is marked present but the
		// typed ToolCalls slice is empty, especially on finish_reason chunks.
		sanitizedChunk := sanitizeChunkForAccumulator(chunkForAccumulator)
		if acc.AddChunk(sanitizedChunk) {
			applyOpenAISDKTokenDetailsAccumulationFix(acc, chunk)
		}

		if m.accumulateChunkUsage != nil {
			accUsage, chunkUsage := completionUsageToModelUsage(acc.Usage), completionUsageToModelUsage(chunk.Usage)
			usage := inverseOpenAISDKAddChunkUsage(accUsage, chunkUsage)
			usage = m.accumulateChunkUsage(usage, chunkUsage)
			acc.Usage = modelUsageToCompletionUsage(usage)
		}
	}

	// Aggregate reasoning delta (if any) for final response fallback.
	if len(chunk.Choices) > 0 {
		reasoningContent := extractReasoningContent(chunk.Choices[0].Delta.JSON.ExtraFields)
		if reasoningContent != "" {
			reasoningBuf.WriteString(reasoningContent)
		}
	}
}

// sendPartialResponse creates and sends a partial response from a chunk.
func (m *Model) sendPartialResponse(
	ctx context.Context,
	chunk openai.ChatCompletionChunk,
	responseChan chan<- *model.Response,
) error {
	response := m.createPartialResponse(chunk)
	select {
	case responseChan <- response:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleStreamCompleteCallback handles the stream complete callback.
func (m *Model) handleStreamCompleteCallback(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	acc openai.ChatCompletionAccumulator,
	streamErr error,
) {
	if m.chatStreamCompleteCallback == nil {
		return
	}
	var callbackAcc *openai.ChatCompletionAccumulator
	if streamErr == nil {
		clonedAcc := cloneChatCompletionAccumulator(acc)
		callbackAcc = &clonedAcc
	}
	defer imodel.RecoverCallbackPanic(ctx, "chat stream complete callback")
	m.chatStreamCompleteCallback(ctx, &chatRequest, callbackAcc, streamErr)
}

func cloneChatCompletionAccumulator(acc openai.ChatCompletionAccumulator) openai.ChatCompletionAccumulator {
	cloned := acc
	cloned.ChatCompletion = cloneChatCompletion(acc.ChatCompletion)
	return cloned
}

func cloneChatCompletion(chatCompletion openai.ChatCompletion) openai.ChatCompletion {
	cloned := chatCompletion
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(chatCompletion.JSON.ExtraFields)
	cloned.Usage = cloneCompletionUsage(chatCompletion.Usage)
	if chatCompletion.Choices != nil {
		cloned.Choices = make([]openai.ChatCompletionChoice, len(chatCompletion.Choices))
		for i, choice := range chatCompletion.Choices {
			cloned.Choices[i] = cloneChatCompletionChoice(choice)
		}
	}
	return cloned
}

func cloneChatCompletionChoice(choice openai.ChatCompletionChoice) openai.ChatCompletionChoice {
	cloned := choice
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(choice.JSON.ExtraFields)
	cloned.Logprobs = cloneChatCompletionChoiceLogprobs(choice.Logprobs)
	cloned.Message = cloneChatCompletionMessage(choice.Message)
	return cloned
}

func cloneChatCompletionChoiceLogprobs(
	logprobs openai.ChatCompletionChoiceLogprobs,
) openai.ChatCompletionChoiceLogprobs {
	cloned := logprobs
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(logprobs.JSON.ExtraFields)
	if logprobs.Content != nil {
		cloned.Content = make([]openai.ChatCompletionTokenLogprob, len(logprobs.Content))
		for i, token := range logprobs.Content {
			cloned.Content[i] = cloneChatCompletionTokenLogprob(token)
		}
	}
	if logprobs.Refusal != nil {
		cloned.Refusal = make([]openai.ChatCompletionTokenLogprob, len(logprobs.Refusal))
		for i, token := range logprobs.Refusal {
			cloned.Refusal[i] = cloneChatCompletionTokenLogprob(token)
		}
	}
	return cloned
}

func cloneChatCompletionTokenLogprob(
	token openai.ChatCompletionTokenLogprob,
) openai.ChatCompletionTokenLogprob {
	cloned := token
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(token.JSON.ExtraFields)
	if token.Bytes != nil {
		cloned.Bytes = append([]int64(nil), token.Bytes...)
	}
	if token.TopLogprobs != nil {
		cloned.TopLogprobs = make([]openai.ChatCompletionTokenLogprobTopLogprob, len(token.TopLogprobs))
		for i, top := range token.TopLogprobs {
			cloned.TopLogprobs[i] = cloneChatCompletionTokenLogprobTopLogprob(top)
		}
	}
	return cloned
}

func cloneChatCompletionTokenLogprobTopLogprob(
	token openai.ChatCompletionTokenLogprobTopLogprob,
) openai.ChatCompletionTokenLogprobTopLogprob {
	cloned := token
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(token.JSON.ExtraFields)
	if token.Bytes != nil {
		cloned.Bytes = append([]int64(nil), token.Bytes...)
	}
	return cloned
}

func cloneChatCompletionMessage(message openai.ChatCompletionMessage) openai.ChatCompletionMessage {
	cloned := message
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(message.JSON.ExtraFields)
	cloned.Audio = cloneChatCompletionAudio(message.Audio)
	cloned.FunctionCall = cloneChatCompletionMessageFunctionCall(message.FunctionCall)
	if message.Annotations != nil {
		cloned.Annotations = make([]openai.ChatCompletionMessageAnnotation, len(message.Annotations))
		for i, annotation := range message.Annotations {
			cloned.Annotations[i] = cloneChatCompletionMessageAnnotation(annotation)
		}
	}
	if message.ToolCalls != nil {
		cloned.ToolCalls = make([]openai.ChatCompletionMessageToolCall, len(message.ToolCalls))
		for i, toolCall := range message.ToolCalls {
			cloned.ToolCalls[i] = cloneChatCompletionMessageToolCall(toolCall)
		}
	}
	return cloned
}

func cloneChatCompletionMessageAnnotation(
	annotation openai.ChatCompletionMessageAnnotation,
) openai.ChatCompletionMessageAnnotation {
	cloned := annotation
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(annotation.JSON.ExtraFields)
	cloned.URLCitation = cloneChatCompletionMessageAnnotationURLCitation(annotation.URLCitation)
	return cloned
}

func cloneChatCompletionMessageAnnotationURLCitation(
	urlCitation openai.ChatCompletionMessageAnnotationURLCitation,
) openai.ChatCompletionMessageAnnotationURLCitation {
	cloned := urlCitation
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(urlCitation.JSON.ExtraFields)
	return cloned
}

func cloneChatCompletionAudio(audio openai.ChatCompletionAudio) openai.ChatCompletionAudio {
	cloned := audio
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(audio.JSON.ExtraFields)
	return cloned
}

func cloneChatCompletionMessageFunctionCall(
	functionCall openai.ChatCompletionMessageFunctionCall,
) openai.ChatCompletionMessageFunctionCall {
	cloned := functionCall
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(functionCall.JSON.ExtraFields)
	return cloned
}

func cloneChatCompletionMessageToolCall(
	toolCall openai.ChatCompletionMessageToolCall,
) openai.ChatCompletionMessageToolCall {
	cloned := toolCall
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(toolCall.JSON.ExtraFields)
	cloned.Function = cloneChatCompletionMessageToolCallFunction(toolCall.Function)
	return cloned
}

func cloneChatCompletionMessageToolCallFunction(
	function openai.ChatCompletionMessageToolCallFunction,
) openai.ChatCompletionMessageToolCallFunction {
	cloned := function
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(function.JSON.ExtraFields)
	return cloned
}

func cloneCompletionUsage(usage openai.CompletionUsage) openai.CompletionUsage {
	cloned := usage
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(usage.JSON.ExtraFields)
	cloned.CompletionTokensDetails = cloneCompletionUsageCompletionTokensDetails(usage.CompletionTokensDetails)
	cloned.PromptTokensDetails = cloneCompletionUsagePromptTokensDetails(usage.PromptTokensDetails)
	return cloned
}

func cloneCompletionUsageCompletionTokensDetails(
	details openai.CompletionUsageCompletionTokensDetails,
) openai.CompletionUsageCompletionTokensDetails {
	cloned := details
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(details.JSON.ExtraFields)
	return cloned
}

func cloneCompletionUsagePromptTokensDetails(
	details openai.CompletionUsagePromptTokensDetails,
) openai.CompletionUsagePromptTokensDetails {
	cloned := details
	cloned.JSON.ExtraFields = cloneRespJSONFieldMap(details.JSON.ExtraFields)
	return cloned
}

func cloneRespJSONFieldMap(fields map[string]respjson.Field) map[string]respjson.Field {
	if fields == nil {
		return nil
	}
	cloned := make(map[string]respjson.Field, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
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
		// Ollama and some providers use "reasoning" instead of "reasoning_content".
		reasoningField, ok = extraFields[model.ReasoningContentKeyAlt]
		if !ok {
			return ""
		}
	}
	reasoningStr, err := strconv.Unquote(reasoningField.Raw())
	if err == nil {
		return reasoningStr
	}
	return ""
}

// convertExtraFields converts SDK's respjson.Field map to a generic map[string]any.
// This preserves all extra fields from the API response (e.g., Gemini 3's thought_signature)
// for transparent passthrough to subsequent requests.
func convertExtraFields(extraFields map[string]respjson.Field) map[string]any {
	if len(extraFields) == 0 {
		return nil
	}

	result := make(map[string]any, len(extraFields))
	for key, field := range extraFields {
		var value any
		if err := json.Unmarshal([]byte(field.Raw()), &value); err == nil {
			result[key] = value
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
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
				toolCalls = append(toolCalls, model.ToolCall{
					Type: string(toolCall.Type),
					Function: model.FunctionDefinitionParam{
						Name:      toolCall.Function.Name,
						Arguments: []byte(toolCall.Function.Arguments),
					},
					ID:    toolCall.ID,
					Index: toolCallDeltaIndexPointer(toolCall),
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

func toolCallDeltaIndexPointer(toolCall openai.ChatCompletionChunkChoiceDeltaToolCall) *int {
	if toolCall.Index == 0 && !toolCall.JSON.Index.Valid() {
		return nil
	}
	index := int(toolCall.Index)
	return &index
}

// emitStreamingFinalResponse emits the final response with accumulated data.
func (m *Model) emitStreamingFinalResponse(
	ctx context.Context,
	stream *ssestream.Stream[openai.ChatCompletionChunk],
	acc openai.ChatCompletionAccumulator,
	idToIndexMap map[string]int,
	extraFieldsMap map[string]map[string]any,
	aggregatedReasoning string,
	emit responseEmitter,
) {
	if stream.Err() == nil {
		// Check accumulated tool calls (batch processing after streaming is complete).
		var hasToolCall bool
		var accumulatedToolCalls []model.ToolCall

		if len(acc.Choices) > 0 && len(acc.Choices[0].Message.ToolCalls) > 0 {
			hasToolCall = true
			accumulatedToolCalls = m.processAccumulatedToolCalls(acc, idToIndexMap, extraFieldsMap)
		}

		// If accumulator is empty but we have aggregated reasoning, create a response with it.
		if len(acc.Choices) == 0 && aggregatedReasoning != "" {
			emit(&model.Response{
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
			})
			return
		}
		emit(m.createFinalResponse(acc, hasToolCall, accumulatedToolCalls, aggregatedReasoning))
		return
	}
	// Send error response.
	emit(&model.Response{
		Error: &model.ResponseError{
			Message: stream.Err().Error(),
			Type:    model.ErrorTypeStreamError,
		},
		Timestamp: time.Now(),
		Done:      true,
	})
}

// processAccumulatedToolCalls processes accumulated tool calls.
func (m *Model) processAccumulatedToolCalls(
	acc openai.ChatCompletionAccumulator,
	idToIndexMap map[string]int,
	extraFieldsMap map[string]map[string]any,
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

		// Look up ExtraFields by ID first, then by index.
		var extraFields map[string]any
		if ef, ok := extraFieldsMap[toolCall.ID]; ok {
			extraFields = ef
		} else if ef, ok := extraFieldsMap[fmt.Sprintf("index_%d", originalIndex)]; ok {
			extraFields = ef
		}

		accumulatedToolCalls = append(accumulatedToolCalls, model.ToolCall{
			Index:       func() *int { idx := originalIndex; return &idx }(),
			ID:          synthesizedID,
			Type:        functionToolType, // OpenAI supports function tools for now.
			ExtraFields: extraFields,
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
	m.handleNonStreamingResponseWithEmitter(ctx, chatRequest, func(resp *model.Response) bool {
		select {
		case responseChan <- resp:
			return true
		case <-ctx.Done():
			return false
		}
	}, opts...)
}

// handleNonStreamingResponseWithEmitter handles non-streaming chat completion responses.
// It returns early when emit returns false.
func (m *Model) handleNonStreamingResponseWithEmitter(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	emit responseEmitter,
	opts ...openaiopt.RequestOption,
) {
	chatCompletion, err := m.client.Chat.Completions.New(ctx, chatRequest, opts...)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		emit(&model.Response{
			Error: &model.ResponseError{
				Message: err.Error(),
				Type:    model.ErrorTypeAPIError,
			},
			Timestamp: time.Now(),
			Done:      true,
		})
		return
	}
	// Some OpenAI-compatible providers return HTTP 200 with an error body
	// instead of a proper 4xx/5xx status code. The SDK does not treat these
	// as errors because it only inspects the HTTP status. However the error
	// payload is still preserved in ChatCompletion.JSON.ExtraFields["error"].
	// Detect this case and convert it into an explicit error response so that
	// downstream consumers see a clear failure instead of an empty completion
	// that can cause silent infinite loops in the agent flow.
	if resp := extractEmbeddedErrorResponse(chatCompletion); resp != nil {
		emit(resp)
		return
	}
	// Call response callback on successful completion.
	m.runChatResponseCallback(ctx, &chatRequest, chatCompletion)
	emit(m.createResponseFromCompletion(chatCompletion))
}

// createResponseFromCompletion converts a provider response into a model.Response.
func (m *Model) createResponseFromCompletion(chatCompletion *openai.ChatCompletion) *model.Response {
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
					ID:          synthesizedID,
					Type:        string(toolCall.Type),
					ExtraFields: convertExtraFields(toolCall.JSON.ExtraFields),
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

	return response
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

// DownloadFile downloads the content for the given file ID.
func (m *Model) DownloadFile(
	ctx context.Context,
	fileID string,
) ([]byte, string, error) {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return nil, "", fmt.Errorf("file_id is required")
	}
	basePath := strings.TrimSpace(m.variantConfig.fileDeletionPath)
	if basePath == "" {
		basePath = strings.TrimSpace(m.variantConfig.fileUploadPath)
	}
	basePath = strings.TrimRight(basePath, "/")
	mw := openaiopt.WithMiddleware(
		func(r *http.Request, next openaiopt.MiddlewareNext) (
			*http.Response, error,
		) {
			if basePath != "" {
				r.URL.Path = basePath + "/" + id + "/content"
			}
			return next(r)
		},
	)
	resp, err := m.client.Files.Content(ctx, id, mw)
	if err != nil {
		return nil, "", fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read file: %w", err)
	}
	mime := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	return data, mime, nil
}

// extractEmbeddedErrorResponse detects an error payload hidden inside an
// otherwise "successful" ChatCompletion. Some OpenAI-compatible providers
// return HTTP 200 with a JSON body like:
//
//	{"error": {"message": "...", "type": "...", "code": "..."}}
//
// The OpenAI SDK only checks HTTP status codes for errors, so it silently
// parses this into an empty ChatCompletion. The error object ends up in
// ChatCompletion.JSON.ExtraFields["error"] because "error" is not a
// recognized field in the ChatCompletion schema.
//
// This function checks for that specific condition and, when found, returns
// a model.Response with the original error information restored.
// Returns nil when no embedded error is detected.
func extractEmbeddedErrorResponse(cc *openai.ChatCompletion) *model.Response {
	if cc == nil {
		return nil
	}
	// Only inspect ExtraFields when the completion is empty (no choices).
	// A completion with valid choices is never treated as an error, even if
	// the provider happens to include extra fields named "error".
	if len(cc.Choices) > 0 {
		return nil
	}
	errField, ok := cc.JSON.ExtraFields["error"]
	if !ok {
		return nil
	}
	// Parse the raw error JSON to extract message and type.
	// Use Raw() instead of Valid() because the SDK may mark non-schema
	// object fields as "invalid" status even though the content is present.
	raw := errField.Raw()
	if raw == "" || raw == "null" {
		return nil
	}
	var errBody struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
		Param   json.RawMessage `json:"param"`
	}
	if err := json.Unmarshal([]byte(raw), &errBody); err != nil {
		// ExtraFields["error"] exists but is not a recognizable error object;
		// leave it for the normal completion path.
		return nil
	}
	if errBody.Message == "" && errBody.Type == "" {
		return nil
	}
	errMsg := errBody.Message
	if errMsg == "" {
		errMsg = "OpenAI-compatible API returned an embedded error in HTTP 200 response"
	}
	log.Debugf("OpenAI-compatible API returned HTTP 200 with embedded error (type=%s)", errBody.Type)
	respErr := &model.ResponseError{
		Message: errMsg,
		Type:    model.ErrorTypeAPIError,
	}
	if code := normalizeEmbeddedErrorCode(errBody.Code); code != "" {
		respErr.Code = &code
	}
	if p := normalizeEmbeddedErrorString(errBody.Param); p != "" {
		respErr.Param = &p
	}
	return &model.Response{
		Error:     respErr,
		Timestamp: time.Now(),
		Done:      true,
	}
}

// normalizeEmbeddedErrorString extracts a JSON string value.
// Returns "" for absent, null, or non-string values.
func normalizeEmbeddedErrorString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

// normalizeEmbeddedErrorCode converts a JSON code value (string, number, or
// null) into a string suitable for model.ResponseError.Code.
// Returns "" when the value is absent, null, or unparsable.
func normalizeEmbeddedErrorCode(raw json.RawMessage) string {
	if s := normalizeEmbeddedErrorString(raw); s != "" {
		return s
	}
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// Try number (some providers return numeric codes).
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}
