//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type overflowTailoringStrategy struct {
	tailored []model.Message
}

func (s overflowTailoringStrategy) TailorMessages(
	ctx context.Context,
	messages []model.Message,
	maxTokens int,
) ([]model.Message, error) {
	return s.tailored, errors.New("minimal protected context exceeds token budget")
}

func TestModel_CallbackPanicsAreRecovered(t *testing.T) {
	t.Run("request callback", func(t *testing.T) {
		callbackCalled := false
		m := &Model{
			chatRequestCallback: func(ctx context.Context, chatRequest []*genai.Content) {
				callbackCalled = true
				panic("boom")
			},
		}

		require.NotPanics(t, func() {
			m.runChatRequestCallback(context.Background(), nil)
		})
		assert.True(t, callbackCalled)
	})

	t.Run("response callback", func(t *testing.T) {
		callbackCalled := false
		m := &Model{
			chatResponseCallback: func(
				ctx context.Context,
				chatRequest []*genai.Content,
				generateConfig *genai.GenerateContentConfig,
				chatResponse *genai.GenerateContentResponse,
			) {
				callbackCalled = true
				panic("boom")
			},
		}

		require.NotPanics(t, func() {
			m.runChatResponseCallback(
				context.Background(),
				nil,
				&genai.GenerateContentConfig{},
				&genai.GenerateContentResponse{},
			)
		})
		assert.True(t, callbackCalled)
	})

	t.Run("chunk callback", func(t *testing.T) {
		callbackCalled := false
		m := &Model{
			chatChunkCallback: func(
				ctx context.Context,
				chatRequest []*genai.Content,
				generateConfig *genai.GenerateContentConfig,
				chatResponse *genai.GenerateContentResponse,
			) {
				callbackCalled = true
				panic("boom")
			},
		}

		require.NotPanics(t, func() {
			m.runChatChunkCallback(
				context.Background(),
				nil,
				&genai.GenerateContentConfig{},
				&genai.GenerateContentResponse{},
			)
		})
		assert.True(t, callbackCalled)
	})

	t.Run("stream complete callback", func(t *testing.T) {
		callbackCalled := false
		m := &Model{
			chatStreamCompleteCallback: func(
				ctx context.Context,
				chatRequest []*genai.Content,
				generateConfig *genai.GenerateContentConfig,
				chatResponse *model.Response,
			) {
				callbackCalled = true
				panic("boom")
			},
		}

		require.NotPanics(t, func() {
			m.runChatStreamCompleteCallback(
				context.Background(),
				nil,
				&genai.GenerateContentConfig{},
				&model.Response{},
			)
		})
		assert.True(t, callbackCalled)
	})
}

func TestWithTokenTailoring_UsesProtectedContextOnOverflow(t *testing.T) {
	tailored := []model.Message{
		model.NewSystemMessage("sys"),
		model.NewUserMessage("q"),
	}
	m := &Model{
		name:                 "test-model",
		enableTokenTailoring: true,
		maxInputTokens:       1,
		tailoringStrategy:    overflowTailoringStrategy{tailored: tailored},
	}
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("sys"),
		model.NewUserMessage("old"),
		model.NewUserMessage("q"),
	}}

	m.applyTokenTailoring(context.Background(), req)

	require.Equal(t, tailored, req.Messages)
}

func TestModel_convertMessages(t *testing.T) {
	var (
		text      = "Text"
		subText   = "subText"
		imageURL  = "imageURL"
		imageData = "imageData"
		audioURL  = "audioURL"
		fileURL   = "fileURL"
	)
	type fields struct {
		m *Model
	}
	type args struct {
		messages []model.Message
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []*genai.Content
	}{
		{
			name: "text",
			fields: fields{
				m: &Model{},
			},
			args: args{
				messages: []model.Message{
					{
						Role:    model.RoleAssistant,
						Content: text,
						ContentParts: []model.ContentPart{
							{
								Type: model.ContentTypeText,
								Text: &subText,
							},
						},
					},
				},
			},
			want: []*genai.Content{
				genai.NewContentFromText(text, genai.RoleModel),
				genai.NewContentFromText(subText, genai.RoleModel),
			},
		},
		{
			name: "image",
			fields: fields{
				m: &Model{},
			},
			args: args{
				messages: []model.Message{
					{
						Role: model.RoleUser,
						ContentParts: []model.ContentPart{
							{
								Type: model.ContentTypeImage,
								Image: &model.Image{
									URL: imageURL,
								},
							},
							{
								Type: model.ContentTypeImage,
								Image: &model.Image{
									Data: []byte(imageData),
								},
							},
						},
					},
				},
			},
			want: []*genai.Content{
				genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromURI(imageURL, ""),
				}, genai.RoleUser),
				genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromBytes([]byte(imageData), ""),
				}, genai.RoleUser),
			},
		},
		{
			name: "audio",
			fields: fields{
				m: &Model{},
			},
			args: args{
				messages: []model.Message{
					{
						Role: model.RoleUser,
						ContentParts: []model.ContentPart{
							{
								Type: model.ContentTypeAudio,
								Audio: &model.Audio{
									Data: []byte(audioURL),
								},
							},
						},
					},
				},
			},
			want: []*genai.Content{
				genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromBytes([]byte(audioURL), ""),
				}, genai.RoleUser),
			},
		},
		{
			name: "file",
			fields: fields{
				m: &Model{},
			},
			args: args{
				messages: []model.Message{
					{
						Role: model.RoleUser,
						ContentParts: []model.ContentPart{
							{
								Type: model.ContentTypeFile,
								File: &model.File{
									Data: []byte(fileURL),
								},
							},
						},
					},
				},
			},
			want: []*genai.Content{
				genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromBytes([]byte(fileURL), ""),
				}, genai.RoleUser),
			},
		},
		{
			name: "file URL",
			fields: fields{
				m: &Model{},
			},
			args: args{
				messages: []model.Message{
					{
						Role: model.RoleUser,
						ContentParts: []model.ContentPart{
							{
								Type: model.ContentTypeFile,
								File: &model.File{
									Name:     "report.pdf",
									URL:      "https://example.com/report.pdf",
									MimeType: "application/pdf",
								},
							},
						},
					},
				},
			},
			want: []*genai.Content{
				genai.NewContentFromParts([]*genai.Part{
					{Text: "File URL: report.pdf (application/pdf): https://example.com/report.pdf"},
				}, genai.RoleUser),
			},
		},
		{
			name: "empty file without URL",
			fields: fields{
				m: &Model{},
			},
			args: args{
				messages: []model.Message{
					{
						Role: model.RoleUser,
						ContentParts: []model.ContentPart{
							{
								Type: model.ContentTypeFile,
								File: &model.File{
									MimeType: "application/pdf",
								},
							},
						},
					},
				},
			},
			want: []*genai.Content{
				genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromBytes(nil, "application/pdf"),
				}, genai.RoleUser),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.fields.m.convertMessages(tt.args.messages),
				"convertMessages(%v)", tt.args.messages)
		})
	}
}

