package gemini

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
	"testing"
	"time"
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
			name: "buildResponse nil",
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
