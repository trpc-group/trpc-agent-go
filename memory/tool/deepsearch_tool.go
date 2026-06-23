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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewCueSearchTool creates a function tool for searching cue/tag cues.
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
		function.WithDescription("Search active memory cues for cue/tag recall. "+
			memoryToolScopeNote+" Use this before expanding tags when reconstructing evidence paths."),
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
		function.WithDescription("Expand cue/tag memory cues into tag-content paths. "+
			memoryToolScopeNote+" Use this to explore candidate evidence before loading content."),
		function.WithInputSchema(tagExpandInputSchema()),
	)
}

// NewContentLoadTool creates a function tool for loading cue/tag content.
func NewContentLoadTool() tool.CallableTool {
	loadFunc := func(ctx context.Context, req *ContentLoadRequest) (*ContentLoadResponse, error) {
		svc, userKey, err := deepSearchServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory content load tool: %w", err)
		}
		if req == nil {
			return nil, fmt.Errorf("memory content load tool: content_ids or refs are required")
		}
		if len(req.ContentIDs) == 0 && len(req.Refs) == 0 {
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
		function.WithDescription("Load cue/tag memory content nodes by path or source reference. "+
			memoryToolScopeNote+" Use this after cue/tag expansion to inspect evidence text."),
		function.WithInputSchema(contentLoadInputSchema()),
	)
}

// NewEdgesByTagTool creates a function tool for traversing Cue/tag tag edges.
func NewEdgesByTagTool() tool.CallableTool {
	call := func(ctx context.Context, req *EdgesByTagRequest) (*EdgesByTagResponse, error) {
		svc, userKey, err := deepSearchQueryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory edges by tag tool: %w", err)
		}
		if req == nil || (len(req.Tags) == 0 && strings.TrimSpace(req.Query) == "") {
			return nil, fmt.Errorf("memory edges by tag tool: tags or query is required")
		}
		result, err := svc.EdgesByTag(ctx, deepsearch.EdgesByTagRequest{
			UserKey:        userKey,
			Tags:           req.Tags,
			Query:          req.Query,
			MaxResults:     req.MaxResults,
			IncludeContent: req.IncludeContent,
		})
		if err != nil {
			return nil, err
		}
		return &EdgesByTagResponse{
			Query: result.Query,
			Tags:  result.Tags,
			Paths: result.Paths,
			Count: len(result.Paths),
		}, nil
	}
	return function.NewFunctionTool(
		call,
		function.WithName(deepsearch.EdgesByTagToolName),
		function.WithDescription("Traverse Cue/tag memory edges by tag or relation label. "+
			memoryToolScopeNote+" Use this when the question names a relation, topic, or aspect."),
		function.WithInputSchema(edgesByTagInputSchema()),
	)
}

// NewQueryConversationTimeTool creates a function tool for time-scoped events.
func NewQueryConversationTimeTool() tool.CallableTool {
	call := func(ctx context.Context, req *QueryConversationTimeRequest) (*DeepSearchQueryResponse, error) {
		svc, userKey, err := deepSearchQueryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory query conversation time tool: %w", err)
		}
		timeAfter, timeBefore, err := parseToolTimeRange(reqTimeAfter(req), reqTimeBefore(req))
		if err != nil {
			return nil, fmt.Errorf("memory query conversation time tool: %w", err)
		}
		result, err := svc.QueryConversationTime(ctx, deepsearch.QueryConversationTimeRequest{
			UserKey:    userKey,
			Query:      reqQuery(req),
			TimeAfter:  timeAfter,
			TimeBefore: timeBefore,
			MaxResults: reqMaxResults(req),
		})
		if err != nil {
			return nil, err
		}
		return queryResponse(result), nil
	}
	return function.NewFunctionTool(
		call,
		function.WithName(deepsearch.ConversationTimeToolName),
		function.WithDescription("Find memory events by conversation or event time. "+
			memoryToolScopeNote+" Use this for before/after/when/date questions."),
		function.WithInputSchema(conversationTimeInputSchema()),
	)
}

