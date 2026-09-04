//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const logicalEventIDExtension = "trpc_agent.replay.logical_event_id"

// InMemoryBackend returns the reference backend used by the lightweight matrix.
func InMemoryBackend() Backend {
	return Backend{
		Name:         "inmemory",
		Capabilities: FullCapabilities(),
		Open: func(_ context.Context, caseName string) (*Services, error) {
			summarizer := &DeterministicSummarizer{}
			memoryService := memory.Service(memoryinmemory.NewMemoryService())
			if caseName == "memory_retry_recovery" {
				memoryService = &failOnceMemoryService{Service: memoryService}
			}
			return &Services{
				Session: sessioninmemory.NewSessionService(
					sessioninmemory.WithSummarizer(summarizer),
					sessioninmemory.WithCascadeFullSessionSummary(false),
				),
				Memory: memoryService,
			}, nil
		},
	}
}

type failOnceMemoryService struct {
	memory.Service
	failed bool
}

func (s *failOnceMemoryService) AddMemory(
	ctx context.Context,
	key memory.UserKey,
	value string,
	topics []string,
	options ...memory.AddOption,
) error {
	if !s.failed {
		s.failed = true
		return errors.New("replaytest: injected pre-commit memory failure")
	}
	return s.Service.AddMemory(ctx, key, value, topics, options...)
}

// DeterministicSummarizer makes summary persistence testable without an LLM.
// The service passes it the filter-scoped session view.
type DeterministicSummarizer struct{}

// ShouldSummarize reports whether the scoped transcript is non-empty.
func (*DeterministicSummarizer) ShouldSummarize(sess *session.Session) bool {
	return sess != nil && len(sess.Events) > 0
}

// Summarize returns a stable projection of the scoped transcript.
func (*DeterministicSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", session.ErrNilSession
	}
	parts := make([]string, 0, len(sess.Events))
	for _, evt := range sess.Events {
		messages := eventMessages(evt.Response)
		if len(messages) == 0 {
			parts = append(parts, evt.Author+":")
			continue
		}
		for _, msg := range messages {
			parts = append(parts, fmt.Sprintf("%s:%s:%s", evt.Author, msg.Role, msg.Content))
		}
	}
	return "summary[" + strings.Join(parts, "|") + "]", nil
}

// SetPrompt is a no-op required by summary.SessionSummarizer.
func (*DeterministicSummarizer) SetPrompt(string) {}

// SetModel is a no-op required by summary.SessionSummarizer.
func (*DeterministicSummarizer) SetModel(model.Model) {}

// Metadata describes the deterministic implementation.
func (*DeterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"name": "replaytest-deterministic"}
}

func eventMessages(response *model.Response) []model.Message {
	if response == nil {
		return nil
	}
	messages := make([]model.Message, 0, len(response.Choices)*2)
	for _, choice := range response.Choices {
		if choice.Message.Role != "" || choice.Message.Content != "" || len(choice.Message.ToolCalls) > 0 {
			messages = append(messages, choice.Message)
		}
		if choice.Delta.Role != "" || choice.Delta.Content != "" || len(choice.Delta.ToolCalls) > 0 {
			messages = append(messages, choice.Delta)
		}
	}
	return messages
}
