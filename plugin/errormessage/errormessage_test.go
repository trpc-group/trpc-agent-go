//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package errormessage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	rootplugin "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/errormessage"
)

func newPluginManager(t *testing.T, p rootplugin.Plugin) *rootplugin.Manager {
	t.Helper()
	m, err := rootplugin.NewManager(p)
	require.NoError(t, err)
	require.NotNil(t, m)
	return m
}

func newErrorEvent(errType, errMsg string) *event.Event {
	return event.NewErrorEvent("inv-1", "agent-1", errType, errMsg)
}

func TestPlugin_RewritesEmptyErrorEventContent(t *testing.T) {
	p := errormessage.New(
		errormessage.WithContent("Friendly fallback."),
	)
	m := newPluginManager(t, p)

	original := newErrorEvent(agent.ErrorTypeStopAgentError, "stopped: reason X")
	require.False(t, original.Response.IsValidContent())

	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.NotNil(t, out)

	require.NotSame(t, original, out, "plugin must not mutate the original event in place")

	// Rewritten event carries the customised visible content.
	require.Len(t, out.Response.Choices, 1)
	require.Equal(t, model.RoleAssistant, out.Response.Choices[0].Message.Role)
	require.Equal(
		t,
		"Friendly fallback.",
		out.Response.Choices[0].Message.Content,
	)
	require.NotNil(t, out.Response.Choices[0].FinishReason)
	require.Equal(t, "error", *out.Response.Choices[0].FinishReason)

	// Structured Response.Error must remain intact for downstream consumers.
	require.NotNil(t, out.Response.Error)
	require.Equal(
		t,
		agent.ErrorTypeStopAgentError,
		out.Response.Error.Type,
	)
	require.Equal(t, "stopped: reason X", out.Response.Error.Message)

	// Original event must be untouched.
	require.Empty(t, original.Response.Choices)
}

func TestPlugin_KeepsExistingAssistantContent(t *testing.T) {
	p := errormessage.New(
		errormessage.WithContent("should NOT be applied"),
	)
	m := newPluginManager(t, p)

	rsp := &model.Response{
		Object: model.ObjectTypeError,
		Done:   true,
		Error: &model.ResponseError{
			Type:    "flow_error",
			Message: "inner details",
		},
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "partial answer before failure",
			},
		}},
	}
	original := event.NewResponseEvent("inv", "agent", rsp)
	require.True(t, original.Response.IsValidContent())

	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Same(t, original, out, "events with valid content must be passed through")
	require.Equal(
		t,
		"partial answer before failure",
		out.Response.Choices[0].Message.Content,
	)
}

func TestPlugin_SkipsNonErrorEvents(t *testing.T) {
	p := errormessage.New(errormessage.WithContent("fallback"))
	m := newPluginManager(t, p)

	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage("normal reply"),
		}},
	}
	original := event.NewResponseEvent("inv", "agent", rsp)

	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.Same(t, original, out)
}

func TestPlugin_ResolverReceivesInvocationAndEvent(t *testing.T) {
	var (
		seenInvocation *agent.Invocation
		seenEvent      *event.Event
	)
	resolver := func(
		_ context.Context,
		inv *agent.Invocation,
		e *event.Event,
	) (string, bool) {
		seenInvocation = inv
		seenEvent = e
		return "resolved", true
	}
	p := errormessage.New(errormessage.WithResolver(resolver))
	m := newPluginManager(t, p)

	inv := agent.NewInvocation(agent.WithInvocationID("inv-42"))
	original := newErrorEvent("flow_error", "boom")

	out, err := m.OnEvent(context.Background(), inv, original)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, inv, seenInvocation)
	require.Same(t, original, seenEvent)
	require.Equal(
		t,
		"resolved",
		out.Response.Choices[0].Message.Content,
	)
}

func TestPlugin_ResolverReturningFalseLeavesEventUntouched(t *testing.T) {
	resolver := func(
		_ context.Context,
		_ *agent.Invocation,
		_ *event.Event,
	) (string, bool) {
		return "unused", false
	}
	p := errormessage.New(errormessage.WithResolver(resolver))
	m := newPluginManager(t, p)

	original := newErrorEvent("flow_error", "boom")
	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.Same(t, original, out)
	require.Empty(t, original.Response.Choices)
}

func TestPlugin_ResolverReturningEmptyLeavesEventUntouched(t *testing.T) {
	resolver := func(
		_ context.Context,
		_ *agent.Invocation,
		_ *event.Event,
	) (string, bool) {
		return "", true
	}
	p := errormessage.New(errormessage.WithResolver(resolver))
	m := newPluginManager(t, p)

	original := newErrorEvent("flow_error", "boom")
	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.Same(t, original, out)
	require.Empty(t, original.Response.Choices)
}

func TestPlugin_NoResolverIsNoop(t *testing.T) {
	p := errormessage.New()
	m := newPluginManager(t, p)

	original := newErrorEvent("flow_error", "boom")
	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.Same(t, original, out)
}

