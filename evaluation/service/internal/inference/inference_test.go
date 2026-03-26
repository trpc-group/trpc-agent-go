//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeRunner struct {
	events                      []*event.Event
	eventRuns                   [][]*event.Event
	runErr                      error
	runCount                    int
	lastInjectedContextMessages []model.Message
	lastInstruction             string
	lastRuntimeState            map[string]any
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
	var opts agent.RunOptions
	for _, opt := range runOpts {
		opt(&opts)
	}
	f.lastInjectedContextMessages = opts.InjectedContextMessages
	f.lastInstruction = opts.Instruction
	f.lastRuntimeState = opts.RuntimeState
	currentEvents := f.events
	if len(f.eventRuns) > 0 && f.runCount < len(f.eventRuns) {
		currentEvents = f.eventRuns[f.runCount]
	}
	f.runCount++
	ch := make(chan *event.Event, len(currentEvents))
	for _, evt := range currentEvents {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (f fakeRunner) Close() error {
	return nil
}

type scenarioRunCall struct {
	userID                  string
	sessionID               string
	message                 model.Message
	injectedContextMessages []model.Message
}

type scenarioRunner struct {
	responses       []string
	executionTraces []*trace.Trace
	runErr          error
	suppressFinal   bool
	calls           []scenarioRunCall
}

func (s *scenarioRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if s.runErr != nil {
		return nil, s.runErr
	}
	var opts agent.RunOptions
	for _, opt := range runOpts {
		opt(&opts)
	}
	s.calls = append(s.calls, scenarioRunCall{
		userID:                  userID,
		sessionID:               sessionID,
		message:                 message,
		injectedContextMessages: opts.InjectedContextMessages,
	})
	eventCount := 0
	if !s.suppressFinal {
		eventCount++
	}
	if len(s.executionTraces) != 0 {
		eventCount++
	}
	ch := make(chan *event.Event, eventCount)
	if !s.suppressFinal {
		content := ""
		if len(s.responses) != 0 {
			content = s.responses[0]
			s.responses = s.responses[1:]
		}
		ch <- &event.Event{
			InvocationID: fmt.Sprintf("generated-%d", len(s.calls)),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{
					{
						Message: model.Message{Role: model.RoleAssistant, Content: content},
					},
				},
			},
		}
	}
	if len(s.executionTraces) != 0 {
		executionTrace := s.executionTraces[0]
		s.executionTraces = s.executionTraces[1:]
		ch <- &event.Event{
			InvocationID:   fmt.Sprintf("generated-%d", len(s.calls)),
			ExecutionTrace: executionTrace,
			Response: &model.Response{
				Object: model.ObjectTypeRunnerCompletion,
				Done:   true,
			},
		}
	}
	close(ch)
	return ch, nil
}

func (s *scenarioRunner) Close() error {
	return nil
}

type stubScenarioConversation struct {
	decisions []*usersimulation.Decision
	requests  []*usersimulation.TurnRequest
	nextErr   error
	closeErr  error
	closed    bool
}

func (s *stubScenarioConversation) Next(ctx context.Context, req *usersimulation.TurnRequest) (*usersimulation.Decision, error) {
	s.requests = append(s.requests, req)
	if s.nextErr != nil {
		return nil, s.nextErr
	}
	if len(s.decisions) == 0 {
		return &usersimulation.Decision{Stop: true}, nil
	}
	decision := s.decisions[0]
	s.decisions = s.decisions[1:]
	return decision, nil
}

func (s *stubScenarioConversation) Close() error {
	s.closed = true
	return s.closeErr
}

type stubScenarioSimulator struct {
	startReq     *usersimulation.StartRequest
	startErr     error
	conversation usersimulation.Conversation
}

func (s *stubScenarioSimulator) Start(ctx context.Context, req *usersimulation.StartRequest) (usersimulation.Conversation, error) {
	s.startReq = req
	if s.startErr != nil {
		return nil, s.startErr
	}
	return s.conversation, nil
}

