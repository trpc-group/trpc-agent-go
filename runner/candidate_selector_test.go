//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestRunnerCandidateSelector_CommitsOnlyWinner(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, 3, selector.attemptCount)
	assert.Equal(t, []string{"answer-1"}, responseContents(events))
	assert.Equal(t, 1, runnerCompletionCount(events))
	assert.Equal(t, runnerCompletionInvocationID(events), firstResponseInvocationID(events))
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.NotContains(t, sessionContents(sess), "answer-0")
	assert.Contains(t, sessionContents(sess), "answer-1")
	assert.NotContains(t, sessionContents(sess), "answer-2")
	state, ok := sess.GetState("attempt")
	require.True(t, ok)
	assert.Equal(t, "1", string(state))
}

func TestCandidateSelectorAgent_NewAttemptInvocationMemoryReader(t *testing.T) {
	selectorAgent := &candidateSelectorAgent{
		inner: &candidateScriptAgent{name: "candidate"},
	}

	t.Run("wraps memory service and leaves reader fallback", func(t *testing.T) {
		memSvc := &mockMemoryServiceForAutoMemory{}
		base := agent.NewInvocation(
			agent.WithInvocationAgent(&candidateScriptAgent{name: "base"}),
			agent.WithInvocationMessage(model.NewUserMessage("question")),
			agent.WithInvocationMemoryService(memSvc),
		)

		attempt := selectorAgent.newAttemptInvocation(
			base,
			session.NewSession("app", "user", "attempt"),
			sessioninmemory.NewSessionService(),
		)

		reader, ok := attempt.MemoryService.(*readOnlyMemoryService)
		require.True(t, ok)
		require.Same(t, memSvc, reader.base)
		require.Nil(t, attempt.MemoryReader)
	})

	t.Run("falls back to base memory reader", func(t *testing.T) {
		reader := &mockMemoryReaderIngestor{}
		base := agent.NewInvocation(
			agent.WithInvocationAgent(&candidateScriptAgent{name: "base"}),
			agent.WithInvocationMessage(model.NewUserMessage("question")),
		)
		base.MemoryReader = reader

		attempt := selectorAgent.newAttemptInvocation(
			base,
			session.NewSession("app", "user", "attempt"),
			sessioninmemory.NewSessionService(),
		)

		require.Nil(t, attempt.MemoryService)
		require.Same(t, reader, attempt.MemoryReader)
	})

	t.Run("preserves explicit reader with wrapped memory service", func(t *testing.T) {
		memSvc := &mockMemoryServiceForAutoMemory{}
		explicitReader := &mockMemoryReaderIngestor{}
		base := agent.NewInvocation(
			agent.WithInvocationAgent(&candidateScriptAgent{name: "base"}),
			agent.WithInvocationMessage(model.NewUserMessage("question")),
			agent.WithInvocationMemoryService(memSvc),
		)
		base.MemoryReader = explicitReader

		attempt := selectorAgent.newAttemptInvocation(
			base,
			session.NewSession("app", "user", "attempt"),
			sessioninmemory.NewSessionService(),
		)

		reader, ok := attempt.MemoryService.(*readOnlyMemoryService)
		require.True(t, ok)
		require.Same(t, memSvc, reader.base)
		require.Same(t, explicitReader, attempt.MemoryReader)
	})
}

func TestRunnerCandidateSelector_AttemptSessionReadsOwnOverlay(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateOverlayAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 0}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"overlay-0"}, responseContents(events))
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	assert.NotContains(t, sessionContents(sess), "overlay-1")
}

