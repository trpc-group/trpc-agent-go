//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/finalevent"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginbase "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestPluginWarnsOncePerUnchangedStreak(t *testing.T) {
	manager, err := pluginbase.NewManager(New())
	require.NoError(t, err)
	invocation := newPluginInvocation(t, manager)

	warnings := make([]bool, 0, 6)
	for i := 0; i < 6; i++ {
		arguments := `{"query":"x","limit":1}`
		if i%2 == 1 {
			arguments = ` { "limit": 1, "query": "x" } `
		}
		warnings = append(warnings, observeCompleteRound(
			t,
			manager,
			invocation,
			eventCall{
				index:     0,
				id:        fmt.Sprintf("call-%d", i),
				name:      "search",
				arguments: arguments,
				result:    "same",
			},
		))
	}
	require.Equal(t, []bool{false, true, false, false, false, false}, warnings)
}

func TestPluginDropsWarningWhenInvocationQueueIsClosed(t *testing.T) {
	manager, err := pluginbase.NewManager(New())
	require.NoError(t, err)
	invocation := newPluginInvocation(t, manager)
	steer.Attach(invocation, steer.NewQueue())
	steer.Close(invocation)

	require.False(t, observeOneCallRound(t, manager, invocation,
		"call-1", "search", `{}`, "same"))
	require.False(t, observeOneCallRound(t, manager, invocation,
		"call-2", "search", `{}`, "same"))
	require.Empty(t, steer.DrainQueued(invocation))
}

func TestPluginChangedOrMalformedRoundResetsDetection(t *testing.T) {
	manager, err := pluginbase.NewManager(New())
	require.NoError(t, err)
	invocation := newPluginInvocation(t, manager)

	require.False(t, observeOneCallRound(t, manager, invocation,
		"call-1", "search", `{"query":"x"}`, "same"))
	require.False(t, observeOneCallRound(t, manager, invocation,
		"call-2", "search", `{"query":"y"}`, "same"))
	require.True(t, observeOneCallRound(t, manager, invocation,
		"call-3", "search", `{"query":"y"}`, "same"))

	require.False(t, observeOneCallRound(t, manager, invocation,
		"call-4", "search", `{"query":"z"}`, "same"))
	toolCalls := []model.ToolCall{newToolCall(
		"call-5", "search", `{"query":"z"}`,
	)}
	malformed := model.NewToolMessage("", "search", "same")
	require.False(t, observeToolMessages(
		t, manager, invocation, toolCalls, []model.Message{malformed},
	))
	require.False(t, observeOneCallRound(t, manager, invocation,
		"call-6", "search", `{"query":"z"}`, "same"))
	require.True(t, observeOneCallRound(t, manager, invocation,
		"call-7", "search", `{"query":"z"}`, "same"))
}

func TestPluginCombinesOutOfOrderPerCallResults(t *testing.T) {
	manager, err := pluginbase.NewManager(New())
	require.NoError(t, err)
	invocation := newPluginInvocation(t, manager)

	for round := 0; round < 2; round++ {
		suffix := round + 1
		toolCalls := []model.ToolCall{
			newToolCall(
				fmt.Sprintf("call-slow-%d", suffix),
				"slow",
				`{"value":"same"}`,
			),
			newToolCall(
				fmt.Sprintf("call-fast-%d", suffix),
				"fast",
				`{"value":"same"}`,
			),
		}
		fastResult := model.NewToolMessage(
			fmt.Sprintf("call-fast-%d", suffix), "fast", "fast",
		)
		require.False(t, observeToolMessages(
			t, manager, invocation, toolCalls, []model.Message{fastResult},
		))

		slowResult := model.NewToolMessage(
			fmt.Sprintf("call-slow-%d", suffix), "slow", "slow",
		)
		require.Equal(
			t,
			round == 1,
			observeToolMessages(
				t, manager, invocation, toolCalls, []model.Message{slowResult},
			),
		)
	}
}