func TestInferenceSuccess(t *testing.T) {
	// Arrange the test inputs.
	args, err := json.Marshal(map[string]any{"foo": "bar"})
	assert.NoError(t, err)
	toolEvent := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "tool-call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "lookup",
									Arguments: args,
								},
							},
						},
					},
				},
			},
		},
	}
	finalEvent := &event.Event{
		InvocationID: "generated-inv",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Content: "answer", Role: model.RoleAssistant}},
			},
		},
	}
	r := &fakeRunner{events: []*event.Event{toolEvent, finalEvent}}
	input := []*evalset.Invocation{
		{
			InvocationID: "input",
			UserContent: &model.Message{
				Role:    model.RoleUser,
				Content: "question",
			},
		},
	}
	session := &evalset.SessionInput{
		UserID: "user-1",
	}
	systemMsg := model.NewSystemMessage("You are a helpful assistant.")
	// Call the function under test.
	result, err := Inference(
		context.Background(),
		r,
		input,
		session,
		"session-1",
		[]agent.RunOption{
			agent.WithInjectedContextMessages([]model.Message{systemMsg}),
			agent.WithInstruction("test-instruction"),
		},
	)
	// Assert the results.
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Invocations, 1)
	assert.Equal(t, "generated-inv", result.Invocations[0].InvocationID)
	assert.Equal(t, input[0].UserContent, result.Invocations[0].UserContent)
	assert.NotNil(t, result.Invocations[0].FinalResponse)
	assert.Equal(t, "answer", result.Invocations[0].FinalResponse.Content)
	assert.Len(t, result.Invocations[0].Tools, 1)
	assert.Equal(t, "lookup", result.Invocations[0].Tools[0].Name)
	assert.Equal(t, map[string]any{"foo": "bar"}, result.Invocations[0].Tools[0].Arguments)
	assert.Len(t, result.ExecutionTraces, 1)
	assert.Nil(t, result.ExecutionTraces[0])
	assert.Equal(t, []model.Message{systemMsg}, r.lastInjectedContextMessages)
	assert.Equal(t, "test-instruction", r.lastInstruction)
}

func TestInferenceValidation(t *testing.T) {
	// It should reject missing invocations.
	_, err := Inference(context.Background(), &fakeRunner{}, nil, &evalset.SessionInput{}, "session", nil)
	assert.Error(t, err)
	// It should reject a missing session input.
	_, err = Inference(context.Background(), &fakeRunner{}, []*evalset.Invocation{
		{
			InvocationID: "inv",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question"},
		},
	}, nil, "session", nil)
	assert.Error(t, err)
	// It should surface runner errors.
	input := []*evalset.Invocation{
		{
			InvocationID: "input",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question"},
		},
	}
	_, err = Inference(context.Background(), &fakeRunner{runErr: errors.New("boom")}, input, &evalset.SessionInput{UserID: "user"}, "session", nil)
	assert.Error(t, err)
}

