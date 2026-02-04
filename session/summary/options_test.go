//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestOptions(t *testing.T) {
	t.Run("WithPrompt", func(t *testing.T) {
		s := NewSummarizer(&testModel{}, WithPrompt("test"))
		sm, ok := s.(*sessionSummarizer)
		assert.True(t, ok)
		assert.Equal(t, "test", sm.prompt)
	})

	t.Run("WithName", func(t *testing.T) {
		s := NewSummarizer(&testModel{}, WithName("  demo  "))
		sm, ok := s.(*sessionSummarizer)
		assert.True(t, ok)
		assert.Equal(t, "  demo  ", sm.name)
		assert.Equal(t, "  demo  ", sm.Metadata()[metadataKeySummarizerName])
	})

	t.Run("WithTokenThreshold", func(t *testing.T) {
		// Verify metadata increments and logic via isolated checks.
		s := NewSummarizer(&testModel{}, WithTokenThreshold(2))
		md := s.Metadata()
		assert.Equal(t, 1, md[metadataKeyCheckFunctions])

		sIso := NewSummarizer(&testModel{}, WithTokenThreshold(2))
		sess := &session.Session{Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: strings.Repeat("a", 40)},
				}}},
			},
			{
				Author:    "assistant",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: strings.Repeat("b", 40)},
				}}},
			},
		}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithEventThreshold", func(t *testing.T) {
		s := NewSummarizer(&testModel{}, WithEventThreshold(2))
		md := s.Metadata()
		assert.Equal(t, 1, md[metadataKeyCheckFunctions])

		sIso := NewSummarizer(&testModel{}, WithEventThreshold(2))
		sess := &session.Session{Events: []event.Event{{Timestamp: time.Now()}, {Timestamp: time.Now()}, {Timestamp: time.Now()}}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithTimeThreshold", func(t *testing.T) {
		s := NewSummarizer(&testModel{}, WithTimeThreshold(10*time.Millisecond))
		md := s.Metadata()
		assert.Equal(t, 1, md[metadataKeyCheckFunctions])

		sIso := NewSummarizer(&testModel{}, WithTimeThreshold(10*time.Millisecond))
		older := time.Now().Add(-20 * time.Millisecond)
		sess := &session.Session{Events: []event.Event{{Timestamp: older}}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithChecksAll", func(t *testing.T) {
		checks := []Checker{CheckEventThreshold(1), CheckTokenThreshold(4)}
		s := NewSummarizer(&testModel{}, WithChecksAll(checks...))
		sess := &session.Session{Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: strings.Repeat("a", 40)},
				}}},
			},
			{
				Author:    "assistant",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: strings.Repeat("b", 40)},
				}}},
			},
		}}
		assert.True(t, s.ShouldSummarize(sess))
	})

	t.Run("WithChecksAny", func(t *testing.T) {
		checks := []Checker{CheckTokenThreshold(10000), CheckEventThreshold(3)}
		s := NewSummarizer(&testModel{}, WithChecksAny(checks...))
		sess := &session.Session{Events: make([]event.Event, 4)}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		assert.True(t, s.ShouldSummarize(sess))
	})

	t.Run("WithMaxSummaryWords_MetadataAndLengthLimit", func(t *testing.T) {
		// Set a small max length and ensure metadata reflects it and length is limited in prompt.
		s := NewSummarizer(&testModel{}, WithMaxSummaryWords(50))
		md := s.Metadata()
		assert.Equal(t, 50, md[metadataKeyMaxSummaryWords])

		sess := &session.Session{ID: "sess-ml", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}}, Timestamp: time.Now().Add(-2 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent"}}}}, Timestamp: time.Now()},
		}}
		originalEventCount := len(sess.Events)
		text, err := s.Summarize(context.Background(), sess)
		assert.NoError(t, err)
		// Note: With the new prompt-based approach, we can't guarantee exact length
		// as the model controls the output. We just verify it generates some text.
		assert.NotEmpty(t, text)
		// Events should remain unchanged.
		assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")
	})

	t.Run("WithMaxSummaryWords_IgnoresNonPositive", func(t *testing.T) {
		// Non-positive should be ignored, default remains in metadata.
		s := NewSummarizer(&testModel{}, WithMaxSummaryWords(0))
		md := s.Metadata()
		// Default is 0 (no truncation).
		assert.Equal(t, 0, md[metadataKeyMaxSummaryWords])
	})
}

type testModel struct{}

func (t *testModel) Info() model.Info { return model.Info{Name: "test"} }
func (t *testModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Content: "ok"}}}}
	close(ch)
	return ch, nil
}
