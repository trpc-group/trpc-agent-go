//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides memory-related tools for the agent system.
package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Memory function implementations using function.NewFunctionTool.

// NewAddTool creates a function tool for adding memories.
func NewAddTool() tool.CallableTool {
	addFunc := func(ctx context.Context, req *AddMemoryRequest) (*AddMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory add tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory add tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req == nil || req.Memory == "" {
			return nil, fmt.Errorf("memory add tool: memory content is required for app %s and user %s", appName, userID)
		}

		// Ensure topics is never nil.
		if req.Topics == nil {
			req.Topics = []string{}
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		ep := buildMetadata(req.MemoryKind, req.EventTime, req.Participants, req.Location)
		var opts []memory.AddOption
		if ep != nil {
			opts = append(opts, memory.WithMetadata(ep))
		}
		err = memoryService.AddMemory(ctx, userKey, req.Memory, req.Topics, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to add memory: %v", err)
		}

		return &AddMemoryResponse{
			Message: "Memory added successfully",
			Memory:  req.Memory,
			Topics:  req.Topics,
		}, nil
	}

	return function.NewFunctionTool(
		addFunc,
		function.WithName(memory.AddToolName),
		function.WithDescription("Add a new memory about the user. Use this tool to store "+
			"important information about the user's preferences, background, or past interactions."),
	)
}

// NewUpdateTool creates a function tool for updating memories.
func NewUpdateTool() tool.CallableTool {
	updateFunc := func(ctx context.Context, req *UpdateMemoryRequest) (*UpdateMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, err
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory update tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req == nil || req.MemoryID == "" {
			return nil, fmt.Errorf("memory update tool: memory ID is required for app %s and user %s", appName, userID)
		}

		if req.Memory == "" {
			return nil, fmt.Errorf("memory update tool: memory content is required for app %s and user %s", appName, userID)
		}

		// Ensure topics is never nil.
		if req.Topics == nil {
			req.Topics = []string{}
		}

		memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: req.MemoryID}
		ep := buildMetadata(req.MemoryKind, req.EventTime, req.Participants, req.Location)
		result := &memory.UpdateResult{MemoryID: req.MemoryID}
		var opts []memory.UpdateOption
		opts = append(opts, memory.WithUpdateResult(result))
		if ep != nil {
			opts = append(opts, memory.WithUpdateMetadata(ep))
		}
		err = memoryService.UpdateMemory(ctx, memoryKey, req.Memory, req.Topics, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to update memory: %v", err)
		}

		return &UpdateMemoryResponse{
			Message:  "Memory updated successfully",
			MemoryID: result.MemoryID,
			Memory:   req.Memory,
			Topics:   req.Topics,
		}, nil
	}

	return function.NewFunctionTool(
		updateFunc,
		function.WithName(memory.UpdateToolName),
		function.WithDescription("Update an existing memory. Use this tool to modify stored "+
			"information about the user."),
	)
}

// NewDeleteTool creates a function tool for deleting memories.
func NewDeleteTool() tool.CallableTool {
	deleteFunc := func(ctx context.Context, req *DeleteMemoryRequest) (*DeleteMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory delete tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory delete tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req == nil || req.MemoryID == "" {
			return nil, fmt.Errorf("memory delete tool: memory ID is required for app %s and user %s", appName, userID)
		}

		memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: req.MemoryID}
		err = memoryService.DeleteMemory(ctx, memoryKey)
		if err != nil {
			return nil, fmt.Errorf("failed to delete memory: %v", err)
		}

		return &DeleteMemoryResponse{
			Message:  "Memory deleted successfully",
			MemoryID: req.MemoryID,
		}, nil
	}

	return function.NewFunctionTool(
		deleteFunc,
		function.WithName(memory.DeleteToolName),
		function.WithDescription("Delete a specific memory. Use this tool to remove outdated "+
			"or incorrect information about the user."),
	)
}

// NewClearTool creates a function tool for clearing all memories.
func NewClearTool() tool.CallableTool {
	clearFunc := func(ctx context.Context, _ *ClearMemoryRequest) (*ClearMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory clear tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory clear tool: failed to get app and user from context: %v", err)
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		err = memoryService.ClearMemories(ctx, userKey)
		if err != nil {
			return nil, fmt.Errorf("memory clear tool: failed to clear memories: %v", err)
		}

		return &ClearMemoryResponse{
			Message: "All memories cleared successfully",
		}, nil
	}

	return function.NewFunctionTool(
		clearFunc,
		function.WithName(memory.ClearToolName),
		function.WithDescription("Clear all memories for the user. Use this tool to reset the "+
			"user's memory completely."),
	)
}

