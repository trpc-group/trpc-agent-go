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
	"encoding/json"
	"errors"
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
					ThinkingBudget:  genai.Ptr(int32(ThinkingTokens)),
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

// TestModel_convertContentBlock_SyntheticFunctionCallID verifies the Vertex AI
// compatibility fix: when Gemini omits FunctionCall.ID, convertContentBlock
// generates a synthetic sequential ID ("gemini_call_N") so the framework's
// SanitizeMessagesWithTools can match tool results to their calls.
func TestModel_convertContentBlock_SyntheticFunctionCallID(t *testing.T) {
	m := &Model{}

	candidates := []*genai.Candidate{
		{
			Content: genai.NewContentFromParts([]*genai.Part{
				{FunctionCall: &genai.FunctionCall{Name: "my_tool", Args: map[string]any{"x": 1.0}}},
			}, genai.RoleModel),
		},
	}

	msg, _ := m.convertContentBlock(candidates)

	require.Len(t, msg.ToolCalls, 1)
	tc := msg.ToolCalls[0]
	require.NotEmpty(t, tc.ID, "ID must be non-empty when Vertex AI omits FunctionCall.ID")
	require.Contains(t, tc.ID, "gemini_call_", "synthetic ID must follow the gemini_call_N pattern")
	require.Equal(t, "my_tool", tc.Function.Name)
}

// TestModel_convertContentBlock_PreservesExistingFunctionCallID verifies that
// when Gemini (non-Vertex) does populate FunctionCall.ID, that original ID is
// preserved unchanged.
func TestModel_convertContentBlock_PreservesExistingFunctionCallID(t *testing.T) {
	m := &Model{}

	const wantID = "call-abc-123"
	candidates := []*genai.Candidate{
		{
			Content: genai.NewContentFromParts([]*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: wantID, Name: "my_tool"}},
			}, genai.RoleModel),
		},
	}

	msg, _ := m.convertContentBlock(candidates)

	require.Len(t, msg.ToolCalls, 1)
	require.Equal(t, wantID, msg.ToolCalls[0].ID, "existing ID must not be replaced by a synthetic one")
}

// TestModel_convertMessageContent_ToolRoleProducesFunctionResponse verifies
// that a RoleTool message is converted to a FunctionResponse part (role=user)
// rather than plain text, as required by the Gemini generateContent API.
func TestModel_convertMessageContent_ToolRoleProducesFunctionResponse(t *testing.T) {
	m := &Model{}

	msg := model.Message{
		Role:     model.RoleTool,
		ToolName: "my_tool",
		Content:  `{"status":"ok","value":42}`,
	}

	contents := m.convertMessageContent(msg)

	require.Len(t, contents, 1)
	require.Equal(t, genai.RoleUser, contents[0].Role)
	require.Len(t, contents[0].Parts, 1)
	fr := contents[0].Parts[0].FunctionResponse
	require.NotNil(t, fr, "part must be a FunctionResponse, not plain text")
	require.Equal(t, "my_tool", fr.Name)
	require.Equal(t, map[string]any{"status": "ok", "value": float64(42)}, fr.Response)
}

// TestModel_convertMessageContent_ToolRoleNonJSONWrapsInOutput verifies that
// non-JSON tool output (e.g. an error string) is wrapped in {"output": ...}
// so the FunctionResponse.Response field is always a valid JSON object.
func TestModel_convertMessageContent_ToolRoleNonJSONWrapsInOutput(t *testing.T) {
	m := &Model{}

	msg := model.Message{
		Role:     model.RoleTool,
		ToolName: "my_tool",
		Content:  "tool execution failed: permission denied",
	}

	contents := m.convertMessageContent(msg)

	require.Len(t, contents, 1)
	fr := contents[0].Parts[0].FunctionResponse
	require.NotNil(t, fr)
	require.Equal(t, map[string]any{"output": "tool execution failed: permission denied"}, fr.Response)
}

// TestIsMalformedFunctionCall verifies the helper that detects
// MALFORMED_FUNCTION_CALL in a raw genai response.
func TestIsMalformedFunctionCall(t *testing.T) {
	require.False(t, isMalformedFunctionCall(nil))
	require.False(t, isMalformedFunctionCall(&genai.GenerateContentResponse{}))
	require.False(t, isMalformedFunctionCall(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{FinishReason: genai.FinishReason("STOP")}},
	}))
	require.True(t, isMalformedFunctionCall(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")}},
	}))
}