func TestRunnerCandidateSelector_AppenderDoesNotPolluteSession(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateAppenderAgent{name: "candidate"}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(&fixedCandidateSelector{winner: 0}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	collectRunnerEvents(ch)
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	assert.NotContains(t, sessionContents(sess), "side-1")
	assert.Contains(t, sessionContents(sess), "side-0")
}

func TestRunnerCandidateSelector_StateDeltaOverridesEarlierDirectState(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateStateOrderingAgent{name: "candidate"}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(&fixedCandidateSelector{winner: 0}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	collectRunnerEvents(ch)
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	state, ok := sess.GetState("ordered")
	require.True(t, ok)
	assert.Equal(t, "event-final-0", string(state))
}

func TestRunnerCandidateSelector_UsesDefaultAttempts(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, 2, selector.attemptCount)
	assert.Equal(t, []string{"answer-1"}, responseContents(events))
}

func TestRunnerCandidateSelector_ExplicitZeroAttemptsBypasses(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(0)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, 0, selector.attemptCount)
	assert.Equal(t, []string{"answer-0"}, responseContents(events))
	assert.Equal(t, 1, candidateAgentCallCount(ag))
}

func TestRunnerCandidateSelector_AllowsStreamingRuns(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"), agent.WithStream(true))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"answer-1"}, responseContents(events))
	assert.Equal(t, 3, selector.attemptCount)
	assert.Equal(t, 3, candidateAgentCallCount(ag))
}

func TestRunnerCandidateSelector_ReplaysOnlyWinnerStreamingEvents(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &streamingCandidateAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"), agent.WithStream(true))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"partial-1", "final-1"}, responseContents(events))
	assert.Equal(t, 2, selector.attemptCount)
	assert.Equal(t, 2, streamingCandidateCallCount(ag))
}

func TestRunnerCandidateSelector_RunsAttemptsInParallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessionService := sessioninmemory.NewSessionService()
	release := make(chan struct{})
	ag := &parallelBlockingCandidateAgent{
		name:    "candidate",
		started: make(chan int, 4),
		release: release,
	}
	selector := &fixedCandidateSelector{winner: 0}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(
			selector,
			WithCandidateAttempts(4),
			WithCandidateAttemptParallelEnabled(true),
			WithCandidateAttemptParallelism(2),
		),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	waitForCandidateStart(t, ag.started)
	waitForCandidateStart(t, ag.started)
	assert.Equal(t, 2, ag.maxActiveCount())
	close(release)
	events := collectRunnerEvents(ch)
	assert.Equal(t, 4, selector.attemptCount)
	assert.Equal(t, 2, ag.maxActiveCount())
	assert.Len(t, responseContents(events), 1)
}

func TestCandidateSelectOptions_EffectiveParallelismDefaultsToGOMAXPROCS(t *testing.T) {
	old := runtime.GOMAXPROCS(3)
	defer runtime.GOMAXPROCS(old)
	assert.Equal(t, 3, candidateSelectOptions{}.effectiveParallelism())
	assert.Equal(t, 5, candidateSelectOptions{parallelism: 5}.effectiveParallelism())
}

func TestRunnerCandidateSelector_AllowsAgentsWithTools(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateToolAgent{candidateScriptAgent: candidateScriptAgent{name: "candidate"}}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"answer-1"}, responseContents(events))
	assert.Equal(t, 3, selector.attemptCount)
	assert.Equal(t, 3, candidateAgentCallCount(&ag.candidateScriptAgent))
}

func TestRunnerCandidateSelector_AllowsInvocationToolSurface(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateInvocationToolSurfaceAgent{
		candidateScriptAgent: candidateScriptAgent{name: "candidate"},
	}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"answer-1"}, responseContents(events))
	assert.Equal(t, 3, selector.attemptCount)
	assert.Equal(t, 3, candidateAgentCallCount(&ag.candidateScriptAgent))
}

func TestRunnerCandidateSelector_AllowsRunOptionsWithTools(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(
		ctx,
		"user",
		"session",
		model.NewUserMessage("question"),
		agent.WithAdditionalTools([]tool.Tool{staticTool{name: "runtime"}}),
	)
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"answer-1"}, responseContents(events))
	assert.Equal(t, 3, selector.attemptCount)
	assert.Equal(t, 3, candidateAgentCallCount(ag))
}