func TestPlugin_CustomFinishReason(t *testing.T) {
	p := errormessage.New(
		errormessage.WithContent("stopped"),
		errormessage.WithFinishReason("stop"),
	)
	m := newPluginManager(t, p)

	original := newErrorEvent(agent.ErrorTypeStopAgentError, "policy stop")
	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Response.Choices[0].FinishReason)
	require.Equal(t, "stop", *out.Response.Choices[0].FinishReason)
}

func TestPlugin_PreservesExistingFinishReason(t *testing.T) {
	existing := "length"
	rsp := &model.Response{
		Object: model.ObjectTypeError,
		Done:   true,
		Error: &model.ResponseError{
			Type:    "flow_error",
			Message: "boom",
		},
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
			},
			FinishReason: &existing,
		}},
	}
	original := event.NewResponseEvent("inv", "agent", rsp)
	require.False(t, original.Response.IsValidContent())

	p := errormessage.New(errormessage.WithContent("friendly"))
	m := newPluginManager(t, p)

	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "friendly", out.Response.Choices[0].Message.Content)
	require.NotNil(t, out.Response.Choices[0].FinishReason)
	require.Equal(t, "length", *out.Response.Choices[0].FinishReason)
}

func TestPlugin_CustomNameIsHonoured(t *testing.T) {
	p := errormessage.New(errormessage.WithName("my_error_rewriter"))
	require.Equal(t, "my_error_rewriter", p.Name())
}

func TestPlugin_EmptyNameFallsBackToDefault(t *testing.T) {
	p := errormessage.New(errormessage.WithName(""))
	require.Equal(t, "error_message_rewriter", p.Name())
}

func TestPlugin_EmptyFinishReasonFallsBackToDefault(t *testing.T) {
	p := errormessage.New(
		errormessage.WithContent("friendly"),
		errormessage.WithFinishReason(""),
	)
	m := newPluginManager(t, p)

	original := newErrorEvent("flow_error", "boom")
	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Response.Choices[0].FinishReason)
	require.Equal(t, "error", *out.Response.Choices[0].FinishReason)
}

// TestPlugin_NilResponseIsSafe guards against upstream emitting malformed
// events where Response is missing. The plugin must not panic and must not
// materialise a fake choice.
func TestPlugin_NilResponseIsSafe(t *testing.T) {
	p := errormessage.New(errormessage.WithContent("friendly"))
	m := newPluginManager(t, p)

	original := &event.Event{InvocationID: "inv", Author: "agent"}
	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.Same(t, original, out)
}

// TestPlugin_SkipsPartialErrorEvents ensures that partial error events are
// forwarded unchanged. A later final event is free to override the outcome,
// so the plugin must not surface a failure message for transient partial
// frames.
func TestPlugin_SkipsPartialErrorEvents(t *testing.T) {
	p := errormessage.New(errormessage.WithContent("should not apply"))
	m := newPluginManager(t, p)

	rsp := &model.Response{
		Object:    model.ObjectTypeError,
		IsPartial: true,
		Error: &model.ResponseError{
			Type:    "flow_error",
			Message: "transient failure",
		},
	}
	original := event.NewResponseEvent("inv", "agent", rsp)

	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.Same(t, original, out)
	require.Empty(t, original.Response.Choices)
}

// TestPlugin_NormalisesNonAssistantFirstChoiceRole ensures the plugin always
// writes its resolved content into an assistant-role choice, even if upstream
// emitted an error event whose first choice was authored as user/system.
func TestPlugin_NormalisesNonAssistantFirstChoiceRole(t *testing.T) {
	p := errormessage.New(errormessage.WithContent("friendly"))
	m := newPluginManager(t, p)

	rsp := &model.Response{
		Object: model.ObjectTypeError,
		Done:   true,
		Error: &model.ResponseError{
			Type:    "flow_error",
			Message: "boom",
		},
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleUser,
			},
		}},
	}
	original := event.NewResponseEvent("inv", "agent", rsp)
	require.False(t, original.Response.IsValidContent())

	out, err := m.OnEvent(context.Background(), nil, original)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.Response.Choices, 1)
	require.Equal(
		t,
		model.RoleAssistant,
		out.Response.Choices[0].Message.Role,
	)
	require.Equal(
		t,
		"friendly",
		out.Response.Choices[0].Message.Content,
	)

	// Original event must not be mutated by the plugin.
	require.Equal(
		t,
		model.RoleUser,
		original.Response.Choices[0].Message.Role,
	)
}

// Using errors.New here keeps the option.go import block matching the rest of
// the repository even when Resolver type evolves in the future.
var _ errormessage.Resolver = func(
	_ context.Context,
	_ *agent.Invocation,
	_ *event.Event,
) (string, bool) {
	return errors.New("unused").Error(), false
}

// TestPlugin_RegisterNilRegistryIsNoop guards the defensive nil-registry
// branch on the plugin.Register contract. It must not panic.
func TestPlugin_RegisterNilRegistryIsNoop(t *testing.T) {
	p := errormessage.New(errormessage.WithContent("friendly"))
	// The plugin.Plugin contract allows implementations to be tolerant of
	// a nil registry. We reach in via the exported interface so the call is
	// identical to how plugin.NewManager would invoke it.
	require.NotPanics(t, func() { p.Register(nil) })
}
