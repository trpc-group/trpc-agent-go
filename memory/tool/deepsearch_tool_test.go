//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	coretool "trpc.group/trpc-go/trpc-agent-go/tool"
)

type deepSearchQueryServiceMock struct {
	*mockDeepSearchMemoryService
	err error
}

func newDeepSearchQueryServiceMock() *deepSearchQueryServiceMock {
	return &deepSearchQueryServiceMock{
		mockDeepSearchMemoryService: newMockDeepSearchMemoryService(),
	}
}

func (m *deepSearchQueryServiceMock) SearchCues(
	_ context.Context,
	req deepsearch.CueSearchRequest,
) (*deepsearch.CueSearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &deepsearch.CueSearchResult{
		Query: req.Query,
		Cues:  []deepsearch.Cue{{ID: "cue-1", Text: "kyoto", Score: 1}},
	}, nil
}

func (m *deepSearchQueryServiceMock) ExpandTags(
	_ context.Context,
	_ deepsearch.TagExpandRequest,
) (*deepsearch.TagExpandResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &deepsearch.TagExpandResult{
		Tags:  []deepsearch.Tag{{ID: "tag-1", Text: "travel"}},
		Paths: []deepsearch.Path{{Score: 1}},
	}, nil
}

func (m *deepSearchQueryServiceMock) LoadContents(
	_ context.Context,
	_ deepsearch.ContentLoadRequest,
) (*deepsearch.ContentLoadResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &deepsearch.ContentLoadResult{
		Contents: []deepsearch.Content{{ID: "content-1", Text: "Kyoto trip"}},
	}, nil
}

func (m *deepSearchQueryServiceMock) EdgesByTag(
	_ context.Context,
	req deepsearch.EdgesByTagRequest,
) (*deepsearch.EdgesByTagResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &deepsearch.EdgesByTagResult{
		Query: req.Query,
		Tags:  []deepsearch.Tag{{ID: "tag-1", Text: "travel"}},
		Paths: []deepsearch.Path{{Score: 1}},
	}, nil
}

func (m *deepSearchQueryServiceMock) QueryConversationTime(
	_ context.Context,
	req deepsearch.QueryConversationTimeRequest,
) (*deepsearch.QueryResult, error) {
	return m.queryResult(req.Query)
}

func (m *deepSearchQueryServiceMock) QueryEventKeywords(
	_ context.Context,
	req deepsearch.QueryEventKeywordsRequest,
) (*deepsearch.QueryResult, error) {
	return m.queryResult(req.Query)
}

func (m *deepSearchQueryServiceMock) QueryEventContext(
	_ context.Context,
	req deepsearch.QueryEventContextRequest,
) (*deepsearch.QueryResult, error) {
	return m.queryResult(req.Query)
}

func (m *deepSearchQueryServiceMock) QueryPersonalInformation(
	_ context.Context,
	req deepsearch.QueryPersonalInformationRequest,
) (*deepsearch.QueryResult, error) {
	return m.queryResult(req.Query)
}

func (m *deepSearchQueryServiceMock) QueryPersonalAspect(
	_ context.Context,
	req deepsearch.QueryPersonalAspectRequest,
) (*deepsearch.QueryResult, error) {
	return m.queryResult(req.Query)
}

func (m *deepSearchQueryServiceMock) QueryTopicEvents(
	_ context.Context,
	req deepsearch.QueryTopicEventsRequest,
) (*deepsearch.QueryResult, error) {
	return m.queryResult(req.Query)
}

func (m *deepSearchQueryServiceMock) queryResult(query string) (*deepsearch.QueryResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &deepsearch.QueryResult{
		Query:    query,
		Contents: []deepsearch.Content{{ID: "content-1", Text: "Kyoto trip"}},
	}, nil
}

