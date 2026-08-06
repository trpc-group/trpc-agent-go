//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fakellm provides a deterministic, API-key-free fake LLM that
// implements model.Model. It is wired into the code review pipeline when
// --model=fake is passed, so the LLM integration path can be exercised
// end-to-end in CI without real credentials.
//
// Borrowed from competitor PR #2243's --fake-model flag.
//
// The fake model does NOT call any external service. Instead it scans
// the user message (the diff text) for a handful of high-signal patterns
// and emits a JSON response containing the corresponding findings. The
// output is deterministic: identical input always produces identical
// output, which makes it usable from tests.
package fakellm

import (
	"context"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ModelName is the value returned by FakeModel.Info().Name.
const ModelName = "fake-review-model"

// FakeModel implements model.Model by returning deterministic review
// findings derived from simple pattern matching against the request's
// user message. It never touches the network.
type FakeModel struct{}

// New returns a FakeModel. It takes no configuration because the model
// is fully deterministic and API-key-free.
func New() *FakeModel { return &FakeModel{} }

// Info implements model.Model.
func (m *FakeModel) Info() model.Info {
	return model.Info{Name: ModelName, ContextWindow: 8192}
}

// GenerateContent implements model.Model. It builds a single
// non-streaming Response whose Choices[0].Message.Content is a JSON
// array of findings derived from the user message. The channel is
// buffered and closed immediately so callers draining it see Done=true
// on the first and only read.
//
// The function never returns an error: fake model failures are not
// part of the contract. Callers that want to test failure paths should
// use --executor=fake-fail instead (Phase 3.7).
func (m *FakeModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	diffText := extractUserContent(req)
	findings := scan(diffText)
	content := encodeFindings(findings)

	finishReason := "stop"
	resp := &model.Response{
		ID:      "fake-review-" + nowID(),
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Model:   ModelName,
		Choices: []model.Choice{{
			Index:        0,
			Message:      model.Message{Role: model.RoleAssistant, Content: content},
			FinishReason: &finishReason,
		}},
		Usage: &model.Usage{
			PromptTokens:     approxTokens(diffText),
			CompletionTokens: approxTokens(content),
			TotalTokens:      approxTokens(diffText) + approxTokens(content),
		},
		Done:      true,
		Timestamp: time.Now(),
	}

	ch := make(chan *model.Response, 1)
	ch <- resp
	close(ch)
	return ch, nil
}

// extractUserContent concatenates the Content of every user-role
// message in the request. The pipeline sends the diff as a single user
// message, so this is usually one message; concatenation keeps the
// fake robust to multi-message prompts.
func extractUserContent(req *model.Request) string {
	if req == nil {
		return ""
	}
	var b strings.Builder
	for _, msg := range req.Messages {
		if msg.Role == model.RoleUser {
			b.WriteString(msg.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// approxTokens returns a rough token count (4 chars per token) used
// only to populate Response.Usage with plausible numbers. It does not
// need to match any real tokenizer.
func approxTokens(s string) int {
	return len(s) / 4
}

// nowID returns a Unix-nano timestamp suffix for the response ID. The
// ID is not used for deduplication; it only needs to be unique within
// a single process run.
func nowID() string {
	return strings.ReplaceAll(time.Now().Format("20060102-150405.000000000"), ".", "")
}
