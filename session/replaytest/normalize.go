//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Normalize builds a stable snapshot from one backend read.
func (n Normalizer) Normalize(sess *session.Session, memories []*memory.Entry, capabilities map[string]Capability) (Snapshot, error) {
	return n.normalize(sess, memories, capabilities, false, false)
}

func (n Normalizer) normalize(
	sess *session.Session,
	memories []*memory.Entry,
	capabilities map[string]Capability,
	orderEventsByTimestamp bool,
	unorderedMemories bool,
) (Snapshot, error) {
	if sess == nil {
		return Snapshot{}, fmt.Errorf("session is nil")
	}
	sess = sess.Clone()
	if orderEventsByTimestamp {
		sort.SliceStable(sess.Events, func(i, j int) bool {
			return sess.Events[i].Timestamp.Before(sess.Events[j].Timestamp)
		})
	}
	var events []map[string]any
	eventIDs := make(map[string]int)
	for i := range sess.Events {
		if sess.Events[i].ID != "" {
			eventIDs[sess.Events[i].ID] = i
		}
	}
	invocations := make(map[string]string)
	toolCalls := make(map[string]string)
	var err error
	if capabilitySupported(capabilities, CapabilityEvents) {
		events, err = n.normalizeEvents(sess.Events, invocations, toolCalls)
	}
	if err != nil {
		return Snapshot{}, err
	}
	var normalizedMemories []MemorySnapshot
	if capabilitySupported(capabilities, CapabilityMemory) {
		normalizedMemories, err = normalizeMemories(memories, unorderedMemories)
		if err != nil {
			return Snapshot{}, err
		}
	}
	snapshot := Snapshot{
		SessionID:   sess.ID,
		AppName:     sess.AppName,
		UserID:      sess.UserID,
		Events:      events,
		Memories:    normalizedMemories,
		Unsupported: unsupportedCapabilities(capabilities),
	}
	if capabilitySupported(capabilities, CapabilityState) {
		snapshot.State = normalizeState(sess.State)
	}
	if capabilitySupported(capabilities, CapabilitySummary) {
		snapshot.Summaries = normalizeSummaries(sess, eventIDs)
	}
	if capabilitySupported(capabilities, CapabilityTracks) {
		snapshot.Tracks = n.normalizeTracks(sess.Tracks, invocations, toolCalls)
	}
	return snapshot, nil
}

func (n Normalizer) normalizeEvents(
	events []event.Event,
	invocations map[string]string,
	toolCalls map[string]string,
) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(events))
	for i := range events {
		raw, err := json.Marshal(events[i])
		if err != nil {
			return nil, fmt.Errorf("marshal event %d: %w", i, err)
		}
		var value map[string]any
		if err := decodeJSON(raw, &value); err != nil {
			return nil, fmt.Errorf("decode event %d: %w", i, err)
		}
		value["id"] = fmt.Sprintf("event-%03d", i)
		delete(value, "timestamp")
		delete(value, "requestID")
		delete(value, "created")
		delete(value, "response")
		aliasMapValue(value, "invocationId", invocations, "invocation")
		aliasMapValue(value, "parentInvocationId", invocations, "invocation")
		normalizeEventToolData(value, toolCalls)
		if events[i].StateDelta != nil {
			value["stateDelta"] = normalizeState(events[i].StateDelta)
		}
		if events[i].Extensions != nil {
			extensions := make(map[string]any, len(events[i].Extensions))
			for key, raw := range events[i].Extensions {
				normalized := normalizeBytes(raw)
				if key == event.ToolCallArgsExtensionKey {
					normalized = aliasMapKeys(normalized, toolCalls, "tool-call")
				}
				extensions[key] = normalized
			}
			value["extensions"] = extensions
		}
		normalizeKnownIdentifiers(value, invocations, toolCalls)
		result = append(result, normalizeJSONMap(value, nil))
	}
	return result, nil
}

func aliasMapValue(value map[string]any, key string, aliases map[string]string, prefix string) {
	original, _ := value[key].(string)
	if original == "" {
		delete(value, key)
		return
	}
	value[key] = stableAlias(original, aliases, prefix)
}