// NewQueryEventKeywordsTool creates a function tool for keyword event lookup.
func NewQueryEventKeywordsTool() tool.CallableTool {
	call := func(ctx context.Context, req *QueryEventKeywordsRequest) (*DeepSearchQueryResponse, error) {
		svc, userKey, err := deepSearchQueryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory query event keywords tool: %w", err)
		}
		if req == nil || (strings.TrimSpace(req.Query) == "" && len(req.Keywords) == 0) {
			return nil, fmt.Errorf("memory query event keywords tool: query or keywords is required")
		}
		timeAfter, timeBefore, err := parseToolTimeRange(req.TimeAfter, req.TimeBefore)
		if err != nil {
			return nil, fmt.Errorf("memory query event keywords tool: %w", err)
		}
		result, err := svc.QueryEventKeywords(ctx, deepsearch.QueryEventKeywordsRequest{
			UserKey:    userKey,
			Query:      req.Query,
			Keywords:   req.Keywords,
			TimeAfter:  timeAfter,
			TimeBefore: timeBefore,
			MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}
		return queryResponse(result), nil
	}
	return function.NewFunctionTool(
		call,
		function.WithName(deepsearch.EventKeywordsToolName),
		function.WithDescription("Find event memories by exact keywords, entities, or phrases. "+memoryToolScopeNote),
		function.WithInputSchema(eventKeywordsInputSchema()),
	)
}

// NewQueryEventContextTool creates a function tool for event context lookup.
func NewQueryEventContextTool() tool.CallableTool {
	call := func(ctx context.Context, req *QueryEventContextRequest) (*DeepSearchQueryResponse, error) {
		svc, userKey, err := deepSearchQueryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory query event context tool: %w", err)
		}
		if req == nil || (len(req.ContentIDs) == 0 && len(req.Refs) == 0) {
			return nil, fmt.Errorf("memory query event context tool: content_ids or refs is required")
		}
		result, err := svc.QueryEventContext(ctx, deepsearch.QueryEventContextRequest{
			UserKey:    userKey,
			Query:      req.Query,
			ContentIDs: req.ContentIDs,
			Refs:       req.Refs,
			MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}
		return queryResponse(result), nil
	}
	return function.NewFunctionTool(
		call,
		function.WithName(deepsearch.EventContextToolName),
		function.WithDescription("Load context around a known memory event or content reference. "+memoryToolScopeNote),
		function.WithInputSchema(eventContextInputSchema()),
	)
}

// NewQueryPersonalInformationTool creates a tool for stable personal facts.
func NewQueryPersonalInformationTool() tool.CallableTool {
	call := func(ctx context.Context, req *QueryPersonalInformationRequest) (*DeepSearchQueryResponse, error) {
		svc, userKey, err := deepSearchQueryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory query personal information tool: %w", err)
		}
		result, err := svc.QueryPersonalInformation(ctx, deepsearch.QueryPersonalInformationRequest{
			UserKey:    userKey,
			Query:      reqQuery(req),
			Aspects:    reqAspects(req),
			MaxResults: reqMaxResults(req),
		})
		if err != nil {
			return nil, err
		}
		return queryResponse(result), nil
	}
	return function.NewFunctionTool(
		call,
		function.WithName(deepsearch.PersonalInformationToolName),
		function.WithDescription("Find stable personal facts, preferences, profile details, and long-term attributes. "+
			memoryToolScopeNote),
		function.WithInputSchema(personalInformationInputSchema()),
	)
}