// TestIsMalformedModelResponse verifies the helper that detects
// MALFORMED_FUNCTION_CALL in an already-converted model.Response.
func TestIsMalformedModelResponse(t *testing.T) {
	reason := "MALFORMED_FUNCTION_CALL"
	other := "STOP"
	require.False(t, isMalformedModelResponse(nil))
	require.False(t, isMalformedModelResponse(&model.Response{}))
	require.False(t, isMalformedModelResponse(&model.Response{
		Choices: []model.Choice{{FinishReason: &other}},
	}))
	require.False(t, isMalformedModelResponse(&model.Response{
		Choices: []model.Choice{{FinishReason: nil}},
	}))
	require.True(t, isMalformedModelResponse(&model.Response{
		Choices: []model.Choice{{FinishReason: &reason}},
	}))
}

// TestRetryConfigForMalformed verifies that retryConfigForMalformed sets
// temperature=0 and FunctionCallingMode=ANY while preserving other fields.
func TestRetryConfigForMalformed(t *testing.T) {
	t.Run("no existing ToolConfig", func(t *testing.T) {
		origTemp := float32(0.9)
		orig := &genai.GenerateContentConfig{
			Temperature:     &origTemp,
			MaxOutputTokens: 100,
		}
		retry := retryConfigForMalformed(orig)

		require.NotNil(t, retry.Temperature)
		require.Equal(t, float32(0), *retry.Temperature, "temperature must be 0")
		require.NotNil(t, retry.ToolConfig)
		require.NotNil(t, retry.ToolConfig.FunctionCallingConfig)
		require.Equal(t, genai.FunctionCallingConfigModeAny, retry.ToolConfig.FunctionCallingConfig.Mode)
		// Original must not be mutated.
		require.Equal(t, float32(0.9), *orig.Temperature)
		require.Nil(t, orig.ToolConfig)
		// Non-temperature fields preserved.
		require.Equal(t, int32(100), retry.MaxOutputTokens)
	})

	t.Run("preserves existing ToolConfig fields", func(t *testing.T) {
		origTemp := float32(0.7)
		allowedFns := []string{"myFunc"}
		orig := &genai.GenerateContentConfig{
			Temperature: &origTemp,
			ToolConfig: &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode:                 genai.FunctionCallingConfigModeAuto,
					AllowedFunctionNames: allowedFns,
				},
			},
		}
		retry := retryConfigForMalformed(orig)

		require.Equal(t, float32(0), *retry.Temperature)
		// Mode is overridden to ANY.
		require.Equal(t, genai.FunctionCallingConfigModeAny, retry.ToolConfig.FunctionCallingConfig.Mode)
		// AllowedFunctionNames is preserved.
		require.Equal(t, allowedFns, retry.ToolConfig.FunctionCallingConfig.AllowedFunctionNames)
		// Original ToolConfig is not mutated.
		require.Equal(t, genai.FunctionCallingConfigModeAuto, orig.ToolConfig.FunctionCallingConfig.Mode)
	})
}

// TestModel_NonStreaming_MalformedFunctionCallRetry verifies that when
// handleNonStreamingResponse receives MALFORMED_FUNCTION_CALL on the first
// attempt, it silently retries and emits the successful retry response.
func TestModel_NonStreaming_MalformedFunctionCallRetry(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	malformedResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	goodResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content:      genai.NewContentFromText("ok", genai.RoleModel),
				FinishReason: genai.FinishReason("STOP"),
			},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	// First call returns malformed; second (retry) returns good.
	gomock.InOrder(
		mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(malformedResp, nil),
		mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(goodResp, nil),
	)

	m := &Model{client: mockClient}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	var responses []*model.Response
	for r := range respChan {
		responses = append(responses, r)
	}
	require.Len(t, responses, 1)
	require.Equal(t, "ok", responses[0].Choices[0].Message.Content)
}

// TestModel_NonStreaming_MalformedFunctionCallRetryError verifies that when a
// retry itself returns an error, that error is propagated as a model.Response
// with Done=true and a non-nil Error field.
func TestModel_NonStreaming_MalformedFunctionCallRetryError(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	malformedResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	retryErr := errors.New("network error on retry")

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	gomock.InOrder(
		mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(malformedResp, nil),
		mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, retryErr),
	)

	m := &Model{client: mockClient}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	var responses []*model.Response
	for r := range respChan {
		responses = append(responses, r)
	}
	require.Len(t, responses, 1)
	require.True(t, responses[0].Done)
	require.NotNil(t, responses[0].Error)
	require.Equal(t, "network error on retry", responses[0].Error.Message)
}

