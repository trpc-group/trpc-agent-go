//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package failover

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewReturnsErrorWithoutCandidates(t *testing.T) {
	llm, err := New()
	require.Error(t, err)
	assert.EqualError(t, err, "failover: at least one candidate model is required")
	assert.Nil(t, llm)
}

func TestNewReturnsErrorWithNilCandidate(t *testing.T) {
	llm, err := New(WithCandidates(nil))
	require.Error(t, err)
	assert.EqualError(t, err, "failover: candidate model at index 0 is nil")
	assert.Nil(t, llm)
}

func TestWithCandidatesAppendsInPriorityOrder(t *testing.T) {
	primary := openai.New("primary-model")
	backup := openai.New("backup-model")
	llm, err := New(
		WithCandidates(primary),
		WithCandidates(backup),
	)
	require.NoError(t, err)
	assert.Equal(t, "primary-model", llm.Info().Name)
	_, ok := llm.(model.IterModel)
	require.True(t, ok)
	impl, ok := llm.(*failoverModel)
	require.True(t, ok)
	require.Len(t, impl.candidates, 2)
	assert.Same(t, primary, impl.candidates[0])
	assert.Same(t, backup, impl.candidates[1])
}

func TestCloneRequestDeepCopiesSerializableFields(t *testing.T) {
	maxTokens := 128
	toolImpl := &stubTool{name: "lookup"}
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system"),
			model.NewUserMessage("user"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream:    true,
			MaxTokens: &maxTokens,
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "answer",
				Strict: true,
				Schema: map[string]any{
					"type": "object",
				},
			},
		},
		Tools: map[string]tool.Tool{
			"lookup": toolImpl,
		},
	}
	cloned, err := cloneRequest(request)
	require.NoError(t, err)
	require.NotNil(t, cloned)
	require.NotSame(t, request, cloned)
	require.NotSame(t, request.StructuredOutput, cloned.StructuredOutput)
	require.NotSame(t, request.StructuredOutput.JSONSchema, cloned.StructuredOutput.JSONSchema)
	cloned.Messages[1].Content = "changed"
	cloned.StructuredOutput.JSONSchema.Name = "changed"
	cloned.StructuredOutput.JSONSchema.Schema["type"] = "array"
	cloned.Tools["other"] = stubTool{name: "other"}
	assert.Equal(t, "user", request.Messages[1].Content)
	assert.Equal(t, "answer", request.StructuredOutput.JSONSchema.Name)
	assert.Equal(t, "object", request.StructuredOutput.JSONSchema.Schema["type"])
	assert.Len(t, request.Tools, 1)
	assert.Same(t, toolImpl, cloned.Tools["lookup"])
}

func TestCloneRequestReturnsMarshalError(t *testing.T) {
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name: "bad-schema",
				Schema: map[string]any{
					"invalid": func() {},
				},
			},
		},
	}
	cloned, err := cloneRequest(request)
	require.Error(t, err)
	assert.Nil(t, cloned)
	assert.Contains(t, err.Error(), "marshal request")
}

func TestHasFailoverResponseError(t *testing.T) {
	tests := []struct {
		name     string
		response *model.Response
		want     bool
	}{
		{
			name: "error message present",
			response: &model.Response{
				Error: &model.ResponseError{
					Message: "upstream unavailable",
				},
			},
			want: true,
		},
		{
			name: "error type present",
			response: &model.Response{
				Error: &model.ResponseError{
					Type: model.ErrorTypeAPIError,
				},
			},
			want: true,
		},
		{
			name: "error code present",
			response: &model.Response{
				Error: &model.ResponseError{
					Code: func() *string {
						s := "rate_limit"
						return &s
					}(),
				},
			},
			want: true,
		},
		{
			name: "error param present",
			response: &model.Response{
				Error: &model.ResponseError{
					Param: func() *string {
						s := "messages"
						return &s
					}(),
				},
			},
			want: true,
		},
		{
			name: "error struct empty",
			response: &model.Response{
				Error: &model.ResponseError{},
			},
			want: false,
		},
		{
			name:     "nil response",
			response: nil,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasFailoverResponseError(tt.response))
		})
	}
}

