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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewCueSearchTool creates a function tool for searching associative cues.
func NewCueSearchTool() tool.CallableTool {
	searchFunc := func(ctx context.Context, req *CueSearchRequest) (*CueSearchResponse, error) {
		svc, userKey, err := associativeServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory cue search tool: %w", err)
		}
		if req == nil || req.Query == "" {
			return nil, fmt.Errorf("memory cue search tool: query is required")
		}
		result, err := svc.SearchCues(ctx, memory.CueSearchRequest{
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
		function.WithName(memory.CueSearchToolName),
		function.WithDescription("Search active memory cues for associative recall. "+
			memoryToolScopeNote+" Use this before expanding tags when reconstructing evidence paths."),
		function.WithInputSchema(cueSearchInputSchema()),
	)
}

// NewTagExpandTool creates a function tool for expanding cue tags.
func NewTagExpandTool() tool.CallableTool {
	expandFunc := func(ctx context.Context, req *TagExpandRequest) (*TagExpandResponse, error) {
		svc, userKey, err := associativeServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory tag expand tool: %w", err)
		}
		if req == nil || (len(req.CueIDs) == 0 && len(req.Cues) == 0) {
			return nil, fmt.Errorf("memory tag expand tool: cue_ids or cues are required")
		}
		result, err := svc.ExpandTags(ctx, memory.TagExpandRequest{
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
		function.WithName(memory.TagExpandToolName),
		function.WithDescription("Expand associative memory cues into tag-content paths. "+
			memoryToolScopeNote+" Use this to explore candidate evidence before loading content."),
		function.WithInputSchema(tagExpandInputSchema()),
	)
}

// NewContentLoadTool creates a function tool for loading associative content.
func NewContentLoadTool() tool.CallableTool {
	loadFunc := func(ctx context.Context, req *ContentLoadRequest) (*ContentLoadResponse, error) {
		svc, userKey, err := associativeServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory content load tool: %w", err)
		}
		if req == nil {
			return nil, fmt.Errorf("memory content load tool: content_ids or refs are required")
		}
		if len(req.ContentIDs) == 0 && len(req.Refs) == 0 {
			return nil, fmt.Errorf("memory content load tool: content_ids or refs are required")
		}
		result, err := svc.LoadContents(ctx, memory.ContentLoadRequest{
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
		function.WithName(memory.ContentLoadToolName),
		function.WithDescription("Load associative memory content nodes by path or source reference. "+
			memoryToolScopeNote+" Use this after cue/tag expansion to inspect evidence text."),
		function.WithInputSchema(contentLoadInputSchema()),
	)
}

func associativeServiceFromContext(
	ctx context.Context,
) (memory.AssociativeService, memory.UserKey, error) {
	memSvc, err := GetMemoryServiceFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	assoc, ok := memSvc.(memory.AssociativeService)
	if !ok {
		return nil, memory.UserKey{}, fmt.Errorf("memory service does not implement AssociativeService")
	}
	appName, userID, err := GetAppAndUserFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	return assoc, memory.UserKey{AppName: appName, UserID: userID}, nil
}

func cueSearchInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"query":       stringSchema("Question or keywords used to find active memory cues."),
		"max_results": integerSchema("Maximum number of cues to return."),
		"min_score":   numberSchema("Minimum cue score."),
	}, "query")
}

func tagExpandInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"cue_ids":          stringArraySchema("Cue IDs returned by memory_cue_search."),
		"cues":             stringArraySchema("Cue texts to expand when IDs are unavailable."),
		"max_tags_per_cue": integerSchema("Maximum tags to expand per cue."),
		"max_contents":     integerSchema("Maximum content paths to return."),
		"min_path_score":   numberSchema("Minimum path score."),
		"include_content":  boolSchema("Whether to include content nodes in returned paths."),
	})
}

func contentLoadInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"content_ids": stringArraySchema("Content node IDs to load."),
		"refs": &tool.Schema{
			Type:        "array",
			Description: "Typed content references to load.",
			Items: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"kind":       stringSchema("Reference kind, such as session_event or memory_entry."),
					"app_name":   stringSchema("Optional source app name."),
					"user_id":    stringSchema("Optional source user ID."),
					"session_id": stringSchema("Optional source session ID."),
					"event_id":   stringSchema("Optional source event ID."),
					"turn_id":    stringSchema("Optional source turn ID."),
					"source_id":  stringSchema("Optional source object ID."),
				},
			},
		},
		"max_results": integerSchema("Maximum content nodes to return."),
	})
}

func numberSchema(description string) *tool.Schema {
	return &tool.Schema{
		Type:        "number",
		Description: description,
	}
}
