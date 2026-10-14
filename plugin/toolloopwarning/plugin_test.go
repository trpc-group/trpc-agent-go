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
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginbase "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestPluginWarnsOnEachEligibleRequest(t *testing.T) {
	manager, invocation, ctx := newCallbackHarness(t, New())

	first := &model.Request{Messages: []model.Message{
		model.NewUserMessage("run"),
	}}
	runBeforeModel(t, manager, ctx, first)
	require.False(t, hasWarning(first.Messages, defaultWarning))

	oneRound := repeatedRoundsRequest("search", 1)
	runBeforeModel(t, manager, ctx, oneRound)
	require.False(t, hasWarning(oneRound.Messages, defaultWarning))

	twoRounds := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, twoRounds)
	require.True(t, hasWarning(twoRounds.Messages, defaultWarning))

	threeRounds := repeatedRoundsRequest("search", 3)
	runBeforeModel(t, manager, ctx, threeRounds)
	require.True(t, hasWarning(threeRounds.Messages, defaultWarning))

	changed := changedTrailingRoundRequest()
	runBeforeModel(t, manager, ctx, changed)
	require.False(t, hasWarning(changed.Messages, defaultWarning))

	changedPair := repeatedRoundsRequest("read", 2)
	runBeforeModel(t, manager, ctx, changedPair)
	require.True(t, hasWarning(changedPair.Messages, defaultWarning))

	_, ok := agent.GetStateValue[*detectorState](invocation, stateKey)
	require.True(t, ok)
	_, err := manager.AgentCallbacks().RunAfterAgent(
		ctx,
		&agent.AfterAgentArgs{Invocation: invocation},
	)
	require.NoError(t, err)
	_, ok = agent.GetStateValue[*detectorState](invocation, stateKey)
	require.False(t, ok)
}

func TestPluginReaddsWarningIfRemovedBeforeRetry(t *testing.T) {
	manager, _, ctx := newCallbackHarness(t, New())
	first := &model.Request{Messages: []model.Message{model.NewUserMessage("run")}}
	runBeforeModel(t, manager, ctx, first)

	request := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, request)
	require.True(t, hasWarning(request.Messages, defaultWarning))
	removeWarning(request, defaultWarning)
	require.False(t, hasWarning(request.Messages, defaultWarning))

	runBeforeModel(t, manager, ctx, request)
	require.Equal(t, 1, countWarningMessages(request.Messages, defaultWarning))
}

func TestPluginDoesNotAppendTwiceWhenBeforeModelReenters(t *testing.T) {
	manager, _, ctx := newCallbackHarness(t, New())
	first := &model.Request{Messages: []model.Message{model.NewUserMessage("run")}}
	runBeforeModel(t, manager, ctx, first)

	request := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, request)
	runBeforeModel(t, manager, ctx, request)
	require.Equal(t, 1, countWarningMessages(request.Messages, defaultWarning))
}

func TestPluginFirstRequestAndUserBoundaryFailOpen(t *testing.T) {
	manager, _, ctx := newCallbackHarness(t, New())

	historicalLoop := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, historicalLoop)
	runBeforeModel(t, manager, ctx, historicalLoop)
	require.False(t, hasWarning(historicalLoop.Messages, defaultWarning))

	currentLoop := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, currentLoop)
	require.True(t, hasWarning(currentLoop.Messages, defaultWarning))

	boundary := repeatedRoundsRequest("search", 2)
	boundary.Messages = append(boundary.Messages, model.NewUserMessage("continue"))
	runBeforeModel(t, manager, ctx, boundary)
	require.False(t, hasWarning(boundary.Messages, defaultWarning))

	rearmed := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, rearmed)
	require.True(t, hasWarning(rearmed.Messages, defaultWarning))
}

