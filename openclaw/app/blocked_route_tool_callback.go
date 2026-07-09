//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	blockedRouteStateKey = "openclaw:web_fetch_blocked_routes"
	webFetchToolName     = "web_fetch"

	blockedRouteSkippedSummary = "Skipped blocked web routes."
)

type blockedRouteMemory struct {
	mu     sync.RWMutex
	routes map[string]string
}

type blockedRouteFetchRequest struct {
	URLS []string `json:"urls"`
}

type blockedRouteFetchResponse struct {
	Results []blockedRouteResultItem `json:"results"`
	Summary string                   `json:"summary"`
}

type blockedRouteResultItem struct {
	RetrievedURL string `json:"retrieved_url"`
	StatusCode   int    `json:"status_code,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	Content      string `json:"content,omitempty"`
	Error        string `json:"error,omitempty"`
}

type blockedRouteRecord struct {
	host   string
	reason string
}

func registerBlockedRouteToolCallback(callbacks *tool.Callbacks) {
	if callbacks == nil {
		return
	}
	callbacks.RegisterBeforeTool(blockedRouteBeforeToolCallback)
	callbacks.RegisterAfterTool(blockedRouteAfterToolCallback)
}

func blockedRouteBeforeToolCallback(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil || args.ToolName != webFetchToolName {
		return nil, nil
	}
	req, ok := parseBlockedRouteFetchRequest(args.Arguments)
	if !ok || len(req.URLS) == 0 {
		return nil, nil
	}
	memory, ok := blockedRouteMemoryFromContext(ctx)
	if !ok {
		return nil, nil
	}
	skipped := blockedRouteSkippedResults(memory, req.URLS)
	if len(skipped) != len(req.URLS) {
		return nil, nil
	}
	return &tool.BeforeToolResult{
		CustomResult: blockedRouteFetchResponse{
			Results: skipped,
			Summary: blockedRouteSkippedSummary,
		},
	}, nil
}

func blockedRouteAfterToolCallback(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if args == nil || args.ToolName != webFetchToolName {
		return nil, nil
	}
	response, ok := parseBlockedRouteFetchResponse(args.Result)
	if !ok || len(response.Results) == 0 {
		return nil, nil
	}
	records := make([]blockedRouteRecord, 0, len(response.Results))
	for _, item := range response.Results {
		host, ok := blockedRouteHostKey(item.RetrievedURL)
		if !ok {
			continue
		}
		reason, blocked := blockedRouteReason(item)
		if !blocked {
			continue
		}
		records = append(records, blockedRouteRecord{
			host:   host,
			reason: reason,
		})
	}
	if len(records) == 0 {
		return nil, nil
	}
	memory, ok := ensureBlockedRouteMemory(ctx)
	if !ok {
		return nil, nil
	}
	for _, record := range records {
		memory.record(record.host, record.reason)
	}
	return nil, nil
}

func blockedRouteSkippedResults(
	memory *blockedRouteMemory,
	rawURLs []string,
) []blockedRouteResultItem {
	out := make([]blockedRouteResultItem, 0, len(rawURLs))
	for _, rawURL := range rawURLs {
		host, ok := blockedRouteHostKey(rawURL)
		if !ok {
			continue
		}
		reason, blocked := memory.reason(host)
		if !blocked {
			continue
		}
		out = append(out, blockedRouteResultItem{
			RetrievedURL: rawURL,
			Error: fmt.Sprintf(
				"web_fetch skipped %s because %s was already "+
					"blocked earlier in this run; use another "+
					"source or existing evidence instead",
				rawURL,
				reason,
			),
		})
	}
	return out
}

func parseBlockedRouteFetchRequest(
	data []byte,
) (blockedRouteFetchRequest, bool) {
	var req blockedRouteFetchRequest
	if len(data) == 0 {
		return req, false
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, false
	}
	return req, true
}

func parseBlockedRouteFetchResponse(v any) (blockedRouteFetchResponse, bool) {
	var response blockedRouteFetchResponse
	if v == nil {
		return response, false
	}
	data, err := json.Marshal(v)
	if err != nil {
		return response, false
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return response, false
	}
	return response, true
}

func ensureBlockedRouteMemory(
	ctx context.Context,
) (*blockedRouteMemory, bool) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, false
	}
	if memory, ok := agent.GetStateValue[*blockedRouteMemory](
		inv,
		blockedRouteStateKey,
	); ok && memory != nil {
		return memory, true
	}
	memory := &blockedRouteMemory{routes: map[string]string{}}
	inv.SetState(blockedRouteStateKey, memory)
	return memory, true
}

func blockedRouteMemoryFromContext(
	ctx context.Context,
) (*blockedRouteMemory, bool) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, false
	}
	memory, ok := agent.GetStateValue[*blockedRouteMemory](
		inv,
		blockedRouteStateKey,
	)
	return memory, ok && memory != nil
}

func (m *blockedRouteMemory) record(host string, reason string) {
	if m == nil || host == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.routes == nil {
		m.routes = map[string]string{}
	}
	m.routes[host] = reason
}

func (m *blockedRouteMemory) reason(host string) (string, bool) {
	if m == nil || host == "" {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	reason, ok := m.routes[host]
	return reason, ok
}

func blockedRouteHostKey(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host == "" {
		return "", false
	}
	return host, true
}

func blockedRouteReason(item blockedRouteResultItem) (string, bool) {
	evidence := strings.ToLower(item.Error + "\n" + item.Content)
	if item.StatusCode == 429 ||
		strings.Contains(evidence, "too many requests") ||
		strings.Contains(evidence, "rate limit") ||
		strings.Contains(evidence, "rate-limit") ||
		strings.Contains(evidence, "http status 429") {
		return "the host returned a rate-limit response", true
	}
	if blockedRouteHasAntiBotEvidence(evidence) {
		return "the host returned an anti-bot challenge", true
	}
	return "", false
}

func blockedRouteHasAntiBotEvidence(evidence string) bool {
	if strings.Contains(evidence, "web_fetch page appears blocked") {
		return true
	}
	phrases := []string{
		"cloudflare",
		"just a moment",
		"unusual traffic",
		"verify you are human",
		"checking if the site connection is secure",
		"enable javascript and cookies to continue",
		"anti-automation",
		"anti automation",
		"anti-bot",
		"bot check",
		"automated queries",
	}
	for _, phrase := range phrases {
		if strings.Contains(evidence, phrase) {
			return true
		}
	}
	if strings.Contains(evidence, "captcha") &&
		(strings.Contains(evidence, "verify") ||
			strings.Contains(evidence, "human") ||
			strings.Contains(evidence, "robot") ||
			strings.Contains(evidence, "challenge")) {
		return true
	}
	return false
}
