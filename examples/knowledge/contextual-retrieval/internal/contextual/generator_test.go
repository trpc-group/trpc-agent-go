//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package contextual

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeModel struct {
	responses []*model.Response
	err       error
	nilStream bool
	request   *model.Request
}

func (m *fakeModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.request = request
	if m.err != nil {
		return nil, m.err
	}
	if m.nilStream {
		return nil, nil
	}
	responses := make(chan *model.Response, len(m.responses))
	for _, response := range m.responses {
		responses <- response
	}
	close(responses)
	return responses, nil
}

func (*fakeModel) Info() model.Info { return model.Info{Name: "fake"} }

func TestModelContextGeneratorAggregatesDeltas(t *testing.T) {
	llm := &fakeModel{responses: []*model.Response{
		{Choices: []model.Choice{{Delta: model.Message{Content: "short "}}}},
		{Choices: []model.Choice{{Delta: model.Message{Content: "context"}}}},
	}}
	generator := newModelContextGenerator(llm)
	got, err := generator.Generate(context.Background(), "parent", "chunk")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "short context" {
		t.Fatalf("Generate = %q, want short context", got)
	}
	if llm.request == nil {
		t.Fatal("model request was not captured")
	}
	if llm.request.Temperature == nil || *llm.request.Temperature != 0 {
		t.Fatal("context request did not set temperature to zero")
	}
	if llm.request.MaxTokens == nil || *llm.request.MaxTokens != contextMaxTokens {
		t.Fatalf("MaxTokens = %v, want %d", llm.request.MaxTokens, contextMaxTokens)
	}
	if llm.request.Stream {
		t.Fatal("context request unexpectedly enabled streaming")
	}
	if len(llm.request.Messages) != 2 ||
		!strings.Contains(llm.request.Messages[1].Content, "<document>\nparent\n</document>") ||
		!strings.Contains(llm.request.Messages[1].Content, "<chunk>\nchunk\n</chunk>") {
		t.Fatalf("unexpected context prompt: %+v", llm.request.Messages)
	}
}

func TestModelContextGeneratorUsesFinalMessage(t *testing.T) {
	llm := &fakeModel{responses: []*model.Response{{
		Choices: []model.Choice{{Message: model.Message{Content: "final context"}}},
	}}}
	got, err := newModelContextGenerator(llm).Generate(context.Background(), "parent", "chunk")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "final context" {
		t.Fatalf("Generate = %q, want final context", got)
	}
}

func TestModelContextGeneratorFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		model     *fakeModel
		wantError string
	}{
		{
			name:      "request error",
			model:     &fakeModel{err: errors.New("provider unavailable")},
			wantError: "provider unavailable",
		},
		{
			name: "response error",
			model: &fakeModel{responses: []*model.Response{{
				Error: &model.ResponseError{Message: "upstream failed"},
			}}},
			wantError: "upstream failed",
		},
		{
			name:      "empty response",
			model:     &fakeModel{},
			wantError: "empty context",
		},
		{
			name:      "nil stream",
			model:     &fakeModel{nilStream: true},
			wantError: "no response stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newModelContextGenerator(tt.model).Generate(
				context.Background(),
				"parent",
				"chunk",
			)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Generate error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestModelContextGeneratorHonorsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newModelContextGenerator(&fakeModel{}).Generate(ctx, "parent", "chunk")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate error = %v, want context.Canceled", err)
	}
}