func TestPluginCorrelatesReplacementEventByToolIDs(t *testing.T) {
	manager, err := pluginbase.NewManager(New())
	require.NoError(t, err)
	invocation := newPluginInvocation(t, manager)

	for round := 0; round < 2; round++ {
		toolCall := newToolCall(
			fmt.Sprintf("call-%d", round),
			"search",
			`{"query":"same"}`,
		)
		result := model.NewToolMessage(toolCall.ID, "search", "same")
		original := event.NewResponseEvent(
			invocation.InvocationID,
			"assistant",
			&model.Response{
				Object: model.ObjectTypeToolResponse,
				Choices: []model.Choice{{
					Message: result,
				}},
			},
		)
		_, err := manager.AfterToolMessages(
			context.Background(),
			&pluginbase.AfterToolMessagesArgs{
				Invocation:         invocation,
				ToolCalls:          []model.ToolCall{toolCall},
				ToolResultMessages: []model.Message{result},
				ToolResultEvent:    original,
			},
		)
		require.NoError(t, err)

		replacement := *original
		replacement.ID = fmt.Sprintf("replacement-%d", round)
		finalevent.Run(
			context.Background(),
			invocation,
			original.ID,
			&replacement,
		)
	}

	queued := steer.DrainQueued(invocation)
	require.Len(t, queued, 1)
	require.Equal(t, warningSource, queued[0].Source)
}

func TestPluginBeforeAgentInitializesStateWithoutAttachingQueue(t *testing.T) {
	manager, err := pluginbase.NewManager(New())
	require.NoError(t, err)
	invocation := &agent.Invocation{}
	stale := &detectorState{previous: "stale"}
	invocation.SetState(stateKey, stale)

	_, err = manager.AgentCallbacks().RunBeforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: invocation},
	)
	require.NoError(t, err)
	state, ok := agent.GetStateValue[*detectorState](invocation, stateKey)
	require.True(t, ok)
	require.NotSame(t, stale, state)
	require.Empty(t, state.previous)
	require.False(t, steer.IsAttached(invocation))
}

func TestOptionsAccumulateExcludedToolNamesAndIgnoreEmpty(t *testing.T) {
	o := newOptions(
		WithExcludedToolNames("poll", ""),
		WithExcludedToolNames("background"),
		WithExcludedToolNames(),
	)
	require.Contains(t, o.excludedToolNames, "poll")
	require.Contains(t, o.excludedToolNames, "background")
	require.NotContains(t, o.excludedToolNames, "")
}

func TestPluginHandlesNilInputsAndMissingInvocation(t *testing.T) {
	var nilPlugin *toolLoopWarningPlugin
	require.Empty(t, nilPlugin.Name())
	nilPlugin.Register(nil)
	_, err := nilPlugin.beforeAgent(context.Background(), nil)
	require.NoError(t, err)
	_, err = nilPlugin.afterToolMessages(context.Background(), nil)
	require.NoError(t, err)

	plugin := &toolLoopWarningPlugin{warning: defaultWarning}
	plugin.Register(nil)
	plugin.Register(&pluginbase.Registry{})
	_, err = plugin.beforeAgent(context.Background(), nil)
	require.NoError(t, err)
	_, err = plugin.beforeAgent(context.Background(), &agent.BeforeAgentArgs{})
	require.NoError(t, err)
	_, err = plugin.afterToolMessages(context.Background(), nil)
	require.NoError(t, err)
	_, err = plugin.afterToolMessages(
		context.Background(),
		&pluginbase.AfterToolMessagesArgs{},
	)
	require.NoError(t, err)
}

func TestPluginRequiresRunnerFinalization(t *testing.T) {
	manager := pluginbase.MustNewManager(New())
	invocation := agent.NewInvocation()
	_, err := manager.AgentCallbacks().RunBeforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: invocation},
	)
	require.NoError(t, err)

	require.False(t, observeOneCallRound(
		t, manager, invocation, "call-1", "search", `{}`, "same",
	))
	require.False(t, observeOneCallRound(
		t, manager, invocation, "call-2", "search", `{}`, "same",
	))
	require.False(t, steer.IsAttached(invocation))
}

