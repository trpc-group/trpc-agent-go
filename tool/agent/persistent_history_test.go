//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type persistentHistoryTestAgent struct {
	name string

	mu       sync.Mutex
	call     int
	seenKeys []string
}

func (a *persistentHistoryTestAgent) Run(
	_ context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.call++
	call := a.call
	a.seenKeys = append(a.seenKeys, inv.GetEventFilterKey())
	a.mu.Unlock()

	fk := inv.GetEventFilterKey()
	var prev []string
	if inv.Session != nil {
		for _, evt := range inv.Session.Events {
			if evt.FilterKey != fk || evt.Response == nil || len(evt.Response.Choices) == 0 {
				continue
			}
			msg := evt.Response.Choices[0].Message
			if msg.Role == model.RoleAssistant && msg.Content != "" {
				prev = append(prev, msg.Content)
			}
		}
	}

	content := fmt.Sprintf("run%d", call)
	if len(prev) > 0 {
		content = strings.Join(prev, "|") + "|" + content
	}

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		},
	}
	close(ch)
	return ch, nil
}

func (a *persistentHistoryTestAgent) Tools() []tool.Tool { return nil }
func (a *persistentHistoryTestAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "persistent-history-test"}
}
func (a *persistentHistoryTestAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *persistentHistoryTestAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *persistentHistoryTestAgent) keys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.seenKeys))
	copy(out, a.seenKeys)
	return out
}

type constantReplyAgent struct {
	name    string
	content string
}

func (a *constantReplyAgent) Run(
	_ context.Context,
	_ *coreagent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(a.content),
			}},
		},
	}
	close(ch)
	return ch, nil
}

func (a *constantReplyAgent) Tools() []tool.Tool { return nil }
func (a *constantReplyAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "constant-reply-test"}
}
func (a *constantReplyAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *constantReplyAgent) FindSubAgent(string) coreagent.Agent { return nil }

// persistentHistoryStreamingAgent emits a completion barrier followed by one
// assistant response. The barrier makes the test exercise the Runner-owned
// completion path used by AgentTool streaming calls.
type persistentHistoryStreamingAgent struct {
	name string

	mu       sync.Mutex
	calls    int
	requests [][]model.Message
}

func (a *persistentHistoryStreamingAgent) Run(
	ctx context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	var request []model.Message
	if inv != nil {
		if inv.Session != nil {
			filterKey := inv.GetEventFilterKey()
			inv.Session.EventMu.RLock()
			for _, evt := range inv.Session.Events {
				if evt.FilterKey != filterKey ||
					evt.Response == nil ||
					len(evt.Response.Choices) == 0 {
					continue
				}
				msg := evt.Response.Choices[0].Message
				if msg.Role == model.RoleUser || msg.Role == model.RoleAssistant {
					request = append(request, msg)
				}
			}
			inv.Session.EventMu.RUnlock()
		}
		// Invocation.Message is supplied directly to the current child model
		// request, before the stream's persisted user event is appended.
		request = append(request, inv.Message)
	}

	a.mu.Lock()
	a.calls++
	call := a.calls
	a.requests = append(a.requests, append([]model.Message(nil), request...))
	a.mu.Unlock()

	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		if inv == nil {
			return
		}
		barrier := event.New(inv.InvocationID, a.name)
		barrier.RequiresCompletion = true
		completionID := coreagent.GetAppendEventNoticeKey(barrier.ID)
		inv.AddNoticeChannel(ctx, completionID)
		ch <- barrier
		if err := inv.AddNoticeChannelAndWait(ctx, completionID, time.Second); err != nil {
			ch <- event.NewErrorEvent(
				inv.InvocationID,
				a.name,
				model.ErrorTypeFlowError,
				err.Error(),
			)
			return
		}
		ch <- event.NewResponseEvent(
			inv.InvocationID,
			a.name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(fmt.Sprintf("A%d", call)),
				}},
			},
		)
	}()
	return ch, nil
}

func (a *persistentHistoryStreamingAgent) Tools() []tool.Tool { return nil }
func (a *persistentHistoryStreamingAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "persistent-history-streaming-test"}
}
func (a *persistentHistoryStreamingAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *persistentHistoryStreamingAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *persistentHistoryStreamingAgent) requestHistory() [][]model.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	requests := make([][]model.Message, len(a.requests))
	for i := range a.requests {
		requests[i] = append([]model.Message(nil), a.requests[i]...)
	}
	return requests
}