// NewSearchTool creates a function tool for searching memories.
func NewSearchTool() tool.CallableTool {
	searchFunc := func(ctx context.Context, req *SearchMemoryRequest) (*SearchMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory search tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory search tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req == nil || req.Query == "" {
			return &SearchMemoryResponse{
				Query:   "",
				Results: []Result{},
				Count:   0,
			}, nil
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		opts := buildSearchOptions(req)
		memories, err := memoryService.SearchMemories(ctx, userKey,
			opts.Query, memory.WithSearchOptions(opts))
		if err != nil {
			return nil, fmt.Errorf("failed to search memories: %v", err)
		}

		// Convert MemoryEntry to MemoryResult.
		results := make([]Result, len(memories))
		for i, m := range memories {
			results[i] = entryToResult(m)
		}

		return &SearchMemoryResponse{
			Query:   req.Query,
			Results: results,
			Count:   len(results),
		}, nil
	}

	return function.NewFunctionTool(
		searchFunc,
		function.WithName(memory.SearchToolName),
		function.WithDescription("Search for relevant memories about the user. "+
			"Returns memories ranked by semantic similarity, each with: id, memory text, topics, kind (fact/episode), "+
			"event_time, participants, location, and similarity score (0-1, higher = more relevant). "+
			"IMPORTANT: Check the 'participants' field to verify the memory is about the correct person before using it as evidence. "+
			"Use short keyword-style queries for best results (e.g. 'Alice hiking trip' instead of 'When did Alice go hiking?'). "+
			"For multi-part questions, search for each sub-question separately and combine the results. "+
			"For temporal questions (e.g. 'when did X happen', 'what did user do in May 2023'), "+
			"use time_after/time_before filters and consider setting order_by_event_time=true. "+
			"The 'kind' filter is optional and acts as a preference with automatic fallback; "+
			"omit it when uncertain whether the answer is stored as a fact or episode."),
	)
}

// NewLoadTool creates a function tool for loading memories.
func NewLoadTool() tool.CallableTool {
	loadFunc := func(ctx context.Context, req *LoadMemoryRequest) (*LoadMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory load tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory load tool: failed to get app and user from context: %v", err)
		}

		// Set default limit.
		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		memories, err := memoryService.ReadMemories(ctx, userKey, limit)
		if err != nil {
			return nil, fmt.Errorf("failed to load memories: %v", err)
		}

		// Convert MemoryEntry to MemoryResult.
		results := make([]Result, len(memories))
		for i, m := range memories {
			results[i] = entryToResult(m)
		}

		return &LoadMemoryResponse{
			Limit:   limit,
			Results: results,
			Count:   len(results),
		}, nil
	}

	return function.NewFunctionTool(
		loadFunc,
		function.WithName(memory.LoadToolName),
		function.WithDescription("Load the most recent memories about the user. "+
			"Returns memories ordered by last update time. Each memory includes: id, text, topics, "+
			"kind (fact/episode), event_time, participants, and location. "+
			"Use this to get a broad overview of what is known about the user."),
	)
}

// GetMemoryServiceFromContext extracts MemoryService from the invocation context.
// This function looks for the MemoryService in the agent invocation context.
//
// This function is exported to allow users to implement custom memory tools
// that need access to the memory service from the invocation context.
func GetMemoryServiceFromContext(ctx context.Context) (memory.Service, error) {
	// Get invocation from context.
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return nil, errors.New("no invocation context found")
	}

	// Check if MemoryService is available.
	if invocation.MemoryService == nil {
		return nil, errors.New("memory service is not available")
	}

	return invocation.MemoryService, nil
}

// GetAppAndUserFromContext extracts appName and userID from the context.
// This function looks for these values in the agent invocation context.
//
// This function is exported to allow users to implement custom memory tools
// that need access to app and user information from the invocation context.
func GetAppAndUserFromContext(ctx context.Context) (string, string, error) {
	// Try to get from agent invocation context.
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return "", "", errors.New("no invocation context found")
	}

	// Try to get from session.
	if invocation.Session == nil {
		return "", "", errors.New("invocation exists but no session available")
	}

	// Session has AppName and UserID fields.
	if invocation.Session.AppName != "" && invocation.Session.UserID != "" {
		return invocation.Session.AppName, invocation.Session.UserID, nil
	}

	// Return error if session exists but missing required fields.
	return "", "", fmt.Errorf("session exists but missing appName or userID: appName=%s, userID=%s",
		invocation.Session.AppName, invocation.Session.UserID)
}