func TestDeepSearchTools_Success(t *testing.T) {
	service := newDeepSearchQueryServiceMock()
	ctx := createMockContext("test-app", "test-user", service)
	tests := []struct {
		name string
		tool coretool.CallableTool
		args string
	}{
		{
			name: "cue search",
			tool: NewCueSearchTool(),
			args: `{"query":"Kyoto","max_results":3,"min_score":0.2}`,
		},
		{
			name: "tag expand",
			tool: NewTagExpandTool(),
			args: `{"cue_ids":["cue-1"],"max_tags_per_cue":2,"max_contents":3,"include_content":true}`,
		},
		{
			name: "content load",
			tool: NewContentLoadTool(),
			args: `{"content_ids":["content-1"],"max_results":2}`,
		},
		{
			name: "edges by tag",
			tool: NewEdgesByTagTool(),
			args: `{"tags":["travel"],"query":"Kyoto","max_results":2,"include_content":true}`,
		},
		{
			name: "conversation time",
			tool: NewQueryConversationTimeTool(),
			args: `{"query":"trip","time_after":"2024-01-01","time_before":"2024-12-31","max_results":2}`,
		},
		{
			name: "event keywords",
			tool: NewQueryEventKeywordsTool(),
			args: `{"query":"trip","keywords":["Kyoto"],"time_after":"2024-01-01T00:00:00Z","max_results":2}`,
		},
		{
			name: "event context",
			tool: NewQueryEventContextTool(),
			args: `{"query":"nearby","content_ids":["content-1"],"max_results":2}`,
		},
		{
			name: "personal information",
			tool: NewQueryPersonalInformationTool(),
			args: `{"query":"travel","aspects":["preference"],"max_results":2}`,
		},
		{
			name: "personal aspect",
			tool: NewQueryPersonalAspectTool(),
			args: `{"aspect":"preference","query":"travel","max_results":2}`,
		},
		{
			name: "topic events",
			tool: NewQueryTopicEventsTool(),
			args: `{"topic":"travel","query":"Kyoto","time_before":"2024-12-31 23:59:59","max_results":2}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.tool.Call(ctx, []byte(test.args))
			require.NoError(t, err)
			require.NotNil(t, result)
		})
	}
}

func TestDeepSearchTools_ValidateInput(t *testing.T) {
	service := newDeepSearchQueryServiceMock()
	ctx := createMockContext("test-app", "test-user", service)
	tests := []struct {
		name string
		tool coretool.CallableTool
		args string
		want string
	}{
		{name: "cue search", tool: NewCueSearchTool(), args: `{}`, want: "query is required"},
		{name: "tag expand", tool: NewTagExpandTool(), args: `{}`, want: "cue_ids or cues are required"},
		{name: "content load", tool: NewContentLoadTool(), args: `{}`, want: "content_ids or refs are required"},
		{name: "edges by tag", tool: NewEdgesByTagTool(), args: `{}`, want: "tags or query is required"},
		{name: "event keywords", tool: NewQueryEventKeywordsTool(), args: `{}`, want: "query or keywords is required"},
		{name: "event context", tool: NewQueryEventContextTool(), args: `{}`, want: "content_ids or refs is required"},
		{name: "personal aspect", tool: NewQueryPersonalAspectTool(), args: `{}`, want: "aspect is required"},
		{name: "topic events", tool: NewQueryTopicEventsTool(), args: `{}`, want: "topic is required"},
		{
			name: "conversation time",
			tool: NewQueryConversationTimeTool(),
			args: `{"time_after":"not-a-time"}`,
			want: "invalid time",
		},
		{
			name: "event keyword time",
			tool: NewQueryEventKeywordsTool(),
			args: `{"query":"trip","time_before":"not-a-time"}`,
			want: "invalid time",
		},
		{
			name: "topic event time",
			tool: NewQueryTopicEventsTool(),
			args: `{"topic":"travel","time_after":"not-a-time"}`,
			want: "invalid time",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.tool.Call(ctx, []byte(test.args))
			require.ErrorContains(t, err, test.want)
			assert.Nil(t, result)
		})
	}
}

func TestDeepSearchTools_PropagateServiceErrors(t *testing.T) {
	serviceErr := errors.New("deepsearch unavailable")
	service := newDeepSearchQueryServiceMock()
	service.err = serviceErr
	ctx := createMockContext("test-app", "test-user", service)
	tests := []struct {
		name string
		tool coretool.CallableTool
		args string
	}{
		{name: "cue search", tool: NewCueSearchTool(), args: `{"query":"Kyoto"}`},
		{name: "tag expand", tool: NewTagExpandTool(), args: `{"cues":["Kyoto"]}`},
		{name: "content load", tool: NewContentLoadTool(), args: `{"content_ids":["content-1"]}`},
		{name: "edges by tag", tool: NewEdgesByTagTool(), args: `{"tags":["travel"]}`},
		{name: "conversation time", tool: NewQueryConversationTimeTool(), args: `{}`},
		{name: "event keywords", tool: NewQueryEventKeywordsTool(), args: `{"query":"Kyoto"}`},
		{name: "event context", tool: NewQueryEventContextTool(), args: `{"content_ids":["content-1"]}`},
		{name: "personal information", tool: NewQueryPersonalInformationTool(), args: `{}`},
		{name: "personal aspect", tool: NewQueryPersonalAspectTool(), args: `{"aspect":"travel"}`},
		{name: "topic events", tool: NewQueryTopicEventsTool(), args: `{"topic":"travel"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.tool.Call(ctx, []byte(test.args))
			require.ErrorIs(t, err, serviceErr)
			assert.Nil(t, result)
		})
	}
}

