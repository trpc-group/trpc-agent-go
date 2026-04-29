//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultGraphToolSetName              = "graph"
	graphSearchToolName                  = "search"
	graphTraverseToolName                = "traverse"
	graphFindPathsToolName               = "find_paths"
	defaultGraphSearchToolDescription    = "Search graph nodes by query and metadata filter before graph traversal or path finding."
	defaultGraphTraverseToolName         = "graph_traverse"
	defaultGraphTraverseToolDescription  = "Traverse a graph knowledge base from known start node IDs, or from nodes resolved by query and filter."
	defaultGraphFindPathsToolName        = "graph_find_paths"
	defaultGraphFindPathsToolDescription = "Find paths between known node IDs, or between nodes resolved by query and filter."
	defaultGraphToolSearchMaxResults     = 5
	defaultGraphToolMaxPaths             = 10
)

// GraphToolOption configures graph knowledge tools.
type GraphToolOption func(*graphToolOptions)

type graphToolOptions struct {
	toolName        string
	toolDescription string
}

type graphToolSet struct {
	name  string
	tools []tool.Tool
}

// NewGraphToolSet creates the graph search, traverse, and path tools as a
// single tool set. When used through llmagent.WithToolSets, the exposed tool
// names become graph_search, graph_traverse, and graph_find_paths. The search
// tool uses graph-native metadata filter guidance.
func NewGraphToolSet(
	kb knowledge.GraphKnowledge,
	searchOpts ...Option,
) tool.ToolSet {
	agenticFilterInfo := map[string][]any{
		"content":       {},
		"metadata.kind": {},
	}
	wrappedSearchOpts := []Option{
		WithToolName(graphSearchToolName),
		WithToolDescription(defaultGraphSearchToolDescription),
	}
	wrappedSearchOpts = append(wrappedSearchOpts, searchOpts...)
	return &graphToolSet{
		name: defaultGraphToolSetName,
		tools: []tool.Tool{
			NewAgenticFilterSearchTool(kb, agenticFilterInfo, wrappedSearchOpts...),
			NewGraphTraverseTool(kb, WithGraphToolName(graphTraverseToolName)),
			NewGraphFindPathsTool(kb, WithGraphToolName(graphFindPathsToolName)),
		},
	}
}

// Tools returns graph knowledge tools.
func (s *graphToolSet) Tools(_ context.Context) []tool.Tool {
	return s.tools
}

// Close releases resources held by the tool set.
func (s *graphToolSet) Close() error {
	return nil
}

// Name returns the graph tool set name.
func (s *graphToolSet) Name() string {
	if s.name == "" {
		return defaultGraphToolSetName
	}
	return s.name
}

// WithGraphToolName sets the graph tool name.
func WithGraphToolName(name string) GraphToolOption {
	return func(opts *graphToolOptions) {
		opts.toolName = name
	}
}

// WithGraphToolDescription sets the graph tool description.
func WithGraphToolDescription(description string) GraphToolOption {
	return func(opts *graphToolOptions) {
		opts.toolDescription = description
	}
}

// GraphTraverseRequest is the input for the graph traverse tool.
type GraphTraverseRequest struct {
	StartIDs  []string                               `json:"start_ids,omitempty" jsonschema:"description=Known graph node IDs to start traversal from. Optional when query or filter is provided."`
	Query     string                                 `json:"query,omitempty" jsonschema:"description=Search query used to resolve start node IDs before traversal. Can be empty when using only filter."`
	Filter    *searchfilter.UniversalFilterCondition `json:"filter,omitempty" jsonschema:"description=Filter conditions used to resolve start node IDs before traversal."`
	MaxSeeds  int                                    `json:"max_seeds,omitempty" jsonschema:"description=Maximum start nodes resolved from query/filter. Default is 5."`
	Direction string                                 `json:"direction,omitempty" jsonschema:"description=Traversal direction: out, in, or both. Default is out,enum=out,enum=in,enum=both"`
	EdgeTypes []string                               `json:"edge_types,omitempty" jsonschema:"description=Optional edge types to follow"`
	MaxDepth  int                                    `json:"max_depth,omitempty" jsonschema:"description=Maximum traversal depth. Default is 1"`
	MaxNodes  int                                    `json:"max_nodes,omitempty" jsonschema:"description=Maximum number of nodes to return. Default is 100"`
}