// buildMetadata constructs MemoryMetadata from tool
// request strings. Returns nil if no episodic data is
// provided (backward compatible).
func buildMetadata(kind, eventTimeStr string, participants []string, location string) *memory.Metadata {
	if kind == "" && eventTimeStr == "" && len(participants) == 0 && location == "" {
		return nil
	}
	ep := &memory.Metadata{
		Kind:         memory.Kind(kind),
		Participants: participants,
		Location:     location,
	}
	if eventTimeStr != "" {
		ep.EventTime = ParseFlexibleTime(eventTimeStr)
	}
	return ep
}

// buildSearchOptions constructs SearchOptions from a SearchMemoryRequest.
func buildSearchOptions(req *SearchMemoryRequest) memory.SearchOptions {
	opts := memory.SearchOptions{
		Query:        req.Query,
		Kind:         memory.Kind(req.Kind),
		Deduplicate:  true,
		HybridSearch: true,
	}
	// Enable kind fallback when a kind filter is requested so that
	// results of the other kind are still included if the filtered
	// set is too small.
	if opts.Kind != "" {
		opts.KindFallback = true
	}
	for _, pair := range []struct {
		raw   string
		dst   **time.Time
		isEnd bool
	}{
		{req.TimeAfter, &opts.TimeAfter, false},
		{req.TimeBefore, &opts.TimeBefore, true},
	} {
		if pair.raw == "" {
			continue
		}
		t := ParseFlexibleTime(pair.raw)
		if t != nil && pair.isEnd {
			end := EndOfPeriod(*t, pair.raw)
			t = &end
		}
		*pair.dst = t
	}
	opts.OrderByEventTime = req.OrderByEventTime
	return opts
}

// ParseFlexibleTime tries multiple date/time formats including natural language dates
// that LLMs commonly produce (e.g. "7 May 2023", "May 7, 2023").
// Returns nil if the string cannot be parsed.
//
// This function is exported so that other packages (e.g. extractor) can reuse
// the same flexible time parsing logic without duplicating format lists.
func ParseFlexibleTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2 January 2006",
		"January 2, 2006",
		"Jan 2, 2006",
		"2 Jan 2006",
		"January 2006",
		"Jan 2006",
		"2006-01",
		"2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// EndOfPeriod adjusts a parsed time to the end of the period implied by the raw string.
// For month-level dates like "2023-05" or "May 2023", returns the last day of that month.
// For year-level dates like "2023", returns Dec 31 of that year.
// For day-level dates, returns the end of that day (23:59:59).
func EndOfPeriod(t time.Time, raw string) time.Time {
	raw = strings.TrimSpace(raw)
	// Year-only: "2023"
	if len(raw) == 4 {
		return time.Date(t.Year(), 12, 31, 23, 59, 59, 0, t.Location())
	}
	// Month-level: "2023-05", "May 2023", "Jan 2023", "January 2023"
	for _, layout := range []string{"2006-01", "January 2006", "Jan 2006"} {
		if _, err := time.Parse(layout, raw); err == nil {
			// Go to the first day of next month, subtract 1 second.
			nextMonth := t.AddDate(0, 1, 0)
			return time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, t.Location()).Add(-time.Second)
		}
	}
	// Day-level: set to end of day.
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
}

// entryToResult converts a memory.Entry to a tool Result, including episodic fields.
func entryToResult(e *memory.Entry) Result {
	r := Result{
		ID:      e.ID,
		Memory:  e.Memory.Memory,
		Topics:  e.Memory.Topics,
		Created: e.CreatedAt,
	}
	if e.Memory.Kind != "" {
		r.Kind = string(e.Memory.Kind)
	}
	if e.Memory.EventTime != nil {
		r.EventTime = e.Memory.EventTime.Format(time.RFC3339)
	}
	if len(e.Memory.Participants) > 0 {
		r.Participants = e.Memory.Participants
	}
	if e.Memory.Location != "" {
		r.Location = e.Memory.Location
	}
	r.Score = e.Score
	return r
}