func TestDeepSearchQueryTool_RequiresQueryService(t *testing.T) {
	ctx := createMockContext("test-app", "test-user", newMockDeepSearchMemoryService())

	result, err := NewEdgesByTagTool().Call(ctx, []byte(`{"tags":["travel"]}`))

	require.ErrorContains(t, err, "does not implement deepsearch.QueryService")
	assert.Nil(t, result)
}

func TestDeepSearchToolHelpers(t *testing.T) {
	for _, value := range []string{
		"",
		"2024-01-02",
		"2024-01-02 03:04:05",
		"2024-01-02T03:04:05",
		"2024-01-02T03:04:05Z",
	} {
		_, err := parseToolTime(value)
		require.NoError(t, err)
	}

	_, err := parseToolTime("January 2")
	require.Error(t, err)

	response := queryResponse(nil)
	require.NotNil(t, response)
	assert.Empty(t, response.Contents)

	req := &QueryPersonalInformationRequest{
		Query:      "travel",
		Aspects:    []string{"preference"},
		MaxResults: 4,
	}
	assert.Equal(t, "travel", reqQuery(req))
	assert.Equal(t, 4, reqMaxResults(req))
	assert.Equal(t, []string{"preference"}, reqAspects(req))
	assert.Empty(t, reqAspects(nil))

	after, before, err := parseToolTimeRange("2024-01-01", "2024-12-31")
	require.NoError(t, err)
	assert.Equal(t, 2024, after.Year())
	assert.Equal(t, 2024, before.Year())
	assert.False(t, time.Time{}.Equal(after))
}

func TestDeepSearchToolSet(t *testing.T) {
	service := newDeepSearchQueryServiceMock()
	ctx := createMockContext("test-app", "test-user", service)
	toolSet := NewDeepSearchToolSet()

	assert.Equal(t, DeepSearchToolSetName, toolSet.Name())
	require.NoError(t, toolSet.Close())

	tools := toolSet.Tools(ctx)
	require.Len(t, tools, 10)
	for _, deepSearchTool := range tools {
		declaration := deepSearchTool.Declaration()
		require.NotNil(t, declaration)
		assert.NotContains(t, declaration.Name, DeepSearchToolSetName+"_")
	}

	callable, ok := tools[0].(coretool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(ctx, []byte(`{"query":"Kyoto"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestRelativeDeepSearchTool_RejectsNonCallableTool(t *testing.T) {
	wrapped := relativeDeepSearchTool{
		tool: declarationOnlyTool{
			declaration: &coretool.Declaration{Name: "memory_deepsearch_test"},
		},
	}

	result, err := wrapped.Call(context.Background(), nil)

	require.ErrorContains(t, err, "is not callable")
	assert.Nil(t, result)
}

type declarationOnlyTool struct {
	declaration *coretool.Declaration
}

func (t declarationOnlyTool) Declaration() *coretool.Declaration {
	return t.declaration
}