// Tool implements  tool.Declaration
type Tool struct {
	inputSchema  *tool.Schema
	outputSchema *tool.Schema
}

// tool.Declaration implements  tool.Declaration
func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         "tool",
		Description:  "tool description",
		InputSchema:  t.inputSchema,
		OutputSchema: t.outputSchema,
	}
}

func TestModel_convertTools_NilInputSchemaIsOmitted(t *testing.T) {
	m := &Model{}

	converted := m.convertTools(map[string]tool.Tool{
		"tool": &Tool{
			outputSchema: &tool.Schema{Type: "object"},
		},
	})
	require.Len(t, converted, 1)
	require.Len(t, converted[0].FunctionDeclarations, 1)

	fd := converted[0].FunctionDeclarations[0]
	require.Equal(t, "tool", fd.Name)
	require.Nil(t, fd.ParametersJsonSchema)
	require.NotNil(t, fd.ResponseJsonSchema)

	body, err := json.Marshal(fd)
	require.NoError(t, err)
	require.NotContains(t, string(body), "parametersJsonSchema")
}

func TestModel_convertTools_ObjectSchemasAreNormalized(t *testing.T) {
	m := &Model{}

	converted := m.convertTools(map[string]tool.Tool{
		"tool": &Tool{
			inputSchema: &tool.Schema{
				Type: "object",
			},
		},
	})
	require.Len(t, converted, 1)
	require.Len(t, converted[0].FunctionDeclarations, 1)

	fd := converted[0].FunctionDeclarations[0]
	require.NotNil(t, fd.ParametersJsonSchema)
	require.Nil(t, fd.ResponseJsonSchema)

	params, ok := fd.ParametersJsonSchema.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", params["type"])
	require.Contains(t, params, "properties")
}

func TestNormalizeToolSchema_ObjectAddsEmptyProperties(t *testing.T) {
	normalized := normalizeToolSchema(
		"tool",
		"input",
		&tool.Schema{Type: "object"},
	)

	out, ok := normalized.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", out["type"])
	props, ok := out["properties"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, props)
}

func TestNormalizeToolSchema_MarshalErrorFallsBack(t *testing.T) {
	normalized := normalizeToolSchema(
		"tool",
		"input",
		&tool.Schema{
			Type:                 "object",
			AdditionalProperties: func() {},
		},
	)

	out, ok := normalized.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", out["type"])
	props, ok := out["properties"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, props)
}

// namedTool is a test helper like Tool but with a configurable name,
// used to create distinct tool entries when testing multi-tool behaviour.
type namedTool struct {
	name        string
	inputSchema *tool.Schema
}

func (t *namedTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: t.name + " description",
		InputSchema: t.inputSchema,
	}
}

// TestModel_convertTools_EmptyMapReturnsNil verifies that convertTools returns
// nil (not an empty slice) when the tool map is empty, so the caller never
// sends an empty "tools" array to the Gemini API.
func TestModel_convertTools_EmptyMapReturnsNil(t *testing.T) {
	m := &Model{}
	require.Nil(t, m.convertTools(nil))
	require.Nil(t, m.convertTools(map[string]tool.Tool{}))
}

// TestModel_convertTools_MultipleToolsGroupedIntoSingleTool verifies the
// Vertex AI compatibility fix: all function declarations must be grouped into
// a single *genai.Tool object. Vertex AI rejects multiple Tool objects with:
// "Multiple tools are supported only when they are all search tools."
// It also verifies that declarations are emitted in sorted-key order so the
// output is deterministic across runs.
func TestModel_convertTools_MultipleToolsGroupedIntoSingleTool(t *testing.T) {
	m := &Model{}

	tools := map[string]tool.Tool{
		"alpha": &namedTool{name: "alpha", inputSchema: &tool.Schema{Type: "object"}},
		"beta":  &namedTool{name: "beta"},
		"gamma": &namedTool{name: "gamma", inputSchema: &tool.Schema{Type: "string"}},
	}

	converted := m.convertTools(tools)

	// Must produce exactly one *genai.Tool (Vertex AI constraint).
	require.Len(t, converted, 1, "all declarations must be grouped into a single genai.Tool")

	// Must contain one declaration per input tool.
	require.Len(t, converted[0].FunctionDeclarations, len(tools),
		"every tool must produce exactly one FunctionDeclaration")

	// Declarations must be in sorted (alphabetical) key order for determinism.
	got := make([]string, len(converted[0].FunctionDeclarations))
	for i, fd := range converted[0].FunctionDeclarations {
		got[i] = fd.Name
	}
	require.Equal(t, []string{"alpha", "beta", "gamma"}, got,
		"declarations must be sorted by tool name for deterministic output")
}