func TestPluginAcceptsReplacementToolMessagesWithoutToolName(t *testing.T) {
	manager := pluginbase.MustNewManager(New())
	invocation := newPluginInvocation(t, manager)
	for round := 0; round < 2; round++ {
		toolCall := newToolCall(
			fmt.Sprintf("call-%d", round),
			"search",
			`{"query":"same"}`,
		)
		result := model.NewToolMessage(toolCall.ID, "search", "same")
		result.ToolName = ""
		require.Equal(t, round == 1, observeToolMessages(
			t,
			manager,
			invocation,
			[]model.ToolCall{toolCall},
			[]model.Message{result},
		))
	}
}

func TestPluginOptionsCustomizeWarningAndExcludeTools(t *testing.T) {
	const customWarning = "choose a different strategy"
	manager := pluginbase.MustNewManager(New(
		WithWarningMessage(customWarning),
		WithExcludedToolNames("poll"),
	))
	invocation := newPluginInvocation(t, manager)

	for round := 0; round < 2; round++ {
		require.False(t, observeCompleteRound(
			t,
			manager,
			invocation,
			eventCall{
				index:  0,
				id:     fmt.Sprintf("poll-%d", round),
				name:   "poll",
				result: "pending",
			},
			eventCall{
				index:  1,
				id:     fmt.Sprintf("search-in-poll-%d", round),
				name:   "search",
				result: "same",
			},
		))
	}
	require.False(t, observeOneCallRound(
		t, manager, invocation, "search-1", "search", `{}`, "same",
	))

	toolCall := newToolCall("search-2", "search", `{}`)
	result := model.NewToolMessage(toolCall.ID, "search", "same")
	toolResultEvent := event.NewResponseEvent(
		invocation.InvocationID,
		"assistant",
		&model.Response{
			Object:  model.ObjectTypeToolResponse,
			Choices: toolResultChoices([]model.Message{result}),
		},
	)
	_, err := manager.AfterToolMessages(
		context.Background(),
		&pluginbase.AfterToolMessagesArgs{
			Invocation:         invocation,
			ToolCalls:          []model.ToolCall{toolCall},
			ToolResultMessages: []model.Message{result},
			ToolResultEvent:    toolResultEvent,
		},
	)
	require.NoError(t, err)
	finalevent.Run(
		context.Background(), invocation, toolResultEvent.ID, toolResultEvent,
	)
	queued := steer.DrainQueued(invocation)
	require.Len(t, queued, 1)
	require.Equal(t, customWarning, queued[0].Message.Content)
}