func TestInference_CollectsExecutionTracesInInvocationOrder(t *testing.T) {
	trace1 := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant-1"}
	trace2 := &trace.Trace{RootInvocationID: "root-2", RootAgentName: "assistant-2"}
	r := &fakeRunner{
		eventRuns: [][]*event.Event{
			{makeFinalEvent("answer-1"), makeRunnerCompletionEvent("generated-inv-1", trace1)},
			{makeFinalEvent("answer-2"), makeRunnerCompletionEvent("generated-inv-2", trace2)},
		},
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
		{
			InvocationID: "input-2",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-2"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Invocations, 2)
	assert.Equal(t, "generated-inv-1", result.Invocations[0].InvocationID)
	assert.Equal(t, "generated-inv-2", result.Invocations[1].InvocationID)
	require.Len(t, result.ExecutionTraces, 2)
	assert.Same(t, trace1, result.ExecutionTraces[0])
	assert.Same(t, trace2, result.ExecutionTraces[1])
}

func TestInference_PreservesExecutionTraceAlignmentWhenTraceMissing(t *testing.T) {
	trace1 := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant-1"}
	r := &fakeRunner{
		eventRuns: [][]*event.Event{
			{makeFinalEvent("answer-1"), makeRunnerCompletionEvent("generated-inv-1", trace1)},
			{makeFinalEvent("answer-2"), makeRunnerCompletionEvent("generated-inv-2", nil)},
		},
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
		{
			InvocationID: "input-2",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-2"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.ExecutionTraces, 2)
	assert.Same(t, trace1, result.ExecutionTraces[0])
	assert.Nil(t, result.ExecutionTraces[1])
}

func TestInference_PreservesFinalResponseFromCompletionEventWithError(t *testing.T) {
	executionTrace := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant"}
	r := &fakeRunner{
		events: []*event.Event{
			{
				InvocationID: "generated-inv",
				Response: &model.Response{
					Object: model.ObjectTypeRunnerCompletion,
					Done:   true,
					Error: &model.ResponseError{
						Message: "partial failure",
					},
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "best effort answer",
							},
						},
					},
				},
				ExecutionTrace: executionTrace,
			},
		},
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Invocations, 1)
	require.NotNil(t, result.Invocations[0].FinalResponse)
	assert.Equal(t, "best effort answer", result.Invocations[0].FinalResponse.Content)
	require.Len(t, result.ExecutionTraces, 1)
	assert.Same(t, executionTrace, result.ExecutionTraces[0])
}

func TestInference_ReturnsPartialResultAndTraceOnEventError(t *testing.T) {
	executionTrace := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant-1"}
	r := &fakeRunner{
		events: []*event.Event{
			makeFinalEvent("answer"),
			makeEventErrorEvent("boom"),
			makeRunnerCompletionEvent("generated-inv", executionTrace),
		},
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Invocations, 1)
	require.Len(t, result.ExecutionTraces, 1)
	assert.Same(t, executionTrace, result.ExecutionTraces[0])
	assert.Equal(t, "generated-inv", result.Invocations[0].InvocationID)
	assert.NotNil(t, result.Invocations[0].FinalResponse)
	var responseErr *model.ResponseError
	require.ErrorAs(t, err, &responseErr)
	require.NotNil(t, responseErr)
	assert.Equal(t, "boom", responseErr.Message)
	if result.Invocations[0].FinalResponse == nil {
		return
	}
	assert.Equal(t, "answer", result.Invocations[0].FinalResponse.Content)
}

func TestInference_ReturnsTraceWhenRunnerCompletionCarriesError(t *testing.T) {
	executionTrace := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant-1"}
	r := &fakeRunner{
		events: []*event.Event{
			makeFinalEvent("answer"),
			makeRunnerCompletionErrorEvent("generated-inv", executionTrace, "boom"),
		},
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Invocations, 1)
	require.Len(t, result.ExecutionTraces, 1)
	assert.Same(t, executionTrace, result.ExecutionTraces[0])
	require.NotNil(t, result.Invocations[0])
	assert.Equal(t, "generated-inv", result.Invocations[0].InvocationID)
}

func TestInference_PrefersRunnerCompletionInvocationID(t *testing.T) {
	executionTrace := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant-1"}
	r := &fakeRunner{
		events: []*event.Event{
			{
				InvocationID: "child-inv",
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "partial"}}},
				},
			},
			makeRunnerCompletionEvent("root-inv", executionTrace),
		},
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Invocations, 1)
	require.Len(t, result.ExecutionTraces, 1)
	require.NotNil(t, result.Invocations[0])
	assert.Equal(t, "root-inv", result.Invocations[0].InvocationID)
	assert.Same(t, executionTrace, result.ExecutionTraces[0])
}

func TestInference_PrefersRootFinalResponseOverChildFinalResponse(t *testing.T) {
	executionTrace := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant-1"}
	r := &fakeRunner{
		events: []*event.Event{
			{
				InvocationID: "root-inv",
				Response: &model.Response{
					Done:    true,
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "root answer"}}},
				},
			},
			{
				InvocationID: "child-inv",
				Response: &model.Response{
					Done:    true,
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "child answer"}}},
				},
			},
			makeRunnerCompletionEvent("root-inv", executionTrace),
		},
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Invocations, 1)
	require.NotNil(t, result.Invocations[0].FinalResponse)
	assert.Equal(t, "root answer", result.Invocations[0].FinalResponse.Content)
}