func TestNormalizeToolSchema_NilSchemaReturnsNil(t *testing.T) {
	require.Nil(t, normalizeToolSchema("tool", "input", nil))
}

func TestNormalizeToolSchema_UnmarshalErrorFallsBack(t *testing.T) {
	normalized := normalizeToolSchemaBytes("tool", "input", []byte("{"))

	out, ok := normalized.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", out["type"])
	props, ok := out["properties"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, props)
}

func TestModel_buildChatConfig(t *testing.T) {
	var (
		MaxTokens        = 10
		Temperature      = 0.01
		TopP             = 0.01
		PresencePenalty  = 0.1
		FrequencyPenalty = 0.1
		ThinkingTokens   = 100
		ThinkingLevel    = "low"
		ThinkingEnabled  = true
		Stop             = []string{"Stop"}
	)
	type fields struct {
		m *Model
	}
	type args struct {
		request *model.Request
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *genai.GenerateContentConfig
	}{
		{
			name: "buildChatConfig",
			fields: fields{
				m: &Model{},
			},
			args: args{
				request: &model.Request{
					Tools: map[string]tool.Tool{
						"tool": &Tool{},
					},
					StructuredOutput: &model.StructuredOutput{
						Type: model.StructuredOutputJSONSchema,
						JSONSchema: &model.JSONSchemaConfig{
							Name: "json_schema",
						},
					},
					GenerationConfig: model.GenerationConfig{
						MaxTokens:        &MaxTokens,
						Temperature:      &Temperature,
						TopP:             &TopP,
						PresencePenalty:  &PresencePenalty,
						FrequencyPenalty: &FrequencyPenalty,
						Stop:             Stop,
						ThinkingTokens:   &ThinkingTokens,
						ThinkingLevel:    &ThinkingLevel,
						ThinkingEnabled:  &ThinkingEnabled,
					},
				},
			},
			want: &genai.GenerateContentConfig{
				MaxOutputTokens:  int32(MaxTokens),
				Temperature:      genai.Ptr(float32(Temperature)),
				TopP:             genai.Ptr(float32(TopP)),
				StopSequences:    Stop,
				PresencePenalty:  genai.Ptr(float32(PresencePenalty)),
				FrequencyPenalty: genai.Ptr(float32(FrequencyPenalty)),
				ThinkingConfig: &genai.ThinkingConfig{
					ThinkingLevel:   genai.ThinkingLevel(ThinkingLevel),
					IncludeThoughts: true,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.fields.m.buildChatConfig(tt.args.request)
			assert.Equal(t, len(c.Tools), 1)
			assert.Equal(t, c.MaxOutputTokens, tt.want.MaxOutputTokens)
			assert.Equal(t, c.Temperature, tt.want.Temperature)
			assert.Equal(t, c.TopP, tt.want.TopP)
			assert.Equal(t, c.StopSequences, tt.want.StopSequences)
			assert.Equal(t, c.PresencePenalty, tt.want.PresencePenalty)
			assert.Equal(t, c.FrequencyPenalty, tt.want.FrequencyPenalty)
			assert.Equal(t, c.ThinkingConfig, tt.want.ThinkingConfig)
		})
	}
}

func TestModel_Info(t *testing.T) {
	type fields struct {
		m *Model
	}
	tests := []struct {
		name   string
		fields fields
		want   model.Info
	}{
		{
			name: "info",
			fields: fields{
				m: &Model{
					name: "gemini-pro",
				},
			},
			want: model.Info{
				Name: "gemini-pro",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.fields.m.Info(), "Info()")
		})
	}
}

