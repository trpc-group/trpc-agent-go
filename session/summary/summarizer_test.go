package summary

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSummarizer_Errors_NoEvents(t *testing.T) {
	s := NewSummarizer()
	sess := &session.Session{ID: "empty", Events: []event.Event{}}
	_, err := s.Summarize(context.Background(), sess, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no events to summarize")
}

func TestSummarizer_Errors_NoOldEvents(t *testing.T) {
	s := NewSummarizer(WithKeepRecentCount(10))
	sess := &session.Session{ID: "no-old", Events: make([]event.Event, 5)}
	for i := range sess.Events {
		sess.Events[i] = event.Event{Timestamp: time.Now()}
	}
	_, err := s.Summarize(context.Background(), sess, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no old events to summarize")
}

func TestSummarizer_Errors_NoConversationText(t *testing.T) {
	s := NewSummarizer(WithKeepRecentCount(1))
	sess := &session.Session{ID: "no-text", Events: make([]event.Event, 3)}
	for i := range sess.Events {
		sess.Events[i] = event.Event{Timestamp: time.Now()} // No Response content.
	}
	_, err := s.Summarize(context.Background(), sess, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no conversation text extracted")
}

func TestWithChecksAny_ORLogic(t *testing.T) {
	checks := []Checker{SetTokenThreshold(10000), SetEventThreshold(3)}
	s := NewSummarizer(WithChecksAny(checks))
	sess := &session.Session{Events: make([]event.Event, 4)}
	for i := range sess.Events {
		sess.Events[i] = event.Event{Timestamp: time.Now()}
	}
	assert.True(t, s.ShouldSummarize(sess))
}

func TestSummarizer_SimpleConcatSummary(t *testing.T) {
	s := NewSummarizer(WithKeepRecentCount(1))
	sess := &session.Session{ID: "concat", Events: []event.Event{
		{
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "hello"}}},
			},
			Timestamp: time.Now(),
		},
		{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "world"}}}}, Timestamp: time.Now()},
		{
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "recent"}}},
			},
			Timestamp: time.Now(),
		},
	}}
	text, err := s.Summarize(context.Background(), sess, 0)
	require.NoError(t, err)
	assert.Contains(t, text, "hello")
	assert.Contains(t, text, "world")
}
