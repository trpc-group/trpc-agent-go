//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gemini provides Gemini-compatible model implementations.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// geminiCallSeq is a process-wide counter used to generate synthetic IDs for
// Gemini FunctionCall objects that do not include an ID in the response.
// Vertex AI does not always populate FunctionCall.ID (it is optional per the
// genai API). Without a non-empty ID, the framework's SanitizeMessagesWithTools
// treats the corresponding tool result as orphaned and downgrades it to a user
// message, injecting "[orphan_tool_result]" noise into the conversation history.
var geminiCallSeq uint64

// Model implements the model.Model interface for Gemini API.
type Model struct {
	client                     Client
	name                       string
	channelBufferSize          int
	chatRequestCallback        ChatRequestCallbackFunc
	chatResponseCallback       ChatResponseCallbackFunc
	chatChunkCallback          ChatChunkCallbackFunc
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
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
}

// New creates a new Gemini-like model.
func New(ctx context.Context, name string, opts ...Option) (*Model, error) {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.tailoringStrategy == nil {
		o.tailoringStrategy = model.NewMiddleOutStrategy(o.tokenCounter)
	}
	client, err := genai.NewClient(ctx, o.geminiClientConfig)
	if err != nil {
		return nil, err
	}
	return &Model{
		client:                     &clientWrapper{client: client},
		name:                       name,
		protocolOverheadTokens:     o.tokenTailoringConfig.ProtocolOverheadTokens,
		reserveOutputTokens:        o.tokenTailoringConfig.ReserveOutputTokens,
		inputTokensFloor:           o.tokenTailoringConfig.InputTokensFloor,
		outputTokensFloor:          o.tokenTailoringConfig.OutputTokensFloor,
		safetyMarginRatio:          o.tokenTailoringConfig.SafetyMarginRatio,
		maxInputTokensRatio:        o.tokenTailoringConfig.MaxInputTokensRatio,
		maxInputTokens:             o.maxInputTokens,
		chatRequestCallback:        o.chatRequestCallback,
		chatResponseCallback:       o.chatResponseCallback,
		chatChunkCallback:          o.chatChunkCallback,
		chatStreamCompleteCallback: o.chatStreamCompleteCallback,
	}, nil
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
	chatRequest := m.convertMessages(request.Messages)
	generateConfig := m.buildChatConfig(request)
	// Execute callback synchronously before starting the goroutine
	// to avoid a race where the runner and HTTP handler finish
	// (closing the SSE writer) while the callback is still running.
	if m.chatRequestCallback != nil {
		m.chatRequestCallback(ctx, chatRequest)
	}
	responseChan := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responseChan)
		if request.Stream {
			m.handleStreamingResponse(ctx, chatRequest, responseChan, generateConfig)
		} else {
			m.handleNonStreamingResponse(ctx, chatRequest, responseChan, generateConfig)
		}
	}()
	return responseChan, nil
}

// malformedFunctionCallRetries is the number of silent retries performed when
// Gemini returns MALFORMED_FUNCTION_CALL. Each retry uses temperature=0 and
// FunctionCallingMode=ANY. The malformed attempt is never emitted to the
// response channel, so nothing is added to the agent's conversation history.
const malformedFunctionCallRetries = 2

// retryConfigForMalformed clones cfg and applies temperature=0 +
// FunctionCallingMode=ANY, which are the two knobs that most reliably correct
// MALFORMED_FUNCTION_CALL responses per production evidence.
func retryConfigForMalformed(cfg *genai.GenerateContentConfig) *genai.GenerateContentConfig {
	retry := *cfg // shallow copy — sufficient for scalar fields
	retry.Temperature = genai.Ptr(float32(0))

	// Clone ToolConfig so we don't mutate the caller's config, then only
	// override FunctionCallingConfig.Mode.  All other ToolConfig fields
	// (e.g. AllowedFunctionNames) are preserved.
	toolCfg := &genai.ToolConfig{}
	if cfg.ToolConfig != nil {
		*toolCfg = *cfg.ToolConfig
	}
	fcCfg := &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny}
	if cfg.ToolConfig != nil && cfg.ToolConfig.FunctionCallingConfig != nil {
		*fcCfg = *cfg.ToolConfig.FunctionCallingConfig
		fcCfg.Mode = genai.FunctionCallingConfigModeAny
	}
	toolCfg.FunctionCallingConfig = fcCfg
	retry.ToolConfig = toolCfg
	return &retry
}

