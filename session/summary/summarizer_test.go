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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionSummarizer_ShouldSummarize(t *testing.T) {
	t.Run("OR logic triggers when any condition true", func(t *testing.T) {
		checks := []Checker{CheckTokenThreshold(10000), CheckEventThreshold(3)}
		s := NewSummarizer(&fakeModel{}, WithChecksAny(checks...))
		sess := &session.Session{Events: make([]event.Event, 4)}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		assert.True(t, s.ShouldSummarize(sess))
	})

	t.Run("ALL logic fails when one condition false", func(t *testing.T) {
		checks := []Checker{CheckEventThreshold(100), CheckTimeThreshold(24 * time.Hour)}
		s := NewSummarizer(&fakeModel{}, WithChecksAll(checks...))
		sess := &session.Session{Events: []event.Event{{Timestamp: time.Now()}}}
		assert.False(t, s.ShouldSummarize(sess))
	})
}

func TestSessionSummarizer_Summarize(t *testing.T) {
	t.Run("errors when no events", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{})
		sess := &session.Session{ID: "empty", Events: []event.Event{}}
		_, err := s.Summarize(context.Background(), sess)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no events to summarize")
	})

	t.Run("errors when no conversation text", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{})
		sess := &session.Session{ID: "no-text", Events: make([]event.Event, 5)}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		_, err := s.Summarize(context.Background(), sess)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no conversation text extracted")
	})

	t.Run("simple concat summary without event modification", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{}) // Use all events
		sess := &session.Session{ID: "concat", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hello"}}}}, Timestamp: time.Now()},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "world"}}}}, Timestamp: time.Now()},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent"}}}}, Timestamp: time.Now()},
		}}
		originalEventCount := len(sess.Events)
		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.Contains(t, text, "hello")
		assert.Contains(t, text, "world")
		// Events should remain unchanged.
		assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")
		// No system summary event should be added.
		for _, event := range sess.Events {
			assert.NotEqual(t, "system", event.Author, "no system events should be added.")
		}
	})

	t.Run("length limit when max length set", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{}, WithMaxSummaryWords(10))
		sess := &session.Session{ID: "limit", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "abcdefghijklmno"}}}}, Timestamp: time.Now().Add(-2 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent"}}}}, Timestamp: time.Now()},
		}}
		originalEventCount := len(sess.Events)
		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		// With the new prompt-based approach, we can't guarantee exact length
		// as the model controls the output. We just verify it generates some text.
		assert.NotEmpty(t, text)
		// Events should remain unchanged.
		assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")
	})

	t.Run("no truncation when max length is zero", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{}, WithMaxSummaryWords(0))
		long := strings.Repeat("abc", 200)
		sess := &session.Session{ID: "no-trunc", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: long}}}}, Timestamp: time.Now().Add(-2 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent"}}}}, Timestamp: time.Now()},
		}}
		originalEventCount := len(sess.Events)
		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.Contains(t, text, long)
		// Events should remain unchanged.
		assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")
	})

	t.Run("author fallback to unknown", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{})
		sess := &session.Session{ID: "author-fallback", Events: []event.Event{
			{Timestamp: time.Now().Add(-3 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "content"}}}}, Timestamp: time.Now().Add(-2 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent"}}}}, Timestamp: time.Now()},
		}}
		originalEventCount := len(sess.Events)
		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.Contains(t, text, "unknown: content")
		// Events should remain unchanged.
		assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")
	})

	t.Run("uses all events for summarization", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{})
		sess := &session.Session{ID: "all-events", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "old1"}}}}, Timestamp: time.Now().Add(-4 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "old2"}}}}, Timestamp: time.Now().Add(-3 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent1"}}}}, Timestamp: time.Now().Add(-2 * time.Second)},
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "recent2"}}}}, Timestamp: time.Now().Add(-1 * time.Second)},
		}}
		originalEventCount := len(sess.Events)
		_, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		// Events should remain unchanged.
		assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")
		// No system events should be added.
		for _, event := range sess.Events {
			assert.NotEqual(t, "system", event.Author, "no system events should be added.")
		}
	})

}

func TestSessionSummarizer_Metadata(t *testing.T) {
	s := NewSummarizer(&fakeModel{}, WithMaxSummaryWords(0))
	md := s.Metadata()
	assert.Equal(t, "fake", md[metadataKeyModelName])
	assert.Equal(t, 0, md[metadataKeyMaxSummaryWords])
	assert.Equal(t, 0, md[metadataKeyCheckFunctions])
}