func TestRunnerCandidateSelector_ExecutesToolsAndCommitsOnlyWinner(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	modelStub := newCandidateToolCallModel("candidate_lookup", candidateAttemptArgs)
	toolCalls := newCandidateToolCounter()
	lookup := function.NewFunctionTool(
		func(ctx context.Context, input candidateLookupInput) (string, error) {
			toolCalls.Inc()
			return fmt.Sprintf("tool-%d", input.Attempt), nil
		},
		function.WithName("candidate_lookup"),
		function.WithDescription("Returns a candidate-specific result."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{lookup}),
	)
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(&fixedCandidateSelector{winner: 1}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Contains(t, responseContents(events), "final-1")
	assert.NotContains(t, responseContents(events), "final-0")
	assert.Equal(t, 2, toolCalls.Count())
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	sessionText := strings.Join(sessionContents(sess), "\n")
	assert.Contains(t, sessionText, "tool-1")
	assert.Contains(t, sessionText, "final-1")
	assert.NotContains(t, sessionText, "tool-0")
	assert.NotContains(t, sessionText, "final-0")
}

func TestRunnerCandidateSelector_ExecutesToolSetTools(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	modelStub := newCandidateToolCallModel("mcp_lookup", candidateAttemptArgs)
	toolCalls := newCandidateToolCounter()
	lookup := function.NewFunctionTool(
		func(ctx context.Context, input candidateLookupInput) (string, error) {
			toolCalls.Inc()
			return fmt.Sprintf("toolset-%d", input.Attempt), nil
		},
		function.WithName("lookup"),
		function.WithDescription("Returns a candidate-specific toolset result."),
	)
	toolSet := &candidateToolSet{name: "mcp", tools: []tool.Tool{lookup}}
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithToolSets([]tool.ToolSet{toolSet}),
		llmagent.WithRefreshToolSetsOnRun(true),
	)
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(&fixedCandidateSelector{winner: 1}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Contains(t, responseContents(events), "final-1")
	assert.Equal(t, 2, toolCalls.Count())
	assert.GreaterOrEqual(t, toolSet.Calls(), 2)
}

func TestRunnerCandidateSelector_ExecutesSkillLoadAndCommitsOnlyWinner(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	repo, err := agentskill.NewFSRepository(createCandidateSkillRoot(t, "demo-0", "demo-1"))
	require.NoError(t, err)
	modelStub := newCandidateToolCallModel("skill_load", func(attempt int) []byte {
		return []byte(fmt.Sprintf(`{"skill":"demo-%d"}`, attempt))
	})
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithSkills(repo),
	)
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(&fixedCandidateSelector{winner: 1}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Contains(t, responseContents(events), "final-1")
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	_, loserLoaded := sess.GetState(agentskill.LoadedKey("assistant", "demo-0"))
	_, winnerLoaded := sess.GetState(agentskill.LoadedKey("assistant", "demo-1"))
	assert.False(t, loserLoaded)
	assert.True(t, winnerLoaded)
}

func TestRunnerCandidateSelector_MultiTurnReadsCommittedWinnerOnly(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &multiTurnCandidateAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(2)),
	)
	first, err := r.Run(ctx, "user", "session", model.NewUserMessage("first"))
	require.NoError(t, err)
	assert.Equal(t, []string{"turn-1-prior=none"}, responseContents(collectRunnerEvents(first)))
	selector.winner = 0
	second, err := r.Run(ctx, "user", "session", model.NewUserMessage("second"))
	require.NoError(t, err)
	events := collectRunnerEvents(second)
	assert.Equal(t, []string{"turn-2-prior=turn-1-prior=none"}, responseContents(events))
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	text := strings.Join(sessionContents(sess), "\n")
	assert.Contains(t, text, "turn-1-prior=none")
	assert.Contains(t, text, "turn-2-prior=turn-1-prior=none")
	assert.NotContains(t, text, "turn-0-prior=none")
	assert.NotContains(t, text, "turn-3-prior=turn-1-prior=none")
}