func TestPluginOptionsAffectObservableBehavior(t *testing.T) {
	const customWarning = "Try another approach."
	manager, _, ctx := newCallbackHarness(
		t,
		New(
			WithWarningMessage(customWarning),
			WithExcludedToolNames("", "poll"),
			WithExcludedToolNames("poll"),
		),
	)
	first := &model.Request{Messages: []model.Message{model.NewUserMessage("run")}}
	runBeforeModel(t, manager, ctx, first)

	excluded := repeatedRoundsRequest("poll", 2)
	runBeforeModel(t, manager, ctx, excluded)
	require.False(t, hasWarning(excluded.Messages, customWarning))

	included := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, included)
	require.True(t, hasWarning(included.Messages, customWarning))
}

func TestPluginStopsBeforeRepeatedToolBundle(t *testing.T) {
	manager, _, ctx := newCallbackHarness(t, New(WithStopAfterWarning()))
	first := &model.Request{Messages: []model.Message{model.NewUserMessage("run")}}
	runBeforeModel(t, manager, ctx, first)

	request := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, request)
	require.True(t, hasWarning(request.Messages, defaultWarning))

	response := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: assistantToolMessage(
				newToolCall("new-id", "search", ` { "limit": 1, "query": "x" } `),
			),
		}},
	}
	err := manager.RunBeforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: response},
	)
	require.Error(t, err)
	stopErr, ok := agent.AsStopError(err)
	require.True(t, ok)
	require.Contains(t, stopErr.Error(), "fingerprint")

	err = manager.RunBeforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: response},
	)
	require.NoError(t, err)
}

func TestPluginBeforeResponseDispatchFailOpenAndUsesDelta(t *testing.T) {
	plugin := &toolLoopWarningPlugin{stopAfterWarning: true}
	var nilPlugin *toolLoopWarningPlugin
	err := nilPlugin.beforeResponseDispatch(context.Background(), nil)
	require.NoError(t, err)
	err = plugin.beforeResponseDispatch(context.Background(), nil)
	require.NoError(t, err)
	err = plugin.beforeResponseDispatch(
		context.Background(),
		&pluginbase.BeforeResponseDispatchArgs{Response: &model.Response{IsPartial: true}},
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	response := &model.Response{Done: true}
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: response},
	)
	require.NoError(t, err)

	state := &detectorState{}
	invocation.SetState(stateKey, state)
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: response},
	)
	require.NoError(t, err)

	state.armedFingerprint = "armed"
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: response},
	)
	require.NoError(t, err)

	state.armedFingerprint = "armed"
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: &model.Response{
			IsPartial: true,
		}},
	)
	require.NoError(t, err)
	require.Equal(t, "armed", state.armedFingerprint)

	state.armedFingerprint = "armed"
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: response},
	)
	require.NoError(t, err)

	state.armedFingerprint = "armed"
	invalid := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: assistantToolMessage(newToolCall("id", "", `{}`))}},
	}
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: invalid},
	)
	require.NoError(t, err)

	state.armedFingerprint = "armed"
	different := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: assistantToolMessage(newToolCall("id", "other", `{}`))}},
	}
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: different},
	)
	require.NoError(t, err)

	call := newToolCall("id", "search", ` { "query": "x" } `)
	state.armedFingerprint, _ = fingerprintToolCalls([]model.ToolCall{call})
	delta := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Delta: assistantToolMessage(call)}},
	}
	err = plugin.beforeResponseDispatch(
		ctx,
		&pluginbase.BeforeResponseDispatchArgs{Response: delta},
	)
	stopErr, ok := agent.AsStopError(err)
	require.True(t, ok)
	require.Contains(t, stopErr.Error(), "fingerprint")
}

