package summary

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestLiveSingleFilterCascadeDoesNotDumpRawEvents(t *testing.T) {
	llm := newLiveSummaryModel(t)
	now := time.Now()
	rawMarker := strings.Repeat("raw-tool-result ", 8000)
	rawEvent := makeEvent(rawMarker, now, "agent_skill")
	sess := &session.Session{
		ID:        "live-session",
		AppName:   "live-app",
		UserID:    "live-user",
		Events:    []event.Event{rawEvent},
		Summaries: make(map[string]*session.Summary),
	}
	view := &summaryview.View{
		SessionID:     sess.ID,
		FilterKey:     "agent_skill",
		RequestTokens: 80,
		Bound:         true,
		Items: []summaryview.Item{{
			Message:        model.NewUserMessage("short projected"),
			EffectiveEvent: rawEvent,
			RequestIndex:   0,
			Boundary: summaryview.Boundary{
				EventID:   rawEvent.ID,
				Timestamp: rawEvent.Timestamp,
			},
		}},
	}
	capture := &countingLiveModel{inner: llm}
	summarizer := summary.NewSummarizer(
		capture,
		summary.WithTokenThreshold(2000),
		summary.WithCacheSafeForking(true),
	)
	ctx, cancel := context.WithTimeout(
		summaryview.ContextWithView(context.Background(), view),
		45*time.Second,
	)
	defer cancel()

	err := CreateSessionSummaryWithCascade(
		ctx,
		sess,
		"agent_skill",
		false,
		NewSummaryDispatchPolicy(nil, true),
		func(ctx context.Context, _ *session.Session, filterKey string, force bool) error {
			_, err := SummarizeSession(ctx, summarizer, sess, filterKey, force)
			return err
		},
	)
	require.NoError(t, err)
	require.Empty(t, capture.contents)
}

func TestLiveSingleFilterCacheSafeForkSummarizesParent(t *testing.T) {
	llm := newLiveSummaryModel(t)
	now := time.Now()
	sess := &session.Session{
		ID:      "live-fork-session",
		AppName: "live-app",
		UserID:  "live-user",
		Events: []event.Event{
			makeEvent("please summarize this short turn", now, "agent_skill"),
		},
		Summaries: make(map[string]*session.Summary),
	}
	parent := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful assistant."),
			model.NewUserMessage("please summarize this short turn"),
		},
	}
	capture := &countingLiveModel{inner: llm}
	summarizer := summary.NewSummarizer(
		capture,
		summary.WithCacheSafeForking(true),
	)
	ctx, cancel := context.WithTimeout(
		summary.ContextWithCacheSafeForkRequest(context.Background(), parent),
		45*time.Second,
	)
	defer cancel()

	err := CreateSessionSummaryWithCascade(
		ctx,
		sess,
		"agent_skill",
		true,
		NewSummaryDispatchPolicy(nil, true),
		func(ctx context.Context, _ *session.Session, filterKey string, force bool) error {
			_, err := SummarizeSession(ctx, summarizer, sess, filterKey, force)
			return err
		},
	)
	require.NoError(t, err)
	require.Len(t, capture.contents, 1)
	require.Contains(t, capture.contents[0], "please summarize this short turn")
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	require.NotNil(t, sess.Summaries["agent_skill"])
	require.NotEmpty(t, sess.Summaries["agent_skill"].Summary)
	require.NotNil(t, sess.Summaries[session.SummaryFilterKeyAllContents])
	require.Equal(
		t,
		sess.Summaries["agent_skill"].Summary,
		sess.Summaries[session.SummaryFilterKeyAllContents].Summary,
	)
}

func newLiveSummaryModel(t *testing.T) model.Model {
	t.Helper()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping live summary smoke test")
	}
	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}
	opts := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return openai.New(modelName, opts...)
}

type countingLiveModel struct {
	inner    model.Model
	mu       sync.Mutex
	contents []string
}

func (m *countingLiveModel) Info() model.Info {
	return m.inner.Info()
}

func (m *countingLiveModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	var b strings.Builder
	for _, msg := range req.Messages {
		b.WriteString(msg.Content)
	}
	m.mu.Lock()
	m.contents = append(m.contents, b.String())
	m.mu.Unlock()
	return m.inner.GenerateContent(ctx, req)
}