func TestRunnerCandidateSelector_RunsAgentModelAndToolCallbacks(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	callbacks := newCandidateCallbackCounters()
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		callbacks.IncBeforeAgent()
		return nil, nil
	})
	agentCallbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		callbacks.IncAfterAgent()
		return nil, nil
	})
	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		callbacks.IncBeforeModel()
		return nil, nil
	})
	modelCallbacks.RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		callbacks.IncAfterModel()
		return nil, nil
	})
	toolCallbacks := tool.NewCallbacks()
	toolCallbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		callbacks.IncBeforeTool()
		return nil, nil
	})
	toolCallbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		callbacks.IncAfterTool()
		return nil, nil
	})
	modelStub := newCandidateToolCallModel("candidate_lookup", candidateAttemptArgs)
	toolCalls := newCandidateToolCounter()
	lookup := function.NewFunctionTool(
		func(ctx context.Context, input candidateLookupInput) (string, error) {
			toolCalls.Inc()
			return fmt.Sprintf("callback-tool-%d", input.Attempt), nil
		},
		function.WithName("candidate_lookup"),
		function.WithDescription("Returns a candidate-specific result."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{lookup}),
		llmagent.WithAgentCallbacks(agentCallbacks),
		llmagent.WithModelCallbacks(modelCallbacks),
		llmagent.WithToolCallbacks(toolCallbacks),
	)
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(&fixedCandidateSelector{winner: 1}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Contains(t, responseContents(events), "final-1")
	assert.Equal(t, 2, toolCalls.Count())
	assert.Equal(t, candidateCallbackCounts{
		beforeAgent: 2,
		afterAgent:  2,
		beforeModel: 4,
		afterModel:  4,
		beforeTool:  2,
		afterTool:   2,
	}, callbacks.Counts())
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	text := strings.Join(sessionContents(sess), "\n")
	assert.Contains(t, text, "callback-tool-1")
	assert.NotContains(t, text, "callback-tool-0")
}

func TestRunnerCandidateSelector_AppliesPluginHooksToCandidateTools(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	modelStub := newCandidateToolCallModel("candidate_lookup", candidateAttemptArgs)
	hooks := &candidateHookPlugin{name: "hooks"}
	lookup := function.NewFunctionTool(
		func(ctx context.Context, input candidateLookupInput) (string, error) {
			return fmt.Sprintf("plugin-tool-%d", input.Attempt), nil
		},
		function.WithName("candidate_lookup"),
		function.WithDescription("Returns a candidate-specific result."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{lookup}),
	)
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithPlugins(hooks),
		WithCandidateSelector(&fixedCandidateSelector{winner: 1}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	collectRunnerEvents(ch)
	assert.Equal(t, 4, hooks.ModelCalls())
	assert.Equal(t, 2, hooks.ToolCalls())
	assert.Contains(t, hooks.EventContents(), "final-1")
	assert.NotContains(t, hooks.EventContents(), "final-0")
}

func TestRunnerCandidateSelector_BypassesGraphCheckpointResume(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(
		ctx,
		"user",
		"session",
		model.NewUserMessage("resume"),
		agent.WithRuntimeState(map[string]any{graph.CfgKeyCheckpointID: "checkpoint"}),
	)
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"answer-0"}, responseContents(events))
	assert.Equal(t, 0, selector.attemptCount)
	assert.Equal(t, 1, candidateAgentCallCount(ag))
}

func TestRunnerCandidateSelector_BypassesExecutionTraceRuns(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"), agent.WithExecutionTraceEnabled(true))
	require.NoError(t, err)
	collectRunnerEvents(ch)
	assert.Equal(t, 0, selector.attemptCount)
	assert.Equal(t, 1, candidateAgentCallCount(ag))
}

func TestRunnerCandidateSelector_AllowsPluginRuns(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &candidateScriptAgent{name: "candidate"}
	selector := &fixedCandidateSelector{winner: 1}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithPlugins(noopPlugin{name: "noop"}),
		WithCandidateSelector(selector, WithCandidateAttempts(3)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	events := collectRunnerEvents(ch)
	assert.Equal(t, []string{"answer-1"}, responseContents(events))
	assert.Equal(t, 3, selector.attemptCount)
	assert.Equal(t, 3, candidateAgentCallCount(ag))
}

func TestAttemptSessionService_DoesNotExposeUnsupportedOptionalInterfaces(t *testing.T) {
	scope := newAttemptSessionService(
		sessioninmemory.NewSessionService(),
		session.NewSession("app", "user", "session"),
	)
	service := scope.Service()
	_, searchable := service.(session.SearchableService)
	_, window := service.(session.WindowService)
	_, track := service.(session.TrackService)
	assert.False(t, searchable)
	assert.False(t, window)
	assert.False(t, track)
}

func TestAttemptSessionService_ListSessionsHidesDeletedBaseSession(t *testing.T) {
	ctx := context.Background()
	base := sessioninmemory.NewSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	baseSession, err := base.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	scope := newAttemptSessionService(base, baseSession)
	require.NoError(t, scope.DeleteSession(ctx, key))
	sessions, err := scope.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "user"})
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