// NewQueryPersonalAspectTool creates a tool for aspect-scoped personal memory.
func NewQueryPersonalAspectTool() tool.CallableTool {
	call := func(ctx context.Context, req *QueryPersonalAspectRequest) (*DeepSearchQueryResponse, error) {
		svc, userKey, err := deepSearchQueryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory query personal aspect tool: %w", err)
		}
		if req == nil || strings.TrimSpace(req.Aspect) == "" {
			return nil, fmt.Errorf("memory query personal aspect tool: aspect is required")
		}
		result, err := svc.QueryPersonalAspect(ctx, deepsearch.QueryPersonalAspectRequest{
			UserKey:    userKey,
			Aspect:     req.Aspect,
			Query:      req.Query,
			MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}
		return queryResponse(result), nil
	}
	return function.NewFunctionTool(
		call,
		function.WithName(deepsearch.PersonalAspectToolName),
		function.WithDescription("Find personal memories for one aspect such as preference, family, work, travel, or health. "+
			memoryToolScopeNote),
		function.WithInputSchema(personalAspectInputSchema()),
	)
}

// NewQueryTopicEventsTool creates a tool for topic-scoped event lookup.
func NewQueryTopicEventsTool() tool.CallableTool {
	call := func(ctx context.Context, req *QueryTopicEventsRequest) (*DeepSearchQueryResponse, error) {
		svc, userKey, err := deepSearchQueryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory query topic events tool: %w", err)
		}
		if req == nil || strings.TrimSpace(req.Topic) == "" {
			return nil, fmt.Errorf("memory query topic events tool: topic is required")
		}
		timeAfter, timeBefore, err := parseToolTimeRange(req.TimeAfter, req.TimeBefore)
		if err != nil {
			return nil, fmt.Errorf("memory query topic events tool: %w", err)
		}
		result, err := svc.QueryTopicEvents(ctx, deepsearch.QueryTopicEventsRequest{
			UserKey:    userKey,
			Topic:      req.Topic,
			Query:      req.Query,
			TimeAfter:  timeAfter,
			TimeBefore: timeBefore,
			MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}
		return queryResponse(result), nil
	}
	return function.NewFunctionTool(
		call,
		function.WithName(deepsearch.TopicEventsToolName),
		function.WithDescription("Find event memories attached to a topic, entity, or relation label. "+memoryToolScopeNote),
		function.WithInputSchema(topicEventsInputSchema()),
	)
}

func deepSearchServiceFromContext(
	ctx context.Context,
) (deepsearch.Service, memory.UserKey, error) {
	deepSearchSvc, err := GetDeepSearchServiceFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	appName, userID, err := GetAppAndUserFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	return deepSearchSvc, memory.UserKey{AppName: appName, UserID: userID}, nil
}

func deepSearchQueryServiceFromContext(
	ctx context.Context,
) (deepsearch.QueryService, memory.UserKey, error) {
	deepSearchSvc, err := GetDeepSearchServiceFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	querySvc, ok := deepSearchSvc.(deepsearch.QueryService)
	if !ok {
		return nil, memory.UserKey{}, fmt.Errorf("memory deepsearch service does not implement deepsearch.QueryService")
	}
	appName, userID, err := GetAppAndUserFromContext(ctx)
	if err != nil {
		return nil, memory.UserKey{}, err
	}
	return querySvc, memory.UserKey{AppName: appName, UserID: userID}, nil
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
		"refs":        contentRefArraySchema("Typed content references to load."),
		"max_results": integerSchema("Maximum content nodes to return."),
	})
}

func edgesByTagInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"tags":            stringArraySchema("Tag names or relation labels to traverse."),
		"query":           stringSchema("Optional question or keywords used to rank tag edges."),
		"max_results":     integerSchema("Maximum number of paths to return."),
		"include_content": boolSchema("Whether to include content nodes in returned paths."),
	})
}

func conversationTimeInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"query":       stringSchema("Optional question or keywords to rank events in the time window."),
		"time_after":  stringSchema("Start time or date in RFC3339 or YYYY-MM-DD format."),
		"time_before": stringSchema("End time or date in RFC3339 or YYYY-MM-DD format."),
		"max_results": integerSchema("Maximum number of events to return."),
	})
}

func eventKeywordsInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"query":       stringSchema("Question or keyword query used to retrieve events."),
		"keywords":    stringArraySchema("Additional exact keywords, entities, or phrases."),
		"time_after":  stringSchema("Optional start time or date in RFC3339 or YYYY-MM-DD format."),
		"time_before": stringSchema("Optional end time or date in RFC3339 or YYYY-MM-DD format."),
		"max_results": integerSchema("Maximum number of events to return."),
	})
}

func eventContextInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"query":          stringSchema("Optional question or keywords used to rank nearby context."),
		"content_ids":    stringArraySchema("Content node IDs to anchor context."),
		"refs":           contentRefArraySchema("Typed content references to anchor context."),
		"session_id":     stringSchema("Session ID to load context from."),
		"event_id":       stringSchema("Event ID to anchor context."),
		"context_radius": integerSchema("Number of nearby events to include on each side when supported."),
		"max_results":    integerSchema("Maximum number of context items to return."),
	})
}

func personalInformationInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"query":       stringSchema("Question or keywords for stable personal facts."),
		"aspects":     stringArraySchema("Personal aspects such as preference, profile, family, work, health, travel, or education."),
		"max_results": integerSchema("Maximum number of facts to return."),
	})
}

func personalAspectInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"aspect":      stringSchema("Personal aspect to inspect."),
		"query":       stringSchema("Optional question or keywords used to rank results."),
		"max_results": integerSchema("Maximum number of memories to return."),
	}, "aspect")
}

func topicEventsInputSchema() *tool.Schema {
	return objectSchema(map[string]*tool.Schema{
		"topic":       stringSchema("Topic, entity, or relation label whose events should be retrieved."),
		"query":       stringSchema("Optional question or keywords used to rank topic events."),
		"time_after":  stringSchema("Optional start time or date in RFC3339 or YYYY-MM-DD format."),
		"time_before": stringSchema("Optional end time or date in RFC3339 or YYYY-MM-DD format."),
		"max_results": integerSchema("Maximum number of events to return."),
	}, "topic")
}

func contentRefArraySchema(description string) *tool.Schema {
	return &tool.Schema{
		Type:        "array",
		Description: description,
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
	}
}

func numberSchema(description string) *tool.Schema {
	return &tool.Schema{
		Type:        "number",
		Description: description,
	}
}

func parseToolTimeRange(after, before string) (time.Time, time.Time, error) {
	timeAfter, err := parseToolTime(after)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	timeBefore, err := parseToolTime(before)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return timeAfter, timeBefore, nil
}

func parseToolTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	formats := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q; expected RFC3339 or YYYY-MM-DD", value)
}

func queryResponse(result *deepsearch.QueryResult) *DeepSearchQueryResponse {
	if result == nil {
		return &DeepSearchQueryResponse{}
	}
	return &DeepSearchQueryResponse{
		Query:    result.Query,
		Contents: result.Contents,
		Count:    len(result.Contents),
	}
}

func reqQuery(req any) string {
	switch typed := req.(type) {
	case *QueryConversationTimeRequest:
		if typed != nil {
			return typed.Query
		}
	case *QueryPersonalInformationRequest:
		if typed != nil {
			return typed.Query
		}
	}
	return ""
}

func reqTimeAfter(req *QueryConversationTimeRequest) string {
	if req == nil {
		return ""
	}
	return req.TimeAfter
}

func reqTimeBefore(req *QueryConversationTimeRequest) string {
	if req == nil {
		return ""
	}
	return req.TimeBefore
}

func reqMaxResults(req any) int {
	switch typed := req.(type) {
	case *QueryConversationTimeRequest:
		if typed != nil {
			return typed.MaxResults
		}
	case *QueryPersonalInformationRequest:
		if typed != nil {
			return typed.MaxResults
		}
	}
	return 0
}

func reqAspects(req *QueryPersonalInformationRequest) []string {
	if req == nil {
		return nil
	}
	return req.Aspects
}
