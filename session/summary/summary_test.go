//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestBuildPromptMessages(t *testing.T) {
	t.Run("with summary and anchor", func(t *testing.T) {
		sess := &session.Session{
			ID: "test-session",
			Events: []event.Event{
				{ID: "event1", Author: "user", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hello"}}}}},
				{ID: "event2", Author: "assistant", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hi there"}}}}},
				{ID: "event3", Author: "user", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "how are you"}}}}},
			},
		}

		messages := buildPromptMessages(sess, "Previous conversation was about greetings", "event2", 2)

		// Should have system message + 1 recent event after anchor.
		assert.Len(t, messages, 2)
		assert.Equal(t, model.Role("system"), messages[0].Role)
		assert.Contains(t, messages[0].Content, "Previous conversation was about greetings")
		assert.Equal(t, model.Role("user"), messages[1].Role)
		assert.Equal(t, "how are you", messages[1].Content)
	})

	t.Run("without summary", func(t *testing.T) {
		sess := &session.Session{
			ID: "test-session",
			Events: []event.Event{
				{ID: "event1", Author: "user", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hello"}}}}},
				{ID: "event2", Author: "assistant", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hi there"}}}}},
			},
		}

		messages := buildPromptMessages(sess, "", "", 1)

		// Should have only 1 recent event.
		assert.Len(t, messages, 1)
		assert.Equal(t, model.Role("assistant"), messages[0].Role)
		assert.Equal(t, "hi there", messages[0].Content)
	})

	t.Run("with window size limit", func(t *testing.T) {
		sess := &session.Session{
			ID: "test-session",
			Events: []event.Event{
				{ID: "event1", Author: "user", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "old1"}}}}},
				{ID: "event2", Author: "assistant", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "old2"}}}}},
				{ID: "event3", Author: "user", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent1"}}}}},
				{ID: "event4", Author: "assistant", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent2"}}}}},
			},
		}

		messages := buildPromptMessages(sess, "Summary", "", 2)

		// Should have system message + 2 most recent events.
		assert.Len(t, messages, 3)
		assert.Equal(t, model.Role("system"), messages[0].Role)
		assert.Equal(t, model.Role("user"), messages[1].Role)
		assert.Equal(t, "recent1", messages[1].Content)
		assert.Equal(t, model.Role("assistant"), messages[2].Role)
		assert.Equal(t, "recent2", messages[2].Content)
	})
}
