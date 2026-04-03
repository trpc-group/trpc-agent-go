//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type summaryInjectingService struct {
	session.Service
	mu    sync.Mutex
	calls int
}

func (s *summaryInjectingService) CreateSessionSummary(
	_ context.Context,
	sess *session.Session,
	filterKey string,
	_ bool,
) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	sess.SummariesMu.Lock()
	defer sess.SummariesMu.Unlock()
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*session.Summary)
	}
	sess.Summaries[filterKey] = &session.Summary{
		Summary:   "compressed history",
		UpdatedAt: time.Now().Add(time.Minute),
	}
	return nil
}

func (s *summaryInjectingService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type summaryFailingService struct {
	session.Service
	mu    sync.Mutex
	calls int
	err   error
}

func (s *summaryFailingService) CreateSessionSummary(
	context.Context,
	*session.Session,
	string,
	bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *summaryFailingService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type summaryPartialFailureService struct {
	session.Service
	mu    sync.Mutex
	calls int
	err   error
}

func (s *summaryPartialFailureService) CreateSessionSummary(
	_ context.Context,
	sess *session.Session,
	filterKey string,
	_ bool,
) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	sess.SummariesMu.Lock()
	defer sess.SummariesMu.Unlock()
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*session.Summary)
	}
	sess.Summaries[filterKey] = &session.Summary{
		Summary:   "compressed despite persistence failure",
		UpdatedAt: time.Now().Add(time.Minute),
	}
	return s.err
}

func (s *summaryPartialFailureService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type countingRequestProcessor struct {
	mu       sync.Mutex
	calls    int
	messages []model.Message
}

func (p *countingRequestProcessor) ProcessRequest(
	_ context.Context,
	_ *agent.Invocation,
	req *model.Request,
	_ chan<- *event.Event,
) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if req == nil {
		return
	}
	req.Messages = append(req.Messages, p.messages...)
}

func (p *countingRequestProcessor) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type unsafeTailRequestProcessor struct {
	calls int
}

func (p *unsafeTailRequestProcessor) ProcessRequest(
	_ context.Context,
	_ *agent.Invocation,
	req *model.Request,
	_ chan<- *event.Event,
) {
	p.calls++
	if req == nil {
		return
	}
	req.Messages = append(req.Messages, model.NewSystemMessage("unsafe tail"))
}

type compactingModel struct {
	name string
}

func (m *compactingModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *compactingModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

type toolsCheckingTailRequestProcessor struct {
	sawNilTools         bool
	sawStructuredOutput bool
	sawStop             []string
}

func (p *toolsCheckingTailRequestProcessor) ProcessRequest(
	_ context.Context,
	_ *agent.Invocation,
	_ *model.Request,
	_ chan<- *event.Event,
) {
}

func (p *toolsCheckingTailRequestProcessor) SupportsContextCompactionRebuild(
	_ *agent.Invocation,
) bool {
	return true
}

func (p *toolsCheckingTailRequestProcessor) RebuildRequestForContextCompaction(
	_ context.Context,
	_ *agent.Invocation,
	req *model.Request,
) {
	if req == nil {
		return
	}
	if req.Tools == nil {
		p.sawNilTools = true
	}
	p.sawStop = append([]string(nil), req.Stop...)
	p.sawStructuredOutput = req.StructuredOutput != nil &&
		req.StructuredOutput.JSONSchema != nil
}

type testSkillRepo struct {
	skills map[string]*skill.Skill
}

func (r *testSkillRepo) Summaries() []skill.Summary {
	if len(r.skills) == 0 {
		return nil
	}
	out := make([]skill.Summary, 0, len(r.skills))
	for _, sk := range r.skills {
		if sk == nil {
			continue
		}
		out = append(out, sk.Summary)
	}
	return out
}

func (r *testSkillRepo) Get(name string) (*skill.Skill, error) {
	if r == nil || r.skills == nil {
		return nil, errors.New("skill not found")
	}
	sk, ok := r.skills[name]
	if !ok || sk == nil {
		return nil, errors.New("skill not found")
	}
	return sk, nil
}

func (r *testSkillRepo) Path(name string) (string, error) {
	if _, err := r.Get(name); err != nil {
		return "", err
	}
	return "", nil
}

func TestMaybeCompactContextBeforeLLM_RebuildsRequestWithSummary(t *testing.T) {
	modelName := "compact-retry-model"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)
	require.Len(t, req.Messages, 2)
	require.Contains(t, req.Messages[0].Content, longContent)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 1, service.Calls())
	require.NotSame(t, req, rebuilt)
	require.Len(t, rebuilt.Messages, 2)
	require.Equal(t, model.RoleSystem, rebuilt.Messages[0].Role)
	require.Contains(t, rebuilt.Messages[0].Content, "compressed history")
	require.Equal(t, "current", rebuilt.Messages[1].Content)
}

