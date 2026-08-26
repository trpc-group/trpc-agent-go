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
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginbase "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestPluginWarnsOncePerUnchangedRequestTail(t *testing.T) {
	manager, invocation, ctx := newCallbackHarness(t, New())

	first := &model.Request{Messages: []model.Message{
		model.NewUserMessage("run"),
	}}
	runBeforeModel(t, manager, ctx, first)
	require.False(t, hasWarning(first.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, first)

	oneRound := repeatedRoundsRequest("search", 1)
	runBeforeModel(t, manager, ctx, oneRound)
	require.False(t, hasWarning(oneRound.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, oneRound)

	twoRounds := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, twoRounds)
	require.True(t, hasWarning(twoRounds.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, twoRounds)

	threeRounds := repeatedRoundsRequest("search", 3)
	runBeforeModel(t, manager, ctx, threeRounds)
	require.False(t, hasWarning(threeRounds.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, threeRounds)

	changed := changedTrailingRoundRequest()
	runBeforeModel(t, manager, ctx, changed)
	require.False(t, hasWarning(changed.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, changed)

	changedPair := repeatedRoundsRequest("read", 2)
	runBeforeModel(t, manager, ctx, changedPair)
	require.True(t, hasWarning(changedPair.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, changedPair)

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

func TestPluginAcknowledgesOnlyWarningsPresentAtAfterModel(t *testing.T) {
	manager, _, ctx := newCallbackHarness(t, New())
	first := &model.Request{Messages: []model.Message{model.NewUserMessage("run")}}
	runBeforeModel(t, manager, ctx, first)
	runAfterModel(t, manager, ctx, first)

	withoutAfterModel := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, withoutAfterModel)
	require.True(t, hasWarning(withoutAfterModel.Messages, defaultWarning))

	retried := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, retried)
	require.True(t, hasWarning(retried.Messages, defaultWarning))
	removeWarning(retried, defaultWarning)
	runAfterModel(t, manager, ctx, retried)

	retriedAgain := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, retriedAgain)
	require.True(t, hasWarning(retriedAgain.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, retriedAgain)

	delivered := repeatedRoundsRequest("search", 3)
	runBeforeModel(t, manager, ctx, delivered)
	require.False(t, hasWarning(delivered.Messages, defaultWarning))
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
	require.False(t, hasWarning(historicalLoop.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, historicalLoop)

	currentLoop := repeatedRoundsRequest("search", 2)
	runBeforeModel(t, manager, ctx, currentLoop)
	require.True(t, hasWarning(currentLoop.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, currentLoop)

	boundary := repeatedRoundsRequest("search", 2)
	boundary.Messages = append(boundary.Messages, model.NewUserMessage("continue"))
	runBeforeModel(t, manager, ctx, boundary)
	require.False(t, hasWarning(boundary.Messages, defaultWarning))
	runAfterModel(t, manager, ctx, boundary)

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

func TestPluginHandlesNilInputsAndMissingInvocation(t *testing.T) {
	var nilPlugin *toolLoopWarningPlugin
	require.Empty(t, nilPlugin.Name())
	nilPlugin.Register(nil)
	_, err := nilPlugin.beforeAgent(context.Background(), nil)
	require.NoError(t, err)
	_, err = nilPlugin.beforeModel(context.Background(), nil)
	require.NoError(t, err)
	_, err = nilPlugin.afterModel(context.Background(), nil)
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
	_, err = plugin.afterModel(
		context.Background(),
		&model.AfterModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
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

func runAfterModel(
	t *testing.T,
	manager *pluginbase.Manager,
	ctx context.Context,
	request *model.Request,
) {
	t.Helper()
	_, err := manager.ModelCallbacks().RunAfterModel(
		ctx,
		&model.AfterModelArgs{
			Request:  request,
			Response: &model.Response{Done: true},
		},
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
				Message: assistantToolMessage(
					newToolCall(fmt.Sprintf("call-slow-%d", suffix), "slow", arguments),
					newToolCall(fmt.Sprintf("call-fast-%d", suffix), "fast", arguments),
				),
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

type repeatedRunConfig struct {
	warningEnabled        bool
	executionTraceEnabled bool
	perCallResults        bool
	varyRawResults        bool
	transformResults      bool
	excludedTools         []string
}

type repeatedRun struct {
	model          *repeatedRoundModel
	slowCalls      *atomic.Int32
	fastCalls      *atomic.Int32
	sessionService *sessioninmemory.SessionService
	runner         runner.Runner
	traceInputs    []string
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
	modelStub := &repeatedRoundModel{}
	agentInstance := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{slowTool, fastTool}),
		llmagent.WithEnableParallelTools(true),
	)
	sessionService := sessioninmemory.NewSessionService()
	runnerOptions := []runner.Option{runner.WithSessionService(sessionService)}
	if config.warningEnabled {
		pluginOptions := []Option{}
		if len(config.excludedTools) > 0 {
			pluginOptions = append(
				pluginOptions,
				WithExcludedToolNames(config.excludedTools...),
			)
		}
		runnerOptions = append(
			runnerOptions,
			runner.WithPlugins(New(pluginOptions...)),
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
	if config.transformResults {
		runOptions = append(
			runOptions,
			pluginbase.WithPlugins(newVisibleResultTransformer()),
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
	for event := range events {
		if event == nil || event.ExecutionTrace == nil {
			continue
		}
		for _, step := range event.ExecutionTrace.Steps {
			if step.Input != nil {
				traceInputs = append(traceInputs, step.Input.Text)
			}
		}
	}
	return repeatedRun{
		model:          modelStub,
		slowCalls:      &slowCalls,
		fastCalls:      &fastCalls,
		sessionService: sessionService,
		runner:         runnerInstance,
		traceInputs:    traceInputs,
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
