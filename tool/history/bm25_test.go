//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package history

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTokenizeText_English(t *testing.T) {
	tokens := tokenizeText("Hello World, this is a test!")
	require.Contains(t, tokens, "hello")
	require.Contains(t, tokens, "world")
	require.Contains(t, tokens, "test")
	// Punctuation should be stripped.
	for _, tok := range tokens {
		require.NotEqual(t, ",", tok)
		require.NotEqual(t, "!", tok)
	}
}

func TestTokenizeText_Chinese(t *testing.T) {
	tokens := tokenizeText("我喜欢吃蓝莓饼干")
	require.NotEmpty(t, tokens)
	// gse should segment Chinese text into meaningful words.
	found := false
	for _, tok := range tokens {
		if tok == "蓝莓" || tok == "饼干" {
			found = true
			break
		}
	}
	require.True(t, found,
		"expected Chinese segmentation to produce '蓝莓' "+
			"or '饼干', got: %v", tokens)
}

func TestTokenizeText_Mixed(t *testing.T) {
	tokens := tokenizeText("I love blueberry cookies 蓝莓饼干")
	require.Contains(t, tokens, "blueberry")
	require.Contains(t, tokens, "cookies")
	// Should also contain Chinese tokens.
	require.NotEmpty(t, tokens)
}

func TestIsPunct(t *testing.T) {
	require.True(t, isPunct(","))
	require.True(t, isPunct("..."))
	require.True(t, isPunct("，"))
	require.True(t, isPunct(" "))
	require.False(t, isPunct("hello"))
	require.False(t, isPunct("123"))
	require.False(t, isPunct("a,"))
}

func TestBM25Scorer_BasicScoring(t *testing.T) {
	docs := [][]string{
		{"the", "cat", "sat", "on", "the", "mat"},
		{"the", "dog", "played", "in", "the", "park"},
		{"blueberry", "cookie", "recipe", "with",
			"fresh", "blueberry"},
	}
	scorer := newBM25Scorer(docs)

	// "blueberry recipe" should score highest on doc 2.
	query := []string{"blueberry", "recipe"}
	s0 := scorer.score(0, query)
	s1 := scorer.score(1, query)
	s2 := scorer.score(2, query)
	require.Equal(t, 0.0, s0)
	require.Equal(t, 0.0, s1)
	require.Greater(t, s2, 0.0)

	// "cat mat" should score highest on doc 0.
	query2 := []string{"cat", "mat"}
	s0b := scorer.score(0, query2)
	s1b := scorer.score(1, query2)
	s2b := scorer.score(2, query2)
	require.Greater(t, s0b, 0.0)
	require.Equal(t, 0.0, s1b)
	require.Equal(t, 0.0, s2b)
}

func TestBM25Scorer_IDFWeighting(t *testing.T) {
	// "the" appears in all docs, so it should have low IDF.
	// "blueberry" appears in only one doc, high IDF.
	docs := [][]string{
		{"the", "cat"},
		{"the", "dog"},
		{"the", "blueberry"},
	}
	scorer := newBM25Scorer(docs)
	idfThe := scorer.idf("the")
	idfBlue := scorer.idf("blueberry")
	// "the" appears in all 3 docs -> low IDF.
	// "blueberry" appears in 1 doc -> high IDF.
	require.Greater(t, idfBlue, idfThe)
}

func TestSearchTool_BM25Ranking(t *testing.T) {
	sess := newTestSessionWithEvents([]event.Event{
		msgEvent("e1", model.RoleUser,
			"I love watching movies, especially thrillers"),
		msgEvent("e2", model.RoleAssistant,
			"Sure! I recommend Se7en, a classic thriller "+
				"movie directed by David Fincher"),
		msgEvent("e3", model.RoleUser,
			"What about comedy movies?"),
		msgEvent("e4", model.RoleAssistant,
			"For comedy I suggest The Grand Budapest Hotel"),
		msgEvent("e5", model.RoleUser,
			"Can you recommend a good recipe for cookies?"),
	})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	search := newSearchTool()

	// Search "thriller movie recommendation" should rank e2
	// (which mentions "thriller" and "movie") highest.
	args, _ := json.Marshal(map[string]any{
		"query":    "thriller movie recommendation",
		"limit":    5,
		"maxChars": 200,
	})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(searchResult)
	require.True(t, res.Success)
	require.NotEmpty(t, res.Items)
	// e2 should be the top result (highest BM25 score).
	require.Equal(t, "e2", res.Items[0].EventID)
	require.Greater(t, res.Items[0].Score, 0.0)
}

func TestSearchTool_BM25_NoResults(t *testing.T) {
	sess := newTestSessionWithEvents([]event.Event{
		msgEvent("e1", model.RoleUser, "hello world"),
		msgEvent("e2", model.RoleAssistant, "hi there"),
	})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	search := newSearchTool()
	args, _ := json.Marshal(map[string]any{
		"query":    "quantum physics relativity",
		"limit":    5,
		"maxChars": 200,
	})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(searchResult)
	require.True(t, res.Success)
	require.Empty(t, res.Items)
}

func TestSearchTool_EmptyQueryReturnsAll(t *testing.T) {
	sess := newTestSessionWithEvents([]event.Event{
		msgEvent("e1", model.RoleUser, "hello"),
		msgEvent("e2", model.RoleAssistant, "world"),
		msgEvent("e3", model.RoleUser, "foo"),
	})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	search := newSearchTool()
	// Empty query should return events in time order.
	args, _ := json.Marshal(map[string]any{
		"limit": 10, "maxChars": 200,
	})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(searchResult)
	require.True(t, res.Success)
	require.Len(t, res.Items, 3)
	require.Equal(t, "e1", res.Items[0].EventID)
	require.Equal(t, "e2", res.Items[1].EventID)
	require.Equal(t, "e3", res.Items[2].EventID)
}

func TestSearchTool_BM25_ChineseSearch(t *testing.T) {
	sess := newTestSessionWithEvents([]event.Event{
		msgEvent("e1", model.RoleUser,
			"我喜欢吃蓝莓饼干"),
		msgEvent("e2", model.RoleAssistant,
			"好的，这是一个蓝莓饼干的食谱："+
				"需要面粉、黄油和新鲜蓝莓"),
		msgEvent("e3", model.RoleUser,
			"今天天气真好，我想去公园散步"),
	})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	search := newSearchTool()
	args, _ := json.Marshal(map[string]any{
		"query":    "蓝莓食谱",
		"limit":    5,
		"maxChars": 500,
	})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(searchResult)
	require.True(t, res.Success)
	require.NotEmpty(t, res.Items)
	// e2 mentions both "蓝莓" and "食谱", should rank first.
	require.Equal(t, "e2", res.Items[0].EventID)
}
