//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package vision

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNew(t *testing.T) {
	visionModel := &fakeModel{}

	tool, err := New(visionModel)
	require.NoError(t, err)
	require.NotNil(t, tool)
	declaration := tool.Declaration()
	assert.Equal(t, ToolName, declaration.Name)
	assert.Contains(t, declaration.Description, "current user message")
	assert.Equal(t, []string{"prompt"}, declaration.InputSchema.Required)
	assert.Contains(t, declaration.InputSchema.Properties, "prompt")
	assert.Contains(t, declaration.InputSchema.Properties, "image_urls")
	assert.Nil(t, declaration.OutputSchema)

	_, err = New(nil)
	assert.EqualError(t, err, "vision model is required")
	_, err = New(visionModel, WithName(" "))
	assert.EqualError(t, err, "tool name is required")
	_, err = New(visionModel, WithDescription(" "))
	assert.EqualError(t, err, "tool description is required")
}

func TestNewAppliesDeclarationOptions(t *testing.T) {
	tool, err := New(
		&fakeModel{},
		WithName("inspect_image"),
		WithDescription("Inspect selected images."),
	)
	require.NoError(t, err)

	declaration := tool.Declaration()
	assert.Equal(t, "inspect_image", declaration.Name)
	assert.Equal(t, "Inspect selected images.", declaration.Description)
}

func TestToolCallUsesExplicitImageURLs(t *testing.T) {
	visionModel := &fakeModel{
		responses: []*model.Response{finalResponse("explicit analysis")},
	}
	tool, err := New(visionModel)
	require.NoError(t, err)

	invocation := agent.NewInvocation()
	invocation.Message = model.NewUserMessage("user request")
	invocation.Message.AddImageURL("https://example.com/attached.png", "high")
	ctx := agent.NewInvocationContext(context.Background(), invocation)

	result, err := tool.Call(ctx, []byte(`{
		"prompt":"compare the selected images",
		"image_urls":[" https://example.com/one.png ","http://example.com/two.jpg"]
	}`))
	require.NoError(t, err)
	assert.Equal(t, "explicit analysis", result)

	req := visionModel.lastRequest
	require.NotNil(t, req)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "untrusted data")
	assert.Empty(t, req.Messages[1].Content)
	require.Len(t, req.Messages[1].ContentParts, 3)
	require.NotNil(t, req.Messages[1].ContentParts[0].Text)
	assert.Equal(t, "compare the selected images", *req.Messages[1].ContentParts[0].Text)
	assert.Equal(t, "https://example.com/one.png", req.Messages[1].ContentParts[1].Image.URL)
	assert.Equal(t, "http://example.com/two.jpg", req.Messages[1].ContentParts[2].Image.URL)
	assert.Equal(t, "auto", req.Messages[1].ContentParts[1].Image.Detail)
	assert.False(t, req.GenerationConfig.Stream)
}

