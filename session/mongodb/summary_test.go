//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
)

// stubSummarizer is the smallest viable session.summary.SessionSummarizer
// implementation. It just claims the session is summarizable and returns a
// fixed text — enough to exercise CreateSessionSummary's persist path.
type stubSummarizer struct{ text string }

func (s *stubSummarizer) ShouldSummarize(_ *session.Session) bool { return true }
func (s *stubSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	return s.text, nil
}
func (s *stubSummarizer) SetPrompt(_ string)       {}
func (s *stubSummarizer) SetModel(_ model.Model)   {}
func (s *stubSummarizer) Metadata() map[string]any { return nil }

type configurableSummarizer struct {
	should bool
	text   string
	err    error
}

func (s *configurableSummarizer) ShouldSummarize(_ *session.Session) bool { return s.should }
func (s *configurableSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	return s.text, s.err
}
func (s *configurableSummarizer) SetPrompt(_ string)       {}
func (s *configurableSummarizer) SetModel(_ model.Model)   {}
func (s *configurableSummarizer) Metadata() map[string]any { return nil }

func TestCreateSessionSummary_PersistsViaUpsert(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "hello"}
	})

	sess := newSessionForTest("app", "u", "s")
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", true))

	// CreateSessionSummary writes via UpdateOne + upsert on session_summaries.
	var sawUpsert bool
	for _, op := range mc.recorded() {
		if op.name == "UpdateOne" && op.coll == "session_summaries" {
			sawUpsert = true
			filter := op.filter.(bson.M)
			assert.Contains(t, filter, "$or")
			upd := op.update.(bson.M)
			assert.Contains(t, upd, "$set")
			assert.Contains(t, upd, "$setOnInsert")
			set := upd["$set"].(bson.M)
			assert.NotContains(t, set, "expires_at")
			unset := upd["$unset"].(bson.M)
			assert.Contains(t, unset, "expires_at")
		}
	}
	assert.True(t, sawUpsert, "expected an UpdateOne(upsert) on session_summaries")
}

func TestCreateSessionSummary_DuplicateKeyIsNoop(t *testing.T) {
	mc := &mockClient{
		updateOneFn: func(_, _ any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, mongo.WriteException{WriteErrors: []mongo.WriteError{{Code: 11000}}}
		},
	}
	s := newServiceForTest(t, mc, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "hello"}
	})

	sess := newSessionForTest("app", "u", "s")
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", true))
}

func TestCreateSessionSummary_PropagatesNonDuplicateUpsertError(t *testing.T) {
	want := errors.New("write failed")
	mc := &mockClient{
		updateOneFn: func(_, _ any, _ []*options.UpdateOptions) (*mongo.UpdateResult, error) {
			return nil, want
		},
	}
	s := newServiceForTest(t, mc, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "hello"}
	})

	sess := newSessionForTest("app", "u", "s")
	err := s.CreateSessionSummary(context.Background(), sess, "", true)
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}

func TestCreateSessionSummary_DoesNotSetExpiresAtWhenSessionTTLConfigured(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "hello"}
		o.sessionTTL = time.Hour
	})

	sess := newSessionForTest("app", "u", "s")
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", true))

	upd := mc.recorded()[0].update.(bson.M)
	set := upd["$set"].(bson.M)
	assert.NotContains(t, set, "expires_at")
	unset := upd["$unset"].(bson.M)
	assert.Contains(t, unset, "expires_at")
}

func TestCreateSessionSummary_RespectsAllowlist(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "x"}
		o.summaryFilterAllowlist = []string{"only-this"}
	})

	sess := newSessionForTest("app", "u", "s")
	// "blocked" is not in the allowlist; CreateSessionSummary returns nil
	// without touching the client.
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "blocked", false))
	assert.Empty(t, mc.recorded(), "filter-key not in allowlist must short-circuit")
}

func TestCreateSessionSummary_SkipAndSummarizeErrorShortCircuit(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		mc := &mockClient{}
		s := newServiceForTest(t, mc, func(o *serviceOpts) {
			o.summarizer = &configurableSummarizer{should: false, text: "unused"}
		})
		sess := newSessionForTest("app", "u", "s")
		require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", false))
		assert.Empty(t, mc.recorded())
	})

	t.Run("error", func(t *testing.T) {
		want := errors.New("summarize failed")
		mc := &mockClient{}
		s := newServiceForTest(t, mc, func(o *serviceOpts) {
			o.summarizer = &configurableSummarizer{should: true, err: want}
		})
		sess := newSessionForTest("app", "u", "s")
		err := s.CreateSessionSummary(context.Background(), sess, "", true)
		require.Error(t, err)
		assert.ErrorIs(t, err, want)
		assert.Empty(t, mc.recorded())
	})
}

