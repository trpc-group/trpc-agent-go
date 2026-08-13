//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/resourcestore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	ctool "trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubResourceKnowledge struct {
	listSourcesRequest   *knowledge.ListSourcesRequest
	listResourcesRequest *knowledge.ListResourcesRequest
	readResourceRequest  *knowledge.ReadResourceRequest
	grepResourceRequest  *knowledge.GrepResourceRequest

	listSourcesResult   *knowledge.ListSourcesResult
	listResourcesResult *knowledge.ListResourcesResult
	readResourceResult  *knowledge.ReadResourceResult
	grepResourceResult  *knowledge.GrepResourceResult

	listSourcesError   error
	listResourcesError error
	readResourceError  error
	grepResourceError  error
}

var _ knowledge.ResourceKnowledge = (*stubResourceKnowledge)(nil)

func (s *stubResourceKnowledge) ListSources(
	_ context.Context,
	req *knowledge.ListSourcesRequest,
) (*knowledge.ListSourcesResult, error) {
	s.listSourcesRequest = req
	return s.listSourcesResult, s.listSourcesError
}

func (s *stubResourceKnowledge) ListResources(
	_ context.Context,
	req *knowledge.ListResourcesRequest,
) (*knowledge.ListResourcesResult, error) {
	s.listResourcesRequest = req
	return s.listResourcesResult, s.listResourcesError
}

func (s *stubResourceKnowledge) ReadResource(
	_ context.Context,
	req *knowledge.ReadResourceRequest,
) (*knowledge.ReadResourceResult, error) {
	s.readResourceRequest = req
	return s.readResourceResult, s.readResourceError
}

func (s *stubResourceKnowledge) GrepResource(
	_ context.Context,
	req *knowledge.GrepResourceRequest,
) (*knowledge.GrepResourceResult, error) {
	s.grepResourceRequest = req
	return s.grepResourceResult, s.grepResourceError
}

func TestResourceToolSetDeclarations(t *testing.T) {
	toolSet := NewResourceToolSet(&stubResourceKnowledge{})
	require.Equal(t, defaultResourceToolSetName, toolSet.Name())
	require.NoError(t, toolSet.Close())
	tests := []struct {
		name     string
		required []string
	}{
		{name: "list_sources"},
		{name: "list_resources", required: []string{"source_id"}},
		{name: "read", required: []string{"source_id", "path"}},
		{name: "grep", required: []string{"source_id", "path", "pattern"}},
	}
	tools := toolSet.Tools(context.Background())
	require.Len(t, tools, len(tests))
	for i, tt := range tests {
		declaration := tools[i].Declaration()
		require.Equal(t, tt.name, declaration.Name)
		require.NotEmpty(t, declaration.Description)
		require.NotNil(t, declaration.InputSchema)
		require.Equal(t, "object", declaration.InputSchema.Type)
		require.ElementsMatch(t, tt.required, declaration.InputSchema.Required)
	}
}

