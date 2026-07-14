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
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Normalizer converts Go structs into normalized Snapshot objects.
type Normalizer struct {
	config NormalizerConfig
}

// NewNormalizer creates a Normalizer with the given config.
func NewNormalizer(config NormalizerConfig) *Normalizer {
	if config.ScorePrecision <= 0 {
		config.ScorePrecision = 6
	}
	if config.VolatilePayloadKeys == nil {
		config.VolatilePayloadKeys = DefaultNormalizerConfig().VolatilePayloadKeys
	}
	return &Normalizer{config: config}
}

// Normalize builds a stable Snapshot from one backend read.
// A nil session is acceptable when only memory capabilities are being tested.
func (n *Normalizer) Normalize(
	sess *session.Session,
	memories []*memory.Entry,
	caps Capabilities,
	opts CaptureOptions,
) (Snapshot, error) {
	aliases := NewIDAliasMap()

	var events []map[string]any
	var state, appState, userState map[string]any
	var sumSnap map[string]SummarySnapshot
	var trackSnap map[string][]TrackSnapshot

	if sess != nil {
		sess = sess.Clone()

		if opts.OrderEventsByTimestamp {
			sort.SliceStable(sess.Events, func(i, j int) bool {
				return sess.Events[i].Timestamp.Before(sess.Events[j].Timestamp)
			})
		}

		if caps.Has(CapEvents) {
			var err error
			events, err = n.normalizeEvents(sess.Events, aliases)
			if err != nil {
				return Snapshot{}, err
			}
		}

		if caps.Has(CapState) {
			state = normalizeState(sess.State, caps)
		}

		if caps.Has(CapSummary) {
			sumSnap = normalizeSummaries(sess, aliases)
		}

		if caps.Has(CapTrack) {
			trackSnap = n.normalizeTracks(sess.Tracks, aliases)
		}
	}

	if opts.AppState != nil {
		appState = normalizeState(opts.AppState, caps)
	}
	if opts.UserState != nil {
		userState = normalizeState(opts.UserState, caps)
	}

	var memSnap []MemorySnapshot
	if caps.Has(CapMemory) {
		var err error
		unordered := opts.UnorderedMemories || n.config.MemoryUnordered
		memSnap, err = normalizeMemories(memories, aliases, unordered, n.config.ScorePrecision)
		if err != nil {
			return Snapshot{}, err
		}
	}

	unsupported := caps.UnsupportedList()

	return Snapshot{
		Events:      events,
		State:       state,
		AppState:    appState,
		UserState:   userState,
		Memories:    memSnap,
		Summaries:   sumSnap,
		Tracks:      trackSnap,
		Unsupported: unsupported,
	}, nil
}

func (n *Normalizer) normalizeEvents(
	events []event.Event,
	aliases *IDAliasMap,
) ([]map[string]any, error) {
	volatileSet := make(map[string]struct{}, len(n.config.VolatilePayloadKeys))
	for _, key := range n.config.VolatilePayloadKeys {
		volatileSet[key] = struct{}{}
	}

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

		// Replace event ID with stable alias.
		value["id"] = aliases.Alias(events[i].ID, "event")
		// Remove non-deterministic fields.
		delete(value, "timestamp")
		delete(value, "requestID")
		delete(value, "created")

		// Alias invocation IDs.
		aliasMapValue(value, "invocationId", aliases, "invocation")
		aliasMapValue(value, "parentInvocationId", aliases, "invocation")

		// Alias ParentMetadata.TriggerID.
		if pm, ok := value["parentMetadata"].(map[string]any); ok {
			if triggerID, ok := pm["triggerId"].(string); ok && triggerID != "" {
				pm["triggerId"] = aliases.Alias(triggerID, "tool-call")
			}
		}

		// Normalize tool call data inside choices.
		normalizeEventToolData(value, aliases)

		// Normalize StateDelta: nil → MissingValue, []byte → decoded any.
		if events[i].StateDelta != nil {
			value["stateDelta"] = normalizeStateDelta(events[i].StateDelta)
		}

		// Normalize Extensions.
		if events[i].Extensions != nil {
			extensions := make(map[string]any, len(events[i].Extensions))
			for key, raw := range events[i].Extensions {
				decoded := decodeBytesWithOmit(raw, volatileSet)
				if key == event.ToolCallArgsExtensionKey {
					decoded = aliasMapKeys(decoded, aliases, "tool-call")
				}
				extensions[key] = decoded
			}
			value["extensions"] = extensions
		}

		// Alias LongRunningToolIDs.
		if lrti, ok := value["longRunningToolIDs"].(map[string]any); ok {
			aliased := aliasMapKeys(lrti, aliases, "tool-call")
			value["longRunningToolIDs"] = aliased
		}

		// Recursively alias known identifiers in nested structures.
		normalizeKnownIdentifiers(value, aliases)

		result = append(result, normalizeJSONMap(value, volatileSet))
	}
	return result, nil
}