func TestModel_buildFinalResponse(t *testing.T) {
	finishReason := "FinishReason"
	now := time.Now()
	functionArgs := map[string]any{"args": "1"}
	functionArgsBytes, _ := json.Marshal(functionArgs)
	thoughtSignature := []byte("thought-signature")

	type fields struct {
		m *Model
	}
	type args struct {
		chatCompletion *genai.GenerateContentResponse
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *model.Response
	}{
		{
			name:   "nil-req",
			fields: fields{m: &Model{}},
			args:   args{chatCompletion: nil},
			want: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Done:   true,
			},
		},
		{
			name:   "empty-usage",
			fields: fields{m: &Model{}},
			args:   args{chatCompletion: &genai.GenerateContentResponse{}},
			want: &model.Response{
				Object:  model.ObjectTypeChatCompletion,
				Created: (time.Time{}).Unix(),
				Done:    true,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role: model.RoleAssistant,
						},
					},
				},
			},
		},
		{
			name:   "buildFinalResponse",
			fields: fields{m: &Model{}},
			args: args{
				chatCompletion: &genai.GenerateContentResponse{
					ResponseID:   "1",
					CreateTime:   now,
					ModelVersion: "pro-v1",
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: ""},
									{
										Thought: true,
										Text:    "Thought",
										FunctionCall: &genai.FunctionCall{
											ID:   "id",
											Name: "function_call",
											Args: functionArgs,
										},
										ThoughtSignature: thoughtSignature,
									},
									{Text: "Answer"},
								},
							},
							FinishReason: genai.FinishReason(finishReason),
						},
					},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:        1,
						CandidatesTokenCount:    1,
						TotalTokenCount:         2,
						CachedContentTokenCount: 1,
					},
				},
			},
			want: &model.Response{
				ID:        "1",
				Object:    model.ObjectTypeChatCompletion,
				Created:   now.Unix(),
				Timestamp: now,
				Model:     "pro-v1",
				Done:      true,
				Choices: []model.Choice{
					{
						Index:        0,
						FinishReason: &finishReason,
						Message: model.Message{
							Role:             model.RoleAssistant,
							ReasoningContent: "Thought",
							Content:          "Answer",
							ToolCalls: []model.ToolCall{
								{
									ID: "id",
									Function: model.FunctionDefinitionParam{
										Name:      "function_call",
										Arguments: functionArgsBytes,
									},
									ExtraFields: map[string]any{
										geminiThoughtSignatureKey: thoughtSignature,
									},
								},
							},
						},
					},
				},
				Usage: &model.Usage{
					PromptTokens:     1,
					TotalTokens:      2,
					CompletionTokens: 1,
					PromptTokensDetails: model.PromptTokensDetails{
						CachedTokens: 1,
					},
				},
			},
		},
		{
			name:   "buildFinalResponse-functionCall-empty-text",
			fields: fields{m: &Model{}},
			args: args{
				chatCompletion: &genai.GenerateContentResponse{
					ResponseID:   "2",
					CreateTime:   now,
					ModelVersion: "pro-v1",
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{
										FunctionCall: &genai.FunctionCall{
											ID:   "id",
											Name: "function_call",
											Args: functionArgs,
										},
									},
									{Text: "Answer"},
								},
							},
							FinishReason: genai.FinishReason(finishReason),
						},
					},
				},
			},
			want: &model.Response{
				ID:        "2",
				Object:    model.ObjectTypeChatCompletion,
				Created:   now.Unix(),
				Timestamp: now,
				Model:     "pro-v1",
				Done:      true,
				Choices: []model.Choice{
					{
						Index:        0,
						FinishReason: &finishReason,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Answer",
							ToolCalls: []model.ToolCall{
								{
									ID: "id",
									Function: model.FunctionDefinitionParam{
										Name:      "function_call",
										Arguments: functionArgsBytes,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := tt.fields.m.buildFinalResponse(tt.args.chatCompletion)
			assert.Equal(t, tt.want, response)
		})
	}
}

func TestModel_buildChunkResponse(t *testing.T) {
	finishReason := "FinishReason"
	now := time.Now()
	functionArgs := map[string]any{"args": "1"}
	functionArgsBytes, _ := json.Marshal(functionArgs)
	thoughtSignature := []byte("thought-signature")

	type fields struct {
		m *Model
	}
	type args struct {
		chatCompletion *genai.GenerateContentResponse
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *model.Response
	}{
		{
			name:   "nil-req",
			fields: fields{m: &Model{}},
			args:   args{chatCompletion: nil},
			want: &model.Response{
				Object:    model.ObjectTypeChatCompletionChunk,
				IsPartial: true,
			},
		},
		{
			name:   "buildChunkResponse",
			fields: fields{m: &Model{}},
			args: args{
				chatCompletion: &genai.GenerateContentResponse{
					ResponseID:   "1",
					CreateTime:   now,
					ModelVersion: "pro-v1",
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: ""},
									{
										Thought: true,
										Text:    "Thought",
										FunctionCall: &genai.FunctionCall{
											ID:   "id",
											Name: "function_call",
											Args: functionArgs,
										},
										ThoughtSignature: thoughtSignature,
									},
									{Text: "Answer"},
								},
							},
							FinishReason: genai.FinishReason(finishReason),
						},
					},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:        1,
						CandidatesTokenCount:    1,
						TotalTokenCount:         2,
						CachedContentTokenCount: 1,
					},
				},
			},
			want: &model.Response{
				ID:        "1",
				Object:    model.ObjectTypeChatCompletionChunk,
				Created:   now.Unix(),
				Timestamp: now,
				Model:     "pro-v1",
				IsPartial: true,
				Choices: []model.Choice{
					{
						Index:        0,
						FinishReason: &finishReason,
						Delta: model.Message{
							Role:             model.RoleAssistant,
							ReasoningContent: "Thought",
							Content:          "Answer",
							ToolCalls: []model.ToolCall{
								{
									ID: "id",
									Function: model.FunctionDefinitionParam{
										Name:      "function_call",
										Arguments: functionArgsBytes,
									},
									ExtraFields: map[string]any{
										geminiThoughtSignatureKey: thoughtSignature,
									},
								},
							},
						},
					},
				},
				Usage: &model.Usage{
					PromptTokens:     1,
					TotalTokens:      2,
					CompletionTokens: 1,
					PromptTokensDetails: model.PromptTokensDetails{
						CachedTokens: 1,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := tt.fields.m.buildChunkResponse(tt.args.chatCompletion)
			assert.Equal(t, tt.want, response)
		})
	}
}

func TestModel_buildChatConfig_ThinkingTokensFallback(t *testing.T) {
	thinkingTokens := 100
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			ThinkingTokens: &thinkingTokens,
		},
	}

	cfg := (&Model{}).buildChatConfig(req)

	require.NotNil(t, cfg.ThinkingConfig)
	require.NotNil(t, cfg.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, int32(thinkingTokens), *cfg.ThinkingConfig.ThinkingBudget)
	assert.Empty(t, cfg.ThinkingConfig.ThinkingLevel)
}

func TestModel_buildThinkingConfig_ThinkingLevelTakesPrecedenceOverBudget(t *testing.T) {
	thinkingTokens := 100
	thinkingLevel := "low"
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			ThinkingTokens: &thinkingTokens,
			ThinkingLevel:  &thinkingLevel,
		},
	}

	cfg := (&Model{}).buildThinkingConfig(req)

	require.NotNil(t, cfg)
	require.Nil(t, cfg.ThinkingBudget)
	assert.Equal(t, genai.ThinkingLevel(thinkingLevel), cfg.ThinkingLevel)
}