func TestPluginKeepsConcurrentInvocationsIndependent(t *testing.T) {
	plugin := New().(*toolLoopWarningPlugin)
	const invocationCount = 32
	var wg sync.WaitGroup
	errors := make(chan error, invocationCount)
	for i := 0; i < invocationCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			invocation := agent.NewInvocation()
			finalevent.Attach(invocation)
			warningCount := 0
			for round := 0; round < 4; round++ {
				toolCall := newToolCall(
					fmt.Sprintf("call-%d-%d", index, round),
					"search",
					`{"query":"same"}`,
				)
				result := model.NewToolMessage(
					toolCall.ID, "search", "same",
				)
				if err := observeToolMessagesDirect(
					context.Background(),
					plugin,
					invocation,
					[]model.ToolCall{toolCall},
					[]model.Message{result},
				); err != nil {
					errors <- err
					return
				}
				warningCount += len(steer.DrainQueued(invocation))
			}
			if warningCount != 1 {
				errors <- fmt.Errorf(
					"invocation %d warning count = %d, want 1",
					index,
					warningCount,
				)
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func TestPluginSerializesSharedInvocationStateInitialization(t *testing.T) {
	plugin := New().(*toolLoopWarningPlugin)
	invocation := agent.NewInvocation()
	finalevent.Attach(invocation)

	const callbackCount = 64
	var wg sync.WaitGroup
	errors := make(chan error, callbackCount)
	for i := 0; i < callbackCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			toolCall := newToolCall(
				fmt.Sprintf("call-%d", index),
				"search",
				`{"query":"same"}`,
			)
			err := observeToolMessagesDirect(
				context.Background(),
				plugin,
				invocation,
				[]model.ToolCall{toolCall},
				[]model.Message{model.NewToolMessage(
					toolCall.ID, "search", "same",
				)},
			)
			if err != nil {
				errors <- err
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	state, ok := agent.GetStateValue[*detectorState](invocation, stateKey)
	require.True(t, ok)
	require.NotNil(t, state)
	queued := steer.DrainQueued(invocation)
	require.Len(t, queued, 1)
	require.Equal(t, warningSource, queued[0].Source)
	state.reset()

	for round := 0; round < 2; round++ {
		toolCall := newToolCall(
			fmt.Sprintf("final-call-%d", round),
			"search",
			`{"query":"same"}`,
		)
		err := observeToolMessagesDirect(
			context.Background(),
			plugin,
			invocation,
			[]model.ToolCall{toolCall},
			[]model.Message{model.NewToolMessage(
				toolCall.ID, "search", "same",
			)},
		)
		require.NoError(t, err)
	}
	queued = steer.DrainQueued(invocation)
	require.Len(t, queued, 1)
	require.Equal(t, warningSource, queued[0].Source)
}

func TestPluginRunnerIntegrationUsesFinalOrderedToolResults(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		perCall bool
	}{
		{name: "disabled", enabled: false},
		{name: "aggregate", enabled: true},
		{name: "per_call", enabled: true, perCall: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modelStub, slowCalls, fastCalls, sessionService, runnerInstance := runRepeatedRound(
				t,
				test.enabled,
				test.perCall,
				false,
				false,
			)
			requests := modelStub.Requests()
			require.Len(t, requests, 3)
			require.False(t, hasWarning(requests[0]))
			require.False(t, hasWarning(requests[1]))
			require.Equal(t, test.enabled, hasWarning(requests[2]))
			require.Equal(t, int32(2), slowCalls.Load())
			require.Equal(t, int32(2), fastCalls.Load())
			require.Equal(
				t,
				[]string{"call-slow-2", "call-fast-2"},
				lastToolResultIDs(requests[2], 2),
			)
			require.Equal(
				t,
				[]string{"visible:slow", "visible:fast"},
				lastToolResultContents(requests[2], 2),
			)
			assertSessionWarning(t, sessionService, test.enabled)

			events, err := runnerInstance.Run(
				context.Background(),
				"user",
				"session",
				model.NewUserMessage("continue"),
			)
			require.NoError(t, err)
			for range events {
			}
			requests = modelStub.Requests()
			require.Len(t, requests, 4)
			require.Equal(t, test.enabled, hasWarning(requests[3]))
		})
	}
}

func TestPluginComposedAgentUsesInvocationLocalQueue(t *testing.T) {
	modelStub, slowCalls, fastCalls, sessionService, _ := runRepeatedRound(
		t,
		true,
		false,
		true,
		false,
	)

	requests := modelStub.Requests()
	require.Len(t, requests, 3)
	require.True(t, hasWarning(requests[2]))
	require.Equal(t, int32(2), slowCalls.Load())
	require.Equal(t, int32(2), fastCalls.Load())
	assertSessionWarning(t, sessionService, true)
}

type clonedChildAgent struct {
	name  string
	child agent.Agent
}

func (a *clonedChildAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *clonedChildAgent) SubAgents() []agent.Agent {
	return []agent.Agent{a.child}
}

func (a *clonedChildAgent) FindSubAgent(name string) agent.Agent {
	if a.child != nil && a.child.Info().Name == name {
		return a.child
	}
	return nil
}

func (a *clonedChildAgent) Tools() []tool.Tool {
	return nil
}

func (a *clonedChildAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	childInvocation := invocation.Clone(agent.WithInvocationAgent(a.child))
	return agent.RunWithPlugins(
		agent.NewInvocationContext(ctx, childInvocation),
		childInvocation,
		a.child,
	)
}

func TestPluginQueuedUserMessageBreaksRoundAdjacency(t *testing.T) {
	modelStub, _, _, sessionService, _ := runRepeatedRound(
		t,
		true,
		false,
		false,
		true,
	)

	requests := modelStub.Requests()
	require.Len(t, requests, 3)
	require.True(t, hasMessage(requests[1], "please retry the same lookup"))
	require.False(t, hasWarning(requests[2]))
	assertSessionWarning(t, sessionService, false)
}

func newPluginInvocation(
	t *testing.T,
	manager *pluginbase.Manager,
) *agent.Invocation {
	t.Helper()
	invocation := agent.NewInvocation()
	finalevent.Attach(invocation)
	_, err := manager.AgentCallbacks().RunBeforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: invocation},
	)
	require.NoError(t, err)
	return invocation
}