func normalizeState(state session.StateMap, caps Capabilities) map[string]any {
	if state == nil {
		return nil
	}
	result := make(map[string]any, len(state))
	for key, raw := range state {
		if key == "tracks" {
			continue
		}
		if raw == nil {
			if caps.Has(CapEventStateDeltaNull) {
				result[key] = nil
			} else {
				result[key] = MissingValue{}
			}
			continue
		}
		result[key] = decodeBytes(raw)
	}
	return result
}

func normalizeStateDelta(delta map[string][]byte) map[string]any {
	result := make(map[string]any, len(delta))
	for key, raw := range delta {
		if raw == nil {
			result[key] = MissingValue{}
		} else {
			result[key] = decodeBytes(raw)
		}
	}
	return result
}

func normalizeMemories(
	entries []*memory.Entry,
	aliases *IDAliasMap,
	unordered bool,
	scorePrecision int,
) ([]MemorySnapshot, error) {
	result := make([]MemorySnapshot, 0, len(entries))
	for rank, entry := range entries {
		if entry == nil {
			return nil, fmt.Errorf("memory %d is nil", rank)
		}
		if entry.Memory == nil {
			return nil, fmt.Errorf("memory %d content is nil", rank)
		}
		item := MemorySnapshot{
			Rank:         rank,
			Content:      entry.Memory.Memory,
			Topics:       sortedCopy(entry.Memory.Topics),
			Kind:         string(entry.Memory.Kind),
			Participants: sortedCopy(entry.Memory.Participants),
			Location:     entry.Memory.Location,
			AppName:      entry.AppName,
			UserID:       entry.UserID,
		}
		if entry.Memory.EventTime != nil {
			item.EventTime = entry.Memory.EventTime.UTC().Format(time.RFC3339Nano)
		}
		score := normalizeMemoryScore(entry.Score, scorePrecision)
		item.Score = &score
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
		result[i].ID = aliases.Alias(fmt.Sprintf("memory-%d", i), "memory")
	}
	return result, nil
}

func normalizeMemoryScore(score float64, precision int) float64 {
	factor := math.Pow(10, float64(precision))
	rounded := math.Round(score*factor) / factor
	if rounded == 0 {
		return 0
	}
	return rounded
}

func normalizeSummaries(sess *session.Session, aliases *IDAliasMap) map[string]SummarySnapshot {
	result := make(map[string]SummarySnapshot, len(sess.Summaries))
	eventIDs := make(map[string]int, len(sess.Events))
	for i, e := range sess.Events {
		if e.ID != "" {
			eventIDs[e.ID] = i
		}
	}
	for key, summary := range sess.Summaries {
		if summary == nil {
			continue
		}
		item := SummarySnapshot{
			SessionID: sess.ID,
			FilterKey: key,
			Text:      summary.Summary,
			Topics:    sortedCopy(summary.Topics),
		}
		if boundary := summary.Boundary; boundary != nil {
			item.BoundaryPresent = true
			item.Version = boundary.Version
			item.BoundaryFilterKey = boundary.FilterKey
			if !boundary.CutoffAt.IsZero() {
				idx := lastEventAtOrBefore(sess.Events, boundary.CutoffAt)
				item.CutoffAtEventIndex = intPointer(idx)
			}
			if boundary.LastEventID != "" {
				item.LastEventIDPresent = true
				idx, ok := eventIDs[boundary.LastEventID]
				if !ok {
					idx = -1
				}
				item.LastEventIndex = intPointer(idx)
			}
		}
		if !summary.UpdatedAt.IsZero() {
			idx := lastEventAtOrBefore(sess.Events, summary.UpdatedAt)
			item.UpdatedAtEventIndex = intPointer(idx)
		}
		result[key] = item
	}
	return result
}

