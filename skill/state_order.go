//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"encoding/json"
	"strings"
)

// LoadedOrderKey returns the session state key that stores the recent
// skill-touch order for a specific agent.
//
// The JSON payload is an array of unique skill names ordered from the
// oldest touch to the newest touch.
func LoadedOrderKey(agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return StateKeyLoadedOrderPrefix
	}
	agentName = escapeScopeSegment(agentName)
	return StateKeyLoadedOrderByAgentPrefix + agentName
}

// ParseLoadedOrder parses a stored skill-touch order.
func ParseLoadedOrder(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil
	}
	return normalizeLoadedOrder(names)
}

// MarshalLoadedOrder serializes a normalized skill-touch order.
func MarshalLoadedOrder(names []string) []byte {
	names = normalizeLoadedOrder(names)
	if len(names) == 0 {
		return nil
	}

	b, err := json.Marshal(names)
	if err != nil {
		return nil
	}
	return b
}

// TouchLoadedOrder moves the touched skills to the end of the order.
func TouchLoadedOrder(names []string, touched ...string) []string {
	order := normalizeLoadedOrder(names)
	for _, name := range touched {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		order = removeLoadedOrderName(order, name)
		order = append(order, name)
	}
	return order
}

func normalizeLoadedOrder(names []string) []string {
	if len(names) == 0 {
		return nil
	}

	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, name)
		seen[name] = struct{}{}
	}
	return out
}

func removeLoadedOrderName(order []string, target string) []string {
	for i, name := range order {
		if name != target {
			continue
		}
		return append(order[:i], order[i+1:]...)
	}
	return order
}
