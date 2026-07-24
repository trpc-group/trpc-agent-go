//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolresultfile

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactmem "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestPluginExternalizesLargeToolResult(t *testing.T) {
	ctx := context.Background()
	artifacts := artifactmem.NewService()
	inv := testInvocation(artifacts)
	manager, err := plugin.NewManager(New(WithThresholdBytes(10)))
	require.NoError(t, err)
	large := model.NewToolMessage("call-large", "lookup", `{"result":"large payload"}`)
	small := model.NewToolMessage("call-small", "lookup", "small")
	ev := toolResultEvent(large, small)
	args := &plugin.AfterToolMessagesArgs{
		Invocation:         inv,
		ToolResultEvent:    ev,
		Messages:           []model.Message{large, small},
		ToolResultMessages: []model.Message{large, small},
	}

	result, err := manager.AfterToolMessages(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.ToolResultMessages, 2)

	replacement := result.ToolResultMessages[0]
	filename := artifactName(inv, large.ToolID)
	require.Contains(t, replacement.Content, "artifact://"+filename+"@0")
	require.Contains(t, replacement.Content, "read_file")
	require.NotContains(t, replacement.Content, "large payload")
	require.Equal(t, small, result.ToolResultMessages[1])
	require.Equal(t, replacement, ev.Response.Choices[0].Message)

	saved, err := artifacts.LoadArtifact(
		ctx,
		artifact.SessionInfo{
			AppName:   inv.Session.AppName,
			UserID:    inv.Session.UserID,
			SessionID: inv.Session.ID,
		},
		filename,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, large.Content, string(saved.Data))
	require.Equal(t, jsonMimeType, saved.MimeType)
}

func TestPluginPreservesInlineResultsWithoutArtifactTarget(t *testing.T) {
	manager, err := plugin.NewManager(New(WithThresholdBytes(1)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "large")

	result, err := manager.AfterToolMessages(
		context.Background(),
		&plugin.AfterToolMessagesArgs{
			Invocation:         &agent.Invocation{},
			ToolResultMessages: []model.Message{msg},
		},
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestPluginPreservesResultsBelowThreshold(t *testing.T) {
	artifacts := artifactmem.NewService()
	manager, err := plugin.NewManager(New(WithThresholdBytes(100)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "small")

	result, err := manager.AfterToolMessages(
		context.Background(),
		&plugin.AfterToolMessagesArgs{
			Invocation:         testInvocation(artifacts),
			ToolResultMessages: []model.Message{msg},
		},
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestPluginVersionsRepeatedToolResults(t *testing.T) {
	ctx := context.Background()
	artifacts := artifactmem.NewService()
	inv := testInvocation(artifacts)
	manager, err := plugin.NewManager(New(WithThresholdBytes(1)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "first")

	for version, content := range []string{"first", "second"} {
		msg.Content = content
		result, err := manager.AfterToolMessages(
			ctx,
			&plugin.AfterToolMessagesArgs{
				Invocation:         inv,
				ToolResultMessages: []model.Message{msg},
				ToolResultEvent:    toolResultEvent(msg),
			},
		)
		require.NoError(t, err)
		require.Contains(
			t,
			result.ToolResultMessages[0].Content,
			"@"+string(rune('0'+version)),
		)
	}
}

func TestContentMimeType(t *testing.T) {
	require.Equal(t, jsonMimeType, contentMimeType(`{"ok":true}`))
	require.Equal(t, textMimeType, contentMimeType("plain text"))
}

func TestOptions(t *testing.T) {
	o := newOptions(
		WithName("custom"),
		WithThresholdBytes(42),
		WithThresholdBytes(0),
	)
	require.Equal(t, "custom", o.name)
	require.Equal(t, 42, o.thresholdBytes)

	defaults := newOptions()
	require.Equal(t, defaultPluginName, defaults.name)
	require.Equal(t, defaultThresholdBytes, defaults.thresholdBytes)
}

func testInvocation(service artifact.Service) *agent.Invocation {
	return &agent.Invocation{
		InvocationID:    "invocation",
		ArtifactService: service,
		Session: &session.Session{
			AppName: "app",
			UserID:  "user",
			ID:      "session",
		},
	}
}

func toolResultEvent(messages ...model.Message) *event.Event {
	choices := make([]model.Choice, 0, len(messages))
	for _, msg := range messages {
		choices = append(choices, model.Choice{Message: msg})
	}
	return event.NewResponseEvent("invocation", "agent", &model.Response{
		Choices: choices,
	})
}

func TestArtifactNameIsStableAndOpaque(t *testing.T) {
	inv := &agent.Invocation{InvocationID: "invocation"}
	first := artifactName(inv, "tool/call:1")
	second := artifactName(inv, "tool/call:1")
	require.Equal(t, first, second)
	require.True(t, strings.HasPrefix(first, "tool_result_"))
	require.NotContains(t, first, "tool/call")
}
