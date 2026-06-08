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
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx := coreagent.NewInvocationContext(context.Background(), parent)

	out1, err := at.Call(ctx, []byte(`{"request":"one"}`))
	require.NoError(t, err)
	require.Equal(t, "run1", out1)

	out2, err := at.Call(ctx, []byte(`{"request":"two"}`))
	require.NoError(t, err)
	require.Equal(t, "run1|run2", out2)

	keys := child.keys()
	require.Len(t, keys, 2)
	require.Equal(t, "agenttool:child:default", keys[0])
	require.Equal(t, keys[0], keys[1])
}

func TestTool_PersistentHistory_CustomKey_Used(t *testing.T) {
	child := &persistentHistoryTestAgent{name: "child"}
	at := NewTool(child, WithPersistentHistoryKey("agenttool:child:task-1"))

	sess := session.NewSession("app", "user", "session")
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx := coreagent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"one"}`))
	require.NoError(t, err)
	_, err = at.Call(ctx, []byte(`{"request":"two"}`))
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
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	ctx := coreagent.NewInvocationContext(context.Background(), parent)

	outA1, err := at.Call(ctx, []byte(`{"task":"A"}`))
	require.NoError(t, err)
	require.Equal(t, "prev=0", outA1)

	outB1, err := at.Call(ctx, []byte(`{"task":"B"}`))
	require.NoError(t, err)
	require.Equal(t, "prev=0", outB1)

	outA2, err := at.Call(ctx, []byte(`{"task":"A"}`))
	require.NoError(t, err)
	require.Equal(t, "prev=1", outA2)

	require.Equal(t,
		[]string{"agenttool:child:task-A", "agenttool:child:task-B", "agenttool:child:task-A"},
		child.keys(),
	)
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
	child := &persistentHistoryTestAgent{name: "child"}
	at := NewTool(
		child,
		WithPersistentHistoryKey("agenttool:child:task-1"),
		WithHistoryScope(HistoryScopeParentBranch),
	)

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
