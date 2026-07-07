//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeEmbedder maps known text to fixed vectors so cosine similarity is
// deterministic. Unknown text embeds to a neutral vector.
type fakeEmbedder struct {
	vectors map[string][]float64
}

func (e *fakeEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	v, _, err := e.GetEmbeddingWithUsage(ctx, text)
	return v, err
}

func (e *fakeEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	usage := map[string]any{"prompt_tokens": int64(1), "total_tokens": int64(1)}
	if v, ok := e.vectors[text]; ok {
		return v, usage, nil
	}
	return []float64{0, 0, 1}, usage, nil
}

func (e *fakeEmbedder) GetDimensions() int { return 3 }

// newKnowledgePlugin builds a plugin whose deferred tools are semantically
// searched via a fakeEmbedder placing get_weather near "weather" and get_time
// near "time".
func newKnowledgePlugin(t *testing.T) *Plugin {
	t.Helper()
	weather := newTestTool("get_weather", "weather forecast")
	timeTool := newTestTool("get_time", "current time")
	emb := &fakeEmbedder{vectors: map[string][]float64{
		"weather":            {1, 0, 0},
		"time":               {0, 1, 0},
		toolToText(weather):  {1, 0, 0},
		toolToText(timeTool): {0, 1, 0},
	}}
	k, err := NewToolKnowledge(emb, WithVectorStore(inmemory.New()))
	require.NoError(t, err)

	return NewPlugin(nil,
		WithToolKnowledge(k),
		WithToolboxes([]Toolbox{{
			Name:        "utility",
			Description: "everyday helpers",
			Tools:       []tool.Tool{weather, timeTool},
		}}),
	)
}

func TestNewToolKnowledge_NilEmbedder(t *testing.T) {
	_, err := NewToolKnowledge(nil)
	require.Error(t, err)
}

func TestEmbeddingSearch_RanksBySimilarity(t *testing.T) {
	p := newKnowledgePlugin(t)
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"weather"}, MaxResults: 1})
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "get_weather", res.Tools[0].Name)
}

func TestEmbeddingSearch_ScopedToNamespace(t *testing.T) {
	p := newKnowledgePlugin(t)
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "utility", Queries: []string{"time"}, MaxResults: 1})
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "get_time", res.Tools[0].Name)
}

func TestEmbeddingSearch_UnknownNamespaceErrors(t *testing.T) {
	p := newKnowledgePlugin(t)
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "ghost", Queries: []string{"weather"}})
	assert.Empty(t, res.Tools, "unknown namespace returns no tools")
	// The raw payload carries a structured unknown_namespace status.
	raw, err := p.searchTools(ctx, toolSearchInput{Namespace: "ghost", Queries: []string{"weather"}})
	require.NoError(t, err)
	assert.Contains(t, raw, "unknown_namespace")
}

func TestEmbeddingSearch_ExactNameStillUsesIndexPath(t *testing.T) {
	// tool_names loads bypass the embedding path entirely (deterministic).
	p := newKnowledgePlugin(t)
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"get_time"}})
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "get_time", res.Tools[0].Name)
}

func TestEmbeddingSearch_OverflowBeyondMaxResults(t *testing.T) {
	p := newKnowledgePlugin(t)
	ctx, _ := ctxWithInvocation()

	// Two OR-combined queries match both tools; cap at 1 → one overflow.
	res := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"weather", "time"}, MaxResults: 1})
	assert.Len(t, res.Tools, 1)
	assert.Len(t, res.AdditionalCandidates, 1)
}

func TestEmbeddingSearch_RecordsUsage(t *testing.T) {
	p := newKnowledgePlugin(t)
	// beforeModel seeds the usage accumulator into the context.
	ctx, inv := ctxWithInvocation()
	res, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: &model.Request{}})
	require.NoError(t, err)
	require.NotNil(t, res)
	ctx = res.Context
	// re-attach the invocation (withUsageAccumulator wraps the same ctx).
	_ = inv

	_ = callSearch(t, ctx, p, toolSearchInput{Queries: []string{"weather"}})
	usage, ok := ToolSearchUsageFromContext(ctx)
	require.True(t, ok)
	assert.Positive(t, usage.TotalTokens, "embedding token usage recorded")
}

