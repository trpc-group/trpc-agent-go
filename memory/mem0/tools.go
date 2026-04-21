//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func buildReadOnlyTools(s *Service) []tool.Tool {
	tools := []tool.Tool{newSearchTool(s)}
	if s.opts.loadToolEnabled {
		tools = append(tools, newLoadTool(s))
	}
	return tools
}

func newSearchTool(s *Service) tool.Tool {
	searchFunc := func(ctx context.Context, req *memorytool.SearchMemoryRequest) (*memorytool.SearchMemoryResponse, error) {
		if req == nil || strings.TrimSpace(req.Query) == "" {
			return &memorytool.SearchMemoryResponse{Query: "", Results: []memorytool.Result{}, Count: 0}, nil
		}
		appName, userID, err := memorytool.GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("mem0 search tool: failed to get app and user from context: %w", err)
		}
		searchOpts, err := buildToolSearchOptions(req)
		if err != nil {
			return nil, err
		}
		entries, err := s.SearchMemories(
			ctx,
			memory.UserKey{AppName: appName, UserID: userID},
			searchOpts.Query,
			memory.WithSearchOptions(searchOpts),
		)
		if err != nil {
			return nil, fmt.Errorf("mem0 search tool: failed to search memories: %w", err)
		}
		results := make([]memorytool.Result, len(entries))
		for i, entry := range entries {
			results[i] = entryToResult(entry)
		}
		return &memorytool.SearchMemoryResponse{
			Query:   req.Query,
			Results: results,
			Count:   len(results),
		}, nil
	}
	return function.NewFunctionTool(
		searchFunc,
		function.WithName(memory.SearchToolName),
		function.WithDescription("Search for relevant memories stored in mem0 for the current user."),
	)
}

func newLoadTool(s *Service) tool.Tool {
	loadFunc := func(ctx context.Context, req *memorytool.LoadMemoryRequest) (*memorytool.LoadMemoryResponse, error) {
		limit := 10
		if req != nil && req.Limit > 0 {
			limit = req.Limit
		}
		appName, userID, err := memorytool.GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("mem0 load tool: failed to get app and user from context: %w", err)
		}
		entries, err := s.ReadMemories(ctx, memory.UserKey{AppName: appName, UserID: userID}, limit)
		if err != nil {
			return nil, fmt.Errorf("mem0 load tool: failed to read memories: %w", err)
		}
		results := make([]memorytool.Result, len(entries))
		for i, entry := range entries {
			results[i] = entryToResult(entry)
		}
		return &memorytool.LoadMemoryResponse{Limit: limit, Results: results, Count: len(results)}, nil
	}
	return function.NewFunctionTool(
		loadFunc,
		function.WithName(memory.LoadToolName),
		function.WithDescription("Load the most recent memories stored in mem0 for the current user."),
	)
}

func buildToolSearchOptions(req *memorytool.SearchMemoryRequest) (memory.SearchOptions, error) {
	opts := memory.SearchOptions{
		Query:        req.Query,
		Kind:         memory.Kind(req.Kind),
		KindFallback: strings.TrimSpace(req.Kind) != "",
	}
	for _, pair := range []struct {
		raw   string
		dst   **time.Time
		isEnd bool
	}{
		{raw: req.TimeAfter, dst: &opts.TimeAfter, isEnd: false},
		{raw: req.TimeBefore, dst: &opts.TimeBefore, isEnd: true},
	} {
		if strings.TrimSpace(pair.raw) == "" {
			continue
		}
		t := memorytool.ParseFlexibleTime(pair.raw)
		if t == nil {
			return memory.SearchOptions{}, fmt.Errorf("invalid time value: %s", pair.raw)
		}
		if pair.isEnd {
			end := memorytool.EndOfPeriod(*t, pair.raw)
			t = &end
		}
		*pair.dst = t
	}
	opts.OrderByEventTime = req.OrderByEventTime
	return opts, nil
}

func entryToResult(e *memory.Entry) memorytool.Result {
	result := memorytool.Result{
		ID:      e.ID,
		Memory:  e.Memory.Memory,
		Topics:  e.Memory.Topics,
		Created: e.CreatedAt,
		Score:   e.Score,
	}
	if e.Memory.Kind != "" {
		result.Kind = string(e.Memory.Kind)
	}
	if e.Memory.EventTime != nil {
		result.EventTime = e.Memory.EventTime.Format(time.RFC3339)
	}
	if len(e.Memory.Participants) > 0 {
		result.Participants = e.Memory.Participants
	}
	if e.Memory.Location != "" {
		result.Location = e.Memory.Location
	}
	return result
}
