//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package usersimulation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type runnerCall struct {
	userID                  string
	sessionID               string
	message                 model.Message
	injectedContextMessages []model.Message
	runtimeState            map[string]any
}

type fakeSimRunner struct {
	responses     []string
	events        []*event.Event
	err           error
	suppressFinal bool
	calls         []runnerCall
}

func (f *fakeSimRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	var opts agent.RunOptions
	for _, opt := range runOpts {
		opt(&opts)
	}
	f.calls = append(f.calls, runnerCall{
		userID:                  userID,
		sessionID:               sessionID,
		message:                 message,
		injectedContextMessages: opts.InjectedContextMessages,
		runtimeState:            opts.RuntimeState,
	})
	bufferSize := 1
	if len(f.events) > 0 {
		bufferSize = len(f.events)
	}
	ch := make(chan *event.Event, bufferSize)
	if len(f.events) > 0 {
		for _, evt := range f.events {
			ch <- evt
		}
		close(ch)
		return ch, nil
	}
	content := ""
	if len(f.responses) != 0 {
		content = f.responses[0]
		f.responses = f.responses[1:]
	}
	if !f.suppressFinal {
		ch <- &event.Event{
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
	close(ch)
	return ch, nil
}

func (f *fakeSimRunner) Close() error {
	return nil
}

func intPtr(v int) *int {
	return &v
}

func makeStartRequest() *StartRequest {
	return &StartRequest{
		EvalCaseID: "case-1",
		Scenario: &evalset.ConversationScenario{
			ConversationPlan: "Ask for missing information, then finish.",
			StopSignal:       "</finished>",
		},
		InitialSession: &evalset.SessionInput{
			AppName: "demo-app",
			UserID:  "target-user",
			State:   map[string]any{"city": "shanghai"},
		},
		SessionID: "target-session",
	}
}

func TestNewValidation(t *testing.T) {
	sim, err := New(nil)
	assert.Error(t, err)
	assert.Nil(t, sim)
	sim, err = New(&fakeSimRunner{}, WithMaxAllowedInvocations(-1))
	assert.Error(t, err)
	assert.Nil(t, sim)
	sim, err = New(&fakeSimRunner{}, WithUserIDSupplier(nil))
	assert.Error(t, err)
	assert.Nil(t, sim)
	sim, err = New(&fakeSimRunner{}, WithSessionIDSupplier(nil))
	assert.Error(t, err)
	assert.Nil(t, sim)
	sim, err = New(&fakeSimRunner{}, WithSystemPromptBuilder(nil))
	assert.Error(t, err)
	assert.Nil(t, sim)
}

func TestNewIgnoresNilOption(t *testing.T) {
	assert.NotPanics(t, func() {
		sim, err := New(&fakeSimRunner{}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, sim)
	})
}

func TestDefaultSimulatorStartValidation(t *testing.T) {
	sim, err := New(&fakeSimRunner{})
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, conv)
	conv, err = sim.Start(context.Background(), &StartRequest{})
	assert.Error(t, err)
	assert.Nil(t, conv)
	req := makeStartRequest()
	req.InitialSession = nil
	conv, err = sim.Start(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, conv)
	req = makeStartRequest()
	req.Scenario.ConversationPlan = " "
	conv, err = sim.Start(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, conv)
	req = makeStartRequest()
	req.Scenario.StopSignal = ""
	req.Scenario.MaxAllowedInvocations = intPtr(0)
	conv, err = sim.Start(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, conv)
	req = makeStartRequest()
	req.Scenario.StopSignal = "   "
	req.Scenario.MaxAllowedInvocations = intPtr(0)
	conv, err = sim.Start(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, conv)
	sim, err = New(&fakeSimRunner{}, WithUserIDSupplier(func(ctx context.Context) string { return "" }))
	assert.NoError(t, err)
	conv, err = sim.Start(context.Background(), makeStartRequest())
	assert.Error(t, err)
	assert.Nil(t, conv)
	sim, err = New(&fakeSimRunner{}, WithSessionIDSupplier(func(ctx context.Context) string { return "" }))
	assert.NoError(t, err)
	conv, err = sim.Start(context.Background(), makeStartRequest())
	assert.Error(t, err)
	assert.Nil(t, conv)
}

func TestDefaultConversationStartingPromptAndRunnerGeneration(t *testing.T) {
	runner := &fakeSimRunner{responses: []string{"I need your travel date."}}
	sim, err := New(runner)
	assert.NoError(t, err)
	req := makeStartRequest()
	req.Scenario.StartingPrompt = "Help me book a train ticket."
	conv, err := sim.Start(context.Background(), req)
	assert.NoError(t, err)
	decision, err := conv.Next(context.Background(), &TurnRequest{})
	assert.NoError(t, err)
	assert.False(t, decision.Stop)
	if assert.NotNil(t, decision.Message) {
		assert.Equal(t, model.RoleUser, decision.Message.Role)
		assert.Equal(t, "Help me book a train ticket.", decision.Message.Content)
	}
	assert.Len(t, runner.calls, 0)
	decision, err = conv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "Which day do you want to travel?"},
	})
	assert.NoError(t, err)
	assert.False(t, decision.Stop)
	if assert.NotNil(t, decision.Message) {
		assert.Equal(t, "I need your travel date.", decision.Message.Content)
	}
	if assert.Len(t, runner.calls, 1) {
		assert.Equal(t, "Which day do you want to travel?", runner.calls[0].message.Content)
		assert.NotEmpty(t, runner.calls[0].userID)
		assert.NotEmpty(t, runner.calls[0].sessionID)
		assert.NotEqual(t, req.SessionID, runner.calls[0].sessionID)
		assert.Len(t, runner.calls[0].injectedContextMessages, 1)
		assert.Contains(t, runner.calls[0].injectedContextMessages[0].Content, req.Scenario.StartingPrompt)
		assert.Contains(t, runner.calls[0].injectedContextMessages[0].Content, req.Scenario.ConversationPlan)
		assert.Contains(t, runner.calls[0].injectedContextMessages[0].Content, req.Scenario.StopSignal)
		assert.Equal(t, map[string]any{"city": "shanghai"}, runner.calls[0].runtimeState)
	}
	req.InitialSession.State["city"] = "beijing"
	assert.Equal(t, map[string]any{"city": "shanghai"}, runner.calls[0].runtimeState)
}

