//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type searchTool struct {
	parent *DeferredToolSet
	decl   *tool.Declaration
}

type searchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type searchOutput struct {
	Query       string            `json:"query"`
	Message     string            `json:"message"`
	Tools       []searchToolMatch `json:"tools"`
	LoadedTools []string          `json:"loaded_tools"`
}

type searchToolMatch struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Score       float64 `json:"score,omitempty"`
}

func newSearchTool(parent *DeferredToolSet) *searchTool {
	return &searchTool{
		parent: parent,
		decl: &tool.Declaration{
			Name:        parent.searchToolName,
			Description: parent.searchToolDescription(),
			InputSchema: searchInputSchema(),
			OutputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"query": {
						Type: "string",
					},
					"message": {
						Type: "string",
					},
					"tools": {
						Type: "array",
						Items: &tool.Schema{
							Type: "object",
							Properties: map[string]*tool.Schema{
								"name": {
									Type: "string",
								},
								"description": {
									Type: "string",
								},
								"score": {
									Type: "number",
								},
							},
							Required: []string{"name"},
						},
					},
					"loaded_tools": {
						Type:  "array",
						Items: &tool.Schema{Type: "string"},
					},
				},
				Required: []string{"query", "message", "tools", "loaded_tools"},
			},
		},
	}
}

func searchInputSchema() *tool.Schema {
	return &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"query": {
				Type:        "string",
				Description: "Natural-language description of the tool you need.",
			},
			"limit": {
				Type:        "integer",
				Description: "Optional maximum number of tools to load.",
			},
		},
		Required: []string{"query"},
	}
}

func (t *searchTool) Declaration() *tool.Declaration {
	return t.decl
}

func (t *searchTool) ExemptFromToolFilter() bool {
	return true
}

func (t *searchTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var input searchInput
	if err := json.Unmarshal(jsonArgs, &input); err != nil {
		return nil, fmt.Errorf("parsing tool_search arguments: %w", err)
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		return nil, fmt.Errorf("tool_search requires a non-empty query")
	}
	snapshot := t.parent.catalogSnapshot(ctx)
	limit := input.Limit
	if limit <= 0 || limit > t.parent.maxResults {
		limit = t.parent.maxResults
	}
	results := snapshot.Index.Search(input.Query, limit)
	loadedNames := make([]string, 0, len(results))
	toolMatches := make([]searchToolMatch, 0, len(results))
	for _, result := range results {
		loadedNames = append(loadedNames, result.Entry.Name)
		toolMatches = append(toolMatches, searchToolMatch{
			Name:        result.Entry.Name,
			Description: result.Entry.Description,
			Score:       roundScore(result.Score),
		})
	}
	state := t.parent.updateLoadedState(ctx, snapshot, loadedNames)
	message := "No matching tools were found."
	if len(toolMatches) > 0 {
		message = "These tools are now loaded and can be called on the next model step."
	}
	return searchOutput{
		Query:       input.Query,
		Message:     message,
		Tools:       toolMatches,
		LoadedTools: append([]string(nil), state.LoadedTools...),
	}, nil
}

func roundScore(score float64) float64 {
	return mathRound(score*1000) / 1000
}

func mathRound(v float64) float64 {
	if v < 0 {
		return float64(int64(v - 0.5))
	}
	return float64(int64(v + 0.5))
}

func (t *searchTool) StateDeltaForInvocation(
	inv *agent.Invocation,
	_ string,
	_ []byte,
	result []byte,
) map[string][]byte {
	if t == nil || t.parent == nil || inv == nil ||
		t.parent.stateScope != StateScopeSession {
		return nil
	}
	carrier := invocationStateCarrier(inv)
	if carrier == nil {
		return nil
	}
	key := t.parent.sessionStateKey()
	state, ok := agent.GetStateValue[loadedState](carrier, t.parent.invocationStateKey())
	if !ok || len(state.LoadedTools) == 0 {
		state = loadedStateFromSearchOutput(result)
		if len(state.LoadedTools) == 0 {
			return map[string][]byte{
				key: nil,
			}
		}
	}
	b, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	return map[string][]byte{
		key: b,
	}
}

func loadedStateFromSearchOutput(result []byte) loadedState {
	if len(result) == 0 {
		return loadedState{}
	}
	var output searchOutput
	if err := json.Unmarshal(result, &output); err != nil {
		return loadedState{}
	}
	return loadedState{
		LoadedTools: append([]string(nil), output.LoadedTools...),
	}
}