func TestToolCallFallsBackToCurrentMessageImages(t *testing.T) {
	visionModel := &fakeModel{
		responses: []*model.Response{finalResponse("attached analysis")},
	}
	maxTokens := 123
	tool, err := New(
		visionModel,
		WithInstruction("custom instruction"),
		WithGenerationConfig(model.GenerationConfig{
			MaxTokens: &maxTokens,
			Stream:    false,
		}),
	)
	require.NoError(t, err)

	data := []byte{1, 2, 3}
	invocation := agent.NewInvocation()
	invocation.Message = model.NewUserMessage("look at these")
	invocation.Message.AddImageURL("https://example.com/attached.png", "high")
	invocation.Message.AddImageData(data, "low", "png")
	invocation.Message.ContentParts = append(invocation.Message.ContentParts,
		model.ContentPart{Type: model.ContentTypeText})
	ctx := agent.NewInvocationContext(context.Background(), invocation)

	result, err := tool.Call(ctx, []byte(`{"prompt":" describe them ","image_urls":[]}`))
	require.NoError(t, err)
	assert.Equal(t, "attached analysis", result)

	req := visionModel.lastRequest
	require.Len(t, req.Messages, 2)
	assert.Equal(t, "custom instruction", req.Messages[0].Content)
	assert.Empty(t, req.Messages[1].Content)
	require.Len(t, req.Messages[1].ContentParts, 3)
	require.NotNil(t, req.Messages[1].ContentParts[0].Text)
	assert.Equal(t, "describe them", *req.Messages[1].ContentParts[0].Text)
	assert.Equal(t, "https://example.com/attached.png", req.Messages[1].ContentParts[1].Image.URL)
	assert.Equal(t, "high", req.Messages[1].ContentParts[1].Image.Detail)
	assert.Equal(t, []byte{1, 2, 3}, req.Messages[1].ContentParts[2].Image.Data)
	assert.Equal(t, "png", req.Messages[1].ContentParts[2].Image.Format)
	assert.Equal(t, 123, *req.GenerationConfig.MaxTokens)

	data[0] = 9
	assert.Equal(t, byte(1), req.Messages[1].ContentParts[2].Image.Data[0])
}

func TestToolCallWithoutInstruction(t *testing.T) {
	visionModel := &fakeModel{
		responses: []*model.Response{finalResponse("analysis")},
	}
	tool, err := New(visionModel, WithInstruction(""))
	require.NoError(t, err)

	result, err := tool.Call(context.Background(), []byte(
		`{"prompt":"describe","image_urls":["https://example.com/image.png"]}`,
	))
	require.NoError(t, err)
	assert.Equal(t, "analysis", result)
	require.Len(t, visionModel.lastRequest.Messages, 1)
	message := visionModel.lastRequest.Messages[0]
	assert.Equal(t, model.RoleUser, message.Role)
	assert.Empty(t, message.Content)
	require.Len(t, message.ContentParts, 2)
	require.NotNil(t, message.ContentParts[0].Text)
	assert.Equal(t, "describe", *message.ContentParts[0].Text)
	require.NotNil(t, message.ContentParts[1].Image)
}