func TestInference_PreservesInvocationAlignmentWhenRunnerRunFailsMidway(t *testing.T) {
	baseRunner := &fakeRunner{
		eventRuns: [][]*event.Event{
			{makeFinalEvent("answer-1"), makeRunnerCompletionEvent("generated-inv-1", nil)},
		},
	}
	r := &failOnRunRunner{
		runner:    baseRunner,
		failRunAt: 2,
		runErr:    errors.New("boom"),
	}
	input := []*evalset.Invocation{
		{
			InvocationID: "input-1",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-1"},
		},
		{
			InvocationID: "input-2",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question-2"},
		},
	}
	session := &evalset.SessionInput{UserID: "user-1"}
	result, err := Inference(context.Background(), r, input, session, "session-1", nil)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Invocations, 1)
	require.Len(t, result.ExecutionTraces, 1)
	require.NotNil(t, result.Invocations[0])
	assert.Nil(t, result.ExecutionTraces[0])
}

func TestInferenceInvocationAppendsSessionRuntimeState(t *testing.T) {
	ctx := context.Background()
	sessionState := map[string]any{"from_session": "yes"}
	overrideState := map[string]any{"from_run_option": "yes"}
	session := &evalset.SessionInput{UserID: "user", State: sessionState}
	r := &fakeRunner{events: []*event.Event{makeFinalEvent("done")}}

	_, _, err := inferenceInvocation(ctx, r, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{Role: model.RoleUser, Content: "hi"},
	}, []agent.RunOption{agent.WithRuntimeState(overrideState)})

	assert.NoError(t, err)
	assert.Equal(t, sessionState, r.lastRuntimeState)
	assert.NotEqual(t, overrideState, r.lastRuntimeState)
}

func TestInferenceInvocationSkipsNilEvent(t *testing.T) {
	ctx := context.Background()
	session := &evalset.SessionInput{UserID: "user"}
	r := &fakeRunner{events: []*event.Event{nil, makeFinalEvent("ok")}}

	result, executionTrace, err := inferenceInvocation(ctx, r, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{Role: model.RoleUser, Content: "hi"},
	}, nil)

	assert.NoError(t, err)
	assert.Nil(t, executionTrace)
	assert.NotNil(t, result.FinalResponse)
	if result.FinalResponse == nil {
		return
	}
	assert.Equal(t, "ok", result.FinalResponse.Content)
}

func TestInferenceInvocationRejectsUnexpectedToolResultResponse(t *testing.T) {
	ctx := context.Background()
	session := &evalset.SessionInput{UserID: "user"}
	toolResultEvent := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:   "missing",
						ToolName: "tool",
						Content:  `{}`,
					},
				},
			},
		},
	}
	r := &fakeRunner{events: []*event.Event{toolResultEvent}}

	_, _, err := inferenceInvocation(ctx, r, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{Role: model.RoleUser, Content: "hi"},
	}, nil)

	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "convert tool result response")
	}
}

func TestInferenceInvocation_PreservesTraceOnToolResultMergeError(t *testing.T) {
	ctx := context.Background()
	session := &evalset.SessionInput{UserID: "user"}
	executionTrace := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant-1"}
	toolResultEvent := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:   "missing",
						ToolName: "tool",
						Content:  `{}`,
					},
				},
			},
		},
	}
	r := &fakeRunner{events: []*event.Event{toolResultEvent, makeRunnerCompletionEvent("generated-inv", executionTrace)}}

	result, gotTrace, err := inferenceInvocation(ctx, r, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{Role: model.RoleUser, Content: "hi"},
	}, nil)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "generated-inv", result.InvocationID)
	assert.Same(t, executionTrace, gotTrace)
	assert.Contains(t, err.Error(), "convert tool result response")
}

