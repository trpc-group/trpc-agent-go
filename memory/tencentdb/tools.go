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
	Query   string `json:"query"`
	Results string `json:"results"`
	Total   int    `json:"total"`
}

type readOffloadRefToolRequest struct {
	ResultRef string `json:"result_ref" description:"Relative result reference produced by TencentDB context offload, for example refs/node_20260612_000001.md."`
}

type readOffloadRefToolResponse struct {
	ResultRef string `json:"result_ref"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

type readOffloadNodeToolRequest struct {
	NodeID string `json:"node_id" description:"Mermaid node_id produced by TencentDB context offload."`
}

type readOffloadNodeToolResponse struct {
	NodeID  string              `json:"node_id"`
	Entries []offloadIndexEntry `json:"entries"`
}

type searchOffloadIndexToolRequest struct {
	Query string `json:"query" description:"Keyword query over TencentDB context offload summaries, tool calls, node IDs, and result_refs."`
	Limit int    `json:"limit,omitempty" description:"Maximum results. Defaults to 5, maximum 20."`
}

type searchOffloadIndexToolResponse struct {
	Query   string              `json:"query"`
	Entries []offloadIndexEntry `json:"entries"`
	Total   int                 `json:"total"`
}

func (s *Service) buildTools() []tool.Tool {
	out := make([]tool.Tool, 0, 5)
	seen := make(map[string]struct{}, 5)
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

	if s.opts.EnableMemorySearchTool {
		add(s.newMemorySearchTool(s.nativeToolName("memory_search")))
		if s.opts.EnableStandardAliases {
			add(s.newMemorySearchTool(memorypkg.SearchToolName))
		}
	}
	if s.opts.EnableConversationSearchTool {
		add(s.newConversationSearchTool(s.nativeToolName("conversation_search")))
	}
	if s.opts.ContextOffload.Enabled {
		add(s.newReadOffloadRefTool(s.nativeToolName("read_offload_ref")))
		add(s.newReadOffloadNodeTool(s.nativeToolName("read_offload_node")))
		add(s.newSearchOffloadIndexTool(s.nativeToolName("search_offload_index")))
	}
	return out
}

func (s *Service) nativeToolName(name string) string {
	return nativeToolName(s.opts, name)
}

func nativeToolName(opts Options, name string) string {
	prefix := strings.Trim(strings.TrimSpace(opts.ToolPrefix), "_-")
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
		sess, err := currentSession(ctx)
		if err != nil {
			return nil, err
		}
		limit := normalizeLimit(req.Limit)
		rsp, err := s.client.searchMemories(ctx, searchMemoriesRequest{
			Query:  strings.TrimSpace(req.Query),
			Limit:  limit,
			Type:   strings.TrimSpace(req.Type),
			Scene:  strings.TrimSpace(req.Scene),
			UserID: sess.UserID,
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
			UserID:     sess.UserID,
		})
		if err != nil {
			return nil, err
		}
		return &searchConversationsToolResponse{
			Query:   strings.TrimSpace(req.Query),
			Results: strings.TrimSpace(rsp.Results),
			Total:   rsp.Total,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Search TencentDB Agent Memory conversation history. "+
			"Defaults to the current session_key and is useful for recalling earlier raw exchanges."),
	)
}

func (s *Service) newReadOffloadRefTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *readOffloadRefToolRequest) (*readOffloadRefToolResponse, error) {
		if req == nil || strings.TrimSpace(req.ResultRef) == "" {
			return nil, fmt.Errorf("%s: result_ref is required", name)
		}
		inv, err := currentInvocation(ctx)
		if err != nil {
			return nil, err
		}
		client := s.contextOffloadClient()
		if client == nil {
			return nil, fmt.Errorf("%s: context offload gateway is unavailable", name)
		}
		rsp, err := client.offloadReadRef(ctx, offloadReadRefRequest{
			Scope:     newOffloadScope(s.opts, inv.Session, inv.AgentName),
			ResultRef: strings.TrimSpace(req.ResultRef),
		})
		if err != nil {
			return nil, err
		}
		if rsp == nil {
			return &readOffloadRefToolResponse{
				ResultRef: strings.TrimSpace(req.ResultRef),
			}, nil
		}
		return &readOffloadRefToolResponse{
			ResultRef: rsp.ResultRef,
			Content:   rsp.Content,
			Truncated: rsp.Truncated,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Read a tool result externalized by TencentDB context offload. "+
			"Use this when the prompt contains a result_ref and exact details are needed."),
	)
}

func (s *Service) newReadOffloadNodeTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *readOffloadNodeToolRequest) (*readOffloadNodeToolResponse, error) {
		if req == nil || strings.TrimSpace(req.NodeID) == "" {
			return nil, fmt.Errorf("%s: node_id is required", name)
		}
		inv, err := currentInvocation(ctx)
		if err != nil {
			return nil, err
		}
		client := s.contextOffloadClient()
		if client == nil {
			return nil, fmt.Errorf("%s: context offload gateway is unavailable", name)
		}
		nodeID := strings.TrimSpace(req.NodeID)
		rsp, err := client.offloadReadNode(ctx, offloadReadNodeRequest{
			Scope:  newOffloadScope(s.opts, inv.Session, inv.AgentName),
			NodeID: nodeID,
		})
		if err != nil {
			return nil, err
		}
		if rsp == nil {
			return &readOffloadNodeToolResponse{NodeID: nodeID}, nil
		}
		return &readOffloadNodeToolResponse{
			NodeID:  rsp.NodeID,
			Entries: rsp.Entries,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Read TencentDB context offload entries mapped to a Mermaid node_id. "+
			"Use this to drill down from the active task Mermaid graph."),
	)
}

func (s *Service) newSearchOffloadIndexTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *searchOffloadIndexToolRequest) (*searchOffloadIndexToolResponse, error) {
		if req == nil || strings.TrimSpace(req.Query) == "" {
			return nil, fmt.Errorf("%s: query is required", name)
		}
		inv, err := currentInvocation(ctx)
		if err != nil {
			return nil, err
		}
		client := s.contextOffloadClient()
		if client == nil {
			return nil, fmt.Errorf("%s: context offload gateway is unavailable", name)
		}
		query := strings.TrimSpace(req.Query)
		limit := normalizeLimit(req.Limit)
		rsp, err := client.offloadSearchIndex(ctx, offloadSearchIndexRequest{
			Scope: newOffloadScope(s.opts, inv.Session, inv.AgentName),
			Query: query,
			Limit: limit,
		})
		if err != nil {
			return nil, err
		}
		if rsp == nil {
			return &searchOffloadIndexToolResponse{Query: query}, nil
		}
		return &searchOffloadIndexToolResponse{
			Query:   rsp.Query,
			Entries: rsp.Entries,
			Total:   rsp.Total,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Search TencentDB context offload L1 summaries and refs for the current session. "+
			"Use this when the active Mermaid graph does not show the exact result_ref needed."),
	)
}

func currentSession(ctx context.Context) (*session.Session, error) {
	inv, err := currentInvocation(ctx)
	if err != nil {
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

func currentInvocation(ctx context.Context) (*agent.Invocation, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, fmt.Errorf("tencentdb memory: invocation session is required")
	}
	if err := validateSessionScope(inv.Session); err != nil {
		return nil, err
	}
	return inv, nil
}