func observeToolMessages(
	t *testing.T,
	manager *pluginbase.Manager,
	invocation *agent.Invocation,
	toolCalls []model.ToolCall,
	results []model.Message,
) bool {
	t.Helper()
	toolResultEvent := event.NewResponseEvent(
		invocation.InvocationID,
		"assistant",
		&model.Response{
			Object:  model.ObjectTypeToolResponse,
			Choices: toolResultChoices(results),
		},
	)
	_, err := manager.AfterToolMessages(
		context.Background(),
		&pluginbase.AfterToolMessagesArgs{
			Invocation:         invocation,
			ToolCalls:          toolCalls,
			ToolResultMessages: results,
			ToolResultEvent:    toolResultEvent,
		},
	)
	require.NoError(t, err)
	finalevent.Run(
		context.Background(),
		invocation,
		toolResultEvent.ID,
		toolResultEvent,
	)
	queued := steer.DrainQueued(invocation)
	if len(queued) == 0 {
		return false
	}
	require.Len(t, queued, 1)
	require.Equal(t, model.RoleUser, queued[0].Message.Role)
	require.Equal(t, defaultWarning, queued[0].Message.Content)
	require.Equal(t, warningSource, queued[0].Source)
	return true
}

func observeToolMessagesDirect(
	ctx context.Context,
	plugin *toolLoopWarningPlugin,
	invocation *agent.Invocation,
	toolCalls []model.ToolCall,
	results []model.Message,
) error {
	toolResultEvent := event.NewResponseEvent(
		invocation.InvocationID,
		"assistant",
		&model.Response{
			Object:  model.ObjectTypeToolResponse,
			Choices: toolResultChoices(results),
		},
	)
	_, err := plugin.afterToolMessages(
		ctx,
		&pluginbase.AfterToolMessagesArgs{
			Invocation:         invocation,
			ToolCalls:          toolCalls,
			ToolResultMessages: results,
			ToolResultEvent:    toolResultEvent,
		},
	)
	if err != nil {
		return err
	}
	finalevent.Run(ctx, invocation, toolResultEvent.ID, toolResultEvent)
	return nil
}

func toolResultChoices(messages []model.Message) []model.Choice {
	choices := make([]model.Choice, len(messages))
	for i, message := range messages {
		choices[i] = model.Choice{Index: i, Message: message}
	}
	return choices
}

func observeOneCallRound(
	t *testing.T,
	manager *pluginbase.Manager,
	invocation *agent.Invocation,
	id string,
	name string,
	arguments string,
	result string,
) bool {
	t.Helper()
	return observeCompleteRound(t, manager, invocation, eventCall{
		index:     0,
		id:        id,
		name:      name,
		arguments: arguments,
		result:    result,
	})
}

func observeCompleteRound(
	t *testing.T,
	manager *pluginbase.Manager,
	invocation *agent.Invocation,
	calls ...eventCall,
) bool {
	t.Helper()
	toolCalls := make([]model.ToolCall, len(calls))
	results := make([]model.Message, len(calls))
	for _, call := range calls {
		toolCalls[call.index] = newToolCall(
			call.id,
			call.name,
			call.arguments,
		)
		results[call.index] = model.NewToolMessage(
			call.id,
			call.name,
			call.result,
		)
	}
	return observeToolMessages(t, manager, invocation, toolCalls, results)
}

func hasWarning(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role == model.RoleUser && message.Content == defaultWarning {
			return true
		}
	}
	return false
}