// isMalformedFunctionCall reports whether a raw Gemini response was rejected
// by the server because the model generated a malformed function call.
func isMalformedFunctionCall(rsp *genai.GenerateContentResponse) bool {
	if rsp == nil {
		return false
	}
	for _, c := range rsp.Candidates {
		if string(c.FinishReason) == "MALFORMED_FUNCTION_CALL" {
			return true
		}
	}
	return false
}

// isMalformedModelResponse reports whether an already-converted model.Response
// carries the MALFORMED_FUNCTION_CALL finish reason. Used after streaming
// accumulation where the raw genai response is no longer available.
func isMalformedModelResponse(rsp *model.Response) bool {
	if rsp == nil {
		return false
	}
	for _, c := range rsp.Choices {
		if c.FinishReason != nil && *c.FinishReason == "MALFORMED_FUNCTION_CALL" {
			return true
		}
	}
	return false
}

// handleNonStreamingResponse handles non-streaming chat completion responses.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest []*genai.Content,
	responseChan chan<- *model.Response,
	generateConfig *genai.GenerateContentConfig,
) {
	chatCompletion, err := m.client.Models().GenerateContent(
		ctx, m.name, chatRequest, generateConfig)
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

	// Silently retry when Gemini rejects the function call as malformed.
	// The malformed attempt is never emitted, so it does not enter the
	// session history. Each retry uses temperature=0 + mode=ANY which
	// are the two most effective mitigations per production data.
	retryCfg := retryConfigForMalformed(generateConfig)
	for attempt := 0; attempt < malformedFunctionCallRetries && isMalformedFunctionCall(chatCompletion); attempt++ {
		log.Warnf("gemini: MALFORMED_FUNCTION_CALL (attempt %d/%d), retrying with temperature=0 mode=ANY",
			attempt+1, malformedFunctionCallRetries)
		chatCompletion, err = m.client.Models().GenerateContent(ctx, m.name, chatRequest, retryCfg)
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
	}

	// If all retries are exhausted but the response is still malformed, return
	// an error rather than silently emitting a broken response.
	if isMalformedFunctionCall(chatCompletion) {
		select {
		case responseChan <- &model.Response{
			Error: &model.ResponseError{
				Message: "gemini: MALFORMED_FUNCTION_CALL persists after retries",
				Type:    model.ErrorTypeAPIError,
			},
			Timestamp: time.Now(),
			Done:      true,
		}:
		case <-ctx.Done():
		}
		return
	}

	// Call response callback on successful completion.
	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, chatRequest, generateConfig, chatCompletion)
	}
	response := m.buildFinalResponse(chatCompletion)
	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}

// handleStreamingResponse handles streaming chat completion responses.
//
// All chunks are buffered before being forwarded to responseChan. This ensures
// that if the stream ends with MALFORMED_FUNCTION_CALL, nothing from the failed
// attempt is ever visible to the caller — not even the partial chunks that
// carry the MALFORMED_FUNCTION_CALL finish_reason. Consumers that inspect
// finish_reason on partial chunks (e.g. OpenAI-compatible clients) will
// therefore never see the malformed result.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	chatRequest []*genai.Content,
	responseChan chan<- *model.Response,
	generateConfig *genai.GenerateContentConfig,
) {
	chatCompletion := m.client.Models().GenerateContentStream(
		ctx, m.name, chatRequest, generateConfig)
	acc := &Accumulator{}
	var chunks []*model.Response
	var rawChunks []*genai.GenerateContentResponse
	for chunk, err := range chatCompletion {
		if err != nil {
			// Flush already-buffered chunks so the caller sees partial data
			// before the error (e.g. network interruption mid-stream).
			for i, c := range chunks {
				if m.chatChunkCallback != nil {
					m.chatChunkCallback(ctx, chatRequest, generateConfig, rawChunks[i])
				}
				select {
				case responseChan <- c:
				case <-ctx.Done():
					return
				}
			}
			select {
			case responseChan <- &model.Response{
				Error: &model.ResponseError{
					Message: err.Error(),
					Type:    model.ErrorTypeAPIError,
				},
				Timestamp: time.Now(),
				Done:      true,
			}:
			case <-ctx.Done():
			}
			return
		}
		response := m.buildChunkResponse(chunk)
		acc.Accumulate(response)
		chunks = append(chunks, response)
		rawChunks = append(rawChunks, chunk)
	}
	finalResponse := acc.BuildResponse()

	// When the stream ends with MALFORMED_FUNCTION_CALL, retry non-streaming
	// with temperature=0 + mode=ANY. Because all chunks were buffered, the
	// caller has not seen any part of the failed attempt yet.
	if isMalformedModelResponse(finalResponse) {
		retryCfg := retryConfigForMalformed(generateConfig)
		var retryRaw *genai.GenerateContentResponse
		var retryErr error
		for attempt := 0; attempt < malformedFunctionCallRetries; attempt++ {
			log.Warnf("gemini: MALFORMED_FUNCTION_CALL in stream (attempt %d/%d), retrying non-streaming with temperature=0 mode=ANY",
				attempt+1, malformedFunctionCallRetries)
			retryRaw, retryErr = m.client.Models().GenerateContent(ctx, m.name, chatRequest, retryCfg)
			if retryErr != nil {
				break
			}
			if !isMalformedFunctionCall(retryRaw) {
				break
			}
		}
		if retryErr != nil {
			select {
			case responseChan <- &model.Response{
				Error: &model.ResponseError{
					Message: retryErr.Error(),
					Type:    model.ErrorTypeAPIError,
				},
				Timestamp: time.Now(),
				Done:      true,
			}:
			case <-ctx.Done():
			}
			return
		}
		if isMalformedFunctionCall(retryRaw) {
			select {
			case responseChan <- &model.Response{
				Error: &model.ResponseError{
					Message: "gemini: MALFORMED_FUNCTION_CALL persists after retries",
					Type:    model.ErrorTypeAPIError,
				},
				Timestamp: time.Now(),
				Done:      true,
			}:
			case <-ctx.Done():
			}
			return
		}
		finalResponse = m.buildFinalResponse(retryRaw)
		if m.chatStreamCompleteCallback != nil {
			m.chatStreamCompleteCallback(ctx, chatRequest, generateConfig, finalResponse)
		}
		select {
		case responseChan <- finalResponse:
		case <-ctx.Done():
		}
		return
	}

	// Normal path: flush buffered chunks then the final Done=true response.
	for i, chunk := range chunks {
		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, chatRequest, generateConfig, rawChunks[i])
		}
		select {
		case responseChan <- chunk:
		case <-ctx.Done():
			return
		}
	}
	if m.chatStreamCompleteCallback != nil {
		m.chatStreamCompleteCallback(ctx, chatRequest, generateConfig, finalResponse)
	}
	select {
	case responseChan <- finalResponse:
	case <-ctx.Done():
		return
	}
}

