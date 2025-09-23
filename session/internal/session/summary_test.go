package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type fakeSummarizer struct {
	allow bool
	out   string
}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool { return f.allow }
func (f *fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return f.out, nil
}
func (f *fakeSummarizer) Metadata() map[string]any { return map[string]any{} }

func makeEvent(content string, ts time.Time, branch string) event.Event {
	return event.Event{
		Branch:    branch,
		FilterKey: branch,
		Timestamp: ts,
		Response:  &model.Response{Choices: []model.Choice{{Message: model.Message{Content: content}}}},
	}
}

func TestSummarizeAndPersist_FilteredBranch_RespectsDeltaAndShould(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("old", now.Add(-2*time.Minute), "b1"),
		makeEvent("new", now.Add(-1*time.Second), "b1"),
	}

	// allow=false and force=false should skip.
	s := &fakeSummarizer{allow: false, out: "sum"}
	var wroteKey, wroteText string
	err := SummarizeAndPersist(context.Background(), s, base, "b1", false,
		func(key string) (string, time.Time) { return "prev", now.Add(-time.Hour) },
		func(key string, text string) error { wroteKey, wroteText = key, text; return nil },
	)
	require.NoError(t, err)
	require.Equal(t, "", wroteKey)

	// allow=true should write.
	s.allow = true
	wroteKey, wroteText = "", ""
	err = SummarizeAndPersist(context.Background(), s, base, "b1", false,
		func(key string) (string, time.Time) { return "prev", now.Add(-time.Hour) },
		func(key string, text string) error { wroteKey, wroteText = key, text; return nil },
	)
	require.NoError(t, err)
	require.Equal(t, "b1", wroteKey)
	require.Equal(t, "sum", wroteText)

	// force=true should write even when ShouldSummarize=false.
	s.allow = false
	wroteKey, wroteText = "", ""
	err = SummarizeAndPersist(context.Background(), s, base, "b1", true,
		func(key string) (string, time.Time) { return "", time.Time{} },
		func(key string, text string) error { wroteKey, wroteText = key, text; return nil },
	)
	require.NoError(t, err)
	require.Equal(t, "b1", wroteKey)
	require.Equal(t, "sum", wroteText)
}

func TestSummarizeAndPersist_AllBranches_MultipleWrites(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-1*time.Minute), "b1"),
		makeEvent("e2", now.Add(-30*time.Second), "b2"),
	}
	s := &fakeSummarizer{allow: true, out: "sum"}
	writes := make(map[string]string)

	err := SummarizeAndPersist(context.Background(), s, base, "", false,
		func(key string) (string, time.Time) { return "", time.Time{} },
		func(key string, text string) error { writes[key] = text; return nil },
	)
	require.NoError(t, err)
	require.Equal(t, 2, len(writes))
	require.Equal(t, "sum", writes["b1"])
	require.Equal(t, "sum", writes["b2"])
}
