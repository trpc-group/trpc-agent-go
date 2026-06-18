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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

func TestCreateSessionSummary_PersistsViaUpsert(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.summarizer = &stubSummarizer{text: "hello"}
	})

	sess := newSessionForTest("app", "u", "s")
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", true))

	// CreateSessionSummary writes via UpdateOne + upsert on session_summaries.
	var sawUpsert bool
	for _, op := range mc.recorded() {
		if op.name == "UpdateOne" && op.coll == "session_summaries" {
			sawUpsert = true
			upd := op.update.(bson.M)
			assert.Contains(t, upd, "$set")
			assert.Contains(t, upd, "$setOnInsert")
		}
	}
	assert.True(t, sawUpsert, "expected an UpdateOne(upsert) on session_summaries")
}

func TestCreateSessionSummary_RespectsAllowlist(t *testing.T) {
	mc := &mockClient{}
	s := newServiceForTest(t, mc, func(o *ServiceOpts) {
		o.summarizer = &stubSummarizer{text: "x"}
		o.summaryFilterAllowlist = []string{"only-this"}
	})

	sess := newSessionForTest("app", "u", "s")
	// "blocked" is not in the allowlist; CreateSessionSummary returns nil
	// without touching the client.
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "blocked", false))
	assert.Empty(t, mc.recorded(), "filter-key not in allowlist must short-circuit")
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