func TestKeywordPathUnaffectedWithoutKnowledge(t *testing.T) {
	// No WithToolKnowledge → the built-in keyword matching still runs.
	p := NewPlugin(nil, WithToolboxes([]Toolbox{{
		Name:  "utility",
		Tools: []tool.Tool{newTestTool("get_weather", "weather forecast")},
	}}))
	ctx, _ := ctxWithInvocation()
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "utility", Queries: []string{"weather"}})
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "get_weather", res.Tools[0].Name)
}

func TestToolToText(t *testing.T) {
	txt := toolToText(newTestTool("get_time", "returns the time"))
	assert.Contains(t, txt, "Tool: get_time")
	assert.Contains(t, txt, "Description: returns the time")
	assert.Empty(t, toolToText(nil))
}

func TestAddEmbedderUsage_TokenTypes(t *testing.T) {
	// Embedders differ: OpenAI reports int64, Ollama int, others float64.
	// All must accumulate rather than be silently dropped.
	var u model.Usage
	addEmbedderUsage(&u, map[string]any{"prompt_tokens": int64(2), "total_tokens": int64(3)})
	addEmbedderUsage(&u, map[string]any{"prompt_tokens": int(4)})
	addEmbedderUsage(&u, map[string]any{"total_tokens": float64(5)})
	assert.Equal(t, 6, u.PromptTokens)
	assert.Equal(t, 8, u.TotalTokens)
}

// failingEmbedder always errors, used to exercise the fail-open path.
type failingEmbedder struct{}

func (failingEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	return nil, errTest("embedder down")
}

func (failingEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	return nil, nil, errTest("embedder down")
}

func (failingEmbedder) GetDimensions() int { return 3 }

type errTest string

func (e errTest) Error() string { return string(e) }

func TestWithMaxTools_CapsResults(t *testing.T) {
	// WithMaxTools is an alias for WithMaxTools: cap tools loaded with schemas.
	weather := newTestTool("get_weather", "weather forecast")
	timeTool := newTestTool("get_time", "current time")
	emb := &fakeEmbedder{vectors: map[string][]float64{
		"helper":             {1, 1, 0},
		toolToText(weather):  {1, 0, 0},
		toolToText(timeTool): {0, 1, 0},
	}}
	k, err := NewToolKnowledge(emb, WithVectorStore(inmemory.New()))
	require.NoError(t, err)

	p := NewPlugin(nil,
		WithToolKnowledge(k),
		WithMaxTools(1),
		WithToolboxes([]Toolbox{{Name: "utility", Tools: []tool.Tool{weather, timeTool}}}),
	)
	ctx, _ := ctxWithInvocation()
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "utility", Queries: []string{"helper"}})
	assert.Len(t, res.Tools, 1, "WithMaxTools(1) caps schema-loaded tools")
	assert.Len(t, res.AdditionalCandidates, 1)
}

func TestWithFailOpen_FallsBackToKeyword(t *testing.T) {
	weather := newTestTool("get_weather", "weather forecast")
	k, err := NewToolKnowledge(failingEmbedder{}, WithVectorStore(inmemory.New()))
	require.NoError(t, err)

	p := NewPlugin(nil,
		WithToolKnowledge(k),
		WithFailOpen(),
		WithToolboxes([]Toolbox{{Name: "utility", Tools: []tool.Tool{weather}}}),
	)
	ctx, _ := ctxWithInvocation()
	// Embedding fails; fail-open falls back to keyword matching, which still
	// finds get_weather by the term "weather".
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "utility", Queries: []string{"weather"}})
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "get_weather", res.Tools[0].Name)
}

func TestWithoutFailOpen_EmbeddingErrorPropagates(t *testing.T) {
	weather := newTestTool("get_weather", "weather forecast")
	k, err := NewToolKnowledge(failingEmbedder{}, WithVectorStore(inmemory.New()))
	require.NoError(t, err)

	p := NewPlugin(nil,
		WithToolKnowledge(k),
		WithToolboxes([]Toolbox{{Name: "utility", Tools: []tool.Tool{weather}}}),
	)
	ctx, _ := ctxWithInvocation()
	_, callErr := p.searchTools(ctx, toolSearchInput{Namespace: "utility", Queries: []string{"weather"}})
	require.Error(t, callErr, "without fail-open the embedding error surfaces")
}
