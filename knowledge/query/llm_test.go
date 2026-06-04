//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package query

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubModel struct {
	content string
	err     error
	apiErr  *model.ResponseError
	capture *model.Request
}

func (s *stubModel) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	s.capture = req
	if s.err != nil {
		return nil, s.err
	}
	ch := make(chan *model.Response, 1)
	resp := &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: s.content}}},
	}
	if s.apiErr != nil {
		resp.Error = s.apiErr
	}
	ch <- resp
	close(ch)
	return ch, nil
}

func (s *stubModel) Info() model.Info {
	return model.Info{Name: "stub"}
}

func TestLLMEnhancer_Basic(t *testing.T) {
	m := &stubModel{content: "rewritten query"}
	e := NewLLMEnhancer(m)

	res, err := e.EnhanceQuery(context.Background(), &Request{Query: "original"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Enhanced != "rewritten query" {
		t.Fatalf("expected 'rewritten query', got %q", res.Enhanced)
	}
}

func TestLLMEnhancer_WithHistory(t *testing.T) {
	m := &stubModel{content: "contextualized query"}
	e := NewLLMEnhancer(m)

	res, err := e.EnhanceQuery(context.Background(), &Request{
		Query: "how to configure it?",
		History: []ConversationMessage{
			{Role: "user", Content: "tell me about query enhancer"},
			{Role: "assistant", Content: "query enhancer rewrites queries for better retrieval"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Enhanced != "contextualized query" {
		t.Fatalf("expected 'contextualized query', got %q", res.Enhanced)
	}
	// system + 2 history + 1 user = 4 messages
	if len(m.capture.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(m.capture.Messages))
	}
	if m.capture.Messages[0].Role != model.RoleSystem {
		t.Fatalf("first message should be system, got %s", m.capture.Messages[0].Role)
	}
	if m.capture.Messages[1].Role != model.RoleUser {
		t.Fatalf("second message should be user history, got %s", m.capture.Messages[1].Role)
	}
	if m.capture.Messages[2].Role != model.RoleAssistant {
		t.Fatalf("third message should be assistant history, got %s", m.capture.Messages[2].Role)
	}
	if m.capture.Messages[3].Content != "how to configure it?" {
		t.Fatalf("last message should be the original query")
	}
}

func TestLLMEnhancer_EmptyQuery(t *testing.T) {
	m := &stubModel{content: "should not be called"}
	e := NewLLMEnhancer(m)

	res, err := e.EnhanceQuery(context.Background(), &Request{Query: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Enhanced != "" {
		t.Fatalf("expected empty enhanced, got %q", res.Enhanced)
	}
	if m.capture != nil {
		t.Fatal("model should not be called for empty query")
	}
}

func TestLLMEnhancer_NilRequest(t *testing.T) {
	m := &stubModel{content: "should not be called"}
	e := NewLLMEnhancer(m)

	res, err := e.EnhanceQuery(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Enhanced != "" {
		t.Fatalf("expected empty enhanced, got %q", res.Enhanced)
	}
}

func TestLLMEnhancer_ModelError(t *testing.T) {
	m := &stubModel{err: errors.New("connection refused")}
	e := NewLLMEnhancer(m)

	_, err := e.EnhanceQuery(context.Background(), &Request{Query: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, m.err) {
		t.Fatalf("expected wrapped connection error, got: %v", err)
	}
}

func TestLLMEnhancer_APIError(t *testing.T) {
	m := &stubModel{apiErr: &model.ResponseError{Message: "rate limited"}}
	e := NewLLMEnhancer(m)

	_, err := e.EnhanceQuery(context.Background(), &Request{Query: "test"})
	if err == nil {
		t.Fatal("expected error from API")
	}
}

func TestLLMEnhancer_EmptyResponse(t *testing.T) {
	m := &stubModel{content: "   "}
	e := NewLLMEnhancer(m)

	res, err := e.EnhanceQuery(context.Background(), &Request{Query: "fallback test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Enhanced != "fallback test" {
		t.Fatalf("expected fallback to original query, got %q", res.Enhanced)
	}
}

func TestLLMEnhancer_CustomSystemPrompt(t *testing.T) {
	m := &stubModel{content: "custom result"}
	e := NewLLMEnhancer(m, WithSystemPrompt("custom prompt"))

	_, err := e.EnhanceQuery(context.Background(), &Request{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.capture.Messages[0].Content != "custom prompt" {
		t.Fatalf("expected custom system prompt, got %q", m.capture.Messages[0].Content)
	}
}

func TestLLMEnhancer_SkipsNonUserAssistantHistory(t *testing.T) {
	m := &stubModel{content: "result"}
	e := NewLLMEnhancer(m)

	_, err := e.EnhanceQuery(context.Background(), &Request{
		Query: "test",
		History: []ConversationMessage{
			{Role: "user", Content: "hello"},
			{Role: "tool", Content: "tool output"},
			{Role: "system", Content: "system note"},
			{Role: "assistant", Content: "hi there"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// system + user history + assistant history + user query = 4
	// tool and system history should be skipped
	if len(m.capture.Messages) != 4 {
		t.Fatalf("expected 4 messages (tool/system history skipped), got %d", len(m.capture.Messages))
	}
}