// convertContentBlock builds a single assistant message from Gemini Candidate.
func (m *Model) convertContentBlock(candidates []*genai.Candidate) (model.Message, string) {
	var (
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
		toolCalls        []model.ToolCall
		finishReason     string
	)
	for _, candidate := range candidates {
		if candidate.FinishReason != "" {
			finishReason = string(candidate.FinishReason)
		}
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					if part.Thought {
						reasoningBuilder.WriteString(part.Text)
					} else {
						textBuilder.WriteString(part.Text)
					}
				}
				if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					// Vertex AI does not always populate FunctionCall.ID.
					// Without a non-empty ID, SanitizeMessagesWithTools treats the
					// corresponding tool result as orphaned and downgrades it to a
					// user message, injecting "[orphan_tool_result]" noise into the
					// conversation history. Generate a synthetic sequential ID so
					// the framework can match results to their calls.
					// Note: generateContent matching is by name and position, not by
					// ID; the synthetic ID only serves the framework's internal tracking.
					id := part.FunctionCall.ID
					if id == "" {
						id = fmt.Sprintf("gemini_call_%d", atomic.AddUint64(&geminiCallSeq, 1))
					}
					toolCalls = append(toolCalls, model.ToolCall{
						ID: id,
						Function: model.FunctionDefinitionParam{
							Name:      part.FunctionCall.Name,
							Arguments: args,
						},
					})
				}
			}
		}
	}
	return model.Message{
		Role:             model.RoleAssistant,
		Content:          textBuilder.String(),
		ReasoningContent: reasoningBuilder.String(),
		ToolCalls:        toolCalls,
	}, finishReason
}

func (m *Model) buildChunkResponse(rsp *genai.GenerateContentResponse) *model.Response {
	return m.buildChatCompletionResponse(
		rsp,
		model.ObjectTypeChatCompletionChunk,
		false,
		true,
	)
}

func (m *Model) buildFinalResponse(rsp *genai.GenerateContentResponse) *model.Response {
	return m.buildChatCompletionResponse(
		rsp,
		model.ObjectTypeChatCompletion,
		true,
		false,
	)
}