func TestMaybeCompactContextBeforeLLM_SkipsWithoutSummaryAwareProcessor(t *testing.T) {
	modelName := "compact-retry-no-summary"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
	)

	f := New(
		[]flow.RequestProcessor{
			&seedMessagesRequestProcessor{
				messages: []model.Message{
					model.NewUserMessage(strings.Repeat("payload ", 2000)),
				},
			},
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)
	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 0, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_SkipsWhenSummaryInjectionDisabled(t *testing.T) {
	modelName := "compact-retry-summary-disabled"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(false),
				processor.WithEnableContextCompaction(true),
				processor.WithContextCompactionToolResultMaxTokens(10),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 0, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_SkipsWhenSummaryRefreshFails(t *testing.T) {
	modelName := "compact-retry-summary-error"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryFailingService{
		Service: baseSvc,
		err:     context.DeadlineExceeded,
	}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 1, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_RebuildsWithoutReplayingEarlierProcessors(t *testing.T) {
	modelName := "compact-retry-safe-rebuild"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	prefixProcessor := &countingRequestProcessor{
		messages: []model.Message{model.NewSystemMessage("prefix guidance")},
	}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{{
			RequestID: "req-old",
			Timestamp: time.Now().Add(-time.Hour),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewUserMessage(longContent),
				}},
			},
		}},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			prefixProcessor,
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
			processor.NewTimeRequestProcessor(
				processor.WithAddCurrentTime(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)
	require.Equal(t, 1, prefixProcessor.Calls())

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 1, prefixProcessor.Calls())
	require.Equal(t, 1, service.Calls())
	require.NotSame(t, req, rebuilt)
	require.Contains(t, rebuilt.Messages[0].Content, "prefix guidance")
	require.Contains(t, rebuilt.Messages[0].Content, "compressed history")
	require.Contains(t, rebuilt.Messages[0].Content, "The current time is:")
	require.Equal(t, "current", rebuilt.Messages[len(rebuilt.Messages)-1].Content)
}

func TestMaybeCompactContextBeforeLLM_SkipsWhenUnsafeTailProcessorPresent(t *testing.T) {
	modelName := "compact-retry-unsafe-tail"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{{
			RequestID: "req-old",
			Timestamp: time.Now().Add(-time.Hour),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewUserMessage(longContent),
				}},
			},
		}},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
			&unsafeTailRequestProcessor{},
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)
	require.Nil(t, rebuildPlan)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 0, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_RebuildsAfterPartialSummaryFailure(t *testing.T) {
	modelName := "compact-retry-partial-summary-error"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryPartialFailureService{
		Service: baseSvc,
		err:     context.DeadlineExceeded,
	}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{{
			RequestID: "req-old",
			Timestamp: time.Now().Add(-time.Hour),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewUserMessage(longContent),
				}},
			},
		}},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 1, service.Calls())
	require.NotSame(t, req, rebuilt)
	require.Contains(t, rebuilt.Messages[0].Content, "compressed despite persistence failure")
	require.Equal(t, "current", rebuilt.Messages[1].Content)
}

