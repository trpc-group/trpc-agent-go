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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
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
	defaultGraphTraverseToolDescription  = "Traverse a graph knowledge base from known start node IDs. Use graph_search first when IDs are unknown."
	defaultGraphFindPathsToolName        = "graph_find_paths"
	defaultGraphFindPathsToolDescription = "Find paths between two known node IDs. Use graph_search first when endpoint IDs are unknown."
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
// tool uses graph-native metadata filter guidance driven by agenticFilterInfo,
// which declares the metadata fields and enumerated values exposed to the LLM
// (e.g. {"metadata.category": ["doc", "tutorial"], "content": {}}).
func NewGraphToolSet(
	kb knowledge.GraphKnowledge,
	agenticFilterInfo map[string][]any,
	searchOpts ...Option,
) tool.ToolSet {
	wrappedSearchOpts := []Option{
		WithToolName(graphSearchToolName),
		WithToolDescription(defaultGraphSearchToolDescription),
		withIncludeContentDefault(false),
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
	StartIDs       []string `json:"start_ids,omitempty" jsonschema:"description=Required. Graph node IDs to start traversal from. Use graph_search first when IDs are unknown."`
	Direction      string   `json:"direction,omitempty" jsonschema:"description=Traversal direction: out, in, or both. Default is out,enum=out,enum=in,enum=both"`
	EdgeTypes      []string `json:"edge_types,omitempty" jsonschema:"description=Optional edge types to follow"`
	MaxDepth       int      `json:"max_depth,omitempty" jsonschema:"description=Maximum traversal depth. Default is 1"`
	MaxNodes       int      `json:"max_nodes,omitempty" jsonschema:"description=Maximum number of nodes to return. Default is 100"`
	IncludeContent bool     `json:"include_content,omitempty" jsonschema:"description=Whether to include full node content in the response. Default false keeps graph responses compact, and you will receive basically node metadata; set true only when the code body is needed."`
}

// GraphFindPathsRequest is the input for the graph find paths tool.
type GraphFindPathsRequest struct {
	FromID         string   `json:"from_id,omitempty" jsonschema:"description=Required graph node ID where the path starts. Use graph_search first when the ID is unknown."`
	ToID           string   `json:"to_id,omitempty" jsonschema:"description=Required graph node ID where the path ends. Use graph_search first when the ID is unknown."`
	Direction      string   `json:"direction,omitempty" jsonschema:"description=Path search direction: out, in, or both. Default is out,enum=out,enum=in,enum=both"`
	EdgeTypes      []string `json:"edge_types,omitempty" jsonschema:"description=Optional edge types to follow"`
	MaxDepth       int      `json:"max_depth,omitempty" jsonschema:"description=Maximum path depth. Default is 5"`
	MaxPaths       int      `json:"max_paths,omitempty" jsonschema:"description=Maximum number of paths to return. Default is 10"`
	IncludeContent bool     `json:"include_content,omitempty" jsonschema:"description=Whether to include full node content in the response. Default false keeps graph responses compact; set true only when the code body is needed."`
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
		if len(req.StartIDs) == 0 {
			return nil, errors.New("start_ids is required; use graph_search first to resolve node IDs")
		}
		result, err := kb.Traverse(ctx, &graph.TraverseQuery{
			StartIDs:  req.StartIDs,
			Direction: dir,
			EdgeTypes: req.EdgeTypes,
			MaxDepth:  req.MaxDepth,
			MaxNodes:  req.MaxNodes,
		})
		if err != nil {
			return nil, err
		}
		return graphTraverseToolResult(result, req.IncludeContent), nil
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
		if req.FromID == "" || req.ToID == "" {
			return nil, errors.New("from_id and to_id are required; use graph_search first to resolve endpoint node IDs")
		}
		maxPaths := resolveGraphToolLimit(req.MaxPaths, defaultGraphToolMaxPaths)
		result, err := kb.FindPaths(ctx, &graph.PathQuery{
			FromID:    req.FromID,
			ToID:      req.ToID,
			Direction: dir,
			EdgeTypes: req.EdgeTypes,
			MaxDepth:  req.MaxDepth,
			MaxPaths:  maxPaths,
		})
		if err != nil {
			return nil, err
		}
		return graphPathToolResult(result, req.IncludeContent), nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(options.toolName),
		function.WithDescription(options.toolDescription),
	)
}

func graphTraverseToolResult(result *graph.TraverseResult, includeContent bool) *graph.TraverseResult {
	if result == nil {
		return result
	}
	return &graph.TraverseResult{
		Nodes:     graphToolNodes(result.Nodes, includeContent),
		Edges:     graphToolEdges(result.Edges),
		Truncated: result.Truncated,
		Message:   result.Message,
	}
}

func graphPathToolResult(result *graph.PathResult, includeContent bool) *graph.PathResult {
	if result == nil {
		return result
	}
	paths := make([]*graph.Path, 0, len(result.Paths))
	for _, path := range result.Paths {
		if path == nil {
			paths = append(paths, nil)
			continue
		}
		paths = append(paths, &graph.Path{
			Nodes: graphToolNodes(path.Nodes, includeContent),
			Edges: graphToolEdges(path.Edges),
		})
	}
	return &graph.PathResult{
		Paths:     paths,
		Truncated: result.Truncated,
		Message:   result.Message,
	}
}

func graphToolNodes(nodes []*graph.Node, includeContent bool) []*graph.Node {
	cloned := make([]*graph.Node, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			cloned = append(cloned, nil)
			continue
		}
		next := *node
		if !includeContent {
			next.Content = ""
		}
		next.Metadata = graphToolMetadata(node.Metadata)
		cloned = append(cloned, &next)
	}
	return cloned
}

func graphToolEdges(edges []*graph.Edge) []*graph.Edge {
	cloned := make([]*graph.Edge, 0, len(edges))
	for _, edge := range edges {
		if edge == nil {
			cloned = append(cloned, nil)
			continue
		}
		next := *edge
		next.Metadata = graphToolMetadata(edge.Metadata)
		cloned = append(cloned, &next)
	}
	return cloned
}

func graphToolMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	filtered := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if !strings.HasPrefix(key, source.MetaPrefix) {
			filtered[key] = value
		}
	}
	copyResourceMetadata(filtered, metadata, nil)
	if len(filtered) == 0 {
		return nil
	}
	return filtered
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

func resolveGraphToolLimit(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
