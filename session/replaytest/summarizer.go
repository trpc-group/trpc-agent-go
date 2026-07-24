//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type deterministicSummarizer struct{}

func (deterministicSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (deterministicSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", nil
	}
	filterKey := summaryFilterKeyFromSessionID(sess.ID)
	parts := []string{fmt.Sprintf("filter=%s", filterKey)}
	for _, evt := range sess.GetEvents() {
		role := ""
		content := ""
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			msg := evt.Response.Choices[0].Message
			role = msg.Role.String()
			content = msg.Content
			if len(msg.ToolCalls) > 0 {
				names := make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					names = append(names, tc.Function.Name)
				}
				content = "tool_calls:" + strings.Join(names, ",")
			}
			if msg.ToolID != "" {
				content = "tool_result:" + msg.ToolName + ":" + msg.ToolID + ":" + msg.Content
			}
		}
		if role == "" {
			role = evt.Author
		}
		parts = append(parts, role+"="+content)
	}
	return strings.Join(parts, " | "), nil
}

func (deterministicSummarizer) SetPrompt(string) {}

func (deterministicSummarizer) SetModel(model.Model) {}

func (deterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"name": "replay-deterministic"}
}

func summaryFilterKeyFromSessionID(id string) string {
	idx := strings.LastIndex(id, ":")
	if idx < 0 {
		return session.SummaryFilterKeyAllContents
	}
	return id[idx+1:]
}