type fixedCandidateSelector struct {
	winner       int
	attemptCount int
}

func (v *fixedCandidateSelector) Select(
	ctx context.Context,
	req *CandidateSelectRequest,
) (int, error) {
	v.attemptCount = len(req.Attempts)
	return v.winner, nil
}

type noopPlugin struct {
	name string
}

func (p noopPlugin) Name() string {
	return p.name
}

func (p noopPlugin) Register(r *plugin.Registry) {
}

type candidateScriptAgent struct {
	name string
	mu   sync.Mutex
	next int
}

func (a *candidateScriptAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	index := a.next
	a.next++
	a.mu.Unlock()
	key := session.Key{
		AppName:   invocation.Session.AppName,
		UserID:    invocation.Session.UserID,
		SessionID: invocation.Session.ID,
	}
	err := invocation.SessionService.UpdateSessionState(ctx, key, session.StateMap{
		"attempt": []byte(fmt.Sprintf("%d", index)),
	})
	if err != nil {
		return nil, err
	}
	out := make(chan *event.Event, 1)
	out <- responseEvent(invocation, a.name, fmt.Sprintf("answer-%d", index))
	close(out)
	return out, nil
}

func candidateAgentCallCount(a *candidateScriptAgent) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next
}

func (a *candidateScriptAgent) Tools() []tool.Tool {
	return nil
}

