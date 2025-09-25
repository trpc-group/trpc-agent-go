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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestOptions(t *testing.T) {
	t.Run("WithTokenThreshold", func(t *testing.T) {
		// Verify metadata increments and logic via isolated checks.
		s := NewSummarizer(&testModel{}, WithTokenThreshold(2))
		md := s.Metadata()
		assert.Equal(t, 1, md[metadataKeyCheckFunctions])

		sIso := NewSummarizer(&testModel{}, WithTokenThreshold(2))
		sess := &session.Session{Events: []event.Event{
			{Response: &model.Response{Usage: &model.Usage{TotalTokens: 2}}, Timestamp: time.Now()},
			{Response: &model.Response{Usage: &model.Usage{TotalTokens: 3}}, Timestamp: time.Now()},
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
			{Response: &model.Response{Usage: &model.Usage{TotalTokens: 5}}, Timestamp: time.Now()},
			{Response: &model.Response{Usage: &model.Usage{TotalTokens: 5}}, Timestamp: time.Now()},
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

	t.Run("WithMaxLength_MetadataAndTruncation", func(t *testing.T) {
		// Set a small max length and ensure metadata reflects it and output is truncated.
		s := NewSummarizer(&testModel{}, WithMaxSummaryChars(50))
		md := s.Metadata()
		assert.Equal(t, 50, md[metadataKeyMaxSummaryLength])

		sess := &session.Session{ID: "sess-ml", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}}, Timestamp: time.Now().Add(-2 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent"}}}}, Timestamp: time.Now()},
		}}
		originalEventCount := len(sess.Events)
		text, err := s.Summarize(context.Background(), sess)
		assert.NoError(t, err)
		assert.LessOrEqual(t, len(text), 50)
		// Events should remain unchanged.
		assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")
	})

	t.Run("WithMaxLength_IgnoresNonPositive", func(t *testing.T) {
		// Non-positive should be ignored, default remains in metadata.
		s := NewSummarizer(&testModel{}, WithMaxSummaryChars(0))
		md := s.Metadata()
		// Default is 0 (no truncation).
		assert.Equal(t, 0, md[metadataKeyMaxSummaryLength])
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
