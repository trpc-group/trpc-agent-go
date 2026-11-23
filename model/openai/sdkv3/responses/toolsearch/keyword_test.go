//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestKeywordToolSearchLimit(t *testing.T) {
	var candidates []responses.ToolSearchEntry
	for i := range 7 {
		name := fmt.Sprintf("action_%d", i)
		candidates = append(candidates, responses.ToolSearchEntry{Name: name, Tool: keywordTool(name, "Execute the action.")})
	}
	for _, tc := range []struct {
		name string
		opts []KeywordOption
		want int
	}{
		{"default", nil, 5},
		{"nil option", []KeywordOption{nil}, 5},
		{"positive", []KeywordOption{WithKeywordMaxResults(2)}, 2},
		{"zero", []KeywordOption{WithKeywordMaxResults(0)}, 5},
		{"negative", []KeywordOption{WithKeywordMaxResults(-1)}, 5},
		{"last wins", []KeywordOption{WithKeywordMaxResults(1), WithKeywordMaxResults(0)}, 5},
		{"larger than catalog", []KeywordOption{WithKeywordMaxResults(10)}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewKeyword(tc.opts...)(context.Background(), &responses.ToolSearchRequest{
				Query: "action", Candidates: candidates,
			})
			require.NoError(t, err)
			require.Equal(t, candidates[:tc.want], got)
		})
	}
}

func TestKeywordToolSearchPreservesEntriesAndVisibleIdentity(t *testing.T) {
	billing := &keywordTestTool{declaration: &tool.Declaration{Name: "internal_billing", Description: "Fetch an invoice."}}
	crm := &keywordTestTool{declaration: &tool.Declaration{Name: "internal_crm", Description: "Fetch a customer."}}
	candidates := []responses.ToolSearchEntry{
		{Namespace: "billing", NamespaceDescription: "Invoice tools.", Name: "lookup", Tool: billing},
		{Namespace: "crm", NamespaceDescription: "Customer tools.", Name: "lookup", Tool: crm},
	}
	handler := NewKeyword()
	for _, tc := range []struct {
		query string
		want  []responses.ToolSearchEntry
	}{
		{"billing", candidates[:1]},
		{"crm", candidates[1:]},
		{"lookup", candidates},
		{"+billing lookup", candidates[:1]},
		{"internal_billing", nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: tc.query, Candidates: candidates})
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			for i := range got {
				require.Same(t, tc.want[i].Tool, got[i].Tool)
			}
		})
	}
	require.Same(t, billing, candidates[0].Tool)
	require.Same(t, crm, candidates[1].Tool)
}

func TestKeywordToolSearchRankAndNameFallback(t *testing.T) {
	candidates := []responses.ToolSearchEntry{
		{Name: "report_detail", Tool: keywordTool("internal_detail", "Load data.")},
		{Name: "report_history", Tool: keywordTool("internal_history", "Load report history.")},
	}
	got, err := NewKeyword()(context.Background(), &responses.ToolSearchRequest{Query: "report", Candidates: candidates})
	require.NoError(t, err)
	require.Equal(t, []responses.ToolSearchEntry{candidates[1], candidates[0]}, got)

	fallback := responses.ToolSearchEntry{Tool: keywordTool("get", "A tool whose name is otherwise a filler word.")}
	for _, query := range []string{"GET", "+get"} {
		got, err = NewKeyword()(context.Background(), &responses.ToolSearchRequest{
			Query: query, Candidates: []responses.ToolSearchEntry{fallback},
		})
		require.NoError(t, err)
		require.Equal(t, []responses.ToolSearchEntry{fallback}, got)
	}
	for _, tc := range []struct{ name, query string }{
		{"media-editor", "media"},
		{"GetTemperature", "temperature"},
		{"lookup_ids", "ids"},
		{"get_id", "id"},
	} {
		candidate := responses.ToolSearchEntry{Name: tc.name, Tool: keywordTool("internal_tool", "")}
		got, err = NewKeyword()(context.Background(), &responses.ToolSearchRequest{
			Query: tc.query, Candidates: []responses.ToolSearchEntry{candidate},
		})
		require.NoError(t, err)
		require.Equal(t, []responses.ToolSearchEntry{candidate}, got)
	}
}