func (a *candidateScriptAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *candidateScriptAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *candidateScriptAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

type candidateOverlayAgent struct {
	name string
	mu   sync.Mutex
	next int
}

func (a *candidateOverlayAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	index := a.next
	a.next++
	a.mu.Unlock()
	out := make(chan *event.Event, 1)
	out <- responseEvent(invocation, a.name, fmt.Sprintf("overlay-%d", index))
	close(out)
	return out, nil
}

func (a *candidateOverlayAgent) Tools() []tool.Tool {
	return nil
}

func (a *candidateOverlayAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *candidateOverlayAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *candidateOverlayAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

type streamingCandidateAgent struct {
	name string
	mu   sync.Mutex
	next int
}

func (a *streamingCandidateAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	index := a.next
	a.next++
	a.mu.Unlock()
	out := make(chan *event.Event, 2)
	out <- streamingResponseEvent(invocation, a.name, fmt.Sprintf("partial-%d", index), true)
	out <- streamingResponseEvent(invocation, a.name, fmt.Sprintf("final-%d", index), false)
	close(out)
	return out, nil
}

func streamingCandidateCallCount(a *streamingCandidateAgent) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next
}

func (a *streamingCandidateAgent) Tools() []tool.Tool {
	return nil
}

func (a *streamingCandidateAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *streamingCandidateAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *streamingCandidateAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

type parallelBlockingCandidateAgent struct {
	name      string
	started   chan int
	release   <-chan struct{}
	mu        sync.Mutex
	next      int
	active    int
	maxActive int
}

func (a *parallelBlockingCandidateAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	index := a.next
	a.next++
	a.active++
	if a.active > a.maxActive {
		a.maxActive = a.active
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.active--
		a.mu.Unlock()
	}()
	select {
	case a.started <- index:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	out := make(chan *event.Event, 1)
	out <- responseEvent(invocation, a.name, fmt.Sprintf("parallel-%d", index))
	close(out)
	return out, nil
}

func (a *parallelBlockingCandidateAgent) maxActiveCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maxActive
}

func (a *parallelBlockingCandidateAgent) Tools() []tool.Tool {
	return nil
}

func (a *parallelBlockingCandidateAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *parallelBlockingCandidateAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *parallelBlockingCandidateAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

type candidateAppenderAgent struct {
	name string
	mu   sync.Mutex
	next int
}

func (a *candidateAppenderAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	index := a.next
	a.next++
	a.mu.Unlock()
	evt := responseEvent(invocation, authorUser, fmt.Sprintf("side-%d", index))
	attached, err := appender.Invoke(ctx, invocation, evt)
	if err != nil {
		return nil, err
	}
	if !attached {
		return nil, fmt.Errorf("appender is not attached")
	}
	out := make(chan *event.Event, 1)
	out <- responseEvent(invocation, a.name, fmt.Sprintf("answer-%d", index))
	close(out)
	return out, nil
}

func (a *candidateAppenderAgent) Tools() []tool.Tool {
	return nil
}

func (a *candidateAppenderAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *candidateAppenderAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *candidateAppenderAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

type candidateStateOrderingAgent struct {
	name string
	mu   sync.Mutex
	next int
}

func (a *candidateStateOrderingAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	index := a.next
	a.next++
	a.mu.Unlock()
	key := session.Key{
		AppName:   invocation.Session.AppName,
		UserID:    invocation.Session.UserID,
		SessionID: invocation.Session.ID,
	}
	err := invocation.SessionService.UpdateSessionState(ctx, key, session.StateMap{
		"ordered": []byte(fmt.Sprintf("direct-first-%d", index)),
	})
	if err != nil {
		return nil, err
	}
	out := make(chan *event.Event, 1)
	out <- event.NewResponseEvent(
		invocation.InvocationID,
		a.name,
		&model.Response{
			ID:     "state",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{
				{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: "state"}},
			},
		},
		event.WithStateDelta(session.StateMap{
			"ordered": []byte(fmt.Sprintf("event-final-%d", index)),
		}),
	)
	close(out)
	return out, nil
}

func (a *candidateStateOrderingAgent) Tools() []tool.Tool {
	return nil
}

func (a *candidateStateOrderingAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *candidateStateOrderingAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *candidateStateOrderingAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

type candidateToolAgent struct {
	candidateScriptAgent
}

func (a *candidateToolAgent) Tools() []tool.Tool {
	return []tool.Tool{staticTool{name: "unsafe"}}
}

type candidateInvocationToolSurfaceAgent struct {
	candidateScriptAgent
}

func (a *candidateInvocationToolSurfaceAgent) InvocationToolSurface(
	ctx context.Context,
	invocation *agent.Invocation,
) ([]tool.Tool, map[string]bool) {
	return []tool.Tool{staticTool{name: "runtime-surface"}}, map[string]bool{"runtime-surface": true}
}

type multiTurnCandidateAgent struct {
	name string
	mu   sync.Mutex
	next int
}

func (a *multiTurnCandidateAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	index := a.next
	a.next++
	a.mu.Unlock()
	prior := "none"
	for _, content := range sessionContents(invocation.Session) {
		if strings.HasPrefix(content, "turn-") {
			prior = content
		}
	}
	out := make(chan *event.Event, 1)
	out <- responseEvent(invocation, a.name, fmt.Sprintf("turn-%d-prior=%s", index, prior))
	close(out)
	return out, nil
}

func (a *multiTurnCandidateAgent) Tools() []tool.Tool {
	return nil
}

func (a *multiTurnCandidateAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *multiTurnCandidateAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *multiTurnCandidateAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

type staticTool struct {
	name string
}

func (t staticTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func responseEvent(
	invocation *agent.Invocation,
	author string,
	content string,
) *event.Event {
	return event.NewResponseEvent(
		invocation.InvocationID,
		author,
		&model.Response{
			ID:     content,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{
				{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: content}},
			},
		},
	)
}

func streamingResponseEvent(
	invocation *agent.Invocation,
	author string,
	content string,
	partial bool,
) *event.Event {
	return event.NewResponseEvent(
		invocation.InvocationID,
		author,
		&model.Response{
			ID:        content,
			Object:    model.ObjectTypeChatCompletionChunk,
			Done:      !partial,
			IsPartial: partial,
			Choices: []model.Choice{
				{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: content}},
			},
		},
	)
}

func collectRunnerEvents(ch <-chan *event.Event) []*event.Event {
	events := make([]*event.Event, 0)
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func responseContents(events []*event.Event) []string {
	contents := make([]string, 0)
	for _, evt := range events {
		if evt == nil || evt.Response == nil || evt.IsRunnerCompletion() || len(evt.Choices) == 0 {
			continue
		}
		content := evt.Choices[0].Message.Content
		if content != "" {
			contents = append(contents, content)
		}
	}
	return contents
}

func runnerCompletionCount(events []*event.Event) int {
	count := 0
	for _, evt := range events {
		if evt != nil && evt.IsRunnerCompletion() {
			count++
		}
	}
	return count
}

func runnerCompletionInvocationID(events []*event.Event) string {
	for _, evt := range events {
		if evt != nil && evt.IsRunnerCompletion() {
			return evt.InvocationID
		}
	}
	return ""
}

func firstResponseInvocationID(events []*event.Event) string {
	for _, evt := range events {
		if evt == nil || evt.Response == nil || evt.IsRunnerCompletion() || len(evt.Choices) == 0 {
			continue
		}
		return evt.InvocationID
	}
	return ""
}

func sessionContents(sess *session.Session) []string {
	contents := make([]string, 0)
	for _, evt := range sess.GetEvents() {
		if evt.Response == nil || len(evt.Choices) == 0 {
			continue
		}
		content := evt.Choices[0].Message.Content
		if content != "" {
			contents = append(contents, content)
		}
	}
	return contents
}

type candidateLookupInput struct {
	Attempt int `json:"attempt"`
}

type candidateToolCallModel struct {
	toolName string
	args     func(int) []byte
	mu       sync.Mutex
	next     int
	attempts map[string]int
}

func newCandidateToolCallModel(toolName string, args func(int) []byte) *candidateToolCallModel {
	return &candidateToolCallModel{
		toolName: toolName,
		args:     args,
		attempts: make(map[string]int),
	}
}

func (m *candidateToolCallModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	attempt := m.attemptIndex(ctx)
	argsFn := m.args
	if argsFn == nil {
		argsFn = candidateAttemptArgs
	}
	callArgs := append([]byte(nil), argsFn(attempt)...)
	var response *model.Response
	if requestHasLastToolResult(request) {
		response = candidateFinalResponse(attempt)
	} else {
		response = candidateToolCallResponse(attempt, m.toolName, callArgs)
	}
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func (m *candidateToolCallModel) Info() model.Info {
	return model.Info{Name: "candidate-tool-call-model"}
}

func (m *candidateToolCallModel) attemptIndex(ctx context.Context) int {
	inv, ok := agent.InvocationFromContext(ctx)
	invocationID := ""
	if ok && inv != nil {
		invocationID = inv.InvocationID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if invocationID == "" {
		invocationID = fmt.Sprintf("candidate-%d", m.next)
	}
	if attempt, ok := m.attempts[invocationID]; ok {
		return attempt
	}
	attempt := m.next
	m.next++
	m.attempts[invocationID] = attempt
	return attempt
}

func candidateAttemptArgs(attempt int) []byte {
	return []byte(fmt.Sprintf(`{"attempt":%d}`, attempt))
}

func candidateToolCallResponse(attempt int, toolName string, args []byte) *model.Response {
	return &model.Response{
		ID:     fmt.Sprintf("tool-call-%d", attempt),
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   fmt.Sprintf("call-%d", attempt),
					Function: model.FunctionDefinitionParam{
						Name:      toolName,
						Arguments: args,
					},
				}},
			},
		}},
	}
}

func candidateFinalResponse(attempt int) *model.Response {
	return &model.Response{
		ID:     fmt.Sprintf("final-%d", attempt),
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(fmt.Sprintf("final-%d", attempt)),
		}},
	}
}

