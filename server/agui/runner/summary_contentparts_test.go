//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	baserunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type numberedAnswerModel struct {
	mu       sync.Mutex
	requests [][]model.Message
}

func (m *numberedAnswerModel) Info() model.Info {
	return model.Info{Name: "numbered-answer-model"}
}

func (m *numberedAnswerModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, append([]model.Message(nil), req.Messages...))
	call := len(m.requests)
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:     fmt.Sprintf("rsp-%d", call),
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(fmt.Sprintf("answer %d", call)),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *numberedAnswerModel) recordedRequests() [][]model.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]model.Message(nil), m.requests...)
}

// summaryPromptModel is called from the session service's async summary
// workers, so recorded prompts are guarded by a mutex.
type summaryPromptModel struct {
	mu      sync.Mutex
	prompts []string
}

func (m *summaryPromptModel) Info() model.Info {
	return model.Info{Name: "summary-prompt-model"}
}

func (m *summaryPromptModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	var prompt string
	for _, msg := range req.Messages {
		if msg.Role == model.RoleUser {
			prompt = msg.Content
		}
	}
	m.mu.Lock()
	m.prompts = append(m.prompts, prompt)
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("multimodal summary"),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *summaryPromptModel) recordedPrompts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.prompts...)
}

// skipLatestUserRound keeps the round started by the latest user message out
// of the summary, so only earlier completed rounds are summarized.
func skipLatestUserRound(events []agentevent.Event) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Author == "user" {
			return len(events) - i
		}
	}
	return 0
}

func TestRunSummarizesArrayInputUserText(t *testing.T) {
	ctx := context.Background()
	// Async summary jobs read the invocation of the run that enqueued them, so
	// they wait for that run to finish before selecting events.
	var runMu sync.RWMutex
	summaryModel := &summaryPromptModel{}
	sessionService := inmemory.NewSessionService(inmemory.WithSummarizer(
		summary.NewSummarizer(
			summaryModel,
			summary.WithSkipRecentContext(
				func(_ context.Context, events []agentevent.Event) int {
					runMu.RLock()
					defer runMu.RUnlock()
					return skipLatestUserRound(events)
				},
			),
		),
	))
	t.Cleanup(func() { require.NoError(t, sessionService.Close()) })
	agentModel := &numberedAnswerModel{}
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(agentModel),
		llmagent.WithAddSessionSummary(true),
	)
	base := baserunner.NewRunner(
		"app",
		ag,
		baserunner.WithSessionService(sessionService),
	)
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	r := New(base)
	run := func(runID string, content any) {
		runMu.Lock()
		defer runMu.Unlock()
		eventsCh, err := r.Run(ctx, &adapter.RunAgentInput{
			ThreadID: "thread",
			RunID:    runID,
			Messages: []types.Message{{
				ID:      runID + "-user",
				Role:    types.RoleUser,
				Content: content,
			}},
		})
		require.NoError(t, err)
		collectEvents(t, eventsCh)
	}
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}

	run("run-1", []types.InputContent{
		{Type: types.InputContentTypeText, Text: "describe the revenue chart"},
		{
			Type: types.InputContentTypeImage,
			Source: &types.InputContentSource{
				Type:  types.InputContentSourceTypeURL,
				Value: "https://example.com/revenue-chart.png",
			},
		},
	})
	sess, err := sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotEmpty(t, sess.Events)
	require.Equal(t, "user", sess.Events[0].Author)
	stored := sess.Events[0].Choices[0].Message
	require.Empty(t, stored.Content)
	var storedTexts []string
	for _, part := range stored.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			storedTexts = append(storedTexts, *part.Text)
		}
	}
	require.Equal(t, []string{"describe the revenue chart"}, storedTexts)
	require.Len(t, stored.ContentParts, 2)

	run("run-2", "what about next quarter?")

	require.Eventually(t, func() bool {
		sess, err := sessionService.GetSession(ctx, key)
		if err != nil || sess == nil {
			return false
		}
		_, ok := sessionService.GetSessionSummaryText(ctx, sess)
		return ok
	}, 5*time.Second, 10*time.Millisecond)
	sess, err = sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	text, ok := sessionService.GetSessionSummaryText(ctx, sess)
	require.True(t, ok)
	require.Equal(t, "multimodal summary", text)

	// Snapshot before the next run, whose asynchronous summary covers the
	// second round.
	prompts := summaryModel.recordedPrompts()
	require.NotEmpty(t, prompts)
	for _, prompt := range prompts {
		require.Contains(t, prompt, "user: describe the revenue chart")
		require.Contains(t, prompt, "assistant: answer 1")
		require.NotContains(t, prompt, "what about next quarter?")
	}

	run("run-3", "and the year after?")

	requests := agentModel.recordedRequests()
	require.Len(t, requests, 3)
	next := requests[2]
	require.NotEmpty(t, next)
	require.Equal(t, model.RoleSystem, next[0].Role)
	require.Contains(
		t,
		next[0].Content,
		"<summary_of_previous_interactions>\nmultimodal summary\n"+
			"</summary_of_previous_interactions>",
	)
	for _, msg := range next[1:] {
		require.NotContains(t, msg.Content, "multimodal summary")
		require.NotContains(t, msg.Content, "describe the revenue chart")
		require.Empty(t, msg.ContentParts)
	}
	latest := next[len(next)-1]
	require.Equal(t, model.RoleUser, latest.Role)
	require.Equal(t, "and the year after?", latest.Content)
}