func TestSessionSummarizer_PlaceholderReplacement(t *testing.T) {
	t.Run("max_summary_words placeholder replacement", func(t *testing.T) {
		// Test with custom prompt containing the placeholder
		customPrompt := "Please summarize the conversation within {max_summary_words} words: {conversation_text}"
		s := NewSummarizer(&fakeModel{}, WithMaxSummaryWords(100), WithPrompt(customPrompt))

		sess := &session.Session{ID: "test", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "Hello world"}}}}, Timestamp: time.Now()},
		}}

		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.NotEmpty(t, text)

		// Verify that the placeholder was replaced with the actual number
		// The fakeModel should have received a prompt with "100" instead of "{max_summary_words}"
		assert.Contains(t, text, "100") // fakeModel returns the prompt as the summary
		// Note: Custom prompts only replace with the number, not the full instruction
	})

	t.Run("placeholder removal when no length limit", func(t *testing.T) {
		// Test with custom prompt containing the placeholder but no length limit
		customPrompt := "Please summarize the conversation within {max_summary_words} words: {conversation_text}"
		s := NewSummarizer(&fakeModel{}, WithMaxSummaryWords(0), WithPrompt(customPrompt))

		sess := &session.Session{ID: "test", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "Hello world"}}}}, Timestamp: time.Now()},
		}}

		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.NotEmpty(t, text)

		// Verify that the placeholder was removed (empty string replacement)
		// The fakeModel should have received a prompt without the placeholder
		assert.NotContains(t, text, "{max_summary_words}")
	})

	t.Run("default prompt with length limit", func(t *testing.T) {
		// Test with default prompt and length limit
		s := NewSummarizer(&fakeModel{}, WithMaxSummaryWords(50))

		sess := &session.Session{ID: "test", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "Hello world"}}}}, Timestamp: time.Now()},
		}}

		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.NotEmpty(t, text)

		// Verify that the default prompt includes length instruction
		assert.Contains(t, text, "50")
		assert.Contains(t, text, "Please keep the summary within")
		assert.NotContains(t, text, "{max_summary_words}")
	})

	t.Run("default prompt without length limit", func(t *testing.T) {
		// Test with default prompt and no length limit
		s := NewSummarizer(&fakeModel{}, WithMaxSummaryWords(0))

		sess := &session.Session{ID: "test", Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "Hello world"}}}}, Timestamp: time.Now()},
		}}

		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.NotEmpty(t, text)

		// Verify that the default prompt doesn't include length instruction
		assert.NotContains(t, text, "Please keep the summary within")
		assert.NotContains(t, text, "{max_summary_words}")
	})
}

// fakeModel is a minimal model that returns the conversation content back to simulate LLM.
type fakeModel struct{}

func (f *fakeModel) Info() model.Info { return model.Info{Name: "fake"} }
func (f *fakeModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	content := ""
	if len(req.Messages) > 0 {
		// Extract conversation text from the prompt for testing.
		prompt := req.Messages[0].Content
		// Find the conversation part after "Conversation:\n"
		if idx := strings.Index(prompt, "Conversation:\n"); idx != -1 {
			conversation := prompt[idx+len("Conversation:\n"):]
			if summaryIdx := strings.Index(conversation, "\n\nSummary:"); summaryIdx != -1 {
				conversation = conversation[:summaryIdx]
			}
			content = strings.TrimSpace(conversation)
		} else {
			content = prompt
		}
		// For testing, return the full conversation content as the summary.
		content = "Summary: " + content
	}
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Content: content}}}}
	close(ch)
	return ch, nil
}

func TestSessionSummarizer_Summarize_NilModel(t *testing.T) {
	s := &sessionSummarizer{
		model:  nil,
		prompt: "test prompt",
	}

	sess := &session.Session{
		ID: "test",
		Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "test"}}}}, Timestamp: time.Now()},
		},
	}

	_, err := s.Summarize(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no model configured")
}

func TestSessionSummarizer_GenerateSummary_ModelError(t *testing.T) {
	errorModel := &errorModel{}
	s := NewSummarizer(errorModel)

	sess := &session.Session{
		ID: "test",
		Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "test"}}}}, Timestamp: time.Now()},
		},
	}

	_, err := s.Summarize(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to generate summary")
}

func TestSessionSummarizer_GenerateSummary_ResponseError(t *testing.T) {
	responseErrorModel := &responseErrorModel{}
	s := NewSummarizer(responseErrorModel)

	sess := &session.Session{
		ID: "test",
		Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "test"}}}}, Timestamp: time.Now()},
		},
	}

	_, err := s.Summarize(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model error during summarization")
}

func TestSessionSummarizer_GenerateSummary_EmptyResponse(t *testing.T) {
	emptyModel := &emptyResponseModel{}
	s := NewSummarizer(emptyModel)

	sess := &session.Session{
		ID: "test",
		Events: []event.Event{
			{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "test"}}}}, Timestamp: time.Now()},
		},
	}

	_, err := s.Summarize(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generated empty summary")
}

func TestSessionSummarizer_ShouldSummarize_EmptyEvents(t *testing.T) {
	s := NewSummarizer(&fakeModel{}, WithEventThreshold(10))
	sess := &session.Session{Events: []event.Event{}}
	assert.False(t, s.ShouldSummarize(sess))
}

func TestSessionSummarizer_Metadata_NilModel(t *testing.T) {
	s := &sessionSummarizer{
		model:           nil,
		maxSummaryWords: 100,
		checks:          []Checker{},
	}
	md := s.Metadata()
	assert.Equal(t, "", md[metadataKeyModelName])
	assert.Equal(t, false, md[metadataKeyModelAvailable])
	assert.Equal(t, 100, md[metadataKeyMaxSummaryWords])
}

func TestSessionSummarizer_ExtractConversationText_WithAuthor(t *testing.T) {
	s := NewSummarizer(&fakeModel{})
	sess := &session.Session{
		ID: "test",
		Events: []event.Event{
			{
				Author:   "user",
				Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hello"}}}},
			},
			{
				Author:   "assistant",
				Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "hi there"}}}},
			},
		},
	}

	text, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Contains(t, text, "user:")
	assert.Contains(t, text, "assistant:")
}

// errorModel returns an error when generating content
type errorModel struct{}

func (e *errorModel) Info() model.Info { return model.Info{Name: "error"} }
func (e *errorModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	return nil, fmt.Errorf("model error")
}

// responseErrorModel returns a response with an error
type responseErrorModel struct{}

func (r *responseErrorModel) Info() model.Info { return model.Info{Name: "response-error"} }
func (r *responseErrorModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done:  true,
		Error: &model.ResponseError{Message: "response error"},
	}
	close(ch)
	return ch, nil
}

// emptyResponseModel returns an empty response
type emptyResponseModel struct{}

func (e *emptyResponseModel) Info() model.Info { return model.Info{Name: "empty"} }
func (e *emptyResponseModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Content: ""}}}}
	close(ch)
	return ch, nil
}