func TestToolCallInputErrors(t *testing.T) {
	visionModel := &fakeModel{}
	tool, err := New(visionModel)
	require.NoError(t, err)

	tests := []struct {
		name    string
		ctx     context.Context
		args    string
		wantErr string
	}{
		{
			name:    "invalid JSON",
			ctx:     context.Background(),
			args:    `{`,
			wantErr: "decode image analysis request",
		},
		{
			name:    "empty prompt",
			ctx:     context.Background(),
			args:    `{"prompt":" "}`,
			wantErr: "prompt is required",
		},
		{
			name:    "invalid URL scheme",
			ctx:     context.Background(),
			args:    `{"prompt":"describe","image_urls":["file:///tmp/image.png"]}`,
			wantErr: "URL must use HTTP or HTTPS",
		},
		{
			name:    "empty URL",
			ctx:     context.Background(),
			args:    `{"prompt":"describe","image_urls":[" "]}`,
			wantErr: "URL is empty",
		},
		{
			name:    "no invocation",
			ctx:     context.Background(),
			args:    `{"prompt":"describe"}`,
			wantErr: "invocation context is unavailable",
		},
		{
			name: "no current images",
			ctx: agent.NewInvocationContext(
				context.Background(),
				&agent.Invocation{Message: model.NewUserMessage("hello")},
			),
			args:    `{"prompt":"describe"}`,
			wantErr: "no images found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.Call(test.ctx, []byte(test.args))
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
	assert.Nil(t, visionModel.lastRequest)
}

func TestToolCallModelErrors(t *testing.T) {
	t.Run("function error", func(t *testing.T) {
		visionModel := &fakeModel{err: errors.New("unavailable")}
		tool, err := New(visionModel)
		require.NoError(t, err)

		_, err = tool.Call(context.Background(), validExplicitArgs())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model call failed: unavailable")
	})

	t.Run("response error", func(t *testing.T) {
		contextDone := make(chan struct{})
		visionModel := &fakeModel{responses: []*model.Response{{
			Error: &model.ResponseError{Message: "filtered"},
		}}, contextDone: contextDone}
		tool, err := New(visionModel)
		require.NoError(t, err)

		_, err = tool.Call(context.Background(), validExplicitArgs())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model returned error: filtered")
		waitForSignal(t, contextDone, "vision model context cancellation")
	})

	t.Run("empty response", func(t *testing.T) {
		visionModel := &fakeModel{responses: []*model.Response{{}}}
		tool, err := New(visionModel)
		require.NoError(t, err)

		_, err = tool.Call(context.Background(), validExplicitArgs())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model returned empty content")
	})

	t.Run("nil response channel", func(t *testing.T) {
		visionModel := &fakeModel{nilResponses: true}
		tool, err := New(visionModel)
		require.NoError(t, err)

		_, err = tool.Call(context.Background(), validExplicitArgs())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model returned a nil response channel")
	})
}

func TestToolCallHonorsContextCancellation(t *testing.T) {
	visionModel := &blockingModel{
		started:     make(chan struct{}),
		contextDone: make(chan struct{}),
	}
	tool, err := New(visionModel)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, err := tool.Call(ctx, validExplicitArgs())
		callDone <- err
	}()

	waitForSignal(t, visionModel.started, "vision model start")
	cancel()

	select {
	case err := <-callDone:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("tool call did not stop after context cancellation")
	}
	waitForSignal(t, visionModel.contextDone, "vision model context cancellation")
}

func TestToolCallAggregatesStreamingResponse(t *testing.T) {
	visionModel := &fakeModel{responses: []*model.Response{
		partialResponse("first "),
		nil,
		partialResponse("second"),
	}}
	tool, err := New(visionModel)
	require.NoError(t, err)

	result, err := tool.Call(context.Background(), validExplicitArgs())
	require.NoError(t, err)
	assert.Equal(t, "first second", result)
}

func TestToolCallPrefersFinalResponseOverStreamingChunks(t *testing.T) {
	visionModel := &fakeModel{responses: []*model.Response{
		partialResponse("partial"),
		finalResponse("final"),
	}}
	tool, err := New(visionModel)
	require.NoError(t, err)

	result, err := tool.Call(context.Background(), validExplicitArgs())
	require.NoError(t, err)
	assert.Equal(t, "final", result)
}

func validExplicitArgs() []byte {
	return []byte(`{"prompt":"describe","image_urls":["https://example.com/image.png"]}`)
}

func finalResponse(content string) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(content),
		}},
	}
}

func partialResponse(content string) *model.Response {
	return &model.Response{
		IsPartial: true,
		Choices: []model.Choice{{
			Delta: model.Message{Content: content},
		}},
	}
}

type fakeModel struct {
	lastRequest  *model.Request
	responses    []*model.Response
	err          error
	nilResponses bool
	contextDone  chan struct{}
}

func (m *fakeModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.lastRequest = req
	if m.contextDone != nil {
		go func() {
			<-ctx.Done()
			close(m.contextDone)
		}()
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.nilResponses {
		return nil, nil
	}
	responses := make(chan *model.Response, len(m.responses))
	for _, response := range m.responses {
		responses <- response
	}
	close(responses)
	return responses, nil
}

func (m *fakeModel) Info() model.Info {
	return model.Info{Name: "fake-vision-model"}
}

type blockingModel struct {
	started     chan struct{}
	contextDone chan struct{}
}

func (m *blockingModel) GenerateContent(
	ctx context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	responses := make(chan *model.Response)
	close(m.started)
	go func() {
		<-ctx.Done()
		close(m.contextDone)
	}()
	return responses, nil
}

func (m *blockingModel) Info() model.Info {
	return model.Info{Name: "blocking-vision-model"}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
