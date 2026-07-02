//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/eventstream"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunnerEventStreamUnusedPreservesRootRun(t *testing.T) {
	service := sessioninmemory.NewSessionService()
	ag := &rootOnlyEventAgent{name: "root-agent", content: "root result"}
	r := NewRunner("eventstream-unused", ag, WithSessionService(service))
	defer r.Close()

	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("start"),
	)
	require.NoError(t, err)

	var assistantContents []string
	for evt := range events {
		if content := eventAssistantContent(evt); content != "" {
			assistantContents = append(assistantContents, content)
		}
	}
	require.Equal(t, []string{"root result"}, assistantContents)

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "eventstream-unused", UserID: "user", SessionID: "session",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"root result"}, sessionAssistantContents(sess))
}

func TestRunnerForwardsNestedEventsThroughMainEventLoop(t *testing.T) {
	service := sessioninmemory.NewSessionService()
	ag := &eventForwardingTestAgent{start: make(chan struct{})}
	r := NewRunner("event-forwarding", ag, WithSessionService(service))
	defer r.Close()

	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("start"),
	)
	require.NoError(t, err)
	close(ag.start)

	var authors []string
	for evt := range events {
		if evt != nil {
			authors = append(authors, evt.Author)
		}
	}
	require.Contains(t, authors, "child-agent")
	require.Contains(t, authors, "root-agent")

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "event-forwarding", UserID: "user", SessionID: "session",
	})
	require.NoError(t, err)
	var childStored bool
	for _, evt := range sess.GetEvents() {
		if evt.Author == "child-agent" {
			childStored = true
			break
		}
	}
	require.True(t, childStored, "forwarded child event must use the normal session persistence path")
}

func TestRunnerForwardedEventDoesNotOverrideRootCompletion(t *testing.T) {
	service := sessioninmemory.NewSessionService()
	ag := &eventForwardingTestAgent{start: make(chan struct{})}
	r := NewRunner("event-forwarding-completion", ag, WithSessionService(service))
	defer r.Close()

	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("start"),
	)
	require.NoError(t, err)
	close(ag.start)

	var runnerCompletion *event.Event
	var assistantContents []string
	for evt := range events {
		if evt != nil && evt.IsRunnerCompletion() {
			runnerCompletion = evt
			continue
		}
		if content := eventAssistantContent(evt); content != "" {
			assistantContents = append(assistantContents, content)
		}
	}
	require.Equal(t, []string{"child result", "root result"}, assistantContents)
	require.NotNil(t, runnerCompletion)
	require.NotEqual(t, "child result", eventAssistantContent(runnerCompletion))

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "event-forwarding-completion", UserID: "user", SessionID: "session",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"child result", "root result"}, sessionAssistantContents(sess))
}

func TestRunnerForwarderIsAvailableDuringAgentRun(t *testing.T) {
	service := sessioninmemory.NewSessionService()
	ag := &synchronousEventForwardingAgent{}
	r := NewRunner("event-forwarding-sync", ag, WithSessionService(service))
	defer r.Close()

	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("start"),
	)
	require.NoError(t, err)

	var authors []string
	for evt := range events {
		if evt != nil {
			authors = append(authors, evt.Author)
		}
	}
	require.True(t, ag.forwarded)
	require.Contains(t, authors, "child-agent")
	require.Contains(t, authors, "root-agent")

	sess, err := service.GetSession(context.Background(), session.Key{
		AppName: "event-forwarding-sync", UserID: "user", SessionID: "session",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"child result", "root result"}, sessionAssistantContents(sess))
}

func TestForwardedEventIsExcludedFromRootCompletionCapture(t *testing.T) {
	r := &runner{}
	rootSession := session.NewSession("app", "user", "session")
	loop := &eventLoopContext{
		sess:                     rootSession,
		processingForwardedEvent: true,
	}
	childEvent := eventstreamResponseEvent("child-invocation", "child-agent", "child result")

	exclude := shouldExcludeRootCompletion(loop, rootSession, false)
	require.True(t, exclude)
	r.captureRootCompletion(loop, childEvent, exclude)
	require.Empty(t, loop.fallbackChoices)
	require.Empty(t, loop.fallbackResponseID)

	loop.processingForwardedEvent = false
	rootEvent := eventstreamResponseEvent("root-invocation", "root-agent", "root result")
	exclude = shouldExcludeRootCompletion(loop, rootSession, false)
	require.False(t, exclude)
	r.captureRootCompletion(loop, rootEvent, exclude)
	require.Equal(t, "root result", assistantChoicePrimaryContent(loop.fallbackChoices))
}

func TestEventStreamClearDisablesInheritedForwarder(t *testing.T) {
	root := agent.NewInvocation(agent.WithInvocationAgent(&rootOnlyEventAgent{name: "root"}))
	var forwarded []*event.Event
	eventstream.Attach(root, func(_ context.Context, evt *event.Event) error {
		forwarded = append(forwarded, evt)
		return nil
	})
	child := root.Clone(agent.WithInvocationAgent(&eventForwardingChildAgent{}))
	evt := event.New(child.InvocationID, "child")

	ok, err := eventstream.Invoke(context.Background(), child, evt)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, forwarded, 1)

	eventstream.Clear(root)

	ok, err = eventstream.Invoke(context.Background(), child, evt)
	require.NoError(t, err)
	require.False(t, ok)
	require.Len(t, forwarded, 1)
}