func TestRunAttemptsFallsBackBeforeFirstNonErrorChunk(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{
				Error: &model.ResponseError{
					Message: "primary failed",
					Type:    model.ErrorTypeStreamError,
				},
				Model: "primary",
				Done:  true,
			},
		},
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			{
				Model: "backup",
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "hello",
						},
					},
				},
				IsPartial: true,
			},
		},
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	assert.Equal(t, "backup", responses[0].Model)
	assert.Equal(t, "hello", responses[0].Choices[0].Delta.Content)
}

func TestRunAttemptsStopsFallbackAfterFirstNonErrorChunk(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{ID: "primary-prelude", Model: "primary", Done: false},
			{
				Error: &model.ResponseError{
					Message: "primary failed",
					Type:    model.ErrorTypeStreamError,
				},
				Model: "primary",
				Done:  true,
			},
		},
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			{
				Model: "backup",
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "hello",
						},
					},
				},
				IsPartial: true,
			},
		},
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 2)
	assert.Equal(t, "primary-prelude", responses[0].ID)
	require.NotNil(t, responses[1].Error)
	assert.Equal(t, "primary failed", responses[1].Error.Message)
}

func TestRunAttemptsYieldsNonMeaningfulNonErrorResponses(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{ID: "primary-prelude", Model: "primary", Done: false},
			{ID: "primary-final", Model: "primary", Done: true},
		},
	}
	llm, err := New(WithCandidates(primary))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 2)
	assert.Equal(t, "primary-prelude", responses[0].ID)
	assert.Equal(t, "primary-final", responses[1].ID)
}

func TestGenerateContentReturnsErrorForNilRequest(t *testing.T) {
	llm, err := New(WithCandidates(&scriptedIterModel{name: "primary"}))
	require.NoError(t, err)
	responseChan, err := llm.GenerateContent(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, responseChan)
	assert.EqualError(t, err, "request cannot be nil")
}

func TestGenerateContentUsesNonIterCandidate(t *testing.T) {
	primary := &scriptedModel{
		name: "primary",
		responses: []*model.Response{
			nil,
			{
				Model: "primary",
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "hello",
						},
					},
				},
				Done: true,
			},
		},
	}
	llm, err := New(WithCandidates(primary))
	require.NoError(t, err)
	responses := collectChannelResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	assert.Equal(t, "primary", responses[0].Model)
	assert.Equal(t, "hello", responses[0].Choices[0].Message.Content)
}

func TestGenerateContentCancelsAbandonedChannelCandidateOnFallback(t *testing.T) {
	primaryStopped := make(chan struct{})
	primary := &cancelAwareChannelModel{
		name: "primary",
		response: &model.Response{
			Error: &model.ResponseError{
				Message: "primary failed",
				Type:    model.ErrorTypeStreamError,
			},
			Model: "primary",
			Done:  true,
		},
		stopped: primaryStopped,
	}
	backup := &scriptedModel{
		name: "backup",
		responses: []*model.Response{
			{
				Model: "backup",
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "from backup",
						},
					},
				},
				Done: true,
			},
		},
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	responses := collectChannelResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	assert.Equal(t, "backup", responses[0].Model)
	assert.Equal(t, "from backup", responses[0].Choices[0].Message.Content)
	select {
	case <-primaryStopped:
	case <-time.After(time.Second):
		t.Fatal("expected primary candidate context to be canceled")
	}
}

func TestGenerateContentIterFallsBackOnFunctionLevelError(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		err:  errors.New("dial tcp timeout"),
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			{
				Model: "backup",
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "from backup",
						},
					},
				},
				IsPartial: true,
			},
		},
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	assert.Equal(t, "backup", responses[0].Model)
	assert.Equal(t, "from backup", responses[0].Choices[0].Delta.Content)
}