func TestModel_buildFinalResponse_PreservesTextThoughtSignature(t *testing.T) {
	now := time.Now()
	thoughtSignature := []byte("text-thought-signature")
	response := (&Model{}).buildFinalResponse(&genai.GenerateContentResponse{
		ResponseID:   "3",
		CreateTime:   now,
		ModelVersion: "pro-v1",
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							Text:             "Answer",
							ThoughtSignature: thoughtSignature,
						},
					},
				},
			},
		},
	})

	require.Len(t, response.Choices, 1)
	msg := response.Choices[0].Message
	assert.Equal(t, "Answer", msg.Content)
	assert.Equal(t, base64.StdEncoding.EncodeToString(thoughtSignature), msg.ReasoningSignature)
}

func TestModel_convertMessageContent_PreservesTextThoughtSignature(t *testing.T) {
	signature := []byte("text-thought-signature")
	message := model.Message{
		Role:               model.RoleAssistant,
		Content:            "Answer",
		ReasoningSignature: base64.StdEncoding.EncodeToString(signature),
	}

	contents := (&Model{}).convertMessageContent(message)

	require.Len(t, contents, 1)
	require.Equal(t, genai.RoleModel, contents[0].Role)
	require.Len(t, contents[0].Parts, 1)
	assert.Equal(t, "Answer", contents[0].Parts[0].Text)
	assert.Equal(t, signature, contents[0].Parts[0].ThoughtSignature)
}

func TestModel_convertMessageContent_PreservesFunctionCallThoughtSignature(t *testing.T) {
	signature := []byte("thought-signature")
	args := []byte(`{"city":"shenzhen"}`)
	message := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{
				ID: "call-1",
				Function: model.FunctionDefinitionParam{
					Name:      "weather",
					Arguments: args,
				},
				ExtraFields: map[string]any{
					geminiThoughtSignatureKey: signature,
				},
			},
		},
	}

	contents := (&Model{}).convertMessageContent(message)

	require.Len(t, contents, 1)
	require.Equal(t, genai.RoleModel, contents[0].Role)
	require.Len(t, contents[0].Parts, 1)
	part := contents[0].Parts[0]
	require.NotNil(t, part.FunctionCall)
	assert.Equal(t, "call-1", part.FunctionCall.ID)
	assert.Equal(t, "weather", part.FunctionCall.Name)
	assert.Equal(t, signature, part.ThoughtSignature)
}

func TestModel_convertMessageContent_InjectsSkipValidatorForCrossProviderFunctionCall(t *testing.T) {
	args := []byte(`{"command":"echo hi"}`)
	message := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{
				ID: "call-1",
				Function: model.FunctionDefinitionParam{
					Name:      "execute_series",
					Arguments: args,
				},
			},
			{
				ID: "call-2",
				Function: model.FunctionDefinitionParam{
					Name:      "execute_series",
					Arguments: args,
				},
			},
		},
	}

	contents := (&Model{}).convertMessageContent(message)

	require.Len(t, contents, 2)
	first := contents[0].Parts[0]
	second := contents[1].Parts[0]
	require.NotNil(t, first.FunctionCall)
	require.NotNil(t, second.FunctionCall)
	assert.Equal(t, []byte(geminiSkipThoughtSignatureValidator), first.ThoughtSignature)
	assert.Empty(t, second.ThoughtSignature)
}

func TestModel_convertToolCallPart_EdgeCases(t *testing.T) {
	t.Run("empty function name returns nil", func(t *testing.T) {
		assert.Nil(t, (&Model{}).convertToolCallPart(model.ToolCall{}, true))
	})

	t.Run("invalid json args falls back to empty args", func(t *testing.T) {
		part := (&Model{}).convertToolCallPart(model.ToolCall{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      "weather",
				Arguments: []byte(`{"city"`),
			},
		}, true)

		require.NotNil(t, part)
		require.NotNil(t, part.FunctionCall)
		assert.Empty(t, part.FunctionCall.Args)
	})
}

func TestThoughtSignatureFromExtraFields(t *testing.T) {
	signature := []byte("thought-signature")
	encoded := base64.StdEncoding.EncodeToString(signature)

	tests := []struct {
		name        string
		extraFields map[string]any
		want        []byte
	}{
		{
			name: "nil extra fields",
		},
		{
			name:        "missing signature key",
			extraFields: map[string]any{"other": signature},
		},
		{
			name:        "byte slice signature is copied",
			extraFields: map[string]any{geminiThoughtSignatureKey: signature},
			want:        signature,
		},
		{
			name:        "base64 string signature decodes",
			extraFields: map[string]any{geminiThoughtSignatureKey: encoded},
			want:        signature,
		},
		{
			name:        "plain string signature falls back to raw bytes",
			extraFields: map[string]any{geminiThoughtSignatureKey: "plain-signature"},
			want:        []byte("plain-signature"),
		},
		{
			name:        "unsupported type returns nil",
			extraFields: map[string]any{geminiThoughtSignatureKey: 123},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := thoughtSignatureFromExtraFields(tt.extraFields)
			assert.Equal(t, tt.want, got)
			if raw, ok := tt.extraFields[geminiThoughtSignatureKey].([]byte); ok && len(got) > 0 {
				got[0] = 'X'
				assert.NotEqual(t, got[0], raw[0], "expected signature bytes to be copied")
			}
		})
	}
}

func TestThoughtSignatureFromString(t *testing.T) {
	signature := []byte("text-thought-signature")
	encoded := base64.StdEncoding.EncodeToString(signature)

	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{name: "empty"},
		{name: "whitespace", in: " \t\n "},
		{name: "base64", in: " " + encoded + " ", want: signature},
		{name: "plain", in: " plain-signature ", want: []byte("plain-signature")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, thoughtSignatureFromString(tt.in))
		})
	}
}

func TestNormalizeThinkingLevel_TrimsWhitespace(t *testing.T) {
	assert.Equal(t, genai.ThinkingLevel("low"), normalizeThinkingLevel(" low "))
}

