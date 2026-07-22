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

	webFetchBatchLimit         = 20
	blockedRouteSkippedSummary = "Skipped blocked web routes."
	blockedRouteMergeError     = "web_fetch returned results that did not " +
		"match the filtered URL batch; results may be incomplete, so " +
		"retry the allowed URLs separately"
)

type blockedRouteMemory struct {
	mu     sync.RWMutex
	routes map[string]string
}

type blockedRoutePendingBatch struct {
	URLs  []string                       `json:"urls"`
	Items map[int]blockedRouteResultItem `json:"items"`
}

type blockedRoutePendingContextKey struct{}

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

func blockedRouteAgentCallbacks() *agent.Callbacks {
	callbacks := agent.NewCallbacks()
	callbacks.RegisterBeforeAgent(func(
		_ context.Context,
		args *agent.BeforeAgentArgs,
	) (*agent.BeforeAgentResult, error) {
		if args != nil {
			ensureBlockedRouteMemoryForInvocation(args.Invocation)
		}
		return nil, nil
	})
	return callbacks
}

func blockedRouteBeforeToolCallback(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil || args.ToolName != webFetchToolName {
		return nil, nil
	}
	req, fields, ok := parseBlockedRouteFetchRequest(args.Arguments)
	if !ok || len(req.URLS) == 0 {
		return nil, nil
	}
	memory, ok := blockedRouteMemoryFromContext(ctx)
	if !ok {
		return nil, nil
	}
	targetURLs := canonicalBlockedRouteFetchURLs(req.URLS)
	allowed, pending := blockedRoutePartitionURLs(memory, targetURLs)
	if len(pending.Items) == 0 {
		return nil, nil
	}
	if len(allowed) > 0 {
		modified, ok := replaceBlockedRouteFetchURLs(
			fields,
			allowed,
		)
		if !ok {
			return nil, nil
		}
		return &tool.BeforeToolResult{
			Context: context.WithValue(
				ctx,
				blockedRoutePendingContextKey{},
				pending,
			),
			ModifiedArguments: modified,
		}, nil
	}
	return &tool.BeforeToolResult{
		CustomResult: blockedRouteFetchResponse{
			Results: pending.results(),
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
	pending, hasPending := blockedRoutePendingFromContext(ctx)
	response, ok := parseBlockedRouteFetchResponse(args.Result)
	if !ok {
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
	if len(records) > 0 {
		memory, hasMemory := ensureBlockedRouteMemory(ctx)
		if hasMemory {
			for _, record := range records {
				memory.record(record.host, record.reason)
			}
		}
	}
	if hasPending {
		merged, ok := mergeBlockedRouteFetchResponse(
			args.Result,
			pending,
		)
		if ok {
			return &tool.AfterToolResult{CustomResult: merged}, nil
		}
		failed, ok := blockedRouteMergeFailureResponse(
			args.Result,
			pending,
		)
		if ok {
			return &tool.AfterToolResult{CustomResult: failed}, nil
		}
	}
	return nil, nil
}

func blockedRoutePartitionURLs(
	memory *blockedRouteMemory,
	rawURLs []string,
) ([]string, blockedRoutePendingBatch) {
	allowed := make([]string, 0, len(rawURLs))
	pending := blockedRoutePendingBatch{
		URLs:  append([]string(nil), rawURLs...),
		Items: map[int]blockedRouteResultItem{},
	}
	for index, rawURL := range rawURLs {
		host, ok := blockedRouteHostKey(rawURL)
		if !ok {
			allowed = append(allowed, rawURL)
			continue
		}
		reason, blocked := memory.reason(host)
		if !blocked {
			allowed = append(allowed, rawURL)
			continue
		}
		pending.Items[index] = blockedRouteResultItem{
			RetrievedURL: rawURL,
			Error: fmt.Sprintf(
				"web_fetch skipped %s because %s was already "+
					"blocked earlier in this run; use another "+
					"source or existing evidence instead",
				rawURL,
				reason,
			),
		}
	}
	return allowed, pending
}

func (b blockedRoutePendingBatch) results() []blockedRouteResultItem {
	results := make([]blockedRouteResultItem, 0, len(b.Items))
	for index := range b.URLs {
		if item, ok := b.Items[index]; ok {
			results = append(results, item)
		}
	}
	return results
}

func canonicalBlockedRouteFetchURLs(rawURLs []string) []string {
	urls := make([]string, 0, len(rawURLs))
	seen := make(map[string]struct{}, len(rawURLs))
	for _, rawURL := range rawURLs {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		urls = append(urls, trimmed)
		if len(urls) == webFetchBatchLimit {
			break
		}
	}
	return urls
}

func replaceBlockedRouteFetchURLs(
	fields map[string]json.RawMessage,
	urls []string,
) ([]byte, bool) {
	rawURLs, err := json.Marshal(urls)
	if err != nil {
		return nil, false
	}
	fields["urls"] = rawURLs
	modified, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return modified, true
}

func parseBlockedRouteFetchRequest(
	data []byte,
) (blockedRouteFetchRequest, map[string]json.RawMessage, bool) {
	var req blockedRouteFetchRequest
	var fields map[string]json.RawMessage
	if len(data) == 0 {
		return req, nil, false
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return req, nil, false
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, nil, false
	}
	return req, fields, true
}

func blockedRoutePendingFromContext(
	ctx context.Context,
) (blockedRoutePendingBatch, bool) {
	if ctx == nil {
		return blockedRoutePendingBatch{}, false
	}
	pending, ok := ctx.Value(
		blockedRoutePendingContextKey{},
	).(blockedRoutePendingBatch)
	if !ok || !pending.valid() {
		return blockedRoutePendingBatch{}, false
	}
	return pending, true
}

func (b blockedRoutePendingBatch) valid() bool {
	if len(b.URLs) == 0 || len(b.URLs) > webFetchBatchLimit ||
		len(b.Items) == 0 {
		return false
	}
	for index, item := range b.Items {
		if index < 0 || index >= len(b.URLs) ||
			item.RetrievedURL != b.URLs[index] || item.Error == "" {
			return false
		}
	}
	return true
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

func mergeBlockedRouteFetchResponse(
	v any,
	pending blockedRoutePendingBatch,
) (any, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, false
	}
	if fields == nil {
		return nil, false
	}
	var fetched []json.RawMessage
	if raw := fields["results"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &fetched); err != nil {
			return nil, false
		}
	}
	merged, ok := mergeBlockedRouteResults(fetched, pending)
	if !ok {
		return nil, false
	}
	fields["results"], err = json.Marshal(merged)
	if err != nil {
		return nil, false
	}
	var summary string
	if raw := fields["summary"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &summary); err != nil {
			return nil, false
		}
	}
	summary = strings.TrimSpace(summary + " " + blockedRouteSkippedSummary)
	fields["summary"], err = json.Marshal(summary)
	if err != nil {
		return nil, false
	}
	return fields, true
}

func blockedRouteMergeFailureResponse(
	v any,
	pending blockedRoutePendingBatch,
) (any, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, false
	}
	var results []json.RawMessage
	if raw := fields["results"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &results); err != nil {
			return nil, false
		}
	}
	for _, item := range pending.results() {
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, false
		}
		results = append(results, raw)
	}
	warning, err := json.Marshal(blockedRouteResultItem{
		Error: blockedRouteMergeError,
	})
	if err != nil {
		return nil, false
	}
	results = append(results, warning)
	fields["results"], err = json.Marshal(results)
	if err != nil {
		return nil, false
	}
	var summary string
	if raw := fields["summary"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &summary); err != nil {
			return nil, false
		}
	}
	summary = strings.TrimSpace(summary + " " + blockedRouteMergeError)
	fields["summary"], err = json.Marshal(summary)
	if err != nil {
		return nil, false
	}
	return fields, true
}