func TestPluginHandlesNilInputsAndMissingInvocation(t *testing.T) {
	var nilPlugin *toolLoopWarningPlugin
	require.Empty(t, nilPlugin.Name())
	nilPlugin.Register(nil)
	_, err := nilPlugin.beforeAgent(context.Background(), nil)
	require.NoError(t, err)
	_, err = nilPlugin.beforeModel(context.Background(), nil)
	require.NoError(t, err)
	_, err = nilPlugin.afterAgent(context.Background(), nil)
	require.NoError(t, err)

	plugin := &toolLoopWarningPlugin{warning: defaultWarning}
	plugin.Register(nil)
	plugin.Register(&pluginbase.Registry{})
	_, err = plugin.beforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	request := repeatedRoundsRequest("search", 2)
	_, err = plugin.beforeModel(
		ctx,
		&model.BeforeModelArgs{Request: request},
	)
	require.NoError(t, err)
	require.False(t, hasWarning(request.Messages, defaultWarning))
}

func TestPluginRunnerIntegrationRequestLocal(t *testing.T) {
	for _, perCall := range []bool{false, true} {
		name := "aggregate"
		if perCall {
			name = "per_call"
		}
		t.Run(name, func(t *testing.T) {
			run := runRepeatedRound(t, repeatedRunConfig{
				warningEnabled: true,
				perCallResults: perCall,
			})
			requests := run.model.Requests()
			require.Len(t, requests, 3)
			require.False(t, hasWarning(requests[0], defaultWarning))
			require.False(t, hasWarning(requests[1], defaultWarning))
			require.True(t, hasWarning(requests[2], defaultWarning))
			require.Equal(t, int32(2), run.slowCalls.Load())
			require.Equal(t, int32(2), run.fastCalls.Load())
			assertSessionHasNoWarning(t, run.sessionService, defaultWarning)

			events, err := run.runner.Run(
				context.Background(),
				"user",
				"session",
				model.NewUserMessage("continue"),
			)
			require.NoError(t, err)
			for range events {
			}
			requests = run.model.Requests()
			require.Len(t, requests, 4)
			require.False(t, hasWarning(requests[3], defaultWarning))
			assertSessionHasNoWarning(t, run.sessionService, defaultWarning)
		})
	}
}

func TestPluginRunnerIntegrationUsesTransformedRequestResults(t *testing.T) {
	run := runRepeatedRound(t, repeatedRunConfig{
		warningEnabled:   true,
		perCallResults:   true,
		varyRawResults:   true,
		transformResults: true,
	})
	requests := run.model.Requests()
	require.Len(t, requests, 3)
	require.True(t, hasWarning(requests[2], defaultWarning))
	require.Equal(
		t,
		[]string{"visible:slow", "visible:fast"},
		lastToolResultContents(requests[2], 2),
	)
}

func TestPluginRunnerIntegrationTraceCapturesRequestLocalWarning(t *testing.T) {
	run := runRepeatedRound(t, repeatedRunConfig{
		warningEnabled:        true,
		executionTraceEnabled: true,
	})
	require.True(t, traceContainsWarning(run.traceInputs, defaultWarning))
	assertSessionHasNoWarning(t, run.sessionService, defaultWarning)
}

func TestPluginRunnerIntegrationDisabledOrExcluded(t *testing.T) {
	tests := map[string]repeatedRunConfig{
		"disabled": {
			warningEnabled: false,
		},
		"excluded": {
			warningEnabled: true,
			excludedTools:  []string{"slow"},
		},
	}
	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			run := runRepeatedRound(t, config)
			requests := run.model.Requests()
			require.Len(t, requests, 3)
			require.False(t, hasWarning(requests[2], defaultWarning))
			assertSessionHasNoWarning(t, run.sessionService, defaultWarning)
		})
	}
}

func TestPluginRunnerIntegrationStopsBeforeThirdToolBundle(t *testing.T) {
	run := runRepeatedRound(t, repeatedRunConfig{
		warningEnabled:   true,
		stopAfterWarning: true,
	})
	requests := run.model.Requests()
	require.Len(t, requests, 3)
	require.True(t, hasWarning(requests[2], defaultWarning))
	require.Equal(t, int32(2), run.slowCalls.Load())
	require.Equal(t, int32(2), run.fastCalls.Load())
	assertStopAgentErrorEvent(t, run.events)
	assertSessionHasNoWarning(t, run.sessionService, defaultWarning)
}