func TestInferenceInvocation_AggregatesEventErrorsAndPreservesArtifacts(t *testing.T) {
	ctx := context.Background()
	session := &evalset.SessionInput{UserID: "user"}
	executionTrace := &trace.Trace{RootInvocationID: "root-1", RootAgentName: "assistant"}
	r := &fakeRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Error: &model.ResponseError{Message: "first failure"},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleTool, ToolID: "missing-tool", Content: `{}`}}},
				},
			},
			{
				InvocationID: "generated-inv",
				Response: &model.Response{
					Object:  model.ObjectTypeRunnerCompletion,
					Done:    true,
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "best effort answer"}}},
				},
				ExecutionTrace: executionTrace,
			},
		},
	}
	result, gotTrace, err := inferenceInvocation(ctx, r, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{Role: model.RoleUser, Content: "ok"},
	}, nil)
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "generated-inv", result.InvocationID)
	require.NotNil(t, result.FinalResponse)
	assert.Equal(t, "best effort answer", result.FinalResponse.Content)
	assert.Same(t, executionTrace, gotTrace)
	assert.ErrorContains(t, err, "first failure")
	assert.ErrorContains(t, err, "convert tool result response")
	assert.ErrorContains(t, err, "missing-tool")
}

func TestInferencePerInvocationErrors(t *testing.T) {
	ctx := context.Background()
	session := &evalset.SessionInput{UserID: "user"}
	// It should reject invocations with missing user content.
	_, _, err := inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{}, nil)
	assert.Error(t, err)
	// It should handle an empty event stream.
	result, executionTrace, err := inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{},
	}, nil)
	assert.NoError(t, err)
	assert.Nil(t, executionTrace)
	assert.Nil(t, result.FinalResponse)
	// It should handle empty content parts.
	result, executionTrace, err = inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:         model.RoleUser,
			ContentParts: []model.ContentPart{{Text: ptr("")}},
		},
	}, nil)
	assert.NoError(t, err)
	assert.Nil(t, executionTrace)
	assert.Nil(t, result.FinalResponse)
	// It should return an error when an error event is received.
	errorEvent := &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "failed"},
		},
	}
	_, _, err = inferenceInvocation(ctx, &fakeRunner{events: []*event.Event{errorEvent}}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:    model.RoleUser,
			Content: "ok",
		},
	}, nil)
	assert.Error(t, err)
	// It should return an error when the runner fails.
	_, _, err = inferenceInvocation(ctx, &fakeRunner{runErr: errors.New("boom")}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:    model.RoleUser,
			Content: "ok",
		},
	}, nil)
	assert.Error(t, err)
	// This ensures the parent function validates the session input.
	_, err = Inference(ctx, &fakeRunner{}, []*evalset.Invocation{}, nil, "session", nil)
	assert.Error(t, err)
}

