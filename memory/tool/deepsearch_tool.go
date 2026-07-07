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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewCueSearchTool creates a function tool for searching DeepSearch cues.
func NewCueSearchTool() tool.CallableTool {
	searchFunc := func(ctx context.Context, req *CueSearchRequest) (*CueSearchResponse, error) {
		svc, userKey, err := deepSearchServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory cue search tool: %w", err)
		}
		if req == nil || req.Query == "" {
			return nil, fmt.Errorf("memory cue search tool: query is required")
		}
		result, err := svc.SearchCues(ctx, deepsearch.CueSearchRequest{
			UserKey:    userKey,
			Query:      req.Query,
			MaxResults: req.MaxResults,
			MinScore:   req.MinScore,
		})
		if err != nil {
			return nil, err
		}
		return &CueSearchResponse{
			Query: result.Query,
			Cues:  result.Cues,
			Count: len(result.Cues),
		}, nil
	}
	return function.NewFunctionTool(
		searchFunc,
		function.WithName(deepsearch.CueSearchToolName),
		function.WithDescription("Search DeepSearch memory cues for the current user. "+
			memoryToolScopeNote+" Use this after memory_search with search_mode='deepsearch' has activated the DeepSearch tool set."),
		function.WithInputSchema(cueSearchInputSchema()),
	)
}

// NewTagExpandTool creates a function tool for expanding cue tags.
func NewTagExpandTool() tool.CallableTool {
	expandFunc := func(ctx context.Context, req *TagExpandRequest) (*TagExpandResponse, error) {
		svc, userKey, err := deepSearchServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory tag expand tool: %w", err)
		}
		if req == nil || (len(req.CueIDs) == 0 && len(req.Cues) == 0) {
			return nil, fmt.Errorf("memory tag expand tool: cue_ids or cues are required")
		}
		result, err := svc.ExpandTags(ctx, deepsearch.TagExpandRequest{
			UserKey:        userKey,
			CueIDs:         req.CueIDs,
			Cues:           req.Cues,
			MaxTagsPerCue:  req.MaxTagsPerCue,
			MaxContents:    req.MaxContents,
			MinPathScore:   req.MinPathScore,
			IncludeContent: req.IncludeContent,
		})
		if err != nil {
			return nil, err
		}
		return &TagExpandResponse{
			Tags:  result.Tags,
			Paths: result.Paths,
			Count: len(result.Paths),
		}, nil
	}
	return function.NewFunctionTool(
		expandFunc,
		function.WithName(deepsearch.TagExpandToolName),
		function.WithDescription("Expand DeepSearch cues into tag-content paths. "+
			memoryToolScopeNote+" Use this to inspect cue/tag evidence paths before loading content."),
		function.WithInputSchema(tagExpandInputSchema()),
	)
}

// NewContentLoadTool creates a function tool for loading DeepSearch content.
func NewContentLoadTool() tool.CallableTool {
	loadFunc := func(ctx context.Context, req *ContentLoadRequest) (*ContentLoadResponse, error) {
		svc, userKey, err := deepSearchServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory content load tool: %w", err)
		}
		if req == nil || (len(req.ContentIDs) == 0 && len(req.Refs) == 0) {
			return nil, fmt.Errorf("memory content load tool: content_ids or refs are required")
		}
		result, err := svc.LoadContents(ctx, deepsearch.ContentLoadRequest{
			UserKey:    userKey,
			ContentIDs: req.ContentIDs,
			Refs:       req.Refs,
			MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}
		return &ContentLoadResponse{
			Contents: result.Contents,
			Count:    len(result.Contents),
		}, nil
	}
	return function.NewFunctionTool(
		loadFunc,
		function.WithName(deepsearch.ContentLoadToolName),
		function.WithDescription("Load DeepSearch content by content id or memory entry reference. "+
			memoryToolScopeNote+" Returned content is the original memory entry text, not a new memory source."),
		function.WithInputSchema(contentLoadInputSchema()),
	)
}

func deepSearchServiceFromContext(ctx context.Context) (deepsearch.Service, memory.UserKey, error) {
	memoryService, err := GetMemoryServiceFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	svc, ok := memoryService.(deepsearch.Service)
	if !ok {
		return nil, memory.UserKey{}, fmt.Errorf("deepsearch is not enabled for this memory service")
	}
	appName, userID, err := GetAppAndUserFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	return svc, memory.UserKey{AppName: appName, UserID: userID}, nil
}