func normalizeState(state session.StateMap) map[string]any {
	result := make(map[string]any, len(state))
	for key, raw := range state {
		if key == "tracks" {
			continue
		}
		result[key] = normalizeBytes(raw)
	}
	return result
}

func normalizeBytes(raw []byte) any {
	if raw == nil {
		return nil
	}
	var value any
	if decodeJSON(raw, &value) == nil {
		return normalizeJSON(value, nil)
	}
	return string(raw)
}

func normalizeMemories(entries []*memory.Entry, unordered bool) ([]MemorySnapshot, error) {
	result := make([]MemorySnapshot, 0, len(entries))
	for rank, entry := range entries {
		if entry == nil {
			return nil, fmt.Errorf("memory %d is nil", rank)
		}
		if entry.Memory == nil {
			return nil, fmt.Errorf("memory %d content is nil", rank)
		}
		item := MemorySnapshot{
			AppName:      entry.AppName,
			UserID:       entry.UserID,
			Rank:         rank,
			Score:        normalizeMemoryScore(entry.Score),
			Content:      entry.Memory.Memory,
			Topics:       sortedCopy(entry.Memory.Topics),
			Kind:         string(entry.Memory.Kind),
			Participants: sortedCopy(entry.Memory.Participants),
			Location:     entry.Memory.Location,
		}
		if entry.Memory.EventTime != nil {
			item.EventTime = entry.Memory.EventTime.UTC().Format(time.RFC3339Nano)
		}
		result = append(result, item)
	}
	if unordered {
		for i := range result {
			result[i].Rank = -1
		}
		sort.Slice(result, func(i, j int) bool {
			left, _ := json.Marshal(result[i])
			right, _ := json.Marshal(result[j])
			return string(left) < string(right)
		})
	}
	for i := range result {
		result[i].ID = fmt.Sprintf("memory-%03d", i)
	}
	return result, nil
}

func normalizeMemoryScore(score float64) float64 {
	const scale = 1_000_000
	rounded := math.Round(score*scale) / scale
	if rounded == 0 {
		return 0
	}
	return rounded
}

func normalizeSummaries(sess *session.Session, eventIDs map[string]int) map[string]SummarySnapshot {
	result := make(map[string]SummarySnapshot, len(sess.Summaries))
	for key, summary := range sess.Summaries {
		if summary == nil {
			continue
		}
		item := SummarySnapshot{
			SessionID: sess.ID, AppName: sess.AppName, UserID: sess.UserID,
			FilterKey: key, Text: summary.Summary,
			Topics: sortedCopy(summary.Topics),
		}
		if !summary.UpdatedAt.IsZero() {
			item.UpdatedAtEventIndex = intPointer(lastEventAtOrBefore(sess.Events, summary.UpdatedAt))
		}
		if boundary := summary.Boundary; boundary != nil {
			item.BoundaryPresent = true
			item.Version = boundary.Version
			item.BoundaryFilterKey = boundary.FilterKey
			if !boundary.CutoffAt.IsZero() {
				item.CutoffAtEventIndex = intPointer(lastEventAtOrBefore(sess.Events, boundary.CutoffAt))
			}
			if boundary.LastEventID != "" {
				item.LastEventIDPresent = true
				index, ok := eventIDs[boundary.LastEventID]
				if !ok {
					index = -1
				}
				item.LastEventIndex = intPointer(index)
			}
		}
		result[key] = item
	}
	return result
}

func lastEventAtOrBefore(events []event.Event, cutoff time.Time) int {
	index := -1
	for i := range events {
		if !events[i].Timestamp.After(cutoff) {
			index = i
		}
	}
	return index
}