// persistentHistoryRunnerAgent runs the AgentTool through a real Runner event
// loop. It forwards the child stream events unchanged so Runner owns their
// persistence and completion notification, just like the function-call path.
type persistentHistoryRunnerAgent struct {
	name  string
	child *Tool

	mu    sync.Mutex
	calls int
}

func (a *persistentHistoryRunnerAgent) Run(
	ctx context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()

	toolCtx := context.WithValue(
		ctx,
		tool.ContextKeyToolCallID{},
		fmt.Sprintf("call-%d", call),
	)
	reader, err := a.child.StreamableCall(
		tool.WithFinalResultChunks(toolCtx),
		[]byte(fmt.Sprintf(`{"request":"U%d"}`, call)),
	)
	if err != nil {
		return nil, err
	}

	out := make(chan *event.Event, 8)
	go func() {
		defer close(out)
		defer reader.Close()
		for {
			chunk, recvErr := reader.Recv()
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				out <- event.NewErrorEvent(
					inv.InvocationID,
					a.name,
					model.ErrorTypeFlowError,
					recvErr.Error(),
				)
				return
			}
			evt, ok := chunk.Content.(*event.Event)
			if ok && evt != nil {
				out <- evt
			}
		}

		final := event.NewResponseEvent(
			inv.InvocationID,
			a.name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(fmt.Sprintf("root-%d", call)),
				}},
			},
		)
		coreagent.InjectIntoEvent(inv, final)
		out <- final
	}()
	return out, nil
}

func (a *persistentHistoryRunnerAgent) Tools() []tool.Tool { return nil }
func (a *persistentHistoryRunnerAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "persistent-history-runner-test"}
}
func (a *persistentHistoryRunnerAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *persistentHistoryRunnerAgent) FindSubAgent(string) coreagent.Agent { return nil }

type prevCountAgent struct {
	name string

	mu       sync.Mutex
	seenKeys []string
}

func (a *prevCountAgent) Run(
	_ context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	fk := inv.GetEventFilterKey()
	a.mu.Lock()
	a.seenKeys = append(a.seenKeys, fk)
	a.mu.Unlock()

	count := 0
	if inv.Session != nil {
		for _, evt := range inv.Session.Events {
			if evt.FilterKey != fk || evt.Response == nil || len(evt.Response.Choices) == 0 {
				continue
			}
			msg := evt.Response.Choices[0].Message
			if msg.Role == model.RoleAssistant && msg.Content != "" {
				count++
			}
		}
	}

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(fmt.Sprintf("prev=%d", count)),
			}},
		},
	}
	close(ch)
	return ch, nil
}

func (a *prevCountAgent) Tools() []tool.Tool { return nil }
func (a *prevCountAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "prev-count-test"}
}
func (a *prevCountAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *prevCountAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *prevCountAgent) keys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.seenKeys))
	copy(out, a.seenKeys)
	return out
}

func TestTool_PersistentHistory_DefaultKey_ReusedAcrossCalls(t *testing.T) {
	child := &persistentHistoryTestAgent{name: "child"}
	at := NewTool(child, WithPersistentHistory())

	sess := session.NewSession("app", "user", "session")
	parent1 := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx1 := coreagent.NewInvocationContext(context.Background(), parent1)

	out1, err := at.Call(ctx1, []byte(`{"request":"one"}`))
	require.NoError(t, err)
	require.Equal(t, "run1", out1)

	parent2 := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx2 := coreagent.NewInvocationContext(context.Background(), parent2)

	out2, err := at.Call(ctx2, []byte(`{"request":"two"}`))
	require.NoError(t, err)
	require.Equal(t, "run1|run2", out2)

	keys := child.keys()
	require.Len(t, keys, 2)
	require.Equal(t, "agenttool:child:default", keys[0])
	require.Equal(t, keys[0], keys[1])
}