func hasMessage(messages []model.Message, content string) bool {
	for _, message := range messages {
		if message.Content == content {
			return true
		}
	}
	return false
}

type eventCall struct {
	index     int
	id        string
	name      string
	arguments string
	result    string
}

type repeatedRoundModel struct {
	mu       sync.Mutex
	requests [][]model.Message
}

func (m *repeatedRoundModel) Info() model.Info {
	return model.Info{Name: "repeated-round-model"}
}

func (m *repeatedRoundModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, cloneMessages(request.Messages))
	callIndex := len(m.requests) - 1
	m.mu.Unlock()

	response := &model.Response{
		ID:   "final-response",
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("done"),
		}},
	}
	if callIndex < 2 {
		suffix := callIndex + 1
		arguments := `{"value":"same"}`
		if callIndex == 1 {
			arguments = ` { "value": "same" } `
		}
		response = &model.Response{
			ID:   fmt.Sprintf("tool-response-%d", suffix),
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						newToolCall(fmt.Sprintf("call-slow-%d", suffix), "slow", arguments),
						newToolCall(fmt.Sprintf("call-fast-%d", suffix), "fast", arguments),
					},
				},
			}},
		}
	}
	responses := make(chan *model.Response, 1)
	responses <- response
	close(responses)
	return responses, nil
}

func (m *repeatedRoundModel) Requests() [][]model.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	requests := make([][]model.Message, len(m.requests))
	for i, messages := range m.requests {
		requests[i] = cloneMessages(messages)
	}
	return requests
}

type parallelInput struct {
	Value string `json:"value"`
}

func runRepeatedRound(
	t *testing.T,
	warningEnabled bool,
	perCallResults bool,
	composed bool,
	queuedUserBoundary bool,
) (
	*repeatedRoundModel,
	*atomic.Int32,
	*atomic.Int32,
	*sessioninmemory.SessionService,
	runner.Runner,
) {
	t.Helper()
	fastDone := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var slowCalls atomic.Int32
	var fastCalls atomic.Int32
	const requestID = "tool-loop-warning-request"
	var runnerInstance runner.Runner
	slowTool := function.NewFunctionTool(
		func(ctx context.Context, _ parallelInput) (string, error) {
			index := int(slowCalls.Add(1) - 1)
			if index >= len(fastDone) {
				return "", fmt.Errorf("unexpected slow tool call %d", index+1)
			}
			select {
			case <-fastDone[index]:
				return fmt.Sprintf("slow-raw-%d", index+1), nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		function.WithName("slow"),
		function.WithDescription("Returns after fast finishes."),
	)
	fastTool := function.NewFunctionTool(
		func(_ context.Context, _ parallelInput) (string, error) {
			index := int(fastCalls.Add(1) - 1)
			if index >= len(fastDone) {
				return "", fmt.Errorf("unexpected fast tool call %d", index+1)
			}
			if queuedUserBoundary && index == 0 {
				if err := runner.EnqueueUserMessage(
					runnerInstance,
					requestID,
					model.NewUserMessage("please retry the same lookup"),
				); err != nil {
					return "", fmt.Errorf("enqueue user message: %w", err)
				}
			}
			close(fastDone[index])
			return fmt.Sprintf("fast-raw-%d", index+1), nil
		},
		function.WithName("fast"),
		function.WithDescription("Returns immediately."),
	)
	modelStub := &repeatedRoundModel{}
	agentInstance := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{slowTool, fastTool}),
		llmagent.WithEnableParallelTools(true),
	)
	var rootAgent agent.Agent = agentInstance
	if composed {
		rootAgent = &clonedChildAgent{
			name:  "composed",
			child: agentInstance,
		}
	}
	resultTransformer := &testPlugin{
		name: "visible-result-transformer",
		register: func(registry *pluginbase.Registry) {
			registry.OnEvent(func(
				_ context.Context,
				_ *agent.Invocation,
				ev *event.Event,
			) (*event.Event, error) {
				if ev == nil || ev.Response == nil ||
					ev.Response.Object != model.ObjectTypeToolResponse {
					return ev, nil
				}
				updated := *ev
				updated.Response = ev.Response.Clone()
				for i := range updated.Response.Choices {
					choice := &updated.Response.Choices[i]
					if choice.Message.ToolID != "" {
						choice.Message.Content = "visible:" +
							choice.Message.ToolName
					}
					if choice.Delta.ToolID != "" {
						choice.Delta.Content = "visible:" +
							choice.Delta.ToolName
					}
				}
				return &updated, nil
			})
		},
	}
	sessionService := sessioninmemory.NewSessionService()
	runnerOptions := []runner.Option{
		runner.WithSessionService(sessionService),
	}
	if warningEnabled {
		runnerOptions = append(runnerOptions, runner.WithPlugins(New()))
	}
	runnerInstance = runner.NewRunner(
		"tool-loop-warning-app",
		rootAgent,
		runnerOptions...,
	)
	t.Cleanup(func() {
		require.NoError(t, runnerInstance.Close())
		require.NoError(t, sessionService.Close())
	})
	var runOptions []agent.RunOption
	if perCallResults {
		runOptions = append(
			runOptions,
			agent.WithToolResultEventPerCallEnabled(true),
		)
	}
	// Install the result transformer as a per-run manager after the Runner's
	// toolloopwarning manager. Detection must still observe its replacement.
	runOptions = append(runOptions, pluginbase.WithPlugins(resultTransformer))
	runOptions = append(runOptions, agent.WithRequestID(requestID))
	events, err := runnerInstance.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("run tools"),
		runOptions...,
	)
	require.NoError(t, err)
	for event := range events {
		_ = event
	}
	return modelStub, &slowCalls, &fastCalls, sessionService, runnerInstance
}