// TestModel_Streaming_MalformedFunctionCallRetry verifies that when the
// streaming accumulator ends with MALFORMED_FUNCTION_CALL, all buffered chunks
// are suppressed and the implementation falls back to a non-streaming retry,
// emitting only the retry result (Done=true) to the caller.
func TestModel_Streaming_MalformedFunctionCallRetry(t *testing.T) {
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "hello"}},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	malformedChunk := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	goodResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content:      genai.NewContentFromText("retry-ok", genai.RoleModel),
				FinishReason: genai.FinishReason("STOP"),
			},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	// Stream returns a single malformed chunk.
	mockModels.EXPECT().
		GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(seqFromSlice([]*genai.GenerateContentResponse{malformedChunk}))
	// Non-streaming retry returns good response.
	mockModels.EXPECT().
		GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(goodResp, nil)

	m := &Model{client: mockClient}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	var responses []*model.Response
	for r := range respChan {
		responses = append(responses, r)
	}
	// With buffering the malformed stream is fully suppressed: no partial
	// chunks are emitted. Only the retry result (Done=true) is sent.
	require.Len(t, responses, 1)
	require.True(t, responses[0].Done)
	require.Equal(t, "retry-ok", responses[0].Choices[0].Message.Content)
}

// TestModel_Streaming_MalformedFunctionCallRetryError verifies that when the
// non-streaming retry for a malformed stream response itself returns an error,
// that error is propagated as a Done=true response with a non-nil Error field.
func TestModel_Streaming_MalformedFunctionCallRetryError(t *testing.T) {
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "hello"}},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	malformedChunk := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	retryErr := errors.New("network error on stream retry")

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	mockModels.EXPECT().
		GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(seqFromSlice([]*genai.GenerateContentResponse{malformedChunk}))
	// The first retry attempt returns an error; loop breaks immediately.
	mockModels.EXPECT().
		GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, retryErr).Times(1)

	m := &Model{client: mockClient}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	var responses []*model.Response
	for r := range respChan {
		responses = append(responses, r)
	}
	var errResp *model.Response
	for _, r := range responses {
		if r.Error != nil {
			errResp = r
		}
	}
	require.NotNil(t, errResp, "error response must be emitted when retry fails")
	require.True(t, errResp.Done)
	require.Equal(t, "network error on stream retry", errResp.Error.Message)
}

// TestModel_Streaming_MalformedFunctionCallWithCallback verifies that the
// chatStreamCompleteCallback is invoked with the retry result (not the
// malformed response) when a streaming fallback retry succeeds.
func TestModel_Streaming_MalformedFunctionCallWithCallback(t *testing.T) {
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "hello"}},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	malformedChunk := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	goodResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content:      genai.NewContentFromText("callback-ok", genai.RoleModel),
				FinishReason: genai.FinishReason("STOP"),
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
		Return(seqFromSlice([]*genai.GenerateContentResponse{malformedChunk}))
	mockModels.EXPECT().
		GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(goodResp, nil)

	var callbackResp *model.Response
	m := &Model{
		client: mockClient,
		chatStreamCompleteCallback: func(_ context.Context, _ []*genai.Content,
			_ *genai.GenerateContentConfig, r *model.Response) {
			callbackResp = r
		},
	}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	for range respChan {
	}

	require.NotNil(t, callbackResp, "chatStreamCompleteCallback must be called")
	require.Equal(t, "callback-ok", callbackResp.Choices[0].Message.Content)
}

// TestModel_NonStreaming_MalformedFunctionCallExhausted verifies that when all
// retries are exhausted and the response is still MALFORMED_FUNCTION_CALL, an
// error response is emitted instead of silently returning the broken response.
func TestModel_NonStreaming_MalformedFunctionCallExhausted(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	malformedResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	// Initial call + malformedFunctionCallRetries (2) retries all return malformed.
	mockModels.EXPECT().
		GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(malformedResp, nil).
		Times(1 + malformedFunctionCallRetries)

	m := &Model{client: mockClient}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	var responses []*model.Response
	for r := range respChan {
		responses = append(responses, r)
	}
	require.Len(t, responses, 1)
	require.True(t, responses[0].Done)
	require.NotNil(t, responses[0].Error)
	require.Contains(t, responses[0].Error.Message, "MALFORMED_FUNCTION_CALL persists after retries")
}

