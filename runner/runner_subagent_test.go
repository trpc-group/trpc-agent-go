//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// blockingSessionService wraps the in-memory session service and blocks
// AppendEvent for a specific author. It was originally used to simulate the
// scenario where the downstream GraphAgent starts reading session history
// before upstream events are fully persisted. The current implementation
// delegates directly to the underlying in-memory service while keeping the
// type for potential future extensions.
type blockingSessionService struct {
	*inmemory.SessionService
	blockAuthor string
	nodeEntered <-chan struct{}
}

func newBlockingSessionService(blockAuthor string, nodeEntered <-chan struct{}) *blockingSessionService {
	return &blockingSessionService{
		SessionService: inmemory.NewSessionService(),
		blockAuthor:    blockAuthor,
		nodeEntered:    nodeEntered,
	}
}

func (s *blockingSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	return s.SessionService.AppendEvent(ctx, sess, evt, opts...)
}

// testHistoryAgent is a simple agent that emits a single assistant message event.
type testHistoryAgent struct {
	name            string
	expectSubstring string
}

func (a *testHistoryAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *testHistoryAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *testHistoryAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *testHistoryAgent) Tools() []tool.Tool {
	return nil
}

func (a *testHistoryAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{
			Object:  model.ObjectTypeChatCompletion,
			Created: time.Now().Unix(),
			Model:   "test-model",
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: a.expectSubstring,
				},
			}},
		}
		evt := event.NewResponseEvent(inv.InvocationID, a.name, rsp)
		_ = agent.EmitEvent(ctx, inv, ch, evt)
	}()
	return ch, nil
}

// newTestGraphAgent creates a GraphAgent whose first node asserts that
// graph.StateKeyMessages contains at least one message whose content includes
// expectSubstring. It also notifies nodeEntered when the node starts so that
// tests can coordinate with session persistence timing.
func newTestGraphAgent(
	t *testing.T,
	name string,
	nodeEntered chan<- struct{},
	expectSubstring string,
) agent.Agent {
	t.Helper()

	schema := graph.NewStateSchema().
		AddField(graph.StateKeyMessages, graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("check", func(ctx context.Context, state graph.State) (any, error) {
			select {
			case nodeEntered <- struct{}{}:
			default:
			}

			raw, ok := state[graph.StateKeyMessages]
			if !ok {
				return nil, fmt.Errorf("messages not found in state")
			}
			msgs, ok := raw.([]model.Message)
			if !ok {
				return nil, fmt.Errorf("messages has wrong type")
			}
			for _, m := range msgs {
				if strings.Contains(m.Content, expectSubstring) {
					return graph.State{"status": "ok"}, nil
				}
			}
			return nil, fmt.Errorf("expected message containing %q not found", expectSubstring)
		}).
		SetEntryPoint("check").
		SetFinishPoint("check").
		Compile()
	require.NoError(t, err)

	ga, err := graphagent.New(name, g)
	require.NoError(t, err)
	return ga
}

// TestGraphAgent_ChainAgent_ShouldSeeUpstreamHistory expresses the desired behavior
// that when ChainAgent runs an upstream agent followed by a GraphAgent, the
// GraphAgent should see the upstream agent's message in graph.StateKeyMessages.
// With the current implementation, this test is expected to fail because
// GraphAgent can start before the upstream event is fully appended to the session.
func TestGraphAgent_ChainAgent_ShouldSeeUpstreamHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodeEntered := make(chan struct{}, 1)
	msg := "from-history-agent"
	graphChild := newTestGraphAgent(t, "graph-child", nodeEntered, msg)
	history := &testHistoryAgent{name: "history-agent", expectSubstring: msg}

	chain := chainagent.New("chain-parent",
		chainagent.WithSubAgents([]agent.Agent{history, graphChild}),
	)

	svc := newBlockingSessionService("history-agent", nodeEntered)
	r := runner.NewRunner("app-chain", chain, runner.WithSessionService(svc))

	evCh, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
	require.NoError(t, err)

	var graphErr *event.Event
	for ev := range evCh {
		if ev.Error != nil {
			graphErr = ev
			break
		}
		if ev.IsRunnerCompletion() {
			break
		}
	}

	require.Nil(t, graphErr)
}