type testPlugin struct {
	name     string
	register func(*pluginbase.Registry)
}

func (p *testPlugin) Name() string {
	return p.name
}

func (p *testPlugin) Register(registry *pluginbase.Registry) {
	p.register(registry)
}

func cloneMessages(messages []model.Message) []model.Message {
	cloned := make([]model.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
		cloned[i].ContentParts = append([]model.ContentPart(nil), message.ContentParts...)
	}
	return cloned
}

func lastToolResultIDs(messages []model.Message, count int) []string {
	results := lastToolResults(messages, count)
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ToolID)
	}
	return ids
}

func lastToolResultContents(messages []model.Message, count int) []string {
	results := lastToolResults(messages, count)
	contents := make([]string, 0, len(results))
	for _, result := range results {
		contents = append(contents, result.Content)
	}
	return contents
}

func lastToolResults(messages []model.Message, count int) []model.Message {
	results := make([]model.Message, 0, count)
	for i := len(messages) - 1; i >= 0 && len(results) < count; i-- {
		if messages[i].Role == model.RoleTool {
			results = append(results, messages[i])
		}
	}
	for left, right := 0, len(results)-1; left < right; left, right = left+1, right-1 {
		results[left], results[right] = results[right], results[left]
	}
	return results
}

func assertSessionWarning(
	t *testing.T,
	service *sessioninmemory.SessionService,
	want bool,
) {
	t.Helper()
	sess, err := service.GetSession(context.Background(), session.Key{
		AppName:   "tool-loop-warning-app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	var warningEvents []event.Event
	for _, sessionEvent := range sess.GetEvents() {
		if sessionEvent.Response == nil {
			continue
		}
		for _, choice := range sessionEvent.Response.Choices {
			if choice.Message.Content == defaultWarning {
				warningEvents = append(warningEvents, sessionEvent)
			}
		}
	}
	if !want {
		require.Empty(t, warningEvents)
		return
	}
	require.Len(t, warningEvents, 1)
	warningEvent := warningEvents[0]
	require.Equal(t, "user", warningEvent.Author)
	require.Equal(t, model.RoleUser, warningEvent.Choices[0].Message.Role)
	metadata, ok, err := event.GetExtension[steer.QueuedUserMessageMetadata](
		&warningEvent,
		steer.ExtensionKeyQueuedUserMessage,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, steer.QueuedUserMessageStatusConsumed, metadata.Status)
	require.Equal(t, warningSource, metadata.Source)
}
