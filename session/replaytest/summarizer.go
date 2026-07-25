//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TranscriptSummarizer generates deterministic summaries without a model
// dependency. It is intended for replay tests, not production summarization.
type TranscriptSummarizer struct{}

// NewTranscriptSummarizer creates a deterministic replay summarizer.
func NewTranscriptSummarizer() *TranscriptSummarizer {
	return &TranscriptSummarizer{}
}

// ShouldSummarize always permits explicit replay summary operations.
func (*TranscriptSummarizer) ShouldSummarize(*session.Session) bool {
	return true
}

// Summarize encodes the semantic transcript as stable JSON.
func (*TranscriptSummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if sess == nil {
		return "", session.ErrNilSession
	}
	events, _ := NormalizeEvents(sess.GetEvents(), false)
	type summaryEvent struct {
		Author    string             `json:"author"`
		Role      string             `json:"role,omitempty"`
		Content   string             `json:"content,omitempty"`
		ToolID    string             `json:"tool_id,omitempty"`
		ToolName  string             `json:"tool_name,omitempty"`
		ToolCalls []ToolCallSnapshot `json:"tool_calls,omitempty"`
	}
	transcript := make([]summaryEvent, 0, len(events))
	for _, item := range events {
		transcript = append(transcript, summaryEvent{
			Author:    item.Author,
			Role:      item.Role,
			Content:   item.Content,
			ToolID:    item.ToolID,
			ToolName:  item.ToolName,
			ToolCalls: item.ToolCalls,
		})
	}
	data, err := json.Marshal(transcript)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SetPrompt is a no-op because the replay summarizer has no prompt.
func (*TranscriptSummarizer) SetPrompt(string) {}

// SetModel is a no-op because the replay summarizer is model-free.
func (*TranscriptSummarizer) SetModel(model.Model) {}

// Metadata describes the deterministic replay implementation.
func (*TranscriptSummarizer) Metadata() map[string]any {
	return map[string]any{
		"kind":          "replay_transcript",
		"deterministic": true,
	}
}