func (n *Normalizer) normalizeTracks(
	tracks map[session.Track]*session.TrackEvents,
	aliases *IDAliasMap,
) map[string][]TrackSnapshot {
	result := make(map[string][]TrackSnapshot, len(tracks))
	names := make([]session.Track, 0, len(tracks))
	for name := range tracks {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return string(names[i]) < string(names[j])
	})

	volatileSet := make(map[string]struct{}, len(n.config.VolatilePayloadKeys))
	for _, key := range n.config.VolatilePayloadKeys {
		volatileSet[key] = struct{}{}
	}

	for _, name := range names {
		history := tracks[name]
		if history == nil {
			result[string(name)] = nil
			continue
		}
		// Sort track events by timestamp for deterministic ordering
		// across backends that may return events in different orders (ASC vs DESC).
		// Using timestamp instead of content-based sorting prevents cascading
		// misalignment when events have genuine content differences between backends:
		// a content-based sort would place differing events at different indices,
		// causing walkDiff's index comparison to report multiple false-positive diffs.
		sort.SliceStable(history.Events, func(i, j int) bool {
			return history.Events[i].Timestamp.Before(history.Events[j].Timestamp)
		})
		events := make([]TrackSnapshot, 0, len(history.Events))
		for _, trackEvent := range history.Events {
			var payload any
			if err := decodeJSON(trackEvent.Payload, &payload); err != nil {
				payload = string(trackEvent.Payload)
			}
			payload = normalizeJSON(payload, volatileSet)
			normalizeKnownIdentifiers(payload, aliases)
			events = append(events, TrackSnapshot{
				Track:   string(trackEvent.Track),
				Payload: payload,
			})
		}
		result[string(name)] = events
	}
	return result
}

// --- Helper functions ---

func aliasMapValue(value map[string]any, key string, aliases *IDAliasMap, category string) {
	raw, exists := value[key]
	if !exists {
		return
	}
	original, ok := raw.(string)
	if !ok {
		return
	}
	if original == "" {
		delete(value, key)
		return
	}
	value[key] = aliases.Alias(original, category)
}

func aliasMapKeys(value any, aliases *IDAliasMap, category string) any {
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
		result[aliases.Alias(key, category)] = item
	}
	return result
}

func normalizeEventToolData(value map[string]any, aliases *IDAliasMap) {
	choices, _ := value["choices"].([]any)
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		for _, messageKey := range []string{"message", "delta"} {
			message, _ := choice[messageKey].(map[string]any)
			if message == nil {
				continue
			}
			aliasMapValue(message, "tool_id", aliases, "tool-call")
			if role, _ := message["role"].(string); role == "tool" {
				if content, ok := message["content"].(string); ok {
					message["content"] = decodeBytes([]byte(content))
				}
			}
			calls, _ := message["tool_calls"].([]any)
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				aliasMapValue(call, "id", aliases, "tool-call")
				function, _ := call["function"].(map[string]any)
				if function != nil {
					if arguments, ok := function["arguments"].(string); ok {
						function["arguments"] = decodeBytes([]byte(arguments))
					}
				}
			}
		}
	}
}

func normalizeKnownIdentifiers(value any, aliases *IDAliasMap) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"invocation", "invocation_id", "invocationId", "parentInvocationId"} {
			if _, exists := typed[key]; exists {
				aliasMapValue(typed, key, aliases, "invocation")
			}
		}
		for _, key := range []string{"tool_id", "tool_call_id", "toolCallId", "triggerId"} {
			if _, exists := typed[key]; exists {
				aliasMapValue(typed, key, aliases, "tool-call")
			}
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			normalizeKnownIdentifiers(typed[key], aliases)
			if key == "longRunningToolIDs" {
				typed[key] = aliasMapKeys(typed[key], aliases, "tool-call")
			}
		}
	case []any:
		for _, item := range typed {
			normalizeKnownIdentifiers(item, aliases)
		}
	}
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

func decodeBytes(raw []byte) any {
	if raw == nil {
		return nil
	}
	var value any
	if decodeJSON(raw, &value) == nil {
		return normalizeJSON(value, nil)
	}
	return string(raw)
}

// decodeBytesWithOmit is like decodeBytes but passes the omit set to normalizeJSON
// so that volatile payload keys are stripped from the decoded value.
func decodeBytesWithOmit(raw []byte, omit map[string]struct{}) any {
	if raw == nil {
		return nil
	}
	var value any
	if decodeJSON(raw, &value) == nil {
		return normalizeJSON(value, omit)
	}
	return string(raw)
}

func decodeJSON(raw []byte, target any) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty JSON input")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
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

func intPointer(value int) *int { return &value }

func sortedCopy(values []string) []string {
	if values == nil {
		return nil
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

// normalizeError normalizes error strings for comparison across backends.
func normalizeError(errStr string) string {
	if errStr == "" {
		return ""
	}
	errStr = strings.TrimSpace(errStr)
	errStr = strings.TrimRight(errStr, ".")
	lower := strings.ToLower(errStr)
	if strings.Contains(lower, "not found") || strings.Contains(lower, "no such") || strings.Contains(lower, "does not exist") {
		return "not-found"
	}
	return errStr
}