func TestNew(t *testing.T) {
	config := &model.TokenTailoringConfig{
		ProtocolOverheadTokens: 1024,
		ReserveOutputTokens:    4096,
		InputTokensFloor:       2048,
		OutputTokensFloor:      512,
		SafetyMarginRatio:      0.15,
		MaxInputTokensRatio:    0.90,
	}
	type args struct {
		ctx  context.Context
		name string
		opts []Option
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "failed",
			args: args{
				ctx:  context.Background(),
				name: "gemini-pro",
				opts: []Option{
					WithTokenTailoringConfig(config),
					WithMaxInputTokens(10),
				},
			},
			wantErr: true,
		},
		{
			name: "success",
			args: args{
				ctx:  context.Background(),
				name: "gemini-pro",
				opts: []Option{
					WithTokenTailoringConfig(config),
					WithMaxInputTokens(10),
					WithGeminiClientConfig(
						&genai.ClientConfig{
							APIKey:     "APIKey",
							Backend:    2,
							HTTPClient: http.DefaultClient,
						},
					),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr {
				t.Setenv("GOOGLE_API_KEY", "")
				t.Setenv("GEMINI_API_KEY", "")
			}
			_, err := New(tt.args.ctx, tt.args.name, tt.args.opts...)
			if tt.wantErr {
				assert.NotNil(t, err)
				return
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

// MockClient is a mock of Client interface.
type MockClient struct {
	ctrl     *gomock.Controller
	recorder *MockClientMockRecorder
	isgomock struct{}
}

// MockClientMockRecorder is the mock recorder for MockClient.
type MockClientMockRecorder struct {
	mock *MockClient
}

// NewMockClient creates a new mock instance.
func NewMockClient(ctrl *gomock.Controller) *MockClient {
	mock := &MockClient{ctrl: ctrl}
	mock.recorder = &MockClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockClient) EXPECT() *MockClientMockRecorder {
	return m.recorder
}

// Models mocks base method.
func (m *MockClient) Models() Models {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Models")
	ret0, _ := ret[0].(Models)
	return ret0
}

// Models indicates an expected call of Models.
func (mr *MockClientMockRecorder) Models() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Models", reflect.TypeOf((*MockClient)(nil).Models))
}

// MockModels is a mock of Models interface.
type MockModels struct {
	ctrl     *gomock.Controller
	recorder *MockModelsMockRecorder
	isgomock struct{}
}

// MockModelsMockRecorder is the mock recorder for MockModels.
type MockModelsMockRecorder struct {
	mock *MockModels
}

// NewMockModels creates a new mock instance.
func NewMockModels(ctrl *gomock.Controller) *MockModels {
	mock := &MockModels{ctrl: ctrl}
	mock.recorder = &MockModelsMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockModels) EXPECT() *MockModelsMockRecorder {
	return m.recorder
}

// GenerateContent mocks base method.
func (m *MockModels) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GenerateContent", ctx, model, contents, config)
	ret0, _ := ret[0].(*genai.GenerateContentResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GenerateContent indicates an expected call of GenerateContent.
func (mr *MockModelsMockRecorder) GenerateContent(ctx, model, contents, config any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GenerateContent", reflect.TypeOf((*MockModels)(nil).GenerateContent), ctx, model, contents, config)
}

// GenerateContentStream mocks base method.
func (m *MockModels) GenerateContentStream(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GenerateContentStream", ctx, model, contents, config)
	ret0, _ := ret[0].(iter.Seq2[*genai.GenerateContentResponse, error])
	return ret0
}

// GenerateContentStream indicates an expected call of GenerateContentStream.
func (mr *MockModelsMockRecorder) GenerateContentStream(ctx, model, contents, config any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GenerateContentStream", reflect.TypeOf((*MockModels)(nil).GenerateContentStream), ctx, model, contents, config)
}

func TestModel_GenerateContentError(t *testing.T) {
	subText := "subText"
	req := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleAssistant,
				Content: "text",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &subText,
					},
				},
			},
		},
	}
	err := errors.New("error")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// 创建 Mock
	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	mockModels.EXPECT().
		GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, err).AnyTimes()
	type args struct {
		ctx     context.Context
		request *model.Request
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "nil request",
			args: args{
				ctx: context.Background(),
			},
		},
		{
			name: "error",
			args: args{
				ctx:     context.Background(),
				request: req,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{
				client: mockClient,
			}
			_, _ = m.GenerateContent(tt.args.ctx, tt.args.request)
		})
	}
}

func TestModel_GenerateContent_NoContentAfterConversion(t *testing.T) {
	m := &Model{name: "gemini-test"}
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleAssistant},
		},
	}

	ch, err := m.GenerateContent(context.Background(), req)
	require.Error(t, err)
	require.EqualError(t, err, "gemini: no content after message conversion")
	require.Nil(t, ch)
}

func TestModel_GenerateContentNoStream(t *testing.T) {
	subText := "subText"
	now := time.Now()
	req := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleAssistant,
				Content: "text",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &subText,
					},
				},
			},
		},
	}
	resp := &genai.GenerateContentResponse{
		ResponseID:   "1",
		CreateTime:   now,
		ModelVersion: "pro-v1",
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							Text: "",
						},
						{
							Text: "Answer",
						},
					},
				},
				FinishReason: genai.FinishReason("finishReason"),
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        1,
			CandidatesTokenCount:    1,
			TotalTokenCount:         2,
			CachedContentTokenCount: 1,
		},
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// 创建 Mock
	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	mockModels.EXPECT().
		GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(resp, nil).AnyTimes()
	type args struct {
		ctx     context.Context
		request *model.Request
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "no-stream",
			args: args{
				ctx:     context.Background(),
				request: req,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{
				client: mockClient,
				chatRequestCallback: func(ctx context.Context, chatRequest []*genai.Content) {
				},
				chatResponseCallback: func(ctx context.Context, chatRequest []*genai.Content,
					generateConfig *genai.GenerateContentConfig, chatResponse *genai.GenerateContentResponse) {
				},
			}
			_, err := m.GenerateContent(tt.args.ctx, tt.args.request)
			assert.Nil(t, err)
		})
	}
}