func TestPluginRunnerIntegrationStopsAfterJSONRepair(t *testing.T) {
	run := runRepeatedRound(t, repeatedRunConfig{
		warningEnabled:      true,
		stopAfterWarning:    true,
		jsonRepairEnabled:   true,
		malformedThirdRound: true,
	})
	requests := run.model.Requests()
	require.Len(t, requests, 3)
	require.True(t, hasWarning(requests[2], defaultWarning))
	require.Equal(t, int32(2), run.slowCalls.Load())
	require.Equal(t, int32(2), run.fastCalls.Load())
	assertStopAgentErrorEvent(t, run.events)
	assertSessionHasNoWarning(t, run.sessionService, defaultWarning)
}

func TestPluginRunnerIntegrationStopsAfterTextRepair(t *testing.T) {
	run := runRepeatedRound(t, repeatedRunConfig{
		warningEnabled:    true,
		stopAfterWarning:  true,
		textRepairEnabled: true,
		textThirdRound:    true,
	})
	requests := run.model.Requests()
	require.Len(t, requests, 3)
	require.True(t, hasWarning(requests[2], defaultWarning))
	require.Equal(t, int32(2), run.slowCalls.Load())
	require.Equal(t, int32(2), run.fastCalls.Load())
	assertStopAgentErrorEvent(t, run.events)
	assertSessionHasNoWarning(t, run.sessionService, defaultWarning)
}

func TestPluginRunnerIntegrationCombinesRunnerAndRunResponseHooks(t *testing.T) {
	run := runRepeatedRound(t, repeatedRunConfig{
		warningEnabled:          true,
		stopAfterWarning:        true,
		runResponseDispatchHook: true,
	})
	require.Equal(t, int32(2), run.responseDispatchCalls.Load())
	require.Equal(t, int32(2), run.slowCalls.Load())
	require.Equal(t, int32(2), run.fastCalls.Load())
	assertStopAgentErrorEvent(t, run.events)
}

func TestPluginRunnerIntegrationCombinesRunAndRunnerResponseHooks(t *testing.T) {
	run := runRepeatedRound(t, repeatedRunConfig{
		warningEnabled:                  true,
		stopAfterWarning:                true,
		runResponseDispatchHook:         true,
		responseDispatchHookRunnerLevel: true,
		guardRunLevel:                   true,
	})
	require.Equal(t, int32(3), run.responseDispatchCalls.Load())
	require.Equal(t, int32(2), run.slowCalls.Load())
	require.Equal(t, int32(2), run.fastCalls.Load())
	assertStopAgentErrorEvent(t, run.events)
}

func assertStopAgentErrorEvent(t *testing.T, events []*event.Event) {
	t.Helper()
	for _, evt := range events {
		if evt == nil || evt.Response == nil || evt.Response.Error == nil {
			continue
		}
		require.Equal(t, agent.ErrorTypeStopAgentError, evt.Response.Error.Type)
		require.Contains(t, evt.Response.Error.Message, "tool loop guard stopped")
		return
	}
	t.Fatal("expected caller-visible stop_agent_error event")
}

func newCallbackHarness(
	t *testing.T,
	p pluginbase.Plugin,
) (*pluginbase.Manager, *agent.Invocation, context.Context) {
	t.Helper()
	manager, err := pluginbase.NewManager(p)
	require.NoError(t, err)
	invocation := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	_, err = manager.AgentCallbacks().RunBeforeAgent(
		ctx,
		&agent.BeforeAgentArgs{Invocation: invocation},
	)
	require.NoError(t, err)
	return manager, invocation, ctx
}

func runBeforeModel(
	t *testing.T,
	manager *pluginbase.Manager,
	ctx context.Context,
	request *model.Request,
) {
	t.Helper()
	_, err := manager.ModelCallbacks().RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: request},
	)
	require.NoError(t, err)
}

