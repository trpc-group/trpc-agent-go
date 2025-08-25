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
	t.Run("WithChecks", func(t *testing.T) {
		c := SetEventThreshold(2)
		s := NewSummarizer(WithChecks([]Checker{c}))
		sess := &session.Session{Events: []event.Event{{Timestamp: time.Now()}, {Timestamp: time.Now()}}}
		assert.True(t, s.ShouldSummarize(sess))
	})

	t.Run("WithTokenThreshold", func(t *testing.T) {
		// Verify metadata increments and logic via isolated checks.
		s := NewSummarizer(WithTokenThreshold(2))
		md := s.Metadata()
		assert.Equal(t, 3, md[MetadataKeyCheckFunctions])

		sIso := NewSummarizer(WithChecks([]Checker{SetTokenThreshold(2)}))
		sess := &session.Session{Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "12345678"}}}}, Timestamp: time.Now()},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "abcdefgh"}}}}, Timestamp: time.Now()},
		}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithEventThreshold", func(t *testing.T) {
		s := NewSummarizer(WithEventThreshold(3))
		md := s.Metadata()
		assert.Equal(t, 3, md[MetadataKeyCheckFunctions])

		sIso := NewSummarizer(WithChecks([]Checker{SetEventThreshold(3)}))
		sess := &session.Session{Events: []event.Event{{Timestamp: time.Now()}, {Timestamp: time.Now()}, {Timestamp: time.Now()}}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithTimeThreshold", func(t *testing.T) {
		s := NewSummarizer(WithTimeThreshold(10 * time.Millisecond))
		md := s.Metadata()
		assert.Equal(t, 3, md[MetadataKeyCheckFunctions])

		sIso := NewSummarizer(WithChecks([]Checker{SetTimeThreshold(10 * time.Millisecond)}))
		older := time.Now().Add(-20 * time.Millisecond)
		sess := &session.Session{Events: []event.Event{{Timestamp: older}}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithImportantThreshold", func(t *testing.T) {
		s := NewSummarizer(WithImportantThreshold(5))
		md := s.Metadata()
		assert.Equal(t, 3, md[MetadataKeyCheckFunctions])

		sIso := NewSummarizer(WithChecks([]Checker{SetImportantThreshold(5)}))
		sess := &session.Session{Events: []event.Event{{
			Response:  &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "   important   "}}}},
			Timestamp: time.Now(),
		}}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithConversationThreshold", func(t *testing.T) {
		s := NewSummarizer(WithConversationThreshold(2))
		md := s.Metadata()
		assert.Equal(t, 3, md[MetadataKeyCheckFunctions])

		sIso := NewSummarizer(WithChecks([]Checker{SetConversationThreshold(2)}))
		sess := &session.Session{Events: []event.Event{{Timestamp: time.Now()}, {Timestamp: time.Now()}}}
		assert.True(t, sIso.ShouldSummarize(sess))
	})

	t.Run("WithChecksAll", func(t *testing.T) {
		checks := []Checker{SetEventThreshold(2), SetTokenThreshold(4)}
		s := NewSummarizer(WithChecksAll(checks))
		sess := &session.Session{Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "abcdefghijkl"}}}}, Timestamp: time.Now()},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "mnopqrstuvwx"}}}}, Timestamp: time.Now()},
		}}
		assert.True(t, s.ShouldSummarize(sess))
	})

	t.Run("WithChecksAny", func(t *testing.T) {
		checks := []Checker{SetTokenThreshold(10000), SetEventThreshold(3)}
		s := NewSummarizer(WithChecksAny(checks))
		sess := &session.Session{Events: make([]event.Event, 4)}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		assert.True(t, s.ShouldSummarize(sess))
	})

	t.Run("WithMaxLength_MetadataAndTruncation", func(t *testing.T) {
		// Set a small max length and ensure metadata reflects it and output is truncated.
		s := NewSummarizer(WithMaxLength(50), WithKeepRecent(1))
		md := s.Metadata()
		assert.Equal(t, 50, md[MetadataKeyMaxSummaryLength])

		sess := &session.Session{ID: "sess-ml", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}}, Timestamp: time.Now().Add(-2 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent"}}}}, Timestamp: time.Now()},
		}}
		text, err := s.Summarize(context.Background(), sess, 0)
		assert.NoError(t, err)
		assert.LessOrEqual(t, len(text), 50)
	})

	t.Run("WithMaxLength_IgnoresNonPositive", func(t *testing.T) {
		// Non-positive should be ignored, default remains in metadata.
		s := NewSummarizer(WithMaxLength(0))
		md := s.Metadata()
		// Default is 1000.
		assert.Equal(t, 1000, md[MetadataKeyMaxSummaryLength])
	})
}