func (m *Model) buildChatCompletionResponse(
	rsp *genai.GenerateContentResponse,
	object string,
	done bool,
	isPartial bool,
) *model.Response {
	if rsp == nil {
		return &model.Response{
			Object:    object,
			IsPartial: isPartial,
			Done:      done,
		}
	}
	response := &model.Response{
		ID:        rsp.ResponseID,
		Object:    object,
		Created:   rsp.CreateTime.Unix(),
		Model:     rsp.ModelVersion,
		Timestamp: rsp.CreateTime,
		Done:      done,
		IsPartial: isPartial,
	}
	message, finishReason := m.convertContentBlock(rsp.Candidates)
	if isPartial {
		// Streaming chunk: only populate Delta (not Message).
		// This matches the OpenAI and Anthropic patterns where streaming
		// chunks carry incremental deltas. Setting both Message and Delta
		// to the same value caused downstream consumers to double-emit
		// content — the chunk's Message.Content was treated as a full
		// response and re-emitted alongside the final accumulated response.
		response.Choices = []model.Choice{
			{
				Index: 0,
				Delta: message,
			},
		}
	} else {
		// Final/non-streaming response: populate Message (the full content).
		response.Choices = []model.Choice{
			{
				Index:   0,
				Message: message,
			},
		}
	}
	// Set finish reason.
	if finishReason != "" {
		response.Choices[0].FinishReason = &finishReason
	}
	// Convert usage information.
	response.Usage = m.completionUsageToModelUsage(rsp.UsageMetadata)
	return response
}

// completionUsageToModelUsage converts genai.GenerateContentResponseUsageMetadata to model.Usage.
func (m *Model) completionUsageToModelUsage(usage *genai.GenerateContentResponseUsageMetadata) *model.Usage {
	if usage == nil {
		return nil
	}
	return &model.Usage{
		PromptTokens:     int(usage.PromptTokenCount),
		CompletionTokens: int(usage.CandidatesTokenCount),
		TotalTokens:      int(usage.TotalTokenCount),
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens: int(usage.CachedContentTokenCount),
		},
	}
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
}

// buildChatConfig converts our Request to Gemini request config.
func (m *Model) buildChatConfig(request *model.Request) *genai.GenerateContentConfig {
	chatRequest := &genai.GenerateContentConfig{
		Tools: m.convertTools(request.Tools),
	}

	// Explicitly set ToolConfig when tools are present to use AUTO mode.
	// AUTO mode allows the model to decide whether to call tools or respond with text.
	if len(request.Tools) > 0 {
		chatRequest.ToolConfig = &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAuto,
			},
		}
	}

	// Set response_format for native structured outputs when requested.
	if request.StructuredOutput != nil &&
		request.StructuredOutput.Type == model.StructuredOutputJSONSchema &&
		request.StructuredOutput.JSONSchema != nil {
		chatRequest.ResponseJsonSchema = request.StructuredOutput.JSONSchema
	}

	if request.MaxTokens != nil {
		chatRequest.MaxOutputTokens = int32(*request.MaxTokens)
	}
	if request.Temperature != nil {
		chatRequest.Temperature = genai.Ptr(float32(*request.Temperature))
	}
	if request.TopP != nil {
		chatRequest.TopP = genai.Ptr(float32(*request.TopP))
	}
	if len(request.Stop) > 0 {
		chatRequest.StopSequences = request.Stop
	}
	if request.PresencePenalty != nil {
		chatRequest.PresencePenalty = genai.Ptr(float32(*request.PresencePenalty))
	}
	if request.FrequencyPenalty != nil {
		chatRequest.FrequencyPenalty = genai.Ptr(float32(*request.FrequencyPenalty))
	}
	chatRequest.ThinkingConfig = m.buildThinkingConfig(request)
	return chatRequest
}

// buildThinkingConfig converts our Request to Gemini request ThinkingConfig
func (m *Model) buildThinkingConfig(request *model.Request) *genai.ThinkingConfig {
	res := &genai.ThinkingConfig{}
	if request.ThinkingTokens != nil {
		res.ThinkingBudget = genai.Ptr(int32(*request.ThinkingTokens))
	}
	if request.ThinkingEnabled != nil {
		res.IncludeThoughts = *request.ThinkingEnabled
	}
	return res
}

// convertMessages converts our Message format to OpenAI's format.
func (m *Model) convertMessages(messages []model.Message) []*genai.Content {
	result := make([]*genai.Content, 0, len(messages))

	for _, msg := range messages {
		result = append(result, m.convertMessageContent(msg)...)
	}
	return result
}