func TestRunnerEventStatsAllowNilResponseEvent(t *testing.T) {
	loop := &eventLoopContext{}

	require.NotPanics(t, func() {
		recordRunnerEventStats(loop, &event.Event{})
	})
	require.Equal(t, 1, loop.processedEventCount)
	require.Zero(t, loop.partialEventCount)
	require.Zero(t, loop.doneEventCount)
}

type eventForwardingTestAgent struct {
	start chan struct{}
}

func (a *eventForwardingTestAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, 1)
	go func() {
		defer close(out)
		<-a.start

		child := inv.Clone(
			agent.WithInvocationAgent(&eventForwardingChildAgent{}),
			agent.WithInvocationEventFilterKey(inv.GetEventFilterKey()+"/child-agent"),
		)
		childEvent := eventstreamResponseEvent(child.InvocationID, "child-agent", "child result")
		agent.InjectIntoEvent(child, childEvent)
		if _, err := eventstream.Invoke(ctx, child, childEvent); err != nil {
			return
		}

		rootEvent := eventstreamResponseEvent(inv.InvocationID, "root-agent", "root result")
		agent.InjectIntoEvent(inv, rootEvent)
		out <- rootEvent
	}()
	return out, nil
}

func (a *eventForwardingTestAgent) Tools() []tool.Tool { return nil }
func (a *eventForwardingTestAgent) Info() agent.Info {
	return agent.Info{Name: "root-agent"}
}
func (a *eventForwardingTestAgent) SubAgents() []agent.Agent { return nil }
func (a *eventForwardingTestAgent) FindSubAgent(string) agent.Agent {
	return nil
}

type eventForwardingChildAgent struct{}

func (a *eventForwardingChildAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}
func (a *eventForwardingChildAgent) Tools() []tool.Tool { return nil }
func (a *eventForwardingChildAgent) Info() agent.Info {
	return agent.Info{Name: "child-agent"}
}
func (a *eventForwardingChildAgent) SubAgents() []agent.Agent { return nil }
func (a *eventForwardingChildAgent) FindSubAgent(string) agent.Agent {
	return nil
}

type synchronousEventForwardingAgent struct {
	forwarded bool
}

func (a *synchronousEventForwardingAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	child := inv.Clone(
		agent.WithInvocationAgent(&eventForwardingChildAgent{}),
		agent.WithInvocationEventFilterKey(inv.GetEventFilterKey()+"/child-agent"),
	)
	childEvent := eventstreamResponseEvent(child.InvocationID, "child-agent", "child result")
	agent.InjectIntoEvent(child, childEvent)
	forwarded, err := eventstream.Invoke(ctx, child, childEvent)
	if err != nil {
		return nil, err
	}
	a.forwarded = forwarded

	ch := make(chan *event.Event, 1)
	rootEvent := eventstreamResponseEvent(inv.InvocationID, "root-agent", "root result")
	agent.InjectIntoEvent(inv, rootEvent)
	ch <- rootEvent
	close(ch)
	return ch, nil
}

func (a *synchronousEventForwardingAgent) Tools() []tool.Tool { return nil }
func (a *synchronousEventForwardingAgent) Info() agent.Info {
	return agent.Info{Name: "root-agent"}
}
func (a *synchronousEventForwardingAgent) SubAgents() []agent.Agent { return nil }
func (a *synchronousEventForwardingAgent) FindSubAgent(string) agent.Agent {
	return nil
}

type rootOnlyEventAgent struct {
	name    string
	content string
}

func (a *rootOnlyEventAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	evt := eventstreamResponseEvent(inv.InvocationID, a.name, a.content)
	agent.InjectIntoEvent(inv, evt)
	ch <- evt
	close(ch)
	return ch, nil
}
func (a *rootOnlyEventAgent) Tools() []tool.Tool { return nil }
func (a *rootOnlyEventAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}
func (a *rootOnlyEventAgent) SubAgents() []agent.Agent        { return nil }
func (a *rootOnlyEventAgent) FindSubAgent(string) agent.Agent { return nil }

func eventstreamResponseEvent(invocationID, author, content string) *event.Event {
	return event.NewResponseEvent(invocationID, author, &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleAssistant, Content: content},
		}},
	})
}

func eventAssistantContent(evt *event.Event) string {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}
	message := evt.Response.Choices[0].Message
	if message.Role == model.RoleAssistant {
		return message.Content
	}
	return ""
}

func sessionAssistantContents(sess *session.Session) []string {
	if sess == nil {
		return nil
	}
	var contents []string
	for _, evt := range sess.GetEvents() {
		if content := eventAssistantContent(&evt); content != "" {
			contents = append(contents, content)
		}
	}
	return contents
}