func TestConversationUsesCustomUserIDAndSessionIDSuppliers(t *testing.T) {
	runner := &fakeSimRunner{responses: []string{"I need your destination."}}
	sim, err := New(
		runner,
		WithUserIDSupplier(func(ctx context.Context) string { return "sim-user" }),
		WithSessionIDSupplier(func(ctx context.Context) string { return "sim-session" }),
	)
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	decision, err := conv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "What route do you want?"},
	})
	assert.NoError(t, err)
	assert.False(t, decision.Stop)
	if assert.Len(t, runner.calls, 1) {
		assert.Equal(t, "sim-user", runner.calls[0].userID)
		assert.Equal(t, "sim-session", runner.calls[0].sessionID)
	}
}

func TestConversationUsesCustomSystemPromptBuilderWithEffectiveScenario(t *testing.T) {
	runner := &fakeSimRunner{responses: []string{"I need your destination."}}
	req := makeStartRequest()
	req.Scenario.Driver = evalset.ConversationScenarioDriverExpected
	req.Scenario.StartingPrompt = "Help me book a train ticket."
	receivedScenario := (*evalset.ConversationScenario)(nil)
	sim, err := New(
		runner,
		WithStopSignal("</override>"),
		WithMaxAllowedInvocations(3),
		WithSystemPromptBuilder(func(ctx context.Context, scenario *evalset.ConversationScenario) string {
			assert.NotNil(t, ctx)
			receivedScenario = scenario
			return "custom system prompt"
		}),
	)
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, receivedScenario)
	assert.Equal(t, evalset.ConversationScenarioDriverExpected, receivedScenario.Driver)
	assert.Equal(t, req.Scenario.StartingPrompt, receivedScenario.StartingPrompt)
	assert.Equal(t, req.Scenario.ConversationPlan, receivedScenario.ConversationPlan)
	assert.Equal(t, "</override>", receivedScenario.StopSignal)
	assert.NotNil(t, receivedScenario.MaxAllowedInvocations)
	assert.Equal(t, 3, *receivedScenario.MaxAllowedInvocations)
	decision, err := conv.Next(context.Background(), &TurnRequest{})
	assert.NoError(t, err)
	assert.False(t, decision.Stop)
	decision, err = conv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "What route do you want?"},
	})
	assert.NoError(t, err)
	assert.False(t, decision.Stop)
	if assert.Len(t, runner.calls, 1) {
		assert.Len(t, runner.calls[0].injectedContextMessages, 1)
		assert.Equal(t, "custom system prompt", runner.calls[0].injectedContextMessages[0].Content)
	}
}

