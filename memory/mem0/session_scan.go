//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func readLastExtractAt(sess *session.Session) time.Time {
	if sess == nil {
		return time.Time{}
	}
	raw, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return ts
}

func writeLastExtractAt(sess *session.Session, ts time.Time) {
	if sess == nil {
		return
	}
	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt, []byte(ts.UTC().Format(time.RFC3339Nano)))
}

func scanDeltaSince(sess *session.Session, since time.Time) (time.Time, []model.Message) {
	if sess == nil {
		return time.Time{}, nil
	}
	var latestTs time.Time
	var messages []model.Message

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for _, e := range sess.Events {
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		if e.Timestamp.After(latestTs) {
			latestTs = e.Timestamp
		}
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message
			if msg.Role == model.RoleTool || msg.ToolID != "" || len(msg.ToolCalls) > 0 {
				continue
			}
			if msg.Role != model.RoleUser && msg.Role != model.RoleAssistant {
				continue
			}
			if msg.Content == "" && len(msg.ContentParts) == 0 {
				continue
			}
			messages = append(messages, msg)
		}
	}
	return latestTs, messages
}
