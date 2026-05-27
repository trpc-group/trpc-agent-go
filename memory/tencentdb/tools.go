//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	memorypkg "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultSearchLimit = 5
	maxSearchLimit     = 20
)

type searchMemoriesToolRequest struct {
	Query string `json:"query" description:"Search query for long-term memories. Use short keyword style queries when possible."`
	Limit int    `json:"limit,omitempty" description:"Maximum number of results to return. Defaults to 5, maximum 20."`
	Type  string `json:"type,omitempty" description:"Optional memory type or layer selector supported by the TencentDB Agent Memory gateway."`
	Scene string `json:"scene,omitempty" description:"Optional scene name to narrow the search if the gateway supports scene filtering."`
}

type searchMemoriesToolResponse struct {
	Query    string `json:"query"`
	Results  string `json:"results"`
	Total    int    `json:"total"`
	Strategy string `json:"strategy,omitempty"`
}

type searchConversationsToolRequest struct {
	Query string `json:"query" description:"Search query for raw or summarized conversation history."`
	Limit int    `json:"limit,omitempty" description:"Maximum number of results to return. Defaults to 5, maximum 20."`
}

type searchConversationsToolResponse struct {
	Query      string `json:"query"`
	SessionKey string `json:"session_key,omitempty"`
	Results    string `json:"results"`
	Total      int    `json:"total"`
}

func (s *Service) buildTools() []tool.Tool {
	out := make([]tool.Tool, 0, 3)
	seen := make(map[string]struct{}, 3)
	add := func(t tool.Tool) {
		if t == nil || t.Declaration() == nil {
			return
		}
		name := t.Declaration().Name
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, t)
	}

	add(s.newMemorySearchTool(s.nativeToolName("memory_search")))
	if s.opts.EnableConversationSearchTool {
		add(s.newConversationSearchTool(s.nativeToolName("conversation_search")))
	}
	if s.opts.EnableStandardAliases {
		add(s.newMemorySearchTool(memorypkg.SearchToolName))
	}
	return out
}

func (s *Service) nativeToolName(name string) string {
	prefix := strings.Trim(strings.TrimSpace(s.opts.ToolPrefix), "_-")
	if prefix == "" {
		return name
	}
	return prefix + "_" + name
}

func (s *Service) newMemorySearchTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *searchMemoriesToolRequest) (*searchMemoriesToolResponse, error) {
		if req == nil || strings.TrimSpace(req.Query) == "" {
			return nil, fmt.Errorf("%s: query is required", name)
		}
		_, err := currentSession(ctx)
		if err != nil {
			return nil, err
		}
		limit := normalizeLimit(req.Limit)
		rsp, err := s.client.searchMemories(ctx, searchMemoriesRequest{
			Query: strings.TrimSpace(req.Query),
			Limit: limit,
			Type:  strings.TrimSpace(req.Type),
			Scene: strings.TrimSpace(req.Scene),
		})
		if err != nil {
			return nil, err
		}
		return &searchMemoriesToolResponse{
			Query:    strings.TrimSpace(req.Query),
			Results:  strings.TrimSpace(rsp.Results),
			Total:    rsp.Total,
			Strategy: rsp.Strategy,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Search TencentDB Agent Memory long-term memories scoped by the configured gateway sidecar. "+
			"Use this directly when the current request depends on remembered facts, preferences, or prior episodes."),
	)
}

func (s *Service) newConversationSearchTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *searchConversationsToolRequest) (*searchConversationsToolResponse, error) {
		if req == nil || strings.TrimSpace(req.Query) == "" {
			return nil, fmt.Errorf("%s: query is required", name)
		}
		sess, err := currentSession(ctx)
		if err != nil {
			return nil, err
		}
		sessionKey := s.sessionKey(sess)
		limit := normalizeLimit(req.Limit)
		rsp, err := s.client.searchConversations(ctx, searchConversationsRequest{
			Query:      strings.TrimSpace(req.Query),
			Limit:      limit,
			SessionKey: sessionKey,
		})
		if err != nil {
			return nil, err
		}
		return &searchConversationsToolResponse{
			Query:      strings.TrimSpace(req.Query),
			SessionKey: sessionKey,
			Results:    strings.TrimSpace(rsp.Results),
			Total:      rsp.Total,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Search TencentDB Agent Memory conversation history. "+
			"Defaults to the current session_key and is useful for recalling earlier raw exchanges."),
	)
}

func currentSession(ctx context.Context) (*session.Session, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, errors.New("tencentdb memory tool: invocation session is required")
	}
	if err := validateSessionScope(inv.Session); err != nil {
		return nil, err
	}
	return inv.Session, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}