// GraphFindPathsRequest is the input for the graph find paths tool.
type GraphFindPathsRequest struct {
	FromID        string                                 `json:"from_id,omitempty" jsonschema:"description=Known graph node ID where the path starts. Optional when from_query or from_filter is provided."`
	FromQuery     string                                 `json:"from_query,omitempty" jsonschema:"description=Search query used to resolve path start node IDs. Can be empty when using only from_filter."`
	FromFilter    *searchfilter.UniversalFilterCondition `json:"from_filter,omitempty" jsonschema:"description=Filter conditions used to resolve path start node IDs."`
	ToID          string                                 `json:"to_id,omitempty" jsonschema:"description=Known graph node ID where the path ends. Optional when to_query or to_filter is provided."`
	ToQuery       string                                 `json:"to_query,omitempty" jsonschema:"description=Search query used to resolve path end node IDs. Can be empty when using only to_filter."`
	ToFilter      *searchfilter.UniversalFilterCondition `json:"to_filter,omitempty" jsonschema:"description=Filter conditions used to resolve path end node IDs."`
	MaxCandidates int                                    `json:"max_candidates,omitempty" jsonschema:"description=Maximum start/end node candidates resolved from query/filter. Default is 5."`
	Direction     string                                 `json:"direction,omitempty" jsonschema:"description=Path search direction: out, in, or both. Default is out,enum=out,enum=in,enum=both"`
	EdgeTypes     []string                               `json:"edge_types,omitempty" jsonschema:"description=Optional edge types to follow"`
	MaxDepth      int                                    `json:"max_depth,omitempty" jsonschema:"description=Maximum path depth. Default is 5"`
	MaxPaths      int                                    `json:"max_paths,omitempty" jsonschema:"description=Maximum number of paths to return. Default is 10"`
}

// NewGraphTraverseTool creates a tool for traversing graph knowledge.
func NewGraphTraverseTool(kb knowledge.GraphKnowledge, opts ...GraphToolOption) tool.Tool {
	options := buildGraphToolOptions(
		defaultGraphTraverseToolName,
		defaultGraphTraverseToolDescription,
		opts...,
	)
	fn := func(ctx context.Context, req *GraphTraverseRequest) (*graph.TraverseResult, error) {
		dir, err := parseGraphDirection(req.Direction)
		if err != nil {
			return nil, err
		}
		startIDs, err := resolveGraphNodeIDs(ctx, kb, req.StartIDs, req.Query, req.Filter, req.MaxSeeds, nil, nil)
		if err != nil {
			return nil, err
		}
		return kb.Traverse(ctx, &graph.TraverseQuery{
			StartIDs:  startIDs,
			Direction: dir,
			EdgeTypes: req.EdgeTypes,
			MaxDepth:  req.MaxDepth,
			MaxNodes:  req.MaxNodes,
		})
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(options.toolName),
		function.WithDescription(options.toolDescription),
	)
}

// NewGraphFindPathsTool creates a tool for finding paths in graph knowledge.
func NewGraphFindPathsTool(kb knowledge.GraphKnowledge, opts ...GraphToolOption) tool.Tool {
	options := buildGraphToolOptions(
		defaultGraphFindPathsToolName,
		defaultGraphFindPathsToolDescription,
		opts...,
	)
	fn := func(ctx context.Context, req *GraphFindPathsRequest) (*graph.PathResult, error) {
		dir, err := parseGraphDirection(req.Direction)
		if err != nil {
			return nil, err
		}
		fromIDs, err := resolveGraphNodeIDs(
			ctx, kb, stringAsIDs(req.FromID), req.FromQuery, req.FromFilter, req.MaxCandidates, nil, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("resolve from node: %w", err)
		}
		toIDs, err := resolveGraphNodeIDs(
			ctx, kb, stringAsIDs(req.ToID), req.ToQuery, req.ToFilter, req.MaxCandidates, nil, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("resolve to node: %w", err)
		}
		maxPaths := resolveGraphToolLimit(req.MaxPaths, defaultGraphToolMaxPaths)
		return findPathsForNodeCandidates(ctx, kb, fromIDs, toIDs, dir, req.EdgeTypes, req.MaxDepth, maxPaths)
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(options.toolName),
		function.WithDescription(options.toolDescription),
	)
}

func buildGraphToolOptions(
	name string,
	description string,
	opts ...GraphToolOption,
) *graphToolOptions {
	options := &graphToolOptions{
		toolName:        name,
		toolDescription: description,
	}
	for _, opt := range opts {
		opt(options)
	}
	return options
}

func parseGraphDirection(value string) (graph.Direction, error) {
	switch graph.Direction(value) {
	case "", graph.DirectionOut:
		return graph.DirectionOut, nil
	case graph.DirectionIn:
		return graph.DirectionIn, nil
	case graph.DirectionBoth:
		return graph.DirectionBoth, nil
	default:
		return "", fmt.Errorf("unsupported direction %q", value)
	}
}