// convertMessageContent converts message content to user message content union.
func (m *Model) convertMessageContent(
	msg model.Message,
) []*genai.Content {
	// Gemini generateContent requires tool results to be sent as FunctionResponse
	// parts (role=user) rather than plain text. The API matches responses to calls
	// by name and position in the contents array; no explicit ID is required.
	// See: https://cloud.google.com/vertex-ai/generative-ai/docs/multimodal/function-calling
	if msg.Role == model.RoleTool {
		var result map[string]any
		if err := json.Unmarshal([]byte(msg.Content), &result); err != nil {
			// Non-JSON content (e.g. error strings) — wrap in a plain map so
			// the FunctionResponse.Response field is always a valid JSON object.
			result = map[string]any{"output": msg.Content}
		}
		part := &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				Name:     msg.ToolName,
				Response: result,
			},
		}
		return []*genai.Content{genai.NewContentFromParts([]*genai.Part{part}, genai.RoleUser)}
	}
	var (
		contentParts []*genai.Content
	)
	role := genai.RoleUser
	if msg.Role == model.RoleAssistant {
		role = genai.RoleModel
	}
	// Add Content as a text part if present.
	if msg.Content != "" {
		contentParts = append(
			contentParts,
			genai.NewContentFromText(msg.Content, genai.Role(role)),
		)
	}
	for _, part := range msg.ContentParts {
		contentPart := m.convertContentPart(part)
		if contentPart == nil {
			continue
		}
		// For non-file or non-skipped file types, add to contentParts.
		contentParts = append(contentParts, genai.NewContentFromParts([]*genai.Part{contentPart}, genai.Role(role)))
	}
	return contentParts
}

func (m *Model) convertTools(tools map[string]tool.Tool) []*genai.Tool {
	// Vertex AI requires all function declarations to be grouped into a single
	// Tool object. Sending one Tool per function causes a 400 INVALID_ARGUMENT:
	// "Multiple tools are supported only when they are all search tools."
	if len(tools) == 0 {
		return nil
	}
	// Sort keys for deterministic declaration order across runs.
	keys := slices.Sorted(maps.Keys(tools))
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, k := range keys {
		t := tools[k]
		decl := t.Declaration()
		funcDeclaration := &genai.FunctionDeclaration{
			Description: decl.Description,
			Name:        decl.Name,
		}
		if decl.InputSchema != nil {
			// Avoid sending `"parametersJsonSchema": null` to Gemini when a tool has no input schema.
			// `ParametersJsonSchema` is `any`, so assigning a typed nil pointer would still marshal as null.
			funcDeclaration.ParametersJsonSchema = normalizeToolSchema(decl.Name, "input", decl.InputSchema)
		}
		if decl.OutputSchema != nil {
			funcDeclaration.ResponseJsonSchema = normalizeToolSchema(decl.Name, "output", decl.OutputSchema)
		}
		decls = append(decls, funcDeclaration)
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

func normalizeToolSchema(toolName, schemaKind string, schema *tool.Schema) any {
	if schema == nil {
		return nil
	}
	// Marshal/unmarshal to ensure the schema is JSON-serializable and to allow safe normalization
	// without mutating shared schema instances.
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		log.Warnf(
			"failed to marshal %s schema for tool %q: %v",
			schemaKind,
			toolName,
			err,
		)
		return emptyObjectSchema()
	}
	return normalizeToolSchemaBytes(toolName, schemaKind, schemaBytes)
}

func normalizeToolSchemaBytes(toolName, schemaKind string, schemaBytes []byte) any {
	var out map[string]any
	if err := json.Unmarshal(schemaBytes, &out); err != nil {
		log.Warnf(
			"failed to unmarshal %s schema for tool %q: %v",
			schemaKind,
			toolName,
			err,
		)
		return emptyObjectSchema()
	}
	// Some function-calling implementations are strict about top-level object schemas having
	// an explicit `properties` key, even for no-arg tools.
	if typ, ok := out["type"].(string); ok && typ == "object" {
		if props, exists := out["properties"]; !exists || props == nil {
			out["properties"] = map[string]any{}
		}
	}
	return out
}

func emptyObjectSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// convertContentPart converts a single content part to Gemini format.
func (m *Model) convertContentPart(part model.ContentPart) *genai.Part {
	switch part.Type {
	case model.ContentTypeText:
		if part.Text != nil {
			return &genai.Part{
				Text: *part.Text,
			}
		}
	case model.ContentTypeImage:
		if part.Image == nil {
			return nil
		}
		if part.Image.URL != "" {
			return genai.NewPartFromURI(part.Image.URL, part.Image.Format)
		}
		if len(part.Image.Data) != 0 {
			return genai.NewPartFromBytes(part.Image.Data, part.Image.Format)
		}
	case model.ContentTypeAudio:
		if part.Audio == nil {
			return nil
		}
		return genai.NewPartFromBytes(part.Audio.Data, part.Audio.Format)
	case model.ContentTypeFile:
		if part.File == nil {
			return nil
		}
		return genai.NewPartFromBytes(part.File.Data, part.File.MimeType)
	}
	return nil
}