func TestInferenceWithConversationScenarioSuccess(t *testing.T) {
	systemMessage := model.NewSystemMessage("You are a helper.")
	initialSession := &evalset.SessionInput{UserID: "target-user", State: map[string]any{"region": "cn"}}
	trace1 := &trace.Trace{RootInvocationID: "generated-1"}
	trace2 := &trace.Trace{RootInvocationID: "generated-2"}
	conv := &stubScenarioConversation{
		decisions: []*usersimulation.Decision{
			{Message: &model.Message{Role: model.RoleUser, Content: "First user turn."}},
			{Message: &model.Message{Content: "Second user turn."}},
			{Stop: true},
		},
	}
	simulator := &stubScenarioSimulator{conversation: conv}
	runner := &scenarioRunner{
		responses:       []string{"Assistant reply 1.", "Assistant reply 2."},
		executionTraces: []*trace.Trace{trace1, trace2},
	}
	result, err := InferenceWithConversationScenario(
		context.Background(),
		runner,
		simulator,
		"case-1",
		&evalset.ConversationScenario{ConversationPlan: "Finish the booking."},
		initialSession,
		"session-1",
		[]agent.RunOption{agent.WithInjectedContextMessages([]model.Message{systemMessage})},
	)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Invocations, 2)
	require.Len(t, result.ExecutionTraces, 2)
	assert.Same(t, trace1, result.ExecutionTraces[0])
	assert.Same(t, trace2, result.ExecutionTraces[1])
	if assert.Len(t, runner.calls, 2) {
		assert.Equal(t, "target-user", runner.calls[0].userID)
		assert.Equal(t, "session-1", runner.calls[0].sessionID)
		assert.Equal(t, "First user turn.", runner.calls[0].message.Content)
		assert.Len(t, runner.calls[0].injectedContextMessages, 1)
		assert.Equal(t, "Second user turn.", runner.calls[1].message.Content)
	}
	if assert.NotNil(t, simulator.startReq) {
		assert.Equal(t, "case-1", simulator.startReq.EvalCaseID)
		assert.Equal(t, "session-1", simulator.startReq.SessionID)
		assert.Equal(t, initialSession, simulator.startReq.InitialSession)
	}
	assert.True(t, conv.closed)
	if assert.Len(t, conv.requests, 3) {
		assert.Nil(t, conv.requests[0].LastTargetResponse)
		if assert.NotNil(t, conv.requests[1].LastTargetResponse) {
			assert.Equal(t, "Assistant reply 1.", conv.requests[1].LastTargetResponse.Content)
		}
		if assert.NotNil(t, conv.requests[2].LastTargetResponse) {
			assert.Equal(t, "Assistant reply 2.", conv.requests[2].LastTargetResponse.Content)
		}
	}
	if assert.NotNil(t, result.Invocations[0].UserContent) {
		assert.Equal(t, "First user turn.", result.Invocations[0].UserContent.Content)
	}
	if assert.NotNil(t, result.Invocations[1].UserContent) {
		assert.Equal(t, "Second user turn.", result.Invocations[1].UserContent.Content)
	}
	if assert.NotNil(t, result.Invocations[0].FinalResponse) {
		assert.Equal(t, "Assistant reply 1.", result.Invocations[0].FinalResponse.Content)
	}
	if assert.NotNil(t, result.Invocations[1].FinalResponse) {
		assert.Equal(t, "Assistant reply 2.", result.Invocations[1].FinalResponse.Content)
	}
}

func TestInferenceWithConversationScenarioValidation(t *testing.T) {
	initialSession := &evalset.SessionInput{UserID: "target-user"}
	scenario := &evalset.ConversationScenario{ConversationPlan: "Continue until done."}
	systemMessage := model.NewSystemMessage("system")
	_, err := InferenceWithConversationScenario(context.Background(), nil, &stubScenarioSimulator{}, "case", scenario, initialSession, "session", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "runner is nil")
	_, err = InferenceWithConversationScenario(context.Background(), &scenarioRunner{}, nil, "case", scenario, initialSession, "session", nil)
	assert.Error(t, err)
	_, err = InferenceWithConversationScenario(context.Background(), &scenarioRunner{}, &stubScenarioSimulator{}, "case", nil, initialSession, "session", nil)
	assert.Error(t, err)
	_, err = InferenceWithConversationScenario(context.Background(), &scenarioRunner{}, &stubScenarioSimulator{}, "case", scenario, nil, "session", nil)
	assert.Error(t, err)
	_, err = InferenceWithConversationScenario(context.Background(), &scenarioRunner{}, &stubScenarioSimulator{conversation: &stubScenarioConversation{decisions: []*usersimulation.Decision{nil}}}, "case", scenario, initialSession, "session", nil)
	assert.Error(t, err)
	_, err = InferenceWithConversationScenario(context.Background(), &scenarioRunner{}, &stubScenarioSimulator{conversation: &stubScenarioConversation{decisions: []*usersimulation.Decision{{Message: nil}}}}, "case", scenario, initialSession, "session", nil)
	assert.Error(t, err)
	_, err = InferenceWithConversationScenario(context.Background(), &scenarioRunner{}, &stubScenarioSimulator{conversation: &stubScenarioConversation{decisions: []*usersimulation.Decision{{Message: &model.Message{Role: model.RoleAssistant, Content: "bad"}}}}}, "case", scenario, initialSession, "session", nil)
	assert.Error(t, err)
	_, err = InferenceWithConversationScenario(
		context.Background(),
		&scenarioRunner{suppressFinal: true},
		&stubScenarioSimulator{conversation: &stubScenarioConversation{decisions: []*usersimulation.Decision{{Message: &model.Message{Content: "hello"}}}}},
		"case",
		scenario,
		initialSession,
		"session",
		[]agent.RunOption{agent.WithInjectedContextMessages([]model.Message{systemMessage})},
	)
	assert.Error(t, err)
}

