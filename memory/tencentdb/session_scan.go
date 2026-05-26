//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const lastCaptureAtStateKey = "tencentdb_agent_memory.last_capture_at"

type scanResult struct {
	Messages []tdaiMessage
	Latest   time.Time
}

func scanTranscript(sess *session.Session, since time.Time) scanResult {
	if sess == nil {
		return scanResult{}
	}
	events := sess.GetEvents()
	if len(events) == 0 {
		return scanResult{}
	}
	var out scanResult
	for _, e := range events {
		if e.Response == nil || !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		if e.Timestamp.After(out.Latest) {
			out.Latest = e.Timestamp
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message
			if msg.Role != model.RoleUser && msg.Role != model.RoleAssistant {
				continue
			}
			content := messageText(msg)
			if content == "" {
				continue
			}
			out.Messages = append(out.Messages, tdaiMessage{
				ID:        messageID(e.ID, choice.Index),
				Role:      string(msg.Role),
				Content:   content,
				Timestamp: e.Timestamp.UTC().UnixMilli(),
			})
		}
	}
	return out
}

func normalizeGatewayMessageTimestamps(messages []tdaiMessage, captureAt time.Time) []tdaiMessage {
	out, _ := normalizeGatewayMessageTimestampsAfter(messages, captureAt, 0)
	return out
}

func normalizeGatewayMessageTimestampsAfter(
	messages []tdaiMessage,
	captureAt time.Time,
	previousTimestamp int64,
) ([]tdaiMessage, int64) {
	if len(messages) == 0 {
		return nil, previousTimestamp
	}
	if captureAt.IsZero() {
		captureAt = time.Now()
	}
	out := make([]tdaiMessage, len(messages))
	copy(out, messages)
	base := captureAt.Add(time.Second).UTC().UnixMilli()
	if base <= previousTimestamp {
		base = previousTimestamp + 1
	}
	for i := range out {
		// The gateway's first per-session cursor is initialized when the capture
		// request arrives. Use capture-time timestamps so the SDK's incremental
		// filter treats this batch as new while preserving message order. A small
		// forward offset avoids clock-order races between Go request construction
		// and gateway request handling. Callers may also pass the last synthetic
		// timestamp for the session so consecutive batches stay strictly
		// increasing even if the wall clock does not move forward.
		out[i].Timestamp = base + int64(i)
	}
	return out, out[len(out)-1].Timestamp
}

func messageID(eventID string, choiceIndex int) string {
	if eventID == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", eventID, choiceIndex)
}

func lastUserAssistantPair(messages []tdaiMessage) (string, string) {
	var pendingUser string
	var lastUser string
	var lastAssistant string
	for _, msg := range messages {
		switch msg.Role {
		case string(model.RoleUser):
			pendingUser = strings.TrimSpace(msg.Content)
		case string(model.RoleAssistant):
			assistant := strings.TrimSpace(msg.Content)
			if pendingUser != "" && assistant != "" {
				lastUser = pendingUser
				lastAssistant = assistant
			}
		}
	}
	return lastUser, lastAssistant
}

func messageText(msg model.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return strings.TrimSpace(msg.Content)
	}
	if len(msg.ContentParts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(msg.ContentParts))
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		text := strings.TrimSpace(*part.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func readBestEffortLastCaptureAt(sess *session.Session) time.Time {
	if sess == nil {
		return time.Time{}
	}
	raw, ok := sess.GetState(lastCaptureAtStateKey)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Time{}
	}
	return t
}

func writeBestEffortLastCaptureAt(sess *session.Session, t time.Time) {
	if sess == nil || t.IsZero() {
		return
	}
	sess.SetState(lastCaptureAtStateKey, []byte(t.UTC().Format(time.RFC3339Nano)))
}