func repeatedRoundsRequest(toolName string, count int) *model.Request {
	messages := []model.Message{model.NewUserMessage("run")}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("call-%d", i+1)
		arguments := `{"query":"x","limit":1}`
		if i%2 == 1 {
			arguments = ` { "limit": 1, "query": "x" } `
		}
		messages = append(messages, roundMessages(
			[]model.ToolCall{newToolCall(id, toolName, arguments)},
			[]model.Message{model.NewToolMessage(id, toolName, "same")},
		)...)
	}
	return &model.Request{Messages: messages}
}

func changedTrailingRoundRequest() *model.Request {
	request := repeatedRoundsRequest("search", 1)
	request.Messages = append(request.Messages, roundMessages(
		[]model.ToolCall{newToolCall("call-changed", "search", `{"query":"y"}`)},
		[]model.Message{model.NewToolMessage(
			"call-changed",
			"search",
			"same",
		)},
	)...)
	return request
}

func removeWarning(request *model.Request, warning string) {
	if request == nil {
		return
	}
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if !isWarningMessage(request.Messages[i], warning) {
			continue
		}
		request.Messages = append(request.Messages[:i], request.Messages[i+1:]...)
		return
	}
}

func hasWarning(messages []model.Message, warning string) bool {
	return countWarningMessages(messages, warning) > 0
}

func countWarningMessages(messages []model.Message, warning string) int {
	count := 0
	for _, message := range messages {
		if isWarningMessage(message, warning) {
			count++
		}
	}
	return count
}

