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
	gomock "go.uber.org/mock/gomock"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

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

var schema *tool.Schema

// Tool implements  tool.Declaration
type Tool struct {
}

// tool.Declaration implements  tool.Declaration
func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         "tool",
		Description:  "tool description",
		InputSchema:  schema,
		OutputSchema: schema,
	}
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

func TestModel_buildResponse(t *testing.T) {
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
			name: "nil-req",
			fields: fields{
				m: &Model{},
			},
			args: args{
				chatCompletion: nil,
			},
			want: &model.Response{},
		},
		{
			name: "empty-usage",
			fields: fields{
				m: &Model{},
			},
			args: args{
				chatCompletion: &genai.GenerateContentResponse{},
			},
		},
		{
			name: "buildResponse",
			fields: fields{
				m: &Model{},
			},
			args: args{
				chatCompletion: &genai.GenerateContentResponse{
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
										Thought: true,
										Text:    "Thought",
										FunctionCall: &genai.FunctionCall{
											ID:   "id",
											Name: "function_call",
											Args: functionArgs,
										},
									},
									{
										Text: "Answer",
									},
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
				Created:   now.Unix(),
				Timestamp: now,
				Model:     "pro-v1",
				Choices: []model.Choice{
					{
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := tt.fields.m.buildResponse(tt.args.chatCompletion)
			if tt.name == "nil-req" || tt.name == "empty-usage" {
				return
			}
			assert.Equal(t, tt.want.ID, response.ID)
			assert.Equal(t, tt.want.Model, response.Model)
			assert.Equal(t, tt.want.Created, response.Created)
			assert.Equal(t, tt.want.Usage, response.Usage)
			assert.Equal(t, tt.want.Choices[0].FinishReason, response.Choices[0].FinishReason)
			assert.Equal(t, tt.want.Choices[0].Delta.ReasoningContent, response.Choices[0].Delta.ReasoningContent)
			assert.Equal(t, tt.want.Choices[0].Delta.Content, response.Choices[0].Delta.Content)
			assert.Equal(t, tt.want.Choices[0].Delta.Role, response.Choices[0].Delta.Role)
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