// TestModel_Streaming_MalformedFunctionCallExhausted verifies that when all
// non-streaming retries after a malformed stream are exhausted and still
// malformed, an error response is emitted instead of the broken response.
func TestModel_Streaming_MalformedFunctionCallExhausted(t *testing.T) {
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "hello"}},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	malformedChunk := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockClient(ctrl)
	mockModels := NewMockModels(ctrl)
	mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
	mockModels.EXPECT().
		GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(seqFromSlice([]*genai.GenerateContentResponse{malformedChunk}))
	// All non-streaming retries also return malformed.
	mockModels.EXPECT().
		GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(malformedChunk, nil).
		Times(malformedFunctionCallRetries)

	m := &Model{client: mockClient}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	var responses []*model.Response
	for r := range respChan {
		responses = append(responses, r)
	}
	var errResp *model.Response
	for _, r := range responses {
		if r.Error != nil {
			errResp = r
		}
	}
	require.NotNil(t, errResp, "error response must be emitted when all retries exhausted")
	require.True(t, errResp.Done)
	require.Contains(t, errResp.Error.Message, "MALFORMED_FUNCTION_CALL persists after retries")
}

// TestModel_Streaming_NormalPathCallbacks verifies that chatChunkCallback and
// chatStreamCompleteCallback are both invoked on the normal (non-malformed)
// streaming path, and that buffered chunks are flushed to the caller.
func TestModel_Streaming_NormalPathCallbacks(t *testing.T) {
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "hello"}},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	chunk1 := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{Content: genai.NewContentFromText("hello", genai.RoleModel)},
		},
	}
	chunk2 := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content:      genai.NewContentFromText(" world", genai.RoleModel),
				FinishReason: genai.FinishReason("STOP"),
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
		Return(seqFromSlice([]*genai.GenerateContentResponse{chunk1, chunk2}))

	var chunkCallbackRaws []*genai.GenerateContentResponse
	var completeCallbackResp *model.Response
	m := &Model{
		client: mockClient,
		chatChunkCallback: func(_ context.Context, _ []*genai.Content,
			_ *genai.GenerateContentConfig, raw *genai.GenerateContentResponse) {
			chunkCallbackRaws = append(chunkCallbackRaws, raw)
		},
		chatStreamCompleteCallback: func(_ context.Context, _ []*genai.Content,
			_ *genai.GenerateContentConfig, r *model.Response) {
			completeCallbackResp = r
		},
	}
	respChan, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	var responses []*model.Response
	for r := range respChan {
		responses = append(responses, r)
	}

	// 2 partial chunks + 1 final Done response.
	require.Len(t, responses, 3)
	// chatChunkCallback called once per chunk.
	require.Len(t, chunkCallbackRaws, 2)
	require.Equal(t, chunk1, chunkCallbackRaws[0])
	require.Equal(t, chunk2, chunkCallbackRaws[1])
	// chatStreamCompleteCallback called with the accumulated final response.
	require.NotNil(t, completeCallbackResp)
	require.True(t, completeCallbackResp.Done)
}

// TestModel_ContextCancellation_NonStreaming verifies that when the context is
// cancelled and the response channel is unbuffered (no reader), the select
// branches in the non-streaming path take the ctx.Done() arm without deadlock.
// Because Go's select is non-deterministic between equally-ready cases, the
// tests drain the channel but do not assert the exact response count — the
// important property is that the goroutine terminates (channel closes) without
// hanging.
func TestModel_ContextCancellation_NonStreaming(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	malformedResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	goodResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content:      genai.NewContentFromText("ok", genai.RoleModel),
				FinishReason: genai.FinishReason("STOP"),
			},
		},
	}
	apiErr := errors.New("api error")

	drainWithTimeout := func(t *testing.T, ch <-chan *model.Response) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("channel did not close: goroutine likely deadlocked")
		}
	}

	t.Run("error_ctx_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, apiErr)
		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})

	t.Run("retry_error_ctx_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		gomock.InOrder(
			mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(malformedResp, nil),
			mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, apiErr),
		)
		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})

	t.Run("exhausted_ctx_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(malformedResp, nil).Times(malformedFunctionCallRetries + 1)
		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})

	t.Run("success_ctx_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(goodResp, nil)
		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})
}