func TestConversationUsesCustomSystemPromptBuilderWithResolvedDefaultDriver(t *testing.T) {
	runner := &fakeSimRunner{responses: []string{"I need your destination."}}
	receivedScenario := (*evalset.ConversationScenario)(nil)
	sim, err := New(
		runner,
		WithSystemPromptBuilder(func(ctx context.Context, scenario *evalset.ConversationScenario) string {
			assert.NotNil(t, ctx)
			receivedScenario = scenario
			return "custom system prompt"
		}),
	)
	assert.NoError(t, err)
	req := makeStartRequest()
	req.Scenario.Driver = ""
	conv, err := sim.Start(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, receivedScenario)
	assert.Equal(t, evalset.ConversationScenarioDriverActual, receivedScenario.Driver)
	decision, err := conv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "What route do you want?"},
	})
	assert.NoError(t, err)
	assert.False(t, decision.Stop)
}

func TestDefaultConversationWithoutStartingPromptGeneratesFirstTurnFromPlan(t *testing.T) {
	runner := &fakeSimRunner{responses: []string{"I want to book a train ticket for tomorrow morning."}}
	sim, err := New(runner)
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	decision, err := conv.Next(context.Background(), &TurnRequest{})
	assert.NoError(t, err)
	assert.False(t, decision.Stop)
	if assert.NotNil(t, decision.Message) {
		assert.Equal(t, model.RoleUser, decision.Message.Role)
		assert.Equal(t, "I want to book a train ticket for tomorrow morning.", decision.Message.Content)
	}
	if assert.Len(t, runner.calls, 1) {
		assert.Equal(t, "", runner.calls[0].message.Content)
		assert.Len(t, runner.calls[0].injectedContextMessages, 1)
		assert.Contains(t, runner.calls[0].injectedContextMessages[0].Content, "Ask for missing information, then finish.")
	}
}

func TestDefaultConversationStopSignalAndMaxAllowedInvocations(t *testing.T) {
	stopRunner := &fakeSimRunner{responses: []string{" \n</finished>\t "}}
	stopSim, err := New(stopRunner)
	assert.NoError(t, err)
	stopConv, err := stopSim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	stopDecision, err := stopConv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "I can help with that."},
	})
	assert.NoError(t, err)
	assert.True(t, stopDecision.Stop)
	assert.Nil(t, stopDecision.Message)
	substringRunner := &fakeSimRunner{responses: []string{"The stop signal is </finished>."}}
	substringSim, err := New(substringRunner)
	assert.NoError(t, err)
	substringConv, err := substringSim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	substringDecision, err := substringConv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "Please continue."},
	})
	assert.NoError(t, err)
	assert.False(t, substringDecision.Stop)
	if assert.NotNil(t, substringDecision.Message) {
		assert.Equal(t, "The stop signal is </finished>.", substringDecision.Message.Content)
	}
	maxRunner := &fakeSimRunner{responses: []string{"Please search trains for tomorrow."}}
	maxSim, err := New(maxRunner)
	assert.NoError(t, err)
	maxReq := makeStartRequest()
	maxReq.Scenario.StopSignal = ""
	maxReq.Scenario.MaxAllowedInvocations = intPtr(1)
	maxConv, err := maxSim.Start(context.Background(), maxReq)
	assert.NoError(t, err)
	maxDecision, err := maxConv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "What should I search for?"},
	})
	assert.NoError(t, err)
	assert.False(t, maxDecision.Stop)
	if assert.NotNil(t, maxDecision.Message) {
		assert.Equal(t, "Please search trains for tomorrow.", maxDecision.Message.Content)
	}
	maxDecision, err = maxConv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "Anything else?"},
	})
	assert.NoError(t, err)
	assert.True(t, maxDecision.Stop)
	assert.Len(t, maxRunner.calls, 1)
}