type repeatedRoundModel struct {
	mu                  sync.Mutex
	requests            [][]model.Message
	repeatThirdRound    bool
	malformedThirdRound bool
	textThirdRound      bool
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
	if callIndex < 2 || (m.repeatThirdRound && callIndex == 2) {
		suffix := callIndex + 1
		arguments := `{"value":"same"}`
		if callIndex == 1 {
			arguments = ` { "value": "same" } `
		}
		if callIndex == 2 && m.malformedThirdRound {
			arguments = `{"value":"same",}`
		}
		if callIndex == 2 && m.textThirdRound {
			response = &model.Response{
				ID:   fmt.Sprintf("tool-response-%d", suffix),
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(
						"<tool_call>slow" +
							"<arg_key>value</arg_key>" +
							"<arg_value>same</arg_value>" +
							"</tool_call>" +
							"<tool_call>fast" +
							"<arg_key>value</arg_key>" +
							"<arg_value>same</arg_value>" +
							"</tool_call>",
					),
				}},
			}
		} else {
			response = &model.Response{
				ID:   fmt.Sprintf("tool-response-%d", suffix),
				Done: true,
				Choices: []model.Choice{{
					Message: assistantToolMessage(
						newToolCall(fmt.Sprintf("call-slow-%d", suffix), "slow", arguments),
						newToolCall(fmt.Sprintf("call-fast-%d", suffix), "fast", arguments),
					),
				}},
			}
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

type repeatedRunConfig struct {
	warningEnabled                  bool
	stopAfterWarning                bool
	jsonRepairEnabled               bool
	malformedThirdRound             bool
	textRepairEnabled               bool
	textThirdRound                  bool
	executionTraceEnabled           bool
	perCallResults                  bool
	varyRawResults                  bool
	transformResults                bool
	runResponseDispatchHook         bool
	responseDispatchHookRunnerLevel bool
	guardRunLevel                   bool
	excludedTools                   []string
}

type repeatedRun struct {
	model                 *repeatedRoundModel
	slowCalls             *atomic.Int32
	fastCalls             *atomic.Int32
	sessionService        *sessioninmemory.SessionService
	runner                runner.Runner
	traceInputs           []string
	events                []*event.Event
	responseDispatchCalls *atomic.Int32
}

func runRepeatedRound(t *testing.T, config repeatedRunConfig) repeatedRun {
	t.Helper()
	fastDone := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var slowCalls atomic.Int32
	var fastCalls atomic.Int32
	slowTool := function.NewFunctionTool(
		func(ctx context.Context, _ parallelInput) (string, error) {
			index := int(slowCalls.Add(1) - 1)
			if index >= len(fastDone) {
				return "", fmt.Errorf("unexpected slow tool call %d", index+1)
			}
			select {
			case <-fastDone[index]:
				if config.varyRawResults {
					return fmt.Sprintf("slow-raw-%d", index+1), nil
				}
				return "slow", nil
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
			close(fastDone[index])
			if config.varyRawResults {
				return fmt.Sprintf("fast-raw-%d", index+1), nil
			}
			return "fast", nil
		},
		function.WithName("fast"),
		function.WithDescription("Returns immediately."),
	)
	modelStub := &repeatedRoundModel{
		repeatThirdRound:    config.stopAfterWarning,
		malformedThirdRound: config.malformedThirdRound,
		textThirdRound:      config.textThirdRound,
	}
	agentInstance := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{slowTool, fastTool}),
		llmagent.WithEnableParallelTools(true),
	)
	sessionService := sessioninmemory.NewSessionService()
	runnerOptions := []runner.Option{runner.WithSessionService(sessionService)}
	var responseDispatchCalls atomic.Int32
	var responseDispatchObserver pluginbase.Plugin
	var warningPluginOptions []Option
	if config.runResponseDispatchHook {
		responseDispatchObserver = newResponseDispatchObserver(&responseDispatchCalls)
	}
	if config.warningEnabled {
		warningPluginOptions = []Option{}
		if config.stopAfterWarning {
			warningPluginOptions = append(warningPluginOptions, WithStopAfterWarning())
		}
		if len(config.excludedTools) > 0 {
			warningPluginOptions = append(
				warningPluginOptions,
				WithExcludedToolNames(config.excludedTools...),
			)
		}
		if !config.guardRunLevel {
			runnerOptions = append(
				runnerOptions,
				runner.WithPlugins(New(warningPluginOptions...)),
			)
		}
	}
	if config.runResponseDispatchHook && config.responseDispatchHookRunnerLevel {
		runnerOptions = append(
			runnerOptions,
			runner.WithPlugins(responseDispatchObserver),
		)
	}
	runnerInstance := runner.NewRunner(
		"tool-loop-warning-app",
		agentInstance,
		runnerOptions...,
	)
	t.Cleanup(func() {
		require.NoError(t, runnerInstance.Close())
		require.NoError(t, sessionService.Close())
	})
	var runOptions []agent.RunOption
	if config.perCallResults {
		runOptions = append(
			runOptions,
			agent.WithToolResultEventPerCallEnabled(true),
		)
	}
	if config.executionTraceEnabled {
		runOptions = append(
			runOptions,
			agent.WithExecutionTraceEnabled(true),
		)
	}
	if config.jsonRepairEnabled {
		runOptions = append(
			runOptions,
			agent.WithToolCallArgumentsJSONRepairEnabled(true),
		)
	}
	if config.textRepairEnabled {
		runOptions = append(
			runOptions,
			agent.WithToolCallTextRepairEnabled(true),
		)
	}
	if config.transformResults {
		runOptions = append(
			runOptions,
			pluginbase.WithPlugins(newVisibleResultTransformer()),
		)
	}
	if config.runResponseDispatchHook && !config.responseDispatchHookRunnerLevel {
		runOptions = append(
			runOptions,
			pluginbase.WithPlugins(newResponseDispatchObserver(&responseDispatchCalls)),
		)
	}
	if config.warningEnabled && config.guardRunLevel {
		runOptions = append(
			runOptions,
			pluginbase.WithPlugins(New(warningPluginOptions...)),
		)
	}
	events, err := runnerInstance.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("run tools"),
		runOptions...,
	)
	require.NoError(t, err)
	var traceInputs []string
	var runEvents []*event.Event
	for evt := range events {
		if evt != nil {
			runEvents = append(runEvents, evt)
		}
		if evt == nil || evt.ExecutionTrace == nil {
			continue
		}
		for _, step := range evt.ExecutionTrace.Steps {
			if step.Input != nil {
				traceInputs = append(traceInputs, step.Input.Text)
			}
		}
	}
	return repeatedRun{
		model:                 modelStub,
		slowCalls:             &slowCalls,
		fastCalls:             &fastCalls,
		sessionService:        sessionService,
		runner:                runnerInstance,
		traceInputs:           traceInputs,
		events:                runEvents,
		responseDispatchCalls: &responseDispatchCalls,
	}
}

