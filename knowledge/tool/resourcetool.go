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
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const defaultResourceToolSetName = "knowledge"

type resourceToolSet struct {
	kb    knowledge.ResourceKnowledge
	tools []tool.Tool
}

// NewResourceToolSet creates tools for browsing and reading persisted resource
// content. Search remains separate because ResourceKnowledge is optional and
// independent from semantic retrieval. The tool set does not own kb.
func NewResourceToolSet(kb knowledge.ResourceKnowledge) tool.ToolSet {
	return &resourceToolSet{
		kb: kb,
		tools: []tool.Tool{
			newListSourcesTool(kb),
			newListResourcesTool(kb),
			newReadResourceTool(kb),
			newGrepResourceTool(kb),
		},
	}
}

func (s *resourceToolSet) Tools(_ context.Context) []tool.Tool {
	return s.tools
}

func (s *resourceToolSet) Close() error {
	return nil
}

func (s *resourceToolSet) Name() string {
	return defaultResourceToolSetName
}

func newListSourcesTool(kb knowledge.ResourceKnowledge) tool.Tool {
	fn := func(ctx context.Context, req *knowledge.ListSourcesRequest) (*knowledge.ListSourcesResult, error) {
		if kb == nil {
			return nil, knowledge.ErrResourceCapabilityUnavailable
		}
		if req == nil {
			return nil, errors.New("list sources request cannot be nil")
		}
		result, err := kb.ListSources(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("list knowledge sources: %w", err)
		}
		return result, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName("list_sources"),
		function.WithDescription("List persisted knowledge sources that support file browsing and reading."),
	)
}

func newListResourcesTool(kb knowledge.ResourceKnowledge) tool.Tool {
	fn := func(ctx context.Context, req *knowledge.ListResourcesRequest) (*knowledge.ListResourcesResult, error) {
		if kb == nil {
			return nil, knowledge.ErrResourceCapabilityUnavailable
		}
		if req == nil || strings.TrimSpace(req.SourceID) == "" {
			return nil, errors.New("source_id is required")
		}
		result, err := kb.ListResources(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("list knowledge resources: %w", err)
		}
		return result, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName("list_resources"),
		function.WithDescription("List direct children in a persisted source tree. Pass parent_path to browse a directory."),
	)
}

func newReadResourceTool(kb knowledge.ResourceKnowledge) tool.Tool {
	fn := func(ctx context.Context, req *knowledge.ReadResourceRequest) (*knowledge.ReadResourceResult, error) {
		if kb == nil {
			return nil, knowledge.ErrResourceCapabilityUnavailable
		}
		if req == nil || strings.TrimSpace(req.SourceID) == "" || strings.TrimSpace(req.Path) == "" {
			return nil, errors.New("source_id and path are required")
		}
		result, err := kb.ReadResource(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("read knowledge resource: %w", err)
		}
		return result, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName("read"),
		function.WithDescription("Read a bounded line range from the persisted text resource identified by source_id and path."),
	)
}

func newGrepResourceTool(kb knowledge.ResourceKnowledge) tool.Tool {
	fn := func(ctx context.Context, req *knowledge.GrepResourceRequest) (*knowledge.GrepResourceResult, error) {
		if kb == nil {
			return nil, knowledge.ErrResourceCapabilityUnavailable
		}
		if req == nil || strings.TrimSpace(req.SourceID) == "" ||
			strings.TrimSpace(req.Path) == "" || strings.TrimSpace(req.Pattern) == "" {
			return nil, errors.New("source_id, path, and pattern are required")
		}
		if req.Before < 0 || req.After < 0 {
			return nil, errors.New("before and after cannot be negative")
		}
		result, err := kb.GrepResource(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("grep knowledge resource: %w", err)
		}
		return result, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName("grep"),
		function.WithDescription("Find a bounded literal or regular-expression match in a persisted text resource."),
	)
}
