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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func captureReadySession() *session.Session {
	now := time.Now()
	return &session.Session{
		ID:      "s1",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			{
				ID:        "u",
				Timestamp: now,
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewUserMessage("remember"),
				}}},
			},
			{
				ID:        "a",
				Timestamp: now.Add(time.Second),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewAssistantMessage("ok"),
				}}},
			},
		},
	}
}

func appendSessionPair(
	sess *session.Session,
	at time.Time,
	userID string,
	userContent string,
	assistantID string,
	assistantContent string,
) {
	sess.EventMu.Lock()
	defer sess.EventMu.Unlock()
	sess.Events = append(sess.Events,
		event.Event{
			ID:        userID,
			Timestamp: at,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage(userContent),
			}}},
		},
		event.Event{
			ID:        assistantID,
			Timestamp: at.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewAssistantMessage(assistantContent),
			}}},
		},
	)
}

func waitCaptureRequest(t *testing.T, ch <-chan captureRequest, name string) captureRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(time.Second):
		t.Fatalf("%s did not start", name)
		return captureRequest{}
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !condition() {
		t.Fatalf("condition was not met within %s", timeout)
	}
}