func TestConvertToolCallResponse(t *testing.T) {
	// Arrange the test inputs.
	args, err := json.Marshal(map[string]any{"count": 1})
	assert.NoError(t, err)
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "tool",
									Arguments: args,
								},
							},
						},
					},
				},
			},
		},
	}
	// Call the function under test.
	result, err := convertTools(ev)
	// Assert the results.
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "tool", result[0].Name)
	assert.Equal(t, map[string]any{"count": float64(1)}, result[0].Arguments)
}

func TestConvertToolCallResponseArrayArguments(t *testing.T) {
	args, err := json.Marshal([]any{1, 2})
	assert.NoError(t, err)
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "tool",
									Arguments: args,
								},
							},
						},
					},
				},
			},
		},
	}
	result, err := convertTools(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, []any{float64(1), float64(2)}, result[0].Arguments)
}

func TestConvertToolCallResponseInvalidJSONArguments(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "tool",
									Arguments: []byte("a=1"),
								},
							},
						},
					},
				},
			},
		},
	}
	result, err := convertTools(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "a=1", result[0].Arguments)
}

func TestConvertToolCallResponseEmptyArguments(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "tool",
									Arguments: []byte(" "),
								},
							},
						},
					},
				},
			},
		},
	}

	result, err := convertTools(ev)

	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, map[string]any{}, result[0].Arguments)
}

func TestMergeToolResultResponse(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:   "call-1",
						ToolName: "tool",
						Content:  `{"result":42}`,
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{
		{ID: "call-1", Name: "tool"},
	}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"result": float64(42)}, tools[0].Result)
}

func TestMergeToolResultResponseMissingID(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:   "missing",
						ToolName: "tool",
						Content:  `{}`,
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1", Name: "tool"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.Error(t, err)
}

func TestMergeToolResultResponseInvalidJSON(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:  "call-1",
						Content: "{",
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, "{", tools[0].Result)
}

func TestMergeToolResultResponseStringContent(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:  "call-1",
						Content: "tool execution failed",
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, "tool execution failed", tools[0].Result)
}

func TestMergeToolResultResponseArrayContent(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:  "call-1",
						Content: `[1,2]`,
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, []any{float64(1), float64(2)}, tools[0].Result)
}

func ptr[T any](v T) *T {
	return &v
}

func makeFinalEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Content: content, Role: model.RoleAssistant}},
			},
		},
	}
}

func makeRunnerCompletionEvent(invocationID string, executionTrace *trace.Trace) *event.Event {
	return &event.Event{
		InvocationID:   invocationID,
		ExecutionTrace: executionTrace,
		Response: &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
	}
}

func makeRunnerCompletionErrorEvent(invocationID string, executionTrace *trace.Trace, message string) *event.Event {
	return &event.Event{
		InvocationID:   invocationID,
		ExecutionTrace: executionTrace,
		Response: &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error: &model.ResponseError{
				Message: message,
				Type:    model.ErrorTypeAPIError,
			},
		},
	}
}

func makeEventErrorEvent(message string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: message, Type: model.ErrorTypeAPIError},
		},
	}
}

type failOnRunRunner struct {
	runner    runnerLike
	failRunAt int
	runCount  int
	runErr    error
}

type runnerLike interface {
	Run(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *event.Event, error)
	Close() error
}

func (f *failOnRunRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	f.runCount++
	if f.failRunAt > 0 && f.runCount == f.failRunAt {
		return nil, f.runErr
	}
	return f.runner.Run(ctx, userID, sessionID, message, runOpts...)
}

func (f *failOnRunRunner) Close() error {
	return f.runner.Close()
}