func TestGenerateContentIterReturnsAggregatedErrorWhenAllCandidatesFail(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		err:  errors.New("dial tcp timeout"),
	}
	backup := &scriptedIterModel{
		name: "backup",
		err:  errors.New("tls handshake failed"),
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	iterModel, ok := llm.(model.IterModel)
	require.True(t, ok)
	seq, err := iterModel.GenerateContentIter(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	assert.Nil(t, seq)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `candidate model "primary" failed before the first non-error chunk: dial tcp timeout`)
	assert.Contains(t, err.Error(), `candidate model "backup" failed before the first non-error chunk: tls handshake failed`)
	failures := failuresFromError(err, nil)
	require.Len(t, failures, 2)
	assert.Equal(t, "primary", failures[0].candidate)
	assert.Equal(t, "backup", failures[1].candidate)
}

func TestRunAttemptsReturnsLastCandidateErrorWithoutPriorFailures(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{
				Error: &model.ResponseError{
					Message: "primary failed",
					Type:    model.ErrorTypeAPIError,
				},
				Model: "primary",
				Done:  true,
			},
		},
	}
	llm, err := New(WithCandidates(primary))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	require.NotNil(t, responses[0].Error)
	assert.Equal(t, "primary failed", responses[0].Error.Message)
	assert.Equal(t, model.ErrorTypeAPIError, responses[0].Error.Type)
}

func TestRunAttemptsBuildsAggregatedFailureResponseAfterFallbacks(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{
				Error: &model.ResponseError{
					Message: "primary failed",
					Type:    model.ErrorTypeStreamError,
				},
				Model: "primary",
				Done:  true,
			},
		},
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			{
				Error: &model.ResponseError{
					Message: "backup failed",
					Type:    model.ErrorTypeAPIError,
				},
				Model: "backup",
				Done:  true,
			},
		},
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	require.NotNil(t, responses[0].Error)
	assert.Contains(t, responses[0].Error.Message, `candidate model "primary" failed before the first non-error chunk: primary failed`)
	assert.Contains(t, responses[0].Error.Message, `candidate model "backup" failed before the first non-error chunk: backup failed`)
	assert.Equal(t, model.ErrorTypeAPIError, responses[0].Error.Type)
	assert.True(t, responses[0].Done)
}

func TestSequenceForCandidateReturnsErrorForNilSequence(t *testing.T) {
	seq, err := sequenceForCandidate(context.Background(), &nilSeqIterModel{name: "primary"}, &model.Request{})
	require.Error(t, err)
	assert.Nil(t, seq)
	assert.EqualError(t, err, `candidate model "primary" returned nil response sequence`)
}

func TestSequenceForCandidateReturnsErrorForNonIterFailures(t *testing.T) {
	_, err := sequenceForCandidate(context.Background(), &scriptedModel{
		name: "primary",
		err:  errors.New("network down"),
	}, &model.Request{})
	require.Error(t, err)
	assert.EqualError(t, err, "network down")
	seq, err := sequenceForCandidate(context.Background(), &scriptedModel{
		name:       "primary",
		nilChannel: true,
	}, &model.Request{})
	require.Error(t, err)
	assert.Nil(t, seq)
	assert.EqualError(t, err, `candidate model "primary" returned nil response channel`)
}

func TestSequenceForCandidateStopsWhenYieldReturnsFalse(t *testing.T) {
	seq, err := sequenceForCandidate(context.Background(), &scriptedModel{
		name: "primary",
		responses: []*model.Response{
			{ID: "first"},
			{ID: "second"},
		},
	}, &model.Request{})
	require.NoError(t, err)
	seenIDs := make([]string, 0, 2)
	seq(func(response *model.Response) bool {
		seenIDs = append(seenIDs, response.ID)
		return false
	})
	assert.Equal(t, []string{"first"}, seenIDs)
}