// TestGraphAgent_CycleAgent_ShouldSeeUpstreamHistory mirrors the chain test but
// uses CycleAgent to orchestrate sub-agents. The expected behavior is the same:
// GraphAgent should see the upstream agent's message in graph.StateKeyMessages.
func TestGraphAgent_CycleAgent_ShouldSeeUpstreamHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodeEntered := make(chan struct{}, 1)
	msg := "from-history-agent"
	graphChild := newTestGraphAgent(t, "graph-child", nodeEntered, msg)
	history := &testHistoryAgent{name: "history-agent", expectSubstring: msg}

	cycle := cycleagent.New("cycle-parent",
		cycleagent.WithSubAgents([]agent.Agent{history, graphChild}),
		cycleagent.WithMaxIterations(1),
	)

	svc := newBlockingSessionService("history-agent", nodeEntered)
	r := runner.NewRunner("app-cycle", cycle, runner.WithSessionService(svc))

	evCh, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
	require.NoError(t, err)

	var graphErr *event.Event
	for ev := range evCh {
		if ev.Error != nil {
			graphErr = ev
			break
		}
		if ev.IsRunnerCompletion() {
			break
		}
	}

	require.Nil(t, graphErr, "GraphAgent should see upstream history without producing an error, got: %+v", graphErr)
}

// agentToolParentAgent is a parent agent that emits a response event and then
// calls a child GraphAgent via AgentTool within the same invocation/session.
// The AgentTool error is reported back through toolErrCh for assertions.
type agentToolParentAgent struct {
	child           agent.Agent
	toolErrCh       chan<- error
	expectSubstring string
}

func (a *agentToolParentAgent) Info() agent.Info {
	return agent.Info{Name: "parent-agent"}
}

func (a *agentToolParentAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *agentToolParentAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *agentToolParentAgent) Tools() []tool.Tool {
	return nil
}

func (a *agentToolParentAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)

		// Emit an upstream assistant message that should become part of session history.
		rsp := &model.Response{
			Object:  model.ObjectTypeChatCompletion,
			Created: time.Now().Unix(),
			Model:   "parent-model",
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: a.expectSubstring,
				},
			}},
		}
		upstream := event.NewResponseEvent(inv.InvocationID, "parent-agent", rsp)
		_ = agent.EmitEvent(ctx, inv, ch, upstream)

		// Call the child GraphAgent via AgentTool using the same invocation/session.
		at := agenttool.NewTool(a.child, agenttool.WithHistoryScope(agenttool.HistoryScopeParentBranch))
		toolCtx := agent.NewInvocationContext(ctx, inv)
		_, err := at.Call(toolCtx, []byte(`{"request":"ignored"}`))
		a.toolErrCh <- err
	}()
	return ch, nil
}

// TestGraphAgent_AgentTool_ShouldSeeParentHistory expresses the desired behavior
// that when a GraphAgent is invoked via AgentTool inside a parent agent, it
// should see the parent agent's prior message in graph.StateKeyMessages and
// therefore the tool call should succeed without error.
func TestGraphAgent_AgentTool_ShouldSeeParentHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodeEntered := make(chan struct{}, 1)
	msg := "from-parent-agent"
	graphChild := newTestGraphAgent(t, "graph-child", nodeEntered, msg)

	toolErrCh := make(chan error, 1)
	parent := &agentToolParentAgent{
		child:           graphChild,
		expectSubstring: msg,
		toolErrCh:       toolErrCh,
	}

	svc := newBlockingSessionService("parent-agent", nodeEntered)
	r := runner.NewRunner("app-tool", parent, runner.WithSessionService(svc))

	evCh, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
	require.NoError(t, err)

	// Drain events to ensure the run completes.
	for ev := range evCh {
		if ev.IsRunnerCompletion() {
			break
		}
	}

	var toolErr error
	select {
	case toolErr = <-toolErrCh:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for agent tool result")
	}

	require.NoError(t, toolErr, "GraphAgent tool call should see parent history and succeed")
}