func requestHasLastToolResult(request *model.Request) bool {
	if request == nil || len(request.Messages) == 0 {
		return false
	}
	last := request.Messages[len(request.Messages)-1]
	return last.Role == model.RoleTool
}

type candidateToolCounter struct {
	mu    sync.Mutex
	calls int
}

func newCandidateToolCounter() *candidateToolCounter {
	return &candidateToolCounter{}
}

func (c *candidateToolCounter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
}

func (c *candidateToolCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type candidateCallbackCounts struct {
	beforeAgent int
	afterAgent  int
	beforeModel int
	afterModel  int
	beforeTool  int
	afterTool   int
}

type candidateCallbackCounters struct {
	mu     sync.Mutex
	counts candidateCallbackCounts
}

func newCandidateCallbackCounters() *candidateCallbackCounters {
	return &candidateCallbackCounters{}
}

func (c *candidateCallbackCounters) IncBeforeAgent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.beforeAgent++
}

func (c *candidateCallbackCounters) IncAfterAgent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.afterAgent++
}

func (c *candidateCallbackCounters) IncBeforeModel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.beforeModel++
}

func (c *candidateCallbackCounters) IncAfterModel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.afterModel++
}

func (c *candidateCallbackCounters) IncBeforeTool() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.beforeTool++
}

