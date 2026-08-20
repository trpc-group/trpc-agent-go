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
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	memorypkg "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultSearchLimit             = 5
	maxSearchLimit                 = 20
	maxV3ScenarioNavigationEntries = 100
	maxV3ScenarioNavigationBytes   = 8 << 10
	maxV3ScenarioContentBytes      = 16 << 10
)

type searchMemoriesToolRequest struct {
	Query string `json:"query" description:"Search query for long-term memories. Use short keyword style queries when possible."`
	Limit int    `json:"limit,omitempty" description:"Maximum number of results to return. Defaults to 5, maximum 20."`
	Type  string `json:"type,omitempty" description:"Optional memory type or layer selector supported by the TencentDB Agent Memory gateway."`
	Scene string `json:"scene,omitempty" description:"Optional scene name to narrow the search if the gateway supports scene filtering."`
}

type searchMemoriesV3ToolRequest struct {
	Query string `json:"query" description:"Search query for long-term memories. Use short keyword style queries when possible."`
	Limit int    `json:"limit,omitempty" description:"Maximum number of results to return. Defaults to 5, maximum 20."`
	Type  string `json:"type,omitempty" description:"Optional L1 memory type: episodic, persona, or instruction."`
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

type readScenarioToolRequest struct {
	Path string `json:"path" description:"Scenario path returned by TencentDB scene navigation, for example reviews.md."`
}

type readScenarioToolResponse struct {
	Path      string `json:"path"`
	Version   string `json:"version"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type memorySearchCall struct {
	query      string
	limit      int
	memoryType string
	scene      string
}

type readOffloadRefToolRequest struct {
	ResultRef string `json:"result_ref" description:"Result reference produced by TencentDB context offload, for example offload/session/refs/call_1.md."`
	Query     string `json:"query,omitempty" description:"Optional case-insensitive text to locate within the archived result. Cannot be combined with line ranges."`
	StartLine int    `json:"start_line,omitempty" description:"Optional one-based first line to read. Cannot be combined with query."`
	EndLine   int    `json:"end_line,omitempty" description:"Optional one-based last line to read. Cannot be combined with query."`
	MaxTokens int    `json:"max_tokens,omitempty" description:"Maximum response size in tokens. Defaults to 1600, maximum 4096."`
}

type readOffloadRefToolResponse struct {
	ResultRef  string `json:"result_ref"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated"`
	MatchFound *bool  `json:"match_found,omitempty"`
}

func (s *Service) buildTools() []tool.Tool {
	out := make([]tool.Tool, 0, 4)
	seen := make(map[string]struct{}, 4)
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
	}
	if s.client.usesV3API() {
		add(s.newScenarioReadTool(s.nativeToolName("read_scenario")))
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
	if s.client.usesV3API() {
		return s.newV3MemorySearchTool(name)
	}
	return s.newLegacyMemorySearchTool(name)
}

func (s *Service) newLegacyMemorySearchTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *searchMemoriesToolRequest) (*searchMemoriesToolResponse, error) {
		if req == nil {
			return nil, fmt.Errorf("%s: query is required", name)
		}
		return s.callMemorySearch(
			ctx,
			name,
			memorySearchCall{
				query:      req.Query,
				limit:      req.Limit,
				memoryType: req.Type,
				scene:      req.Scene,
			},
		)
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Search TencentDB Agent Memory long-term memories scoped by the configured gateway sidecar. "+
			"Use this directly when the current request depends on remembered facts, preferences, or prior episodes."),
	)
}

func (s *Service) newV3MemorySearchTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *searchMemoriesV3ToolRequest) (*searchMemoriesToolResponse, error) {
		if req == nil {
			return nil, fmt.Errorf("%s: query is required", name)
		}
		return s.callMemorySearch(
			ctx,
			name,
			memorySearchCall{
				query:      req.Query,
				limit:      req.Limit,
				memoryType: req.Type,
			},
		)
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Search TencentDB Agent Memory long-term memories scoped by the configured service, team, agent, and current user. "+
			"Use this directly when the current request depends on remembered facts, preferences, or prior episodes."),
	)
}

func (s *Service) callMemorySearch(
	ctx context.Context,
	name string,
	call memorySearchCall,
) (*searchMemoriesToolResponse, error) {
	query := strings.TrimSpace(call.query)
	if query == "" {
		return nil, fmt.Errorf("%s: query is required", name)
	}
	sess, err := currentSession(ctx)
	if err != nil {
		return nil, err
	}
	rsp, err := s.client.searchMemories(ctx, searchMemoriesRequest{
		Query:  query,
		Limit:  normalizeLimit(call.limit),
		Type:   strings.TrimSpace(call.memoryType),
		Scene:  strings.TrimSpace(call.scene),
		UserID: sess.UserID,
	})
	if err != nil {
		return nil, err
	}
	return &searchMemoriesToolResponse{
		Query:    query,
		Results:  strings.TrimSpace(rsp.Results),
		Total:    rsp.Total,
		Strategy: rsp.Strategy,
	}, nil
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
			SessionID:  sess.ID,
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
	description := "Search TencentDB Agent Memory conversation history. " +
		"Defaults to the current session_key and is useful for recalling earlier raw exchanges."
	if s.client.usesV3API() {
		description = "Search TencentDB Agent Memory conversation history. " +
			"Defaults to the current session_id and is useful for recalling earlier raw exchanges."
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription(description),
	)
}