func TestMaybeCompactContextBeforeLLM_RebuildPreservesPreContentRequestState(
	t *testing.T,
) {
	modelName := "compact-retry-preserve-request-state"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	tailProcessor := &toolsCheckingTailRequestProcessor{}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{{
			RequestID: "req-old",
			Timestamp: time.Now().Add(-time.Hour),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewUserMessage(longContent),
				}},
			},
		}},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
			tailProcessor,
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stop: []string{"DONE"},
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "result",
				Schema: map[string]any{"type": "object"},
			},
		},
	}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 1, service.Calls())
	require.NotSame(t, req, rebuilt)
	require.False(t, tailProcessor.sawNilTools)
	require.Equal(t, []string{"DONE"}, tailProcessor.sawStop)
	require.True(t, tailProcessor.sawStructuredOutput)
	require.Equal(t, []string{"DONE"}, rebuilt.Stop)
	require.NotNil(t, rebuilt.StructuredOutput)
	require.Equal(t, "result", rebuilt.StructuredOutput.JSONSchema.Name)
	require.Equal(
		t,
		"object",
		rebuilt.StructuredOutput.JSONSchema.Schema["type"],
	)
}

func TestMaybeCompactContextBeforeLLM_RebuildsWithSkillsToolResultTailWhenSafe(
	t *testing.T,
) {
	modelName := "compact-retry-skills-tail-safe"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	repo := &testSkillRepo{
		skills: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc", Description: "math"},
				Body:    "calculator skill body",
			},
		},
	}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		State: session.StateMap{
			skill.LoadedKey("tester", "calc"): []byte("1"),
		},
		Events: []event.Event{{
			RequestID: "req-old",
			Timestamp: time.Now().Add(-time.Hour),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewUserMessage(longContent),
				}},
			},
		}},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)
	inv.AgentName = "tester"

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
			processor.NewSkillsToolResultRequestProcessor(
				repo,
				processor.WithSkillsToolResultLoadMode(processor.SkillLoadModeTurn),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "tc1",
					Function: model.FunctionDefinitionParam{
						Name:      "skill_load",
						Arguments: []byte(`{"skill":"calc"}`),
					},
				}},
			},
			{
				Role:     model.RoleTool,
				ToolName: "skill_load",
				ToolID:   "tc1",
				Content:  "loaded: calc",
			},
		},
	}
	rebuildPlan := f.preprocess(context.Background(), inv, req, nil)
	require.NotNil(t, rebuildPlan)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
		rebuildPlan,
	)

	require.Equal(t, 1, service.Calls())
	require.NotSame(t, req, rebuilt)
	require.Len(t, rebuildPlan.tailProcessors, 1)
	var rebuiltToolContent string
	for _, msg := range rebuilt.Messages {
		if msg.Role == model.RoleTool && msg.ToolName == "skill_load" {
			rebuiltToolContent = msg.Content
			break
		}
	}
	require.Contains(t, rebuiltToolContent, "[Loaded] calc")
	require.Contains(t, rebuiltToolContent, "calculator skill body")
}

func TestPreprocess_SkipsSkillsToolResultTailWhenLoadModeOnceIsActive(
	t *testing.T,
) {
	repo := &testSkillRepo{
		skills: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc"},
				Body:    "calculator skill body",
			},
		},
	}
	inv := &agent.Invocation{
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "calc"): []byte("1"),
			},
		},
	}

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
			processor.NewSkillsToolResultRequestProcessor(
				repo,
				processor.WithSkillsToolResultLoadMode(processor.SkillLoadModeOnce),
			),
		},
		nil,
		Options{EnableContextCompaction: true},
	)

	rebuildPlan := f.preprocess(context.Background(), inv, &model.Request{}, nil)
	require.Nil(t, rebuildPlan)
}

func TestMaybeCompactContextBeforeLLM_InitialGuards(t *testing.T) {
	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationSessionService(inmemory.NewSessionService()),
	)
	t.Cleanup(func() {
		require.NoError(t, inv.SessionService.Close())
	})

	require.Nil(t, f.maybeCompactContextBeforeLLM(context.Background(), inv, nil, nil))

	disabled := New(
		f.requestProcessors,
		nil,
		Options{EnableContextCompaction: false},
	)
	require.Same(t, req, disabled.maybeCompactContextBeforeLLM(context.Background(), inv, req, nil))
	require.Same(t, req, f.maybeCompactContextBeforeLLM(context.Background(), nil, req, nil))
	require.Same(t, req, f.maybeCompactContextBeforeLLM(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSessionService(inmemory.NewSessionService()),
		),
		req,
		nil,
	))
	require.Same(t, req, f.maybeCompactContextBeforeLLM(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{}),
		),
		req,
		nil,
	))
}