func (n Normalizer) normalizeTracks(
	tracks map[session.Track]*session.TrackEvents,
	invocations map[string]string,
	toolCalls map[string]string,
) map[string][]TrackSnapshot {
	result := make(map[string][]TrackSnapshot, len(tracks))
	for name, history := range tracks {
		if history == nil {
			result[string(name)] = nil
			continue
		}
		events := make([]TrackSnapshot, 0, len(history.Events))
		for _, trackEvent := range history.Events {
			var payload any
			if err := decodeJSON(trackEvent.Payload, &payload); err != nil {
				payload = string(trackEvent.Payload)
			}
			payload = normalizeJSON(payload, n.VolatilePayloadKeys)
			normalizeKnownIdentifiers(payload, invocations, toolCalls)
			events = append(events, TrackSnapshot{
				Track: string(trackEvent.Track), Payload: payload,
			})
		}
		result[string(name)] = events
	}
	return result
}

func normalizeJSON(value any, omit map[string]struct{}) any {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeJSONMap(typed, omit)
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = normalizeJSON(typed[i], omit)
		}
		return result
	case json.Number:
		if integer, err := strconv.ParseInt(string(typed), 10, 64); err == nil {
			return integer
		}
	}
	return value
}

func normalizeJSONMap(value map[string]any, omit map[string]struct{}) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		if _, skip := omit[key]; skip {
			continue
		}
		result[key] = normalizeJSON(item, omit)
	}
	return result
}

func normalizeEventToolData(value map[string]any, toolCalls map[string]string) {
	choices, _ := value["choices"].([]any)
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		for _, messageKey := range []string{"message", "delta"} {
			message, _ := choice[messageKey].(map[string]any)
			aliasMapValue(message, "tool_id", toolCalls, "tool-call")
			if role, _ := message["role"].(string); role == "tool" {
				if content, ok := message["content"].(string); ok {
					message["content"] = normalizeBytes([]byte(content))
				}
			}
			calls, _ := message["tool_calls"].([]any)
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				aliasMapValue(call, "id", toolCalls, "tool-call")
				function, _ := call["function"].(map[string]any)
				arguments, ok := function["arguments"].(string)
				if ok {
					function["arguments"] = normalizeBytes([]byte(arguments))
				}
			}
		}
	}
}

func aliasMapKeys(value any, aliases map[string]string, prefix string) any {
	typed, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result := make(map[string]any, len(typed))
	keys := make([]string, 0, len(typed))
	for key := range typed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := typed[key]
		result[stableAlias(key, aliases, prefix)] = item
	}
	return result
}

func stableAlias(original string, aliases map[string]string, prefix string) string {
	if alias, exists := aliases[original]; exists {
		return alias
	}
	for _, alias := range aliases {
		if alias == original {
			return original
		}
	}
	alias := fmt.Sprintf("%s-%03d", prefix, len(aliases))
	aliases[original] = alias
	return alias
}

func normalizeKnownIdentifiers(value any, invocations, toolCalls map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"invocation", "invocation_id", "invocationId", "parentInvocationId"} {
			if _, exists := typed[key]; exists {
				aliasMapValue(typed, key, invocations, "invocation")
			}
		}
		for _, key := range []string{"tool_id", "tool_call_id", "toolCallId", "triggerId"} {
			if _, exists := typed[key]; exists {
				aliasMapValue(typed, key, toolCalls, "tool-call")
			}
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := typed[key]
			normalizeKnownIdentifiers(item, invocations, toolCalls)
			if key != "longRunningToolIDs" {
				continue
			}
			typed[key] = aliasMapKeys(item, toolCalls, "tool-call")
		}
	case []any:
		for _, item := range typed {
			normalizeKnownIdentifiers(item, invocations, toolCalls)
		}
	}
}

func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func capabilitySupported(capabilities map[string]Capability, name string) bool {
	capability, ok := capabilities[name]
	return !ok || capability.Supported
}

func unsupportedCapabilities(capabilities map[string]Capability) map[string]string {
	result := make(map[string]string)
	for name, capability := range capabilities {
		if !capability.Supported {
			result[name] = capability.Reason
		}
	}
	return result
}

func sortedCopy(values []string) []string {
	if values == nil {
		return nil
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func intPointer(value int) *int { return &value }