func (s *Service) newScenarioReadTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *readScenarioToolRequest) (*readScenarioToolResponse, error) {
		if req == nil || strings.TrimSpace(req.Path) == "" {
			return nil, fmt.Errorf("%s: path is required", name)
		}
		sess, err := currentSession(ctx)
		if err != nil {
			return nil, err
		}
		rsp, err := s.client.readScenarioV3(
			ctx,
			sess.UserID,
			strings.TrimSpace(req.Path),
		)
		if err != nil {
			return nil, err
		}
		content, truncated := truncateV3ScenarioContent(rsp.Content)
		return &readScenarioToolResponse{
			Path:      rsp.Path,
			Version:   string(rsp.Version),
			Content:   content,
			Truncated: truncated,
			CreatedAt: rsp.CreatedAt,
			UpdatedAt: rsp.UpdatedAt,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Read a TencentDB Agent Memory L2 scenario file by a path returned by scene navigation. "+
			"Responses are capped at 16 KiB and mark truncated content."),
	)
}

func formatV3ScenarioEntries(items []v3ScenarioEntry) (string, int) {
	lines := make([]string, 0, maxV3ScenarioNavigationEntries)
	bytesUsed := 0
	truncated := false
	for _, item := range items {
		path := strings.TrimSpace(item.Path)
		if path == "" {
			continue
		}
		line := "- " + path
		separatorBytes := 0
		if len(lines) > 0 {
			separatorBytes = 1
		}
		if len(lines) == maxV3ScenarioNavigationEntries ||
			bytesUsed+separatorBytes+len(line) > maxV3ScenarioNavigationBytes {
			truncated = true
			break
		}
		lines = append(lines, line)
		bytesUsed += separatorBytes + len(line)
	}
	for truncated && len(lines) > 0 &&
		bytesUsed+len(v3TruncationMarker) > maxV3ScenarioNavigationBytes {
		last := len(lines) - 1
		bytesUsed -= len(lines[last])
		if last > 0 {
			bytesUsed--
		}
		lines = lines[:last]
	}
	context := strings.Join(lines, "\n")
	if truncated {
		if context == "" {
			context = strings.TrimPrefix(v3TruncationMarker, "\n")
		} else {
			context += v3TruncationMarker
		}
	}
	return context, len(lines)
}

func truncateV3ScenarioContent(content string) (string, bool) {
	if len(content) <= maxV3ScenarioContentBytes {
		return content, false
	}
	limit := maxV3ScenarioContentBytes - len(v3TruncationMarker)
	end := 0
	for offset := 0; offset < len(content); {
		_, size := utf8.DecodeRuneInString(content[offset:])
		next := offset + size
		if next > limit {
			break
		}
		end = next
		offset = next
	}
	return content[:end] + v3TruncationMarker, true
}

func (s *Service) newReadOffloadRefTool(name string) tool.CallableTool {
	fn := func(ctx context.Context, req *readOffloadRefToolRequest) (*readOffloadRefToolResponse, error) {
		if req == nil || strings.TrimSpace(req.ResultRef) == "" {
			return nil, fmt.Errorf("%s: result_ref is required", name)
		}
		query := strings.TrimSpace(req.Query)
		if query != "" && (req.StartLine != 0 || req.EndLine != 0) {
			return nil, fmt.Errorf("%s: query cannot be combined with line ranges", name)
		}
		if req.StartLine < 0 || req.EndLine < 0 ||
			req.MaxTokens < 0 || req.MaxTokens > 4096 {
			return nil, fmt.Errorf(
				"%s: start_line, end_line, and max_tokens must be within the supported ranges",
				name,
			)
		}
		if req.StartLine > 0 && req.EndLine > 0 &&
			req.StartLine > req.EndLine {
			return nil, fmt.Errorf("%s: start_line must not exceed end_line", name)
		}
		inv, err := currentInvocation(ctx)
		if err != nil {
			return nil, err
		}
		client := s.contextOffloadClient()
		if client == nil {
			return nil, fmt.Errorf("%s: context offload gateway is unavailable", name)
		}
		rsp, err := client.readRef(ctx, offloadReadRefRequest{
			SessionID: s.sessionKey(inv.Session),
			ResultRef: strings.TrimSpace(req.ResultRef),
			Query:     query,
			StartLine: optionalPositiveInt(req.StartLine),
			EndLine:   optionalPositiveInt(req.EndLine),
			MaxTokens: optionalPositiveInt(req.MaxTokens),
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
			ResultRef:  rsp.ResultRef,
			Content:    rsp.Content,
			Truncated:  rsp.Truncated,
			MatchFound: rsp.MatchFound,
		}, nil
	}
	return function.NewFunctionTool(
		fn,
		function.WithName(name),
		function.WithDescription("Read a tool result externalized by TencentDB context offload. "+
			"Use this when the prompt contains a result_ref and exact details are needed."),
	)
}

func optionalPositiveInt(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
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