func (c *candidateCallbackCounters) IncAfterTool() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.afterTool++
}

func (c *candidateCallbackCounters) Counts() candidateCallbackCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts
}

type candidateToolSet struct {
	name  string
	tools []tool.Tool
	mu    sync.Mutex
	calls int
}

func (s *candidateToolSet) Tools(ctx context.Context) []tool.Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.tools
}

func (s *candidateToolSet) Close() error {
	return nil
}

func (s *candidateToolSet) Name() string {
	return s.name
}

func (s *candidateToolSet) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type candidateHookPlugin struct {
	name          string
	mu            sync.Mutex
	modelCalls    int
	toolCalls     int
	eventContents []string
}

func (p *candidateHookPlugin) Name() string {
	return p.name
}

func (p *candidateHookPlugin) Register(r *plugin.Registry) {
	r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.modelCalls++
		return nil, nil
	})
	r.BeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.toolCalls++
		return nil, nil
	})
	r.OnEvent(func(ctx context.Context, invocation *agent.Invocation, evt *event.Event) (*event.Event, error) {
		if evt == nil || evt.Response == nil || len(evt.Choices) == 0 {
			return evt, nil
		}
		content := evt.Choices[0].Message.Content
		if content == "" {
			return evt, nil
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.eventContents = append(p.eventContents, content)
		return evt, nil
	})
}

func (p *candidateHookPlugin) ModelCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.modelCalls
}

func (p *candidateHookPlugin) ToolCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.toolCalls
}

func (p *candidateHookPlugin) EventContents() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.eventContents...)
}

func createCandidateSkillRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		dir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		data := fmt.Sprintf("---\nname: %s\ndescription: demo\n---\n# %s\n", name, name)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(data), 0o644))
	}
	return root
}

func TestRunnerCandidateSelector_RequiresCompletionDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	ag := &requiresCompletionAgent{name: "candidate"}
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessionService),
		WithCandidateSelector(&fixedCandidateSelector{winner: 0}, WithCandidateAttempts(2)),
	)
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("question"))
	require.NoError(t, err)
	select {
	case <-drainDone(ch):
	case <-time.After(time.Second):
		t.Fatal("candidate selector run blocked on RequiresCompletion")
	}
}

type requiresCompletionAgent struct {
	name string
}

func (a *requiresCompletionAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, 1)
	evt := responseEvent(invocation, a.name, "done")
	evt.RequiresCompletion = true
	out <- evt
	close(out)
	return out, nil
}

func (a *requiresCompletionAgent) Tools() []tool.Tool {
	return nil
}

func (a *requiresCompletionAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *requiresCompletionAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *requiresCompletionAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func drainDone(ch <-chan *event.Event) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	return done
}

func waitForCandidateStart(t *testing.T, started <-chan int) int {
	t.Helper()
	select {
	case index := <-started:
		return index
	case <-time.After(time.Second):
		require.FailNow(t, "candidate attempt did not start")
		return 0
	}
}