// TestModel_ContextCancellation_Streaming verifies that ctx.Done() arms in the
// streaming path do not deadlock when the context is pre-cancelled.
func TestModel_ContextCancellation_Streaming(t *testing.T) {
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "hi"}},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	malformedChunk := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	goodChunk := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content:      genai.NewContentFromText("ok", genai.RoleModel),
				FinishReason: genai.FinishReason("STOP"),
			},
		},
	}
	malformedNonStream := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{FinishReason: genai.FinishReason("MALFORMED_FUNCTION_CALL")},
		},
	}
	apiErr := errors.New("stream api error")

	drainWithTimeout := func(t *testing.T, ch <-chan *model.Response) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("channel did not close: goroutine likely deadlocked")
		}
	}

	t.Run("streaming_retry_error_ctx_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqFromSlice([]*genai.GenerateContentResponse{malformedChunk}))
		mockModels.EXPECT().
			GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, apiErr)
		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})

	t.Run("streaming_exhausted_ctx_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqFromSlice([]*genai.GenerateContentResponse{malformedChunk}))
		mockModels.EXPECT().
			GenerateContent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(malformedNonStream, nil).Times(malformedFunctionCallRetries)
		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})

	t.Run("streaming_normal_ctx_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqFromSlice([]*genai.GenerateContentResponse{goodChunk}))
		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})

	t.Run("mid_stream_error_flush_ctx_cancelled", func(t *testing.T) {
		// ctx.Done() arm at lines 296-297: mid-stream error with buffered chunks,
		// ctx pre-cancelled so chunk-flush select takes ctx.Done().
		streamErr := errors.New("stream fail")
		successChunk := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{{Text: "partial"}}}},
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockClient := NewMockClient(ctrl)
		mockModels := NewMockModels(ctrl)
		mockClient.EXPECT().Models().Return(mockModels).AnyTimes()
		mockModels.EXPECT().
			GenerateContentStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(seqFromSliceWithError([]*genai.GenerateContentResponse{successChunk}, streamErr))

		m := &Model{client: mockClient}
		respChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		drainWithTimeout(t, respChan)
	})
}

// TestChatRequestCallbackSynchronous verifies that
// chatRequestCallback is invoked synchronously inside
// GenerateContent, before the response goroutine starts.
func TestChatRequestCallbackSynchronous(t *testing.T) {
	subText := "text"
	now := time.Now()
	resp := &genai.GenerateContentResponse{
		ResponseID:   "1",
		CreateTime:   now,
		ModelVersion: "v1",
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []*genai.Part{{Text: "hi"}},
			},
			FinishReason: genai.FinishReason("stop"),
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			TotalTokenCount: 1,
		},
	}

	tests := []struct {
		name   string
		stream bool
	}{
		{name: "non_streaming", stream: false},
		{name: "streaming", stream: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := NewMockClient(ctrl)
			mockModels := NewMockModels(ctrl)
			mockClient.EXPECT().Models().
				Return(mockModels).AnyTimes()

			if tt.stream {
				mockModels.EXPECT().
					GenerateContentStream(
						gomock.Any(), gomock.Any(),
						gomock.Any(), gomock.Any()).
					Return(seqFromSlice(
						[]*genai.GenerateContentResponse{
							resp,
						})).AnyTimes()
			} else {
				mockModels.EXPECT().
					GenerateContent(
						gomock.Any(), gomock.Any(),
						gomock.Any(), gomock.Any()).
					Return(resp, nil).AnyTimes()
			}

			var callCount int64
			m := &Model{
				client: mockClient,
				chatRequestCallback: func(
					_ context.Context,
					_ []*genai.Content,
				) {
					callCount++
				},
			}

			req := &model.Request{
				Messages: []model.Message{{
					Role:    model.RoleUser,
					Content: "hi",
					ContentParts: []model.ContentPart{{
						Type: model.ContentTypeText,
						Text: &subText,
					}},
				}},
				GenerationConfig: model.GenerationConfig{
					Stream: tt.stream,
				},
			}

			ch, err := m.GenerateContent(
				context.Background(), req)
			require.NoError(t, err)

			// Callback must have fired synchronously
			// before GenerateContent returned.
			assert.Equal(t, int64(1), callCount,
				"callback must execute exactly once "+
					"before GenerateContent returns")

			// Drain the channel to avoid goroutine leak.
			for range ch {
			}

			// Confirm no extra invocations after drain.
			assert.Equal(t, int64(1), callCount,
				"callback must not be called more than once")
		})
	}
}