func TestSummaryMethodsValidateSessionInput(t *testing.T) {
	s := newServiceForTest(t, &mockClient{}, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "unused"}
	})
	ctx := context.Background()

	err := s.CreateSessionSummary(ctx, nil, "", false)
	require.ErrorIs(t, err, session.ErrNilSession)

	bad := newSessionForTest("", "u", "s")
	err = s.CreateSessionSummary(ctx, bad, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check session key failed")

	err = s.EnqueueSummaryJob(ctx, nil, "", false)
	require.ErrorIs(t, err, session.ErrNilSession)

	err = s.EnqueueSummaryJob(ctx, bad, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check session key failed")

	text, ok := s.GetSessionSummaryText(ctx, nil)
	assert.False(t, ok)
	assert.Empty(t, text)

	text, ok = s.GetSessionSummaryText(ctx, bad)
	assert.False(t, ok)
	assert.Empty(t, text)
}

func TestGetSessionSummaryText_FromInMemorySummariesFirst(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc)

	sess := newSessionForTest("app", "u", "s")
	sess.Summaries = map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   "live",
			UpdatedAt: time.Now(),
		},
	}
	got, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "live", got)
	// In-memory hit means we never call the database.
	assert.Empty(t, mc.recorded())
}

func TestGetSessionSummaryText_FallsBackToFullSession(t *testing.T) {
	calls := 0
	mc := &mockClient{
		findOneFn: func(_ any) *mongo.SingleResult {
			calls++
			if calls == 1 {
				// First lookup (requested filterKey) returns nothing.
				return mongo.NewSingleResultFromDocument(bson.D{}, mongo.ErrNoDocuments, nil)
			}
			// Second lookup (full-session fallback) returns a summary.
			doc := sessionSummaryDoc{
				AppName:   "app",
				UserID:    "u",
				SessionID: "s",
				FilterKey: session.SummaryFilterKeyAllContents,
				Summary:   []byte(`{"summary":"fallback"}`),
				UpdatedAt: time.Now(),
			}
			return mongo.NewSingleResultFromDocument(doc, nil, nil)
		},
	}
	s := newServiceForTest(t, mc)
	sess := newSessionForTest("app", "u", "s")

	got, ok := s.GetSessionSummaryText(context.Background(), sess,
		session.WithSummaryFilterKey("specific-branch"))
	require.True(t, ok)
	assert.Equal(t, "fallback", got)
	assert.Equal(t, 2, calls)
}

func TestGetSessionSummaryText_IgnoresInvalidOrEmptyPersistedSummary(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		mc := &mockClient{
			findOneFn: func(_ any) *mongo.SingleResult {
				return mongo.NewSingleResultFromDocument(sessionSummaryDoc{
					AppName:   "app",
					UserID:    "u",
					SessionID: "s",
					FilterKey: session.SummaryFilterKeyAllContents,
					Summary:   []byte(`{`),
					UpdatedAt: time.Now(),
				}, nil, nil)
			},
		}
		s := newServiceForTest(t, mc)
		got, ok := s.GetSessionSummaryText(context.Background(), newSessionForTest("app", "u", "s"))
		assert.False(t, ok)
		assert.Empty(t, got)
	})

	t.Run("empty summary", func(t *testing.T) {
		mc := &mockClient{
			findOneFn: func(_ any) *mongo.SingleResult {
				return mongo.NewSingleResultFromDocument(sessionSummaryDoc{
					AppName:   "app",
					UserID:    "u",
					SessionID: "s",
					FilterKey: session.SummaryFilterKeyAllContents,
					Summary:   []byte(`{"summary":""}`),
					UpdatedAt: time.Now(),
				}, nil, nil)
			},
		}
		s := newServiceForTest(t, mc)
		got, ok := s.GetSessionSummaryText(context.Background(), newSessionForTest("app", "u", "s"))
		assert.False(t, ok)
		assert.Empty(t, got)
	})
}

func TestEnqueueSummaryJob_FallsBackWithoutWorker(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "queued"}
	})

	sess := newSessionForTest("app", "u", "s")
	sess.Events = []event.Event{
		windowEventForTest("u1", model.RoleUser, "hello", time.Now()),
	}
	require.NoError(t, s.EnqueueSummaryJob(context.Background(), sess, "", true))

	got, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "queued", got)
	var sawSummaryUpsert bool
	for _, op := range mc.recorded() {
		if op.name == "UpdateOne" && op.coll == "session_summaries" {
			sawSummaryUpsert = true
			break
		}
	}
	assert.True(t, sawSummaryUpsert)
}

func TestEnqueueSummaryJob_RejectsNilSessionWithSummarizer(t *testing.T) {
	s := newServiceForTest(t, &mockClient{}, func(o *serviceOpts) {
		o.summarizer = &stubSummarizer{text: "x"}
	})

	require.ErrorIs(t, s.EnqueueSummaryJob(context.Background(), nil, "", false), session.ErrNilSession)
}

func TestNewService_WithSummarizerStartsAsyncWorker(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(oldBuilder)

	mc := &mockClient{}
	storage.SetClientBuilder(func(context.Context, ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mc, nil
	})

	s, err := NewService(
		WithMongoClientURI("mongodb://example"),
		WithSkipDBInit(true),
		WithSummarizer(&stubSummarizer{text: "async"}),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(2),
		WithSummaryJobTimeout(10*time.Millisecond),
	)
	require.NoError(t, err)
	require.NotNil(t, s.asyncWorker)
	require.NoError(t, s.Close())
}