func TestModel_GenerateContentStreaming(t *testing.T) {
	subText := "subText"
	now := time.Now()
	req := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleAssistant,
				Content: "text",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &subText,
					},
				},
			},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	resp := &genai.GenerateContentResponse{
		ResponseID:   "1",
		CreateTime:   now,
		ModelVersion: "pro-v1",
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							Text: "",
						},
						{
							Text: "Answer",
						},
					},
				},
				FinishReason: genai.FinishReason("finishReason"),
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        1,
			CandidatesTokenCount:    1,
			TotalTokenCount:         2,
			CachedContentTokenCount: 1,
		},
	}
	ctrl := gomock.NewController(t)

	// 创建 Mock
	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	mockModels.EXPECT().
		GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(seqFromSlice([]*genai.GenerateContentResponse{resp, resp})).AnyTimes()
	type args struct {
		ctx     context.Context
		request *model.Request
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "stream",
			args: args{
				ctx:     context.Background(),
				request: req,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{
				client:                 mockClient,
				enableTokenTailoring:   true,
				protocolOverheadTokens: 1,
				chatChunkCallback: func(ctx context.Context, chatRequest []*genai.Content,
					generateConfig *genai.GenerateContentConfig, chatResponse *genai.GenerateContentResponse) {
				},
				chatStreamCompleteCallback: func(ctx context.Context, chatRequest []*genai.Content,
					generateConfig *genai.GenerateContentConfig, chatResponse *model.Response) {
				},
				chatRequestCallback: func(ctx context.Context, chatRequest []*genai.Content) {
				},
				tokenCounter:      model.NewSimpleTokenCounter(),
				tailoringStrategy: model.NewMiddleOutStrategy(model.NewSimpleTokenCounter()),
			}
			_, err := m.GenerateContent(tt.args.ctx, tt.args.request)
			assert.Nil(t, err)
		})
	}
	ctrl.Finish()
}

func seqFromSlice[T any](items []T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
	}
}

// seqFromSliceWithError creates an iter.Seq2 that yields items then returns an error.
func seqFromSliceWithError[T any](items []T, err error) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
		// Yield the error after all items
		var zero T
		yield(zero, err)
	}
}

// seqWithImmediateError creates an iter.Seq2 that immediately returns an error.
func seqWithImmediateError[T any](err error) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		yield(zero, err)
	}
}