func TestTool_PersistentHistory_StreamableCall_MultiRoundRunnerHistory(t *testing.T) {
	child := &persistentHistoryStreamingAgent{name: "stream-child"}
	childTool := NewTool(child, WithStreamInner(true), WithPersistentHistory())
	root := &persistentHistoryRunnerAgent{name: "stream-root", child: childTool}
	r := runner.NewRunner("persistent-history-stream-test", root)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	for i := 1; i <= 4; i++ {
		events, err := r.Run(
			context.Background(),
			"user",
			"session",
			model.NewUserMessage(fmt.Sprintf("turn-%d", i)),
			coreagent.WithStream(true),
		)
		require.NoError(t, err)
		for evt := range events {
			require.NotNil(t, evt)
			require.Nil(t, evt.Error)
		}
	}

	requests := child.requestHistory()
	require.Len(t, requests, 4)
	for i, request := range requests {
		var want []model.Message
		for previous := 1; previous <= i; previous++ {
			want = append(want,
				model.NewUserMessage(fmt.Sprintf(`{"request":"U%d"}`, previous)),
				model.NewAssistantMessage(fmt.Sprintf("A%d", previous)),
			)
		}
		want = append(want, model.NewUserMessage(fmt.Sprintf(`{"request":"U%d"}`, i+1)))
		require.Equal(t, want, request, "child request history on round %d", i+1)
	}
}

func TestTool_PersistentHistory_CustomKey_Used(t *testing.T) {
	child := &persistentHistoryTestAgent{name: "child"}
	at := NewTool(child, WithPersistentHistoryKey("agenttool:child:task-1"))

	sess := session.NewSession("app", "user", "session")
	parent1 := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx1 := coreagent.NewInvocationContext(context.Background(), parent1)

	_, err := at.Call(ctx1, []byte(`{"request":"one"}`))
	require.NoError(t, err)

	parent2 := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx2 := coreagent.NewInvocationContext(context.Background(), parent2)

	_, err = at.Call(ctx2, []byte(`{"request":"two"}`))
	require.NoError(t, err)

	keys := child.keys()
	require.Len(t, keys, 2)
	require.Equal(t, "agenttool:child:task-1", keys[0])
	require.Equal(t, keys[0], keys[1])
}

func TestTool_PersistentHistory_KeyFunc_IsolatesHistoryByKey(t *testing.T) {
	child := &prevCountAgent{name: "child"}
	at := NewTool(child, WithPersistentHistoryKeyFunc(
		func(_ context.Context, _ *coreagent.Invocation, jsonArgs []byte) string {
			switch {
			case strings.Contains(string(jsonArgs), `"task":"A"`):
				return "agenttool:child:task-A"
			case strings.Contains(string(jsonArgs), `"task":"B"`):
				return "agenttool:child:task-B"
			default:
				return "agenttool:child:default"
			}
		},
	))

	sess := session.NewSession("app", "user", "session")
	parent1 := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx1 := coreagent.NewInvocationContext(context.Background(), parent1)

	outA1, err := at.Call(ctx1, []byte(`{"task":"A"}`))
	require.NoError(t, err)
	require.Equal(t, "prev=0", outA1)

	parent2 := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx2 := coreagent.NewInvocationContext(context.Background(), parent2)

	outB1, err := at.Call(ctx2, []byte(`{"task":"B"}`))
	require.NoError(t, err)
	require.Equal(t, "prev=0", outB1)

	parent3 := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx3 := coreagent.NewInvocationContext(context.Background(), parent3)

	outA2, err := at.Call(ctx3, []byte(`{"task":"A"}`))
	require.NoError(t, err)
	require.Equal(t, "prev=1", outA2)

	require.Equal(t,
		[]string{"agenttool:child:task-A", "agenttool:child:task-B", "agenttool:child:task-A"},
		child.keys(),
	)
}

func TestTool_PersistentHistory_LastOptionWins(t *testing.T) {
	child := &prevCountAgent{name: "child"}
	at := NewTool(
		child,
		WithPersistentHistoryKeyFunc(func(_ context.Context, _ *coreagent.Invocation, jsonArgs []byte) string {
			if strings.Contains(string(jsonArgs), `"task":"A"`) {
				return "agenttool:child:task-A"
			}
			return "agenttool:child:default"
		}),
		WithPersistentHistoryKey("agenttool:child:task-1"),
	)

	sess := session.NewSession("app", "user", "session")
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx := coreagent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"task":"A"}`))
	require.NoError(t, err)
	require.Equal(t, []string{"agenttool:child:task-1"}, child.keys())
}

func TestTool_PersistentHistory_WithPersistentHistoryClearsOverrides(t *testing.T) {
	child := &persistentHistoryTestAgent{name: "child"}
	at := NewTool(
		child,
		WithPersistentHistoryKey("agenttool:child:task-1"),
		WithPersistentHistory(),
	)

	sess := session.NewSession("app", "user", "session")
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx := coreagent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"one"}`))
	require.NoError(t, err)
	require.Equal(t, []string{"agenttool:child:default"}, child.keys())
}