func TestResourceToolsValidateRequiredArguments(t *testing.T) {
	toolSet := NewResourceToolSet(&stubResourceKnowledge{})
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{name: "list_sources", args: "null", wantErr: "list sources request cannot be nil"},
		{name: "list_resources", args: `{}`, wantErr: "source_id is required"},
		{name: "read", args: `{"source_id":"source-1"}`, wantErr: "source_id and path are required"},
		{name: "grep", args: `{"source_id":"source-1","path":"a.txt"}`, wantErr: "source_id, path, and pattern are required"},
		{name: "grep", args: `{"source_id":"source-1","path":"a.txt","pattern":"key","before":-1}`, wantErr: "before and after cannot be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"_"+tt.wantErr, func(t *testing.T) {
			_, err := resourceToolByName(t, toolSet, tt.name).(ctool.CallableTool).Call(
				context.Background(), []byte(tt.args),
			)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestResourceToolsDispatch(t *testing.T) {
	stub := &stubResourceKnowledge{
		listSourcesResult: &knowledge.ListSourcesResult{Sources: []*knowledge.SourceInfo{{ID: "source-1"}}},
		listResourcesResult: &knowledge.ListResourcesResult{
			Resources: []*resourcestore.ResourceInfo{{SourceID: "source-1", Path: "docs"}},
		},
		readResourceResult: &knowledge.ReadResourceResult{SourceID: "source-1", Path: "docs/config.md"},
		grepResourceResult: &knowledge.GrepResourceResult{SourceID: "source-1", Path: "docs/config.md"},
	}
	toolSet := NewResourceToolSet(stub)

	listSourcesRequest := &knowledge.ListSourcesRequest{}
	require.Same(t, stub.listSourcesResult, callResourceTool(t, toolSet, "list_sources", listSourcesRequest))
	require.Equal(t, listSourcesRequest, stub.listSourcesRequest)

	listResourcesRequest := &knowledge.ListResourcesRequest{
		SourceID:   "source-1",
		ParentPath: "docs",
	}
	require.Same(t, stub.listResourcesResult, callResourceTool(t, toolSet, "list_resources", listResourcesRequest))
	require.Equal(t, listResourcesRequest, stub.listResourcesRequest)

	readResourceRequest := &knowledge.ReadResourceRequest{
		SourceID:  "source-1",
		Path:      "docs/config.md",
		StartLine: 11,
		EndLine:   20,
	}
	require.Same(t, stub.readResourceResult, callResourceTool(t, toolSet, "read", readResourceRequest))
	require.Equal(t, readResourceRequest, stub.readResourceRequest)

	grepResourceRequest := &knowledge.GrepResourceRequest{
		SourceID:   "source-1",
		Path:       "docs/config.md",
		Pattern:    "timeout",
		Regex:      true,
		Before:     2,
		After:      3,
		MaxMatches: 4,
	}
	require.Same(t, stub.grepResourceResult, callResourceTool(t, toolSet, "grep", grepResourceRequest))
	require.Equal(t, grepResourceRequest, stub.grepResourceRequest)
}

func TestResourceToolsWrapKnowledgeErrors(t *testing.T) {
	backendError := errors.New("backend unavailable")
	stub := &stubResourceKnowledge{
		listSourcesError:   backendError,
		listResourcesError: backendError,
		readResourceError:  backendError,
		grepResourceError:  backendError,
	}
	toolSet := NewResourceToolSet(stub)
	tests := []struct {
		name       string
		args       any
		wantPrefix string
	}{
		{name: "list_sources", args: &knowledge.ListSourcesRequest{}, wantPrefix: "list knowledge sources"},
		{name: "list_resources", args: &knowledge.ListResourcesRequest{SourceID: "source-1"}, wantPrefix: "list knowledge resources"},
		{name: "read", args: &knowledge.ReadResourceRequest{SourceID: "source-1", Path: "a.txt"}, wantPrefix: "read knowledge resource"},
		{name: "grep", args: &knowledge.GrepResourceRequest{SourceID: "source-1", Path: "a.txt", Pattern: "key"}, wantPrefix: "grep knowledge resource"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callResourceToolWithError(t, toolSet, tt.name, tt.args)
			require.ErrorIs(t, err, backendError)
			require.ErrorContains(t, err, tt.wantPrefix)
		})
	}
}

func TestConvertSearchResultsProjectsResourceStoreReference(t *testing.T) {
	const providerURI = "hdfs://private-cluster/secret/config.md"
	result := &knowledge.SearchResult{Documents: []*knowledge.Result{{
		Document: &document.Document{
			ID:      "chunk-1",
			Content: "resource metadata",
			Metadata: map[string]any{
				"owner":                      "platform",
				source.MetaSourceID:          "source-1",
				source.MetaResourcePath:      "guides/config.md",
				source.MetaResourceStartLine: 21,
				source.MetaResourceEndLine:   40,
				source.MetaURI:               providerURI,
			},
		},
		Score: 0.91,
	}}}

	response, err := convertSearchResults(result, nil, true)
	require.NoError(t, err)
	require.Len(t, response.Documents, 1)
	require.Equal(t, map[string]any{
		"owner":                      "platform",
		source.MetaSourceID:          "source-1",
		source.MetaResourcePath:      "guides/config.md",
		source.MetaResourceStartLine: 21,
		source.MetaResourceEndLine:   40,
	}, response.Documents[0].Metadata)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), source.MetaURI)
	require.NotContains(t, string(encoded), providerURI)
}

func TestConvertSearchResultsRejectsUnsafeResourceMetadata(t *testing.T) {
	const providerURI = "hdfs://private-cluster/secret/config.md"
	result := &knowledge.SearchResult{Documents: []*knowledge.Result{{
		Document: &document.Document{
			ID: "chunk-1",
			Metadata: map[string]any{
				"owner":                 "platform",
				source.MetaSourceID:     "source-1",
				source.MetaResourcePath: "./" + providerURI,
				source.MetaURI:          providerURI,
			},
		},
	}}}

	response, err := convertSearchResults(result, nil, true)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"owner": "platform"}, response.Documents[0].Metadata)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), providerURI)
}

func TestConvertSearchResultsDropsInvalidResourceLineRange(t *testing.T) {
	result := &knowledge.SearchResult{Documents: []*knowledge.Result{{
		Document: &document.Document{Metadata: map[string]any{
			source.MetaSourceID:          "source-1",
			source.MetaResourcePath:      "guides/config.md",
			source.MetaResourceStartLine: 40,
			source.MetaResourceEndLine:   21,
		}},
	}}}

	response, err := convertSearchResults(result, nil, true)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		source.MetaSourceID:     "source-1",
		source.MetaResourcePath: "guides/config.md",
	}, response.Documents[0].Metadata)
}

func resourceToolByName(t *testing.T, toolSet ctool.ToolSet, name string) ctool.Tool {
	t.Helper()
	for _, resourceTool := range toolSet.Tools(context.Background()) {
		if resourceTool.Declaration().Name == name {
			return resourceTool
		}
	}
	require.FailNow(t, "resource tool not found", "name=%q", name)
	return nil
}

func callResourceTool(t *testing.T, toolSet ctool.ToolSet, name string, args any) any {
	t.Helper()
	result, err := callResourceToolWithError(t, toolSet, name, args)
	require.NoError(t, err)
	return result
}

func callResourceToolWithError(t *testing.T, toolSet ctool.ToolSet, name string, args any) (any, error) {
	t.Helper()
	payload, err := json.Marshal(args)
	require.NoError(t, err)
	callable, ok := resourceToolByName(t, toolSet, name).(ctool.CallableTool)
	require.True(t, ok)
	return callable.Call(context.Background(), payload)
}