func TestRebuildRequestForContextCompaction_PopulatesFilteredTools(t *testing.T) {
	f := New(nil, nil, Options{EnableContextCompaction: true})
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&minimalAgent{
			tools: []tool.Tool{
				&mockLongRunnerTool{name: "search"},
			},
		}),
		agent.WithInvocationSession(&session.Session{}),
	)
	tailProcessor := &toolsCheckingTailRequestProcessor{}
	rebuildPlan := &contextCompactionRebuildPlan{
		beforeContent: &model.Request{},
		contentProcessor: processor.NewContentRequestProcessor(
			processor.WithAddSessionSummary(true),
		),
		tailProcessors: []contextCompactionTailProcessor{
			tailProcessor,
		},
	}

	require.Nil(t, f.rebuildRequestForContextCompaction(
		context.Background(),
		inv,
		nil,
	))

	rebuilt := f.rebuildRequestForContextCompaction(
		context.Background(),
		inv,
		rebuildPlan,
	)

	require.NotNil(t, rebuilt)
	require.False(t, tailProcessor.sawNilTools)
	require.Contains(t, rebuilt.Tools, "search")
}

func TestSupportsSyncSummaryRetry(t *testing.T) {
	tests := []struct {
		name       string
		processors []flow.RequestProcessor
		want       bool
	}{
		{
			name: "summary aware content processor",
			processors: []flow.RequestProcessor{
				processor.NewContentRequestProcessor(
					processor.WithAddSessionSummary(true),
				),
			},
			want: true,
		},
		{
			name: "summary disabled",
			processors: []flow.RequestProcessor{
				processor.NewContentRequestProcessor(
					processor.WithAddSessionSummary(false),
				),
			},
			want: false,
		},
		{
			name: "non all timeline filter",
			processors: []flow.RequestProcessor{
				processor.NewContentRequestProcessor(
					processor.WithAddSessionSummary(true),
					processor.WithTimelineFilterMode(processor.TimelineFilterCurrentRequest),
				),
			},
			want: false,
		},
		{
			name: "non content processor",
			processors: []flow.RequestProcessor{
				&seedMessagesRequestProcessor{
					messages: []model.Message{model.NewUserMessage("hello")},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(tt.processors, nil, Options{})
			require.Equal(t, tt.want, f.supportsSyncSummaryRetry())
		})
	}
}

func TestNormalizeContextCompactionThresholdRatio(t *testing.T) {
	require.Equal(t, defaultContextCompactionThresholdRatio,
		normalizeContextCompactionThresholdRatio(0))
	require.Equal(t, defaultContextCompactionThresholdRatio,
		normalizeContextCompactionThresholdRatio(1.5))
	require.Equal(t, defaultContextCompactionThresholdRatio,
		normalizeContextCompactionThresholdRatio(-0.1))
	require.Equal(t, 0.5, normalizeContextCompactionThresholdRatio(0.5))
}

func TestContextCompactionThreshold(t *testing.T) {
	t.Run("caps to small model window", func(t *testing.T) {
		const modelName = "compact-threshold-small-window"
		model.RegisterModelContextWindow(modelName, 1024)

		inv := agent.NewInvocation(
			agent.WithInvocationModel(&compactingModel{name: modelName}),
		)
		require.Equal(t, 1024, contextCompactionThreshold(inv, 0.1))
	})

	t.Run("uses fallback window for unknown model", func(t *testing.T) {
		inv := agent.NewInvocation(
			agent.WithInvocationModel(&compactingModel{name: "compact-threshold-unknown"}),
		)
		require.Equal(t, 5734, contextCompactionThreshold(inv, 0))
	})
}

func TestShouldSyncCompactContext(t *testing.T) {
	const modelName = "compact-threshold-sync"
	model.RegisterModelContextWindow(modelName, 1024)

	inv := agent.NewInvocation(
		agent.WithInvocationModel(&compactingModel{name: modelName}),
	)

	require.False(t, shouldSyncCompactContext(context.Background(), nil, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}, 0.5))
	require.False(t, shouldSyncCompactContext(context.Background(), inv, nil, 0.5))
	require.False(t, shouldSyncCompactContext(context.Background(), inv, &model.Request{}, 0.5))

	require.False(t, shouldSyncCompactContext(context.Background(), inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage(strings.Repeat("a", 100))},
	}, 0.5))
	require.True(t, shouldSyncCompactContext(context.Background(), inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage(strings.Repeat("a", 5000))},
	}, 0.5))
}