type testPlugin struct {
	name     string
	register func(*pluginbase.Registry)
}

func (p *testPlugin) Name() string {
	return p.name
}

func (p *testPlugin) Register(registry *pluginbase.Registry) {
	if p != nil && p.register != nil {
		p.register(registry)
	}
}

func newVisibleResultTransformer() pluginbase.Plugin {
	return &testPlugin{
		name: "visible-result-transformer",
		register: func(registry *pluginbase.Registry) {
			registry.AfterToolMessages(func(
				_ context.Context,
				args *pluginbase.AfterToolMessagesArgs,
			) (*pluginbase.AfterToolMessagesResult, error) {
				if args == nil || len(args.ToolResultMessages) == 0 {
					return nil, nil
				}
				messages := cloneMessages(args.ToolResultMessages)
				for i := range messages {
					messages[i].Content = "visible:" + messages[i].ToolName
				}
				return &pluginbase.AfterToolMessagesResult{
					ToolResultMessages: messages,
				}, nil
			})
		},
	}
}

func newResponseDispatchObserver(calls *atomic.Int32) pluginbase.Plugin {
	return &testPlugin{
		name: "response-dispatch-observer",
		register: func(registry *pluginbase.Registry) {
			registry.BeforeResponseDispatch(func(
				context.Context,
				*pluginbase.BeforeResponseDispatchArgs,
			) error {
				calls.Add(1)
				return nil
			})
		},
	}
}

func assertSessionHasNoWarning(
	t *testing.T,
	service *sessioninmemory.SessionService,
	warning string,
) {
	t.Helper()
	sess, err := service.GetSession(context.Background(), session.Key{
		AppName:   "tool-loop-warning-app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	for _, event := range sess.GetEvents() {
		if event.Response == nil {
			continue
		}
		for _, choice := range event.Response.Choices {
			require.NotEqual(t, warning, choice.Message.Content)
			require.NotEqual(t, warning, choice.Delta.Content)
		}
	}
}

func lastToolResultContents(messages []model.Message, count int) []string {
	results := make([]string, 0, count)
	for i := len(messages) - 1; i >= 0 && len(results) < count; i-- {
		if messages[i].Role != model.RoleTool {
			continue
		}
		results = append(results, messages[i].Content)
	}
	for left, right := 0, len(results)-1; left < right; left, right = left+1, right-1 {
		results[left], results[right] = results[right], results[left]
	}
	return results
}

func traceContainsWarning(inputs []string, warning string) bool {
	for _, input := range inputs {
		var messages []model.Message
		if json.Unmarshal([]byte(input), &messages) == nil &&
			hasWarning(messages, warning) {
			return true
		}
	}
	return false
}

func cloneMessages(messages []model.Message) []model.Message {
	cloned := make([]model.Message, len(messages))
	copy(cloned, messages)
	for i := range cloned {
		cloned[i].ContentParts = append(
			[]model.ContentPart(nil),
			messages[i].ContentParts...,
		)
		cloned[i].ToolCalls = append(
			[]model.ToolCall(nil),
			messages[i].ToolCalls...,
		)
		for j := range cloned[i].ToolCalls {
			cloned[i].ToolCalls[j].Function.Arguments = append(
				[]byte(nil),
				messages[i].ToolCalls[j].Function.Arguments...,
			)
		}
	}
	return cloned
}