func resolveGraphNodeIDs(
	ctx context.Context,
	kb knowledge.GraphKnowledge,
	explicitIDs []string,
	query string,
	filter *searchfilter.UniversalFilterCondition,
	maxResults int,
	staticFilter map[string]any,
	conditionedFilter *searchfilter.UniversalFilterCondition,
) ([]string, error) {
	if len(explicitIDs) > 0 {
		return dedupStrings(explicitIDs), nil
	}
	if query == "" && filter == nil {
		return nil, errors.New("requires node IDs, query, or filter")
	}
	ids, err := searchGraphNodeIDs(ctx, kb, query, filter, maxResults, staticFilter, conditionedFilter)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("no matching graph nodes found")
	}
	return ids, nil
}

func searchGraphNodeIDs(
	ctx context.Context,
	kb knowledge.Knowledge,
	query string,
	filter *searchfilter.UniversalFilterCondition,
	maxResults int,
	staticFilter map[string]any,
	conditionedFilter *searchfilter.UniversalFilterCondition,
) ([]string, error) {
	maxResults = resolveGraphToolLimit(maxResults, defaultGraphToolSearchMaxResults)
	invocation, ok := agent.InvocationFromContext(ctx)
	var runnerFilter map[string]any
	var runnerConditionedFilter *searchfilter.UniversalFilterCondition
	if ok {
		runnerFilter = invocation.RunOptions.KnowledgeFilter
		runnerConditionedFilter = invocation.RunOptions.KnowledgeConditionedFilter
	}

	finalFilter := mergeFilterConditions(
		convertMetadataToFilterCondition(staticFilter),
		conditionedFilter,
		convertMetadataToFilterCondition(runnerFilter),
		runnerConditionedFilter,
		filter,
	)
	if query == "" && finalFilter == nil {
		return nil, errors.New("requires query or filter")
	}

	searchReq := &knowledge.SearchRequest{
		Query: query,
		SearchFilter: &knowledge.SearchFilter{
			FilterCondition: finalFilter,
		},
		MaxResults: maxResults,
	}
	if query == "" {
		searchReq.SearchMode = vectorstore.SearchModeFilter
	}

	result, err := kb.Search(ctx, searchReq)
	if err != nil {
		return nil, fmt.Errorf("search graph nodes: %w", err)
	}
	return graphNodeIDsFromSearchResult(result), nil
}

func graphNodeIDsFromSearchResult(result *knowledge.SearchResult) []string {
	if result == nil {
		return nil
	}
	ids := make([]string, 0, 1+len(result.Documents))
	if result.Document != nil {
		ids = append(ids, graphNodeIDFromDocument(result.Document))
	}
	for _, item := range result.Documents {
		if item == nil || item.Document == nil {
			continue
		}
		ids = append(ids, graphNodeIDFromDocument(item.Document))
	}
	return dedupStrings(ids)
}

func graphNodeIDFromDocument(doc *document.Document) string {
	if doc == nil {
		return ""
	}
	return doc.ID
}

func findPathsForNodeCandidates(
	ctx context.Context,
	kb knowledge.GraphKnowledge,
	fromIDs []string,
	toIDs []string,
	direction graph.Direction,
	edgeTypes []string,
	maxDepth int,
	maxPaths int,
) (*graph.PathResult, error) {
	result := &graph.PathResult{}
	for _, fromID := range fromIDs {
		for _, toID := range toIDs {
			if fromID == "" || toID == "" {
				continue
			}
			pairMaxPaths := maxPaths
			if maxPaths > 0 {
				remaining := maxPaths - len(result.Paths)
				if remaining <= 0 {
					result.Truncated = true
					return result, nil
				}
				pairMaxPaths = remaining
			}
			pairResult, err := kb.FindPaths(ctx, &graph.PathQuery{
				FromID:    fromID,
				ToID:      toID,
				Direction: direction,
				EdgeTypes: edgeTypes,
				MaxDepth:  maxDepth,
				MaxPaths:  pairMaxPaths,
			})
			if err != nil {
				return nil, err
			}
			if pairResult == nil {
				continue
			}
			result.Paths = append(result.Paths, pairResult.Paths...)
			if pairResult.Truncated {
				result.Truncated = true
			}
			if maxPaths > 0 && len(result.Paths) >= maxPaths {
				if len(result.Paths) > maxPaths {
					result.Paths = result.Paths[:maxPaths]
				}
				result.Truncated = true
				return result, nil
			}
		}
	}
	return result, nil
}

func stringAsIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

func dedupStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func resolveGraphToolLimit(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
