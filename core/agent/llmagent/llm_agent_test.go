package llmagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

func newDummyModel() model.Model {
	return openai.New("dummy-model", openai.Options{})
}

func TestLLMAgent_InfoAndTools(t *testing.T) {
	agt := New("test-agent",
		WithDescription("desc"),
		WithModel(newDummyModel()),
		WithTools([]tool.Tool{}),
	)
	info := agt.Info()
	if info.Name != "test-agent" || info.Description != "desc" {
		t.Errorf("unexpected agent info: %+v", info)
	}
	if len(agt.Tools()) != 0 {
		t.Errorf("expected no tools")
	}
}

func TestLLMAgent_SubAgents(t *testing.T) {
	sub := New("sub", WithDescription("subdesc"))
	agt := New("main", WithSubAgents([]agent.Agent{sub}))
	if len(agt.SubAgents()) != 1 {
		t.Errorf("expected 1 subagent")
	}
	if agt.FindSubAgent("sub") == nil {
		t.Errorf("FindSubAgent failed")
	}
	if agt.FindSubAgent("notfound") != nil {
		t.Errorf("FindSubAgent should return nil for missing")
	}
}

func TestLLMAgent_Run_BeforeAgentShortCircuit(t *testing.T) {
	// BeforeAgentCallback returns a custom response, should short-circuit.
	agentCallbacks := agent.NewAgentCallbacks()
	agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "short-circuit"},
			}},
		}, nil
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	ev := <-events
	if ev.Response == nil || len(ev.Response.Choices) == 0 || !strings.Contains(ev.Response.Choices[0].Message.Content, "short-circuit") {
		t.Errorf("expected short-circuit response, got %+v", ev.Response)
	}
}

func TestLLMAgent_Run_BeforeAgentError(t *testing.T) {
	agentCallbacks := agent.NewAgentCallbacks()
	agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, errors.New("fail")
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	_, err := agt.Run(context.Background(), inv)
	if err == nil || !strings.Contains(err.Error(), "before agent callback failed") {
		t.Errorf("expected before agent callback error, got %v", err)
	}
}

func TestLLMAgent_Run_AfterAgentCallback(t *testing.T) {
	// AfterAgentCallback should append a custom event after normal flow.
	agentCallbacks := agent.NewAgentCallbacks()
	agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
		return &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "after-cb"},
			}},
		}, nil
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	var found bool
	for ev := range events {
		if ev.Response != nil && len(ev.Response.Choices) > 0 && strings.Contains(ev.Response.Choices[0].Message.Content, "after-cb") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected after agent callback event")
	}
}

func TestLLMAgent_Run_NormalFlow(t *testing.T) {
	agt := New("test", WithModel(newDummyModel()))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// Should get at least one event (may be empty response)
	_, ok := <-events
	if !ok {
		t.Errorf("expected at least one event")
	}
}

func TestLLMAgent_Run_AfterAgentCallbackError(t *testing.T) {
	agentCallbacks := agent.NewAgentCallbacks()
	agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
		return nil, errors.New("after error")
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	var foundErr bool
	for ev := range events {
		if ev.Error != nil && strings.Contains(ev.Error.Message, "after error") {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Errorf("expected error event from after agent callback")
	}
}