func mergeBlockedRouteResults(
	fetched []json.RawMessage,
	pending blockedRoutePendingBatch,
) ([]json.RawMessage, bool) {
	allowedCount := len(pending.URLs) - len(pending.Items)
	if len(fetched) != allowedCount {
		return nil, false
	}
	slots := make(map[int]json.RawMessage, len(fetched))
	positions := make(map[string][]int, len(pending.URLs))
	for index, rawURL := range pending.URLs {
		if _, blocked := pending.Items[index]; blocked {
			continue
		}
		key := strings.TrimSpace(rawURL)
		positions[key] = append(positions[key], index)
	}
	for _, raw := range fetched {
		var identity struct {
			RetrievedURL string `json:"retrieved_url"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return nil, false
		}
		key := strings.TrimSpace(identity.RetrievedURL)
		candidates := positions[key]
		if len(candidates) == 0 {
			return nil, false
		}
		slots[candidates[0]] = raw
		positions[key] = candidates[1:]
	}

	merged := make([]json.RawMessage, 0, len(fetched)+len(pending.Items))
	for index := range pending.URLs {
		if item, ok := pending.Items[index]; ok {
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, false
			}
			merged = append(merged, raw)
			continue
		}
		if raw, ok := slots[index]; ok {
			merged = append(merged, raw)
			continue
		}
		return nil, false
	}
	return merged, true
}

var blockedRouteMemoryInitMu sync.Mutex

func ensureBlockedRouteMemory(
	ctx context.Context,
) (*blockedRouteMemory, bool) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, false
	}
	return ensureBlockedRouteMemoryForInvocation(inv)
}

func ensureBlockedRouteMemoryForInvocation(
	inv *agent.Invocation,
) (*blockedRouteMemory, bool) {
	if inv == nil {
		return nil, false
	}
	if memory, ok := agent.GetStateValue[*blockedRouteMemory](
		inv,
		blockedRouteStateKey,
	); ok && memory != nil {
		return memory, true
	}
	blockedRouteMemoryInitMu.Lock()
	defer blockedRouteMemoryInitMu.Unlock()
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
	evidence := strings.ToLower(item.Error)
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