func TestModel_GenerateContentStreamingError(t *testing.T) {
	subText := "subText"
	req := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleAssistant,
				Content: "text",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &subText,
					},
				},
			},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	t.Run("immediate_stream_error", func(t *testing.T) {
		// Test when the stream immediately returns an error on the first chunk
		streamErr := errors.New("stream connection failed")
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqWithImmediateError[*genai.GenerateContentResponse](streamErr)).AnyTimes()

		m := &Model{
			client: mockClient,
		}
		respChan, err := m.GenerateContent(context.Background(), req)
		assert.Nil(t, err)

		// Read response from channel
		resp := <-respChan
		assert.NotNil(t, resp)
		assert.NotNil(t, resp.Error)
		assert.Equal(t, "stream connection failed", resp.Error.Message)
		assert.Equal(t, model.ErrorTypeAPIError, resp.Error.Type)
		assert.True(t, resp.Done)

		// Verify channel is closed and no extra messages are delivered
		select {
		case extraMsg, ok := <-respChan:
			if ok {
				t.Errorf("unexpected extra message received after error: %+v", extraMsg)
			}
			// Channel closed as expected
		case <-time.After(100 * time.Millisecond):
			t.Error("channel was not closed after error - potential goroutine leak")
		}
	})

	t.Run("mid_stream_error", func(t *testing.T) {
		// Test when the stream returns some chunks then fails
		now := time.Now()
		streamErr := errors.New("stream interrupted")
		successChunk := &genai.GenerateContentResponse{
			ResponseID:   "1",
			CreateTime:   now,
			ModelVersion: "pro-v1",
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "partial response"},
						},
					},
				},
			},
		}

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqFromSliceWithError([]*genai.GenerateContentResponse{successChunk}, streamErr)).AnyTimes()

		m := &Model{
			client: mockClient,
		}
		respChan, err := m.GenerateContent(context.Background(), req)
		assert.Nil(t, err)

		// First response should be the successful chunk
		resp := <-respChan
		assert.NotNil(t, resp)
		assert.Nil(t, resp.Error)
		assert.Len(t, resp.Choices, 1)
		assert.Equal(t, "partial response", resp.Choices[0].Delta.Content)

		// Second response should be the error
		errorResp := <-respChan
		assert.NotNil(t, errorResp)
		assert.NotNil(t, errorResp.Error)
		assert.Equal(t, "stream interrupted", errorResp.Error.Message)
		assert.Equal(t, model.ErrorTypeAPIError, errorResp.Error.Type)
		assert.True(t, errorResp.Done)
	})

	t.Run("error_with_callbacks", func(t *testing.T) {
		// Test that an immediate streaming error skips chunk callbacks but still
		// invokes the stream-complete callback before the error becomes visible.
		streamErr := errors.New("callback test error")
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqWithImmediateError[*genai.GenerateContentResponse](streamErr)).AnyTimes()

		chunkCallbackCalled := false
		completeCallbackCalled := make(chan struct{})
		var completeCallbackResp *model.Response

		m := &Model{
			client: mockClient,
			chatChunkCallback: func(ctx context.Context, chatRequest []*genai.Content,
				generateConfig *genai.GenerateContentConfig, chatResponse *genai.GenerateContentResponse) {
				chunkCallbackCalled = true
			},
			chatStreamCompleteCallback: func(ctx context.Context, chatRequest []*genai.Content,
				generateConfig *genai.GenerateContentConfig, chatResponse *model.Response) {
				completeCallbackResp = chatResponse
				close(completeCallbackCalled)
			},
		}
		respChan, err := m.GenerateContent(context.Background(), req)
		assert.Nil(t, err)

		// Read response (error)
		resp := <-respChan
		assert.NotNil(t, resp.Error)
		select {
		case <-completeCallbackCalled:
		default:
			t.Fatal("stream-complete callback must run before error response is emitted")
		}

		// Wait for channel to close
		for range respChan {
		}

		// Chunk callbacks should still not run on immediate errors.
		assert.False(t, chunkCallbackCalled, "chunk callback should not be called on immediate error")
		require.NotNil(t, completeCallbackResp)
		require.NotNil(t, completeCallbackResp.Error)
		assert.Equal(t, "callback test error", completeCallbackResp.Error.Message)
	})

	t.Run("mid_stream_error_with_chunk_callback", func(t *testing.T) {
		// When a mid-stream error occurs after some buffered chunks, the
		// chatChunkCallback must be invoked for each flushed chunk before the
		// error response is emitted.  This exercises lines 291-292 of gemini.go.
		streamErr := errors.New("mid-stream interruption")
		successChunk := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{{Text: "partial"}}}},
			},
		}

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqFromSliceWithError([]*genai.GenerateContentResponse{successChunk}, streamErr))

		var chunkCallbackRaw []*genai.GenerateContentResponse
		completeCallbackCalled := make(chan struct{})
		var completeCallbackResp *model.Response
		m := &Model{
			client: mockClient,
			chatChunkCallback: func(_ context.Context, _ []*genai.Content,
				_ *genai.GenerateContentConfig, r *genai.GenerateContentResponse) {
				chunkCallbackRaw = append(chunkCallbackRaw, r)
			},
			chatStreamCompleteCallback: func(_ context.Context, _ []*genai.Content,
				_ *genai.GenerateContentConfig, r *model.Response) {
				completeCallbackResp = r
				close(completeCallbackCalled)
			},
		}
		respChan, err := m.GenerateContent(context.Background(), req)
		require.NoError(t, err)

		partialResp := <-respChan
		require.NotNil(t, partialResp)
		require.Nil(t, partialResp.Error)
		require.Equal(t, "partial", partialResp.Choices[0].Delta.Content)

		errorResp := <-respChan
		require.NotNil(t, errorResp)
		require.NotNil(t, errorResp.Error)
		require.Equal(t, "mid-stream interruption", errorResp.Error.Message)
		select {
		case <-completeCallbackCalled:
		default:
			t.Fatal("stream-complete callback must run before error response is emitted")
		}
		for range respChan {
		}
		// chatChunkCallback must have been called once (for the flushed chunk).
		require.Len(t, chunkCallbackRaw, 1)
		require.Equal(t, successChunk, chunkCallbackRaw[0])
		require.NotNil(t, completeCallbackResp)
		require.NotNil(t, completeCallbackResp.Error)
		require.Equal(t, "mid-stream interruption", completeCallbackResp.Error.Message)
	})
}

func TestModel_convertContentPartNil(t *testing.T) {
	type args struct {
		part model.ContentPart
	}
	tests := []struct {
		name string
		args args
		want *genai.Part
	}{
		{
			name: "nil-Type",
			args: args{
				part: model.ContentPart{},
			},
			want: nil,
		},
		{
			name: "empty-image",
			args: args{
				part: model.ContentPart{
					Type: model.ContentTypeImage,
				},
			},
			want: nil,
		},
		{
			name: "empty-audio",
			args: args{
				part: model.ContentPart{
					Type: model.ContentTypeAudio,
				},
			},
			want: nil,
		},
		{
			name: "empty-file",
			args: args{
				part: model.ContentPart{
					Type: model.ContentTypeFile,
				},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{}
			assert.Equalf(t, tt.want, m.convertContentPart(tt.args.part), "convertContentPart(%v)", tt.args.part)
		})
	}
}

// testStubCounter is a stub TokenCounter for testing token tailoring.
type testStubCounter struct{}

func (testStubCounter) CountTokens(
	ctx context.Context,
	message model.Message,
) (int, error) {
	return 1, nil
}

func (testStubCounter) CountTokensRange(
	ctx context.Context,
	messages []model.Message,
	start,
	end int,
) (int, error) {
	if start < 0 || end > len(messages) || start >= end {
		return 0, fmt.Errorf("invalid range: start=%d, end=%d, len=%d", start, end, len(messages))
	}
	return end - start, nil
}

// emptyTailoringStrategy returns empty slice always.
type emptyTailoringStrategy struct{}

func (emptyTailoringStrategy) TailorMessages(
	ctx context.Context,
	messages []model.Message,
	maxTokens int,
) ([]model.Message, error) {
	return []model.Message{}, nil
}

// TestWithTokenTailoring_PreservesOriginalOnEmptyResult verifies empty tailoring
// results do not wipe a non-empty request (modeltailoring.ApplyResult guard).
func TestWithTokenTailoring_PreservesOriginalOnEmptyResult(t *testing.T) {
	original := []model.Message{model.NewUserMessage("A")}
	m := &Model{
		enableTokenTailoring: true,
		maxInputTokens:       100,
		tokenCounter:         testStubCounter{},
		tailoringStrategy:    emptyTailoringStrategy{},
	}
	req := &model.Request{Messages: append([]model.Message(nil), original...)}
	m.applyTokenTailoring(context.Background(), req)
	require.Equal(t, original, req.Messages)
}