func TestDefaultConversationClose(t *testing.T) {
	sim, err := New(&fakeSimRunner{})
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	assert.NoError(t, conv.Close())
	decision, err := conv.Next(context.Background(), &TurnRequest{})
	assert.Error(t, err)
	assert.Nil(t, decision)
}

func TestCollectFinalResponseContentReturnsResponseError(t *testing.T) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &model.Response{Error: &model.ResponseError{Message: "boom"}}}
	close(ch)
	content, err := collectFinalResponseContent(ch)
	assert.Error(t, err)
	assert.Empty(t, content)
	assert.Contains(t, err.Error(), "boom")
}

func TestCollectFinalResponseContentUsesRunnerCompletionResponse(t *testing.T) {
	ch := make(chan *event.Event, 2)
	ch <- &event.Event{
		InvocationID: "child",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: "child output"},
				},
			},
		},
	}
	ch <- &event.Event{
		InvocationID: "root",
		Response: &model.Response{
			Done:   true,
			Object: model.ObjectTypeRunnerCompletion,
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: "root output"},
				},
			},
		},
	}
	close(ch)
	content, err := collectFinalResponseContent(ch)
	assert.NoError(t, err)
	assert.Equal(t, "root output", content)
}

func TestCollectFinalResponseContentFallsBackToRootInvocationAfterError(t *testing.T) {
	ch := make(chan *event.Event, 3)
	ch <- &event.Event{
		InvocationID: "root",
		Response: &model.Response{
			Error: &model.ResponseError{Message: "boom"},
		},
	}
	ch <- &event.Event{
		InvocationID: "root",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: "root output"},
				},
			},
		},
	}
	ch <- &event.Event{
		InvocationID: "root",
		Response: &model.Response{
			Done:   true,
			Object: model.ObjectTypeRunnerCompletion,
		},
	}
	close(ch)
	content, err := collectFinalResponseContent(ch)
	assert.Error(t, err)
	assert.Empty(t, content)
	assert.Contains(t, err.Error(), "boom")
}

func TestCollectFinalResponseContentRequiresFinalResponse(t *testing.T) {
	ch := make(chan *event.Event)
	close(ch)
	content, err := collectFinalResponseContent(ch)
	assert.Error(t, err)
	assert.Empty(t, content)
	assert.Contains(t, err.Error(), "final response is missing")
}

func TestDefaultConversationRequiresNonEmptyFinalResponseContent(t *testing.T) {
	sim, err := New(&fakeSimRunner{suppressFinal: true})
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	decision, err := conv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "reply"},
	})
	assert.Error(t, err)
	assert.Nil(t, decision)
	assert.Contains(t, err.Error(), "final response is missing")
}

func TestDefaultConversationUsesRootRunnerCompletionContent(t *testing.T) {
	sim, err := New(&fakeSimRunner{
		events: []*event.Event{
			{
				InvocationID: "child",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{
						{
							Message: model.Message{Role: model.RoleAssistant, Content: "child output"},
						},
					},
				},
			},
			{
				InvocationID: "root",
				Response: &model.Response{
					Done:   true,
					Object: model.ObjectTypeRunnerCompletion,
					Choices: []model.Choice{
						{
							Message: model.Message{Role: model.RoleAssistant, Content: "root output"},
						},
					},
				},
			},
		},
	})
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	decision, err := conv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "reply"},
	})
	assert.NoError(t, err)
	assert.NotNil(t, decision)
	if assert.NotNil(t, decision.Message) {
		assert.Equal(t, "root output", decision.Message.Content)
	}
}

func TestDefaultConversationRunnerError(t *testing.T) {
	sim, err := New(&fakeSimRunner{err: errors.New("boom")})
	assert.NoError(t, err)
	conv, err := sim.Start(context.Background(), makeStartRequest())
	assert.NoError(t, err)
	decision, err := conv.Next(context.Background(), &TurnRequest{
		LastTargetResponse: &model.Message{Role: model.RoleAssistant, Content: "reply"},
	})
	assert.Error(t, err)
	assert.Nil(t, decision)
}
