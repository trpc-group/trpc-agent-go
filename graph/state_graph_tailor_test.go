//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockModel captures the last request passed to GenerateContent and returns a final response.
type mockModel struct {
	lastRequest *model.Request
}

func (m *mockModel) Info() model.Info { return model.Info{Name: "mock"} }

func (m *mockModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	m.lastRequest = request
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}}
	close(ch)
	return ch, nil
}

// dummyCounter implements TokenCounter but is not used by the strategy in these tests.
type dummyCounter struct{}

func (d *dummyCounter) CountTokens(ctx context.Context, messages []model.Message) (int, error) {
	return 0, nil
}
func (d *dummyCounter) RemainingTokens(ctx context.Context, messages []model.Message) (int, error) {
	return 0, nil
}

// errStrategy returns an error to simulate tailoring failure.
type errStrategy struct{}

func (e *errStrategy) TailorMessages(ctx context.Context, messages []model.Message, maxTokens int) ([]model.Message, error) {
	return nil, fmt.Errorf("tailor failed")
}

// okStrategy returns a provided tailored slice to simulate success.
type okStrategy struct{ tailored []model.Message }

func (o *okStrategy) TailorMessages(ctx context.Context, messages []model.Message, maxTokens int) ([]model.Message, error) {
	return o.tailored, nil
}

func TestLLMNode_TailoringError_ProceedsWithOriginalMessages(t *testing.T) {
	t.Helper()
	mm := &mockModel{}
	// Empty instruction to avoid injecting system message at head.
	node := NewLLMNodeFunc(mm, "", nil, WithTokenTailoring(100, &dummyCounter{}, &errStrategy{}))

	// State with two messages.
	orig := []model.Message{model.NewUserMessage("u1"), model.NewAssistantMessage("a1")}
	state := State{StateKeyMessages: orig}

	_, err := node(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, mm.lastRequest)
	require.Equal(t, orig, mm.lastRequest.Messages)
}

func TestLLMNode_TailoringSuccess_UsesTailoredMessages(t *testing.T) {
	t.Helper()
	mm := &mockModel{}
	tailored := []model.Message{model.NewUserMessage("u_only")}
	node := NewLLMNodeFunc(mm, "", nil, WithTokenTailoring(100, &dummyCounter{}, &okStrategy{tailored: tailored}))

	// Original has two messages; strategy will trim to one.
	orig := []model.Message{model.NewUserMessage("u1"), model.NewAssistantMessage("a1")}
	state := State{StateKeyMessages: orig}

	_, err := node(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, mm.lastRequest)
	require.Equal(t, tailored, mm.lastRequest.Messages)
}