func TestBuildFailureResponseUsesLastNonEmptyErrorType(t *testing.T) {
	failures := []failureRecord{
		{candidate: "primary", message: "primary failed", errType: model.ErrorTypeStreamError},
		{candidate: "backup", message: "backup failed", errType: model.ErrorTypeAPIError},
	}
	response := buildFailureResponse(failures)
	require.NotNil(t, response)
	require.NotNil(t, response.Error)
	assert.Equal(t, model.ErrorTypeAPIError, response.Error.Type)
	assert.Contains(t, response.Error.Message, `candidate model "primary" failed before the first non-error chunk: primary failed`)
	assert.Contains(t, response.Error.Message, `candidate model "backup" failed before the first non-error chunk: backup failed`)
	assert.True(t, response.Done)
	assert.False(t, response.Timestamp.IsZero())
}

func TestFailuresFromErrorAppendsGenericError(t *testing.T) {
	failures := failuresFromError(errors.New("generic failure"), []failureRecord{
		{candidate: "primary", message: "primary failed", errType: model.ErrorTypeAPIError},
	})
	require.Len(t, failures, 2)
	assert.Equal(t, "", failures[1].candidate)
	assert.Equal(t, "generic failure", failures[1].message)
}

func TestBuildFailureMessageIncludesUnnamedFailures(t *testing.T) {
	message := buildFailureMessage([]failureRecord{
		{candidate: "primary", message: "primary failed"},
		{message: "fallback preparation failed"},
	})
	assert.True(t, strings.Contains(message, `candidate model "primary" failed before the first non-error chunk: primary failed`))
	assert.True(t, strings.Contains(message, "fallback preparation failed"))
}

type scriptedIterModel struct {
	name      string
	responses []*model.Response
	err       error
}

func (m *scriptedIterModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *scriptedIterModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	if m.err != nil {
		return nil, m.err
	}
	return func(yield func(*model.Response) bool) {
		for _, response := range m.responses {
			if !yield(response) {
				return
			}
		}
	}, nil
}

func (m *scriptedIterModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type nilSeqIterModel struct {
	name string
}

func (m *nilSeqIterModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *nilSeqIterModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	return nil, nil
}

func (m *nilSeqIterModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type scriptedModel struct {
	name       string
	responses  []*model.Response
	err        error
	nilChannel bool
}

func (m *scriptedModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.nilChannel {
		return nil, nil
	}
	ch := make(chan *model.Response, len(m.responses))
	for _, response := range m.responses {
		ch <- response
	}
	close(ch)
	return ch, nil
}

func (m *scriptedModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type cancelAwareChannelModel struct {
	name     string
	response *model.Response
	stopped  chan struct{}
}

func (m *cancelAwareChannelModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		signalStopped := func() {
			select {
			case <-m.stopped:
			default:
				close(m.stopped)
			}
		}
		select {
		case ch <- m.response:
		case <-ctx.Done():
			signalStopped()
			return
		}
		<-ctx.Done()
		signalStopped()
	}()
	return ch, nil
}

func (m *cancelAwareChannelModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type stubTool struct {
	name string
}

func (t stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func collectIterResponses(t *testing.T, llm model.Model, request *model.Request) []*model.Response {
	t.Helper()
	iterModel, ok := llm.(model.IterModel)
	require.True(t, ok)
	seq, err := iterModel.GenerateContentIter(context.Background(), request)
	require.NoError(t, err)
	responses := make([]*model.Response, 0)
	seq(func(response *model.Response) bool {
		responses = append(responses, response)
		return true
	})
	return responses
}

func collectChannelResponses(t *testing.T, llm model.Model, request *model.Request) []*model.Response {
	t.Helper()
	responseChan, err := llm.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	responses := make([]*model.Response, 0)
	for response := range responseChan {
		responses = append(responses, response)
	}
	return responses
}
