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
	"encoding/json"
	"errors"
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolfile "trpc.group/trpc-go/trpc-agent-go/tool/file"
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
		Request:            testRequest(),
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
			Request:            testRequest(),
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
			Request:            testRequest(),
			ToolResultMessages: []model.Message{msg},
		},
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestPluginExternalizesResultAtThreshold(t *testing.T) {
	artifacts := artifactmem.NewService()
	manager, err := plugin.NewManager(New(WithThresholdBytes(5)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "12345")

	result, err := manager.AfterToolMessages(
		context.Background(),
		&plugin.AfterToolMessagesArgs{
			Invocation:         testInvocation(artifacts),
			Request:            testRequest(),
			ToolResultMessages: []model.Message{msg},
			ToolResultEvent:    toolResultEvent(msg),
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.ToolResultMessages[0].Content, "artifact://")
}

func TestPluginSaveFailurePreservesOriginalResult(t *testing.T) {
	artifacts := failingArtifactService{
		Service: artifactmem.NewService(),
		err:     errors.New("storage unavailable"),
	}
	manager, err := plugin.NewManager(New(WithThresholdBytes(1)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "original")
	ev := toolResultEvent(msg)
	args := &plugin.AfterToolMessagesArgs{
		Invocation:         testInvocation(artifacts),
		Request:            testRequest(),
		ToolResultMessages: []model.Message{msg},
		ToolResultEvent:    ev,
	}

	result, err := manager.AfterToolMessages(context.Background(), args)
	require.ErrorContains(t, err, "storage unavailable")
	require.Nil(t, result)
	require.Equal(t, msg, args.ToolResultMessages[0])
	require.Equal(t, msg, ev.Response.Choices[0].Message)
}

func TestPluginExternalizesMultipartResultWithoutLeavingPartsInline(t *testing.T) {
	ctx := context.Background()
	artifacts := artifactmem.NewService()
	inv := testInvocation(artifacts)
	manager, err := plugin.NewManager(New(WithThresholdBytes(1)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "summary")
	msg.AddFileData("result.txt", []byte("large result"), textMimeType)
	ev := toolResultEvent(msg)

	result, err := manager.AfterToolMessages(
		ctx,
		&plugin.AfterToolMessagesArgs{
			Invocation:         inv,
			Request:            testRequest(),
			ToolResultMessages: []model.Message{msg},
			ToolResultEvent:    ev,
		},
	)
	require.NoError(t, err)
	require.Empty(t, result.ToolResultMessages[0].ContentParts)
	require.Empty(t, ev.Response.Choices[0].Message.ContentParts)

	saved, err := artifacts.LoadArtifact(
		ctx,
		artifact.SessionInfo{
			AppName:   inv.Session.AppName,
			UserID:    inv.Session.UserID,
			SessionID: inv.Session.ID,
		},
		artifactName(inv, msg.ToolID),
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, jsonMimeType, saved.MimeType)
	var envelope struct {
		ContentParts []model.ContentPart `json:"content_parts"`
	}
	require.NoError(t, json.Unmarshal(saved.Data, &envelope))
	require.Len(t, envelope.ContentParts, 1)
	require.Equal(t, []byte("large result"), envelope.ContentParts[0].File.Data)
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
				Request:            testRequest(),
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

func TestPluginPreservesInlineResultWithoutReadFile(t *testing.T) {
	ctx := context.Background()
	artifacts := artifactmem.NewService()
	inv := testInvocation(artifacts)
	manager, err := plugin.NewManager(New(WithThresholdBytes(1)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "large")

	result, err := manager.AfterToolMessages(
		ctx,
		&plugin.AfterToolMessagesArgs{
			Invocation:         inv,
			Request:            &model.Request{},
			ToolResultMessages: []model.Message{msg},
		},
	)
	require.NoError(t, err)
	require.Nil(t, result)
	keys, err := artifacts.ListArtifactKeys(ctx, artifactInfo(inv))
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestPluginPreservesInlineResultWhenArtifactWritesDisabled(t *testing.T) {
	ctx := context.Background()
	artifacts := readOnlyArtifactService{Service: artifactmem.NewService()}
	manager, err := plugin.NewManager(New(WithThresholdBytes(1)))
	require.NoError(t, err)
	msg := model.NewToolMessage("call", "lookup", "large")

	result, err := manager.AfterToolMessages(
		ctx,
		&plugin.AfterToolMessagesArgs{
			Invocation:         testInvocation(artifacts),
			Request:            testRequest(),
			ToolResultMessages: []model.Message{msg},
		},
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestPluginChunksLargeSingleLineJSONForDefaultReadFile(t *testing.T) {
	ctx := context.Background()
	artifacts := artifactmem.NewService()
	inv := testInvocation(artifacts)
	readFile := defaultReadFileTool(t)
	request := &model.Request{
		Tools: map[string]tool.Tool{"read_file": readFile},
	}
	manager, err := plugin.NewManager(New(WithThresholdBytes(1)))
	require.NoError(t, err)
	content := `{"value":"` +
		strings.Repeat("界", artifactChunkSize) +
		`"}`
	require.Greater(t, len(content), 1024*1024)
	msg := model.NewToolMessage("call", "lookup", content)

	result, err := manager.AfterToolMessages(
		agent.NewInvocationContext(ctx, inv),
		&plugin.AfterToolMessagesArgs{
			Invocation:         inv,
			Request:            request,
			ToolResultMessages: []model.Message{msg},
			ToolResultEvent:    toolResultEvent(msg),
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.ToolResultMessages[0].Content, "manifest")

	manifestRef := "artifact://" +
		artifactManifestName(inv, msg.ToolID) + "@0"
	manifestJSON := callReadFile(t, readFile, inv, manifestRef)
	var manifest artifactChunkManifest
	require.NoError(t, json.Unmarshal([]byte(manifestJSON), &manifest))
	require.Equal(t, len(content), manifest.ByteCount)
	require.Equal(t, jsonMimeType, manifest.MimeType)
	require.Greater(t, len(manifest.Parts), 2)

	var reconstructed strings.Builder
	for _, ref := range manifest.Parts {
		reconstructed.WriteString(callReadFile(t, readFile, inv, ref))
	}
	require.Equal(t, content, reconstructed.String())
}

func TestContentMimeType(t *testing.T) {
	require.Equal(t, jsonMimeType, contentMimeType(`{"ok":true}`))
	require.Equal(t, textMimeType, contentMimeType("plain text"))
}

func TestRetrievalToolNamePrefersExactThenSortedPrefix(t *testing.T) {
	request := &model.Request{Tools: map[string]tool.Tool{
		"read_file":     declarationTool{name: "read_file"},
		"aaa_read_file": declarationTool{name: "aaa_read_file"},
		"zzz_read_file": declarationTool{name: "zzz_read_file"},
	}}
	require.Equal(t, "read_file", retrievalToolName(request))

	delete(request.Tools, "read_file")
	require.Equal(t, "aaa_read_file", retrievalToolName(request))
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

func artifactInfo(inv *agent.Invocation) artifact.SessionInfo {
	return artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	}
}

func testRequest() *model.Request {
	readFile := declarationTool{name: "read_file"}
	return &model.Request{
		Tools: map[string]tool.Tool{"read_file": readFile},
	}
}

type declarationTool struct {
	name string
}

func (t declarationTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func defaultReadFileTool(t *testing.T) tool.CallableTool {
	t.Helper()
	toolSet, err := toolfile.NewToolSet(toolfile.WithBaseDir(t.TempDir()))
	require.NoError(t, err)
	for _, candidate := range toolSet.Tools(context.Background()) {
		if candidate.Declaration().Name != "read_file" {
			continue
		}
		readFile, ok := candidate.(tool.CallableTool)
		require.True(t, ok)
		return readFile
	}
	t.Fatal("default file tool set did not expose read_file")
	return nil
}

func callReadFile(
	t *testing.T,
	readFile tool.CallableTool,
	inv *agent.Invocation,
	ref string,
) string {
	t.Helper()
	input, err := json.Marshal(map[string]string{"file_name": ref})
	require.NoError(t, err)
	output, err := readFile.Call(
		agent.NewInvocationContext(context.Background(), inv),
		input,
	)
	require.NoError(t, err)
	encoded, err := json.Marshal(output)
	require.NoError(t, err)
	var response struct {
		Contents string `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(encoded, &response))
	return response.Contents
}

type failingArtifactService struct {
	artifact.Service
	err error
}

func (s failingArtifactService) SaveArtifact(
	context.Context,
	artifact.SessionInfo,
	string,
	*artifact.Artifact,
) (int, error) {
	return 0, s.err
}

type readOnlyArtifactService struct {
	artifact.Service
}

func (readOnlyArtifactService) ArtifactWritesEnabled() bool {
	return false
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