func TestKeywordToolSearchEmptyAndInvalidCandidates(t *testing.T) {
	handler := NewKeyword()
	for _, req := range []*responses.ToolSearchRequest{
		nil,
		{},
		{Query: " \t\n"},
		{Query: "the tools and for"},
		{Query: "the tools and for", Candidates: []responses.ToolSearchEntry{{Tool: keywordTool("and_for", "The tools.")}}},
		{Query: "unknown"},
		{Query: "invoice", Candidates: []responses.ToolSearchEntry{{}, {Tool: &keywordTestTool{}}}},
	} {
		got, err := handler(context.Background(), req)
		require.NoError(t, err)
		require.Nil(t, got)
	}
	valid := responses.ToolSearchEntry{Tool: keywordTool("report", "")}
	got, err := handler(context.Background(), &responses.ToolSearchRequest{
		Query: "report", Candidates: []responses.ToolSearchEntry{{}, {Tool: &keywordTestTool{}}, valid},
	})
	require.NoError(t, err)
	require.Equal(t, []responses.ToolSearchEntry{valid}, got)
}

func TestKeywordToolSearchUnicode(t *testing.T) {
	candidate := responses.ToolSearchEntry{Tool: keywordTool("forecast", "获取天气预报")}
	for _, query := range []string{"天气，预报", "天气 天气 天气 预报"} {
		got, err := NewKeyword()(context.Background(), &responses.ToolSearchRequest{
			Query: query, Candidates: []responses.ToolSearchEntry{candidate},
		})
		require.NoError(t, err)
		require.Equal(t, []responses.ToolSearchEntry{candidate}, got)
	}
}

func TestKeywordToolSearchKeepsDescriptionOnlyCapability(t *testing.T) {
	candidates := []responses.ToolSearchEntry{
		{Tool: keywordTool("list_tasks", "List outstanding tasks.")},
		{Tool: keywordTool("reminder", "Deliver alarms.")},
	}
	for _, query := range []string{"tasks and alarms", "tasks tasks tasks and alarms"} {
		got, err := NewKeyword()(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: candidates})
		require.NoError(t, err)
		require.Equal(t, candidates, got)
	}
}

func TestKeywordToolSearchCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := NewKeyword()(ctx, &responses.ToolSearchRequest{Query: "report"})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, got)

	for _, query := range []string{"report", "the tools"} {
		ctx, cancel = context.WithCancel(context.Background())
		got, err = NewKeyword()(ctx, &responses.ToolSearchRequest{
			Query: query, Candidates: []responses.ToolSearchEntry{{Tool: &keywordTestTool{
				declaration: &tool.Declaration{Name: "report"}, onDeclaration: cancel,
			}}},
		})
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, got)
	}
}

func TestKeywordToolSearchConcurrentCalls(t *testing.T) {
	option := WithKeywordMaxResults(0)
	candidate := responses.ToolSearchEntry{Tool: keywordTool("report", "Read a report.")}
	req := &responses.ToolSearchRequest{Query: "report", Candidates: []responses.ToolSearchEntry{candidate}}
	handler := NewKeyword(option)
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Reusing an option concurrently must also leave it immutable.
			_ = NewKeyword(option)
			got, err := handler(context.Background(), req)
			if err == nil && !reflect.DeepEqual(got, []responses.ToolSearchEntry{candidate}) {
				err = fmt.Errorf("unexpected search result: %v", got)
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func keywordTool(name, description string) *keywordTestTool {
	return &keywordTestTool{declaration: &tool.Declaration{Name: name, Description: description}}
}

type keywordTestTool struct {
	declaration   *tool.Declaration
	onDeclaration func()
}

func (t *keywordTestTool) Declaration() *tool.Declaration {
	if t.onDeclaration != nil {
		t.onDeclaration()
	}
	return t.declaration
}

func (*keywordTestTool) Call(context.Context, []byte) (any, error) { return "", nil }