func TestTool_PersistentHistory_ParentFilterExcludesChildEvents(t *testing.T) {
	const (
		childKey      = "agenttool:child:task-1"
		childInternal = "CHILD_INTERNAL"
		parentToolOut = "TOOL_FINAL"
	)

	at := NewTool(
		&constantReplyAgent{name: "child", content: childInternal},
		WithPersistentHistoryKey(childKey),
	)

	sess := session.NewSession("app", "user", "session")
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
		coreagent.WithInvocationMessage(model.NewUserMessage("parent ask")),
	)
	parent.AgentName = "parent"
	ctx := coreagent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)

	// Verify child internal event exists under the child key.
	foundChildInternal := false
	for _, evt := range sess.Events {
		if evt.FilterKey != childKey || evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		msg := evt.Response.Choices[0].Message
		if msg.Role == model.RoleAssistant && msg.Content == childInternal {
			foundChildInternal = true
			break
		}
	}
	require.True(t, foundChildInternal, "expected child internal event to be persisted under child key")

	// Simulate the parent-visible tool call + tool response events (they belong
	// to the parent track). The content processor drops orphan tool results, so
	// include both for a realistic parent transcript.
	toolCallID := "call-1"
	toolCallEvt := event.NewResponseEvent(
		parent.InvocationID,
		parent.AgentName,
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   toolCallID,
						Function: model.FunctionDefinitionParam{
							Name:      at.Declaration().Name,
							Arguments: []byte(`{"request":"ignored"}`),
						},
					}},
				},
			}},
		},
	)
	coreagent.InjectIntoEvent(parent, toolCallEvt)
	sess.Events = append(sess.Events, *toolCallEvt)

	toolMsg := model.NewToolMessage(toolCallID, at.Declaration().Name, parentToolOut)
	toolEvt := event.NewResponseEvent(
		parent.InvocationID,
		parent.AgentName,
		&model.Response{
			Done:    true,
			Object:  model.ObjectTypeToolResponse,
			Choices: []model.Choice{{Index: 0, Message: toolMsg}},
		},
	)
	coreagent.InjectIntoEvent(parent, toolEvt)
	sess.Events = append(sess.Events, *toolEvt)

	req := &model.Request{}
	p := processor.NewContentRequestProcessor()
	p.ProcessRequest(context.Background(), parent, req, nil)

	var rendered strings.Builder
	for _, msg := range req.Messages {
		rendered.WriteString(msg.Role.String())
		rendered.WriteString(":")
		rendered.WriteString(msg.Content)
		rendered.WriteString("\n")
	}
	out := rendered.String()
	require.Contains(t, out, parentToolOut)
	require.NotContains(t, out, childInternal)
}

func TestTool_PersistentHistory_IgnoresParentBranchHistoryScope(t *testing.T) {
	original := agentlog.Default
	logger := &dynTestWarnLogger{}
	agentlog.Default = logger
	t.Cleanup(func() {
		agentlog.Default = original
	})

	child := &persistentHistoryTestAgent{name: "child"}
	at := NewTool(
		child,
		WithPersistentHistoryKey("agenttool:child:task-1"),
		WithHistoryScope(HistoryScopeParentBranch),
	)
	require.Equal(t, 1, logger.warnfCalls)
	require.Nil(t, at.persistentHistory)

	sess := session.NewSession("app", "user", "session")
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx := coreagent.NewInvocationContext(context.Background(), parent)

	out, err := at.Call(ctx, []byte(`{"request":"one"}`))
	require.NoError(t, err)
	// ParentBranch should use the legacy hierarchical key "parent/child-uuid".
	require.True(t, strings.HasPrefix(out.(string), "run1"), "sanity: child agent should run")

	keys := child.keys()
	require.Len(t, keys, 1)
	require.True(t, strings.HasPrefix(keys[0], "parent/child-"))
	require.NotEqual(t, "agenttool:child:task-1", keys[0])
}
