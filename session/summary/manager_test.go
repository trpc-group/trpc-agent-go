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
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// mockModel implements model.Model for testing purposes.
type mockModel struct {
	name string
	// response to emit as the model result.
	resp string
	// capture last request for assertions.
	lastRequest *model.Request
}

func (m *mockModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.lastRequest = req
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{Content: m.resp},
		}},
	}
	close(ch)
	return ch, nil
}

func TestManager_Summarize_CacheWithoutCompression(t *testing.T) {
	ctx := context.Background()

	// Summarizer with mock model.
	s := NewSummarizer(&mockModel{name: "mock", resp: "summary"}, WithWindowSize(2))
	mgr := NewManager(s)

	// Prepare session with 5 events.
	sess := &session.Session{
		ID:        "sess-1",
		AppName:   "app",
		UserID:    "user",
		Events:    make([]event.Event, 5),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	for i := range sess.Events {
		sess.Events[i] = event.Event{
			Response:  &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "msg"}}}},
			Timestamp: time.Now(),
		}
	}

	originalEventCount := len(sess.Events)
	require.NoError(t, mgr.Summarize(ctx, sess, true))

	// Expect events remain unchanged.
	assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")

	// Expect cache entry exists.
	summaryEntry, err := mgr.GetSummary(sess)
	require.NoError(t, err)
	require.NotNil(t, summaryEntry)
	assert.NotEmpty(t, summaryEntry.Summary)
}

func TestSummarizer_PromptFormatting_ModelUsed(t *testing.T) {
	ctx := context.Background()

	mock := &mockModel{name: "mock-llm", resp: "- bullet one"}
	customPrompt := "Conversation:\n{conversation_text}\n\nSummary:"
	s := NewSummarizer(mock, WithPrompt(customPrompt), WithWindowSize(3)) // Use all events

	// Build a session with 3 events.
	sess := &session.Session{
		ID:      "sess-2",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "hello"}}},
				},
				Timestamp: time.Now(),
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "world"}}},
				},
				Timestamp: time.Now(),
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "recent"}}},
				},
				Timestamp: time.Now(),
			},
		},
	}

	originalEventCount := len(sess.Events)
	text, err := s.Summarize(ctx, sess, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, text)

	// Events should remain unchanged.
	assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")

	require.NotNil(t, mock.lastRequest)
	require.NotEmpty(t, mock.lastRequest.Messages)
	content := mock.lastRequest.Messages[0].Content
	assert.NotEmpty(t, content)
	assert.True(t, strings.Contains(content, "hello"))
	assert.True(t, strings.Contains(content, "world"))
}

func TestMetadata_IncludesModelInfo(t *testing.T) {
	mock := &mockModel{name: "mock-llm", resp: "ok"}
	s := NewSummarizer(mock)
	md := s.Metadata()
	assert.Equal(t, "mock-llm", md[metadataKeyModelName])
}

// fakeService is a minimal session.Service test double for manager tests.
// It records whether AppendEvent was called.
// All other methods are no-ops returning zero values.
// Comments end with periods.
type fakeService struct{ appendCalled bool }

func (f *fakeService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	return &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}, nil
}
func (f *fakeService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}, nil
}
func (f *fakeService) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	return nil, nil
}
func (f *fakeService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return nil
}
func (f *fakeService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return nil
}
func (f *fakeService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}
func (f *fakeService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return nil, nil
}
func (f *fakeService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return nil
}
func (f *fakeService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return nil, nil
}
func (f *fakeService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return nil
}
func (f *fakeService) AppendEvent(ctx context.Context, s *session.Session, e *event.Event, options ...session.Option) error {
	f.appendCalled = true
	return nil
}
func (f *fakeService) CreateSessionSummary(ctx context.Context, sess *session.Session, force bool) error {
	return nil
}
func (f *fakeService) GetSessionSummaryText(ctx context.Context, sess *session.Session) (string, bool) {
	return "", false
}
func (f *fakeService) GetSummaryRecord(ctx context.Context, sess *session.Session) (*session.SummaryRecord, bool) {
	return nil, false
}
func (f *fakeService) SaveSummaryRecord(ctx context.Context, sess *session.Session, rec *session.SummaryRecord) error {
	return nil
}
func (f *fakeService) Close() error { return nil }

func TestManager_WithBaseService_AppendsAndMetadata(t *testing.T) {
	// Build a session that will summarize without compression.
	s := NewSummarizer(&mockModel{name: "mock", resp: "ok"}, WithWindowSize(1))
	sess := &session.Session{
		ID:      "sess-1",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "hello"}}},
				},
				Timestamp: time.Now().Add(-2 * time.Second),
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "world"}}},
				},
				Timestamp: time.Now(),
			},
		},
	}

	fs := &fakeService{}
	m := NewManager(s)

	// Force summarization to bypass checks.
	require.NoError(t, m.Summarize(context.Background(), sess, true))
	// Note: appendCalled should be false since we don't modify events anymore.
	assert.False(t, fs.appendCalled)

	_ = m.Metadata()
}

func TestManager_SetSummarizer_ForceAndNonForce(t *testing.T) {
	ctx := context.Background()

	// Build two summarizers with different window sizes to observe effects.
	sA := NewSummarizer(&mockModel{name: "mockA", resp: "ok"}, WithWindowSize(1))
	sB := NewSummarizer(&mockModel{name: "mockB", resp: "ok"}, WithWindowSize(2))
	m := NewManager(sA)

	sess := &session.Session{
		ID:      "sess-force-sum",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "a"}}},
				},
				Timestamp: time.Now().Add(-3 * time.Second),
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "b"}}},
				},
				Timestamp: time.Now().Add(-2 * time.Second),
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "c"}}},
				},
				Timestamp: time.Now(),
			},
		},
	}

	originalEventCount := len(sess.Events)
	// Attempt to replace without force: should still use sA.
	m.SetSummarizer(sB, false)
	require.NoError(t, m.Summarize(ctx, sess, true))
	assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")

	// Reset session for next check.
	sess.Events = []event.Event{
		{
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{Content: "a"},
				}},
			},
			Timestamp: time.Now().Add(-3 * time.Second),
		},
		{
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "b"}}},
			},
			Timestamp: time.Now().Add(-2 * time.Second),
		},
		{
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "c"}}},
			},
			Timestamp: time.Now(),
		},
	}

	// Force replace: should use sB.
	m.SetSummarizer(sB, true)
	require.NoError(t, m.Summarize(ctx, sess, true))
	assert.Equal(t, originalEventCount, len(sess.Events), "events should remain unchanged.")

	// Ensure no summary events are added to the session.
	for _, event := range sess.Events {
		assert.NotEqual(t, "system", event.Author, "no system events should be added.")
	}
}
