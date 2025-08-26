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

func TestManager_Summarize_CacheAndCompression(t *testing.T) {
	ctx := context.Background()

	// Summarizer without model uses simple concatenation.
	s := NewSummarizer(WithKeepRecent(2))
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

	require.NoError(t, mgr.Summarize(ctx, sess, true))

	// Expect compressed events: 1 summary + 2 recent.
	assert.Equal(t, 3, len(sess.Events), "compressed events should be 3.")

	// Expect cache entry exists.
	summaryEntry, err := mgr.GetSummary(sess)
	require.NoError(t, err)
	require.NotNil(t, summaryEntry)
	assert.NotEmpty(t, summaryEntry.Summary)
	assert.Equal(t, 5, summaryEntry.OriginalCount)
	assert.Equal(t, 3, summaryEntry.CompressedCount)
}

func TestSummarizer_PromptFormatting_ModelUsed(t *testing.T) {
	ctx := context.Background()

	mock := &mockModel{name: "mock-llm", resp: "- bullet one"}
	customPrompt := "Conversation:\n%s\n\nSummary:"
	s := NewSummarizer(
		WithModel(mock),
		WithPrompt(customPrompt),
		WithKeepRecent(1),
	)

	// Build a session with 3 events so we have 2 old events to summarize when keepRecent=1.
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

	text, err := s.Summarize(ctx, sess, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, text)
	require.NotNil(t, mock.lastRequest)
	require.NotEmpty(t, mock.lastRequest.Messages)
	content := mock.lastRequest.Messages[0].Content
	assert.NotEmpty(t, content)
	assert.True(t, strings.Contains(content, "hello"))
	assert.True(t, strings.Contains(content, "world"))
}

func TestMetadata_IncludesModelInfo(t *testing.T) {
	mock := &mockModel{name: "mock-llm", resp: "ok"}
	s := NewSummarizer(WithModel(mock))
	md := s.Metadata()
	assert.Equal(t, "mock-llm", md[MetadataKeyModelName])
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
func (f *fakeService) Close() error { return nil }

func TestManager_WithAutoSummarize_Disable(t *testing.T) {
	s := NewSummarizer(WithEventThreshold(1))
	m := NewManager(s, WithAutoSummarize(false))
	sess := &session.Session{Events: []event.Event{{Timestamp: time.Now()}}}
	assert.False(t, m.ShouldSummarize(sess))
}

func TestManager_WithBaseService_AppendsAndMetadata(t *testing.T) {
	// Build a session that will summarize and compress.
	s := NewSummarizer(WithKeepRecent(1))
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
	m := NewManager(s, WithBaseService(fs))

	// Force summarization to bypass checks.
	require.NoError(t, m.Summarize(context.Background(), sess, true))
	assert.True(t, fs.appendCalled)

	md := m.Metadata()
	assert.Equal(t, true, md[MetadataKeyBaseServiceConfigured])
}

func TestManager_SetSessionService_ForceAndNonForce(t *testing.T) {
	ctx := context.Background()

	// Use a simple summarizer that always compresses when forced.
	s := NewSummarizer(WithKeepRecent(1))
	m := NewManager(s)

	// Two fake services to distinguish which one receives AppendEvent.
	svcA := &fakeService{}
	svcB := &fakeService{}

	// First set without force, then attempt to replace without force.
	m.SetSessionService(svcA, false)
	m.SetSessionService(svcB, false)

	sess := &session.Session{
		ID:      "sess-force-svc",
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

	require.NoError(t, m.Summarize(ctx, sess, true))
	assert.True(t, svcA.appendCalled, "svcA should be used when non-force replace attempted.")
	assert.False(t, svcB.appendCalled, "svcB should not be used without force.")

	// Now force replace and summarize again.
	svcA.appendCalled = false
	m.SetSessionService(svcB, true)
	require.NoError(t, m.Summarize(ctx, sess, true))
	assert.True(t, svcB.appendCalled, "svcB should be used after force replace.")
}

func TestManager_SetSummarizer_ForceAndNonForce(t *testing.T) {
	ctx := context.Background()

	// Build two summarizers with different keepRecent to observe effects.
	sA := NewSummarizer(WithKeepRecent(1))
	sB := NewSummarizer(WithKeepRecent(2))
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

	// Attempt to replace without force: should still use sA (keepRecent=1 → 1 summary + 1 recent = 2 events).
	m.SetSummarizer(sB, false)
	require.NoError(t, m.Summarize(ctx, sess, true))
	assert.Equal(t, 2, len(sess.Events), "non-force replace should keep sA behavior.")

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

	// Force replace: should use sB (keepRecent=2 → 1 summary + 2 recent = 3 events).
	m.SetSummarizer(sB, true)
	require.NoError(t, m.Summarize(ctx, sess, true))
	assert.Equal(t, 3, len(sess.Events), "force replace should switch to sB behavior.")

	// Ensure the summary event is present at the head and content is non-empty.
	if len(sess.Events) > 0 && sess.Events[0].Response != nil && len(sess.Events[0].Response.Choices) > 0 {
		content := sess.Events[0].Response.Choices[0].Message.Content
		assert.True(t, strings.Contains(strings.ToLower(content), "summary"))
		assert.NotEmpty(t, content)
	}
}