func TestCloneRequestForContextCompaction_DeepCopiesMutableFields(t *testing.T) {
	require.Nil(t, cloneRequestForContextCompaction(nil))

	text := "hello"
	index := 3
	req := &model.Request{
		Messages: []model.Message{{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &text,
			}, {
				Type: model.ContentTypeImage,
				Image: &model.Image{
					URL:    "https://example.com/a.png",
					Data:   []byte{1, 2, 3},
					Detail: "high",
				},
			}, {
				Type: model.ContentTypeAudio,
				Audio: &model.Audio{
					Data:   []byte{4, 5, 6},
					Format: "wav",
				},
			}, {
				Type: model.ContentTypeFile,
				File: &model.File{
					Name: "test.txt",
					Data: []byte("abc"),
				},
			}},
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call-1",
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"q":"go"}`),
				},
				Index: &index,
				ExtraFields: map[string]any{
					"nested": map[string]any{"k": "v"},
				},
			}},
		}},
		GenerationConfig: model.GenerationConfig{
			Stop: []string{"DONE"},
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "schema",
				Schema: map[string]any{"type": "object"},
			},
		},
		Tools: map[string]tool.Tool{},
	}

	cloned := cloneRequestForContextCompaction(req)
	require.NotNil(t, cloned)
	require.NotSame(t, req, cloned)

	req.Messages[0].ContentParts[0].Text = nil
	req.Messages[0].ContentParts[1].Image.Data[0] = 9
	req.Messages[0].ContentParts[2].Audio.Data[0] = 8
	req.Messages[0].ContentParts[3].File.Data[0] = 'z'
	req.Messages[0].ToolCalls[0].Function.Arguments[0] = '['
	req.Messages[0].ToolCalls[0].ExtraFields["nested"] = map[string]any{"k": "changed"}
	req.Messages[0].ToolCalls[0].Index = nil
	req.Stop[0] = "STOP"
	req.StructuredOutput.JSONSchema.Schema["type"] = "array"
	req.Tools["search"] = nil

	require.NotNil(t, cloned.Messages[0].ContentParts[0].Text)
	require.Equal(t, "hello", *cloned.Messages[0].ContentParts[0].Text)
	require.Equal(t, []byte{1, 2, 3}, cloned.Messages[0].ContentParts[1].Image.Data)
	require.Equal(t, []byte{4, 5, 6}, cloned.Messages[0].ContentParts[2].Audio.Data)
	require.Equal(t, []byte("abc"), cloned.Messages[0].ContentParts[3].File.Data)
	require.Equal(
		t,
		[]byte(`{"q":"go"}`),
		cloned.Messages[0].ToolCalls[0].Function.Arguments,
	)
	require.Equal(
		t,
		map[string]any{"nested": map[string]any{"k": "v"}},
		cloned.Messages[0].ToolCalls[0].ExtraFields,
	)
	require.NotNil(t, cloned.Messages[0].ToolCalls[0].Index)
	require.Equal(t, 3, *cloned.Messages[0].ToolCalls[0].Index)
	require.Equal(t, []string{"DONE"}, cloned.Stop)
	require.Equal(
		t,
		"object",
		cloned.StructuredOutput.JSONSchema.Schema["type"],
	)
	require.Empty(t, cloned.Tools)
	fallbackCloned := cloneJSONMapForContextCompaction(
		map[string]any{"bad": make(chan int)},
	)
	_, ok := fallbackCloned["bad"].(chan int)
	require.True(t, ok)
	require.Nil(t, cloneJSONMapForContextCompaction(nil))
}
