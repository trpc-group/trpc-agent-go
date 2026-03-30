//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graphagent

import (
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

type terminalMessageFilter struct {
	enabled                bool
	llmNodeAuthors         map[string]struct{}
	terminalLLMAuthors     map[string]struct{}
	agentScopePrefixes     map[string]struct{}
	terminalScopePrefixes  map[string]struct{}
	terminalResponseIDs    map[string]struct{}
	nonTerminalResponseIDs map[string]struct{}
}

func newTerminalMessageFilter(
	invocation *agent.Invocation,
	g *graph.Graph,
) *terminalMessageFilter {
	if invocation == nil ||
		!invocation.RunOptions.GraphTerminalMessagesOnly ||
		g == nil {
		return &terminalMessageFilter{}
	}

	filter := &terminalMessageFilter{
		enabled:                true,
		llmNodeAuthors:         make(map[string]struct{}),
		terminalLLMAuthors:     make(map[string]struct{}),
		agentScopePrefixes:     make(map[string]struct{}),
		terminalScopePrefixes:  make(map[string]struct{}),
		terminalResponseIDs:    make(map[string]struct{}),
		nonTerminalResponseIDs: make(map[string]struct{}),
	}
	rootFilterKey := terminalMessageRootFilterKey(invocation)
	for _, node := range g.Nodes() {
		if node == nil {
			continue
		}
		switch node.Type {
		case graph.NodeTypeLLM:
			filter.llmNodeAuthors[node.ID] = struct{}{}
			if isTerminalMessageNode(g, node) {
				filter.terminalLLMAuthors[node.ID] = struct{}{}
			}
		case graph.NodeTypeAgent:
			prefix := terminalAgentScopePrefix(rootFilterKey, node)
			if prefix == "" {
				continue
			}
			filter.agentScopePrefixes[prefix] = struct{}{}
			if isTerminalMessageNode(g, node) {
				filter.terminalScopePrefixes[prefix] = struct{}{}
			}
		}
	}
	return filter
}

func terminalMessageRootFilterKey(
	invocation *agent.Invocation,
) string {
	if invocation == nil {
		return ""
	}
	if filterKey := invocation.GetEventFilterKey(); filterKey != "" {
		return filterKey
	}
	return invocation.AgentName
}

func (f *terminalMessageFilter) Allows(evt *event.Event) bool {
	if f == nil {
		return true
	}
	f.observe(evt)
	if !f.enabled || !shouldFilterTerminalMessageEvent(evt) {
		return true
	}
	if graph.IsVisibleGraphCompletionEvent(evt) {
		return f.allowsVisibleGraphCompletion(evt)
	}

	if prefix, ok := f.matchAgentScopePrefix(evt.FilterKey); ok {
		_, ok = f.terminalScopePrefixes[prefix]
		return ok
	}

	if _, ok := f.llmNodeAuthors[evt.Author]; !ok {
		return true
	}
	_, ok := f.terminalLLMAuthors[evt.Author]
	return ok
}

func (f *terminalMessageFilter) observe(evt *event.Event) {
	if !f.enabled || evt == nil || evt.Response == nil {
		return
	}
	responseID := evt.Response.ID
	if responseID == "" {
		return
	}
	if prefix, ok := f.matchAgentScopePrefix(evt.FilterKey); ok {
		_, terminal := f.terminalScopePrefixes[prefix]
		f.recordResponseID(responseID, terminal)
		return
	}
	if _, ok := f.llmNodeAuthors[evt.Author]; !ok {
		return
	}
	_, terminal := f.terminalLLMAuthors[evt.Author]
	f.recordResponseID(responseID, terminal)
}

func (f *terminalMessageFilter) recordResponseID(
	responseID string,
	terminal bool,
) {
	if responseID == "" {
		return
	}
	if f.terminalResponseIDs == nil {
		f.terminalResponseIDs = make(map[string]struct{})
	}
	if f.nonTerminalResponseIDs == nil {
		f.nonTerminalResponseIDs = make(map[string]struct{})
	}
	if terminal {
		f.terminalResponseIDs[responseID] = struct{}{}
		delete(f.nonTerminalResponseIDs, responseID)
		return
	}
	if _, ok := f.terminalResponseIDs[responseID]; ok {
		return
	}
	f.nonTerminalResponseIDs[responseID] = struct{}{}
}

func (f *terminalMessageFilter) allowsVisibleGraphCompletion(
	evt *event.Event,
) bool {
	responseID := completionResponseIDFromStateDelta(evt)
	if responseID == "" {
		return true
	}
	if _, ok := f.nonTerminalResponseIDs[responseID]; ok {
		return false
	}
	return true
}

func (f *terminalMessageFilter) matchAgentScopePrefix(
	filterKey string,
) (string, bool) {
	if filterKey == "" {
		return "", false
	}
	var (
		matchedPrefix string
		matchCount    int
	)
	for prefix := range f.agentScopePrefixes {
		if !matchesFilterPrefix(filterKey, prefix) {
			continue
		}
		matchCount++
		if len(prefix) > len(matchedPrefix) {
			matchedPrefix = prefix
		}
	}
	// Overlapping scopes such as "a" and "a/b" are ambiguous for an event
	// keyed as "a/b/...". Filter only when the scope owner is unique.
	if matchCount != 1 {
		return "", false
	}
	return matchedPrefix, true
}

func terminalAgentScopePrefix(rootFilterKey string, node *graph.Node) string {
	if node == nil {
		return ""
	}
	scope := node.AgentEventScope()
	if scope == "" {
		scope = node.ID
	}
	if scope == "" {
		return ""
	}
	if rootFilterKey == "" {
		return scope
	}
	return rootFilterKey + agent.EventFilterKeyDelimiter + scope
}

func isTerminalMessageNode(g *graph.Graph, node *graph.Node) bool {
	if g == nil || node == nil {
		return false
	}
	for _, edge := range g.Edges(node.ID) {
		if edge == nil ||
			edge.From == graph.Start ||
			edge.To == "" ||
			edge.To == graph.End {
			continue
		}
		return false
	}
	for _, target := range node.EndTargets() {
		if target == "" || target == graph.End {
			continue
		}
		return false
	}
	if conditionalEdge, ok := g.ConditionalEdge(node.ID); ok &&
		conditionalEdge != nil {
		// Without an explicit PathMap, runtime may still fall back to a
		// concrete node ID, so we cannot prove the node is terminal.
		if len(conditionalEdge.PathMap) == 0 {
			return false
		}
		for _, target := range conditionalEdge.PathMap {
			if target == "" || target == graph.End {
				continue
			}
			return false
		}
	}
	return true
}

func shouldFilterTerminalMessageEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	if graph.IsVisibleGraphCompletionEvent(evt) {
		return true
	}
	return evt.IsPartial || len(evt.Response.Choices) > 0
}

func completionResponseIDFromStateDelta(evt *event.Event) string {
	if evt == nil || evt.StateDelta == nil {
		return ""
	}
	raw, ok := evt.StateDelta[graph.StateKeyLastResponseID]
	if !ok || len(raw) == 0 {
		return ""
	}
	var responseID string
	if err := json.Unmarshal(raw, &responseID); err != nil {
		return ""
	}
	return responseID
}

func matchesFilterPrefix(filterKey string, prefix string) bool {
	if filterKey == prefix {
		return true
	}
	if filterKey == "" || prefix == "" {
		return false
	}
	prefixWithDelimiter := prefix + agent.EventFilterKeyDelimiter
	return strings.HasPrefix(filterKey, prefixWithDelimiter)
}
