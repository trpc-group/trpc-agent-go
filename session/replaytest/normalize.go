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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var defaultVolatilePayloadKeys = map[string]struct{}{
	"duration": {}, "duration_ms": {}, "elapsed": {}, "elapsed_ms": {},
	"latency": {}, "latency_ms": {},
}

// Normalizer converts public backend values into stable semantic snapshots.
type Normalizer struct {
	options NormalizeOptions
}

// NewNormalizer creates a normalizer with safe defaults.
func NewNormalizer(options NormalizeOptions) Normalizer {
	if options.MemoryOrder == "" {
		options.MemoryOrder = MemoryOrderExact
	}
	if options.ScorePrecision <= 0 {
		options.ScorePrecision = 6
	}
	if options.VolatilePayloadKeys == nil {
		options.VolatilePayloadKeys = cloneStringSet(defaultVolatilePayloadKeys)
	}
	return Normalizer{options: options}
}

// Normalize captures semantic fields while removing generated identifiers,
// timestamps, map order, and explicitly configured volatile payload metrics.
func (n Normalizer) Normalize(input CaptureInput, ledger *IdentityLedger) (Snapshot, error) {
	if input.Session == nil {
		return Snapshot{}, fmt.Errorf("session is nil")
	}
	sess := input.Session.Clone()
	if ledger == nil {
		ledger = NewIdentityLedger()
	}
	events, eventIndexes, err := n.normalizeEvents(sess.Events, ledger)
	if err != nil {
		return Snapshot{}, err
	}
	memories, err := n.normalizeMemories(input.Memories, ledger, n.options.MemoryOrder)
	if err != nil {
		return Snapshot{}, err
	}
	queries := make(map[string][]MemorySnapshot, len(input.MemoryQueries))
	for name, entries := range input.MemoryQueries {
		order := n.options.MemoryOrder
		if configured, ok := n.options.MemoryQueryOrders[name]; ok {
			order = configured
		}
		values, err := n.normalizeMemories(entries, ledger, order)
		if err != nil {
			return Snapshot{}, fmt.Errorf("normalize memory query %q: %w", name, err)
		}
		queries[name] = values
	}
	return Snapshot{
		SessionID:     sess.ID,
		AppName:       sess.AppName,
		UserID:        sess.UserID,
		Events:        events,
		State:         normalizeState(sess.State),
		AppState:      normalizeState(input.AppState),
		UserState:     normalizeState(input.UserState),
		Memories:      memories,
		MemoryQueries: queries,
		Summaries:     n.normalizeSummaries(sess, ledger, eventIndexes),
		Tracks:        n.normalizeTracks(sess, ledger),
		Unsupported:   cloneUnsupported(input.Unsupported),
	}, nil
}

func (n Normalizer) normalizeEvents(
	events []event.Event,
	ledger *IdentityLedger,
) ([]map[string]any, map[string]int, error) {
	result := make([]map[string]any, 0, len(events))
	indexes := make(map[string]int, len(events))
	fallbacks := make(map[string]int)
	for i := range events {
		raw, err := json.Marshal(events[i])
		if err != nil {
			return nil, nil, fmt.Errorf("marshal event %d: %w", i, err)
		}
		var value map[string]any
		if err := decodeJSON(raw, &value); err != nil {
			return nil, nil, fmt.Errorf("decode event %d: %w", i, err)
		}
		delete(value, "timestamp")
		delete(value, "requestID")
		delete(value, "created")
		delete(value, "system_fingerprint")
		if response, ok := value["response"].(map[string]any); ok {
			delete(response, "id")
			delete(response, "timestamp")
			if len(response) == 0 {
				delete(value, "response")
			}
		}
		removeTimingFields(value)
		n.normalizeEventIdentifiers(value, ledger)
		if events[i].StateDelta != nil {
			value["stateDelta"] = normalizeState(events[i].StateDelta)
		}
		if events[i].Extensions != nil {
			extensions := make(map[string]any, len(events[i].Extensions))
			for key, extension := range events[i].Extensions {
				normalized := normalizeRawJSON(extension)
				if key == event.ToolCallArgsExtensionKey {
					normalized = aliasObjectKeys(normalized, IdentityToolCall, ledger)
				}
				extensions[key] = normalized
			}
			value["extensions"] = extensions
		}
		logical, ok := ledger.Logical(IdentityEvent, events[i].ID)
		if !ok {
			copyForID := cloneGenericMap(value)
			delete(copyForID, "id")
			fingerprint := semanticFingerprint(copyForID)
			fallbacks[fingerprint]++
			logical = fmt.Sprintf("auto-%s-%d", fingerprint, fallbacks[fingerprint])
		}
		value["id"] = string(IdentityEvent) + ":" + logical
		if events[i].ID != "" {
			indexes[events[i].ID] = i
		}
		result = append(result, normalizeJSONMap(value, nil))
	}
	return result, indexes, nil
}

func (n Normalizer) normalizeEventIdentifiers(value map[string]any, ledger *IdentityLedger) {
	aliasMapString(value, "invocationId", IdentityInvocation, ledger)
	aliasMapString(value, "parentInvocationId", IdentityInvocation, ledger)
	if metadata, ok := value["parentMetadata"].(map[string]any); ok {
		aliasMapString(metadata, "triggerId", IdentityToolCall, ledger)
	}
	if ids, ok := value["longRunningToolIDs"].(map[string]any); ok {
		value["longRunningToolIDs"] = aliasObjectKeys(ids, IdentityToolCall, ledger)
	}
	choices, _ := value["choices"].([]any)
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		message, _ := choice["message"].(map[string]any)
		aliasMapString(message, "tool_id", IdentityToolCall, ledger)
		toolCalls, _ := message["tool_calls"].([]any)
		for _, rawCall := range toolCalls {
			call, _ := rawCall.(map[string]any)
			aliasMapString(call, "id", IdentityToolCall, ledger)
			function, _ := call["function"].(map[string]any)
			if arguments, ok := function["arguments"].(string); ok {
				function["arguments"] = normalizeRawJSON([]byte(arguments))
			}
		}
	}
}

func removeTimingFields(value map[string]any) {
	usage, _ := value["usage"].(map[string]any)
	if usage != nil {
		delete(usage, "timing_info")
	}
	delete(value, "timing_info")
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

func normalizeBytes(raw []byte) TaggedBytes {
	if raw == nil {
		return TaggedBytes{Kind: "nil"}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 {
		var value any
		if decodeJSON(trimmed, &value) == nil {
			return TaggedBytes{Kind: "json", Value: normalizeJSON(value, nil)}
		}
	}
	if utf8.Valid(raw) {
		return TaggedBytes{Kind: "utf8", Value: string(raw)}
	}
	return TaggedBytes{Kind: "base64", Value: base64.StdEncoding.EncodeToString(raw)}
}

func normalizeRawJSON(raw []byte) any {
	if raw == nil {
		return nil
	}
	var value any
	if decodeJSON(raw, &value) == nil {
		return normalizeJSON(value, nil)
	}
	return normalizeBytes(raw)
}

func (n Normalizer) normalizeMemories(
	entries []*memory.Entry,
	ledger *IdentityLedger,
	order MemoryOrder,
) ([]MemorySnapshot, error) {
	type pending struct {
		rawID string
		item  MemorySnapshot
		key   string
	}
	values := make([]pending, 0, len(entries))
	for rank, entry := range entries {
		if entry == nil || entry.Memory == nil {
			return nil, fmt.Errorf("memory %d is nil", rank)
		}
		item := MemorySnapshot{
			AppName:      entry.AppName,
			UserID:       entry.UserID,
			Rank:         rank,
			Score:        roundFloat(entry.Score, n.options.ScorePrecision),
			Content:      entry.Memory.Memory,
			Topics:       sortedStrings(entry.Memory.Topics),
			Kind:         string(entry.Memory.Kind),
			Participants: sortedStrings(entry.Memory.Participants),
			Location:     entry.Memory.Location,
		}
		if entry.Memory.EventTime != nil {
			item.EventTime = entry.Memory.EventTime.UTC().Format(time.RFC3339Nano)
		}
		key := semanticFingerprint(struct {
			AppName      string
			UserID       string
			Content      string
			Topics       []string
			Kind         string
			EventTime    string
			Participants []string
			Location     string
		}{item.AppName, item.UserID, item.Content, item.Topics, item.Kind,
			item.EventTime, item.Participants, item.Location})
		values = append(values, pending{rawID: entry.ID, item: item, key: key})
	}
	if order == MemoryOrderUnordered {
		for i := range values {
			values[i].item.Rank = -1
		}
		sort.SliceStable(values, func(i, j int) bool {
			if values[i].key == values[j].key {
				return values[i].item.Score > values[j].item.Score
			}
			return values[i].key < values[j].key
		})
	}
	occurrences := make(map[string]int)
	result := make([]MemorySnapshot, len(values))
	for i := range values {
		logical, ok := ledger.Logical(IdentityMemory, values[i].rawID)
		if !ok {
			occurrences[values[i].key]++
			logical = fmt.Sprintf("auto-%s-%d", values[i].key, occurrences[values[i].key])
		}
		values[i].item.ID = string(IdentityMemory) + ":" + logical
		result[i] = values[i].item
	}
	return result, nil
}

func (n Normalizer) normalizeSummaries(
	sess *session.Session,
	ledger *IdentityLedger,
	eventIndexes map[string]int,
) map[string]SummarySnapshot {
	result := make(map[string]SummarySnapshot, len(sess.Summaries))
	for filterKey, summaryValue := range sess.Summaries {
		if summaryValue == nil {
			result[filterKey] = SummarySnapshot{
				SessionID: sess.ID, AppName: sess.AppName, UserID: sess.UserID,
				FilterKey: filterKey,
			}
			continue
		}
		item := SummarySnapshot{
			SessionID: sess.ID, AppName: sess.AppName, UserID: sess.UserID,
			FilterKey: filterKey, Text: summaryValue.Summary,
			Topics:              sortedStrings(summaryValue.Topics),
			UpdatedAtEventIndex: lastEventAtOrBefore(sess.Events, summaryValue.UpdatedAt),
		}
		boundary := summaryValue.CutoffBoundary()
		if boundary != nil {
			item.BoundaryPresent = true
			item.BoundaryFilterKey = boundary.FilterKey
			item.Version = boundary.Version
			item.CutoffAtEventIndex = lastEventAtOrBefore(sess.Events, boundary.CutoffAt)
			if boundary.LastEventID != "" {
				item.LastEventIDPresent = true
				if logical, ok := ledger.Logical(IdentityEvent, boundary.LastEventID); ok {
					item.LastEventLogicalID = string(IdentityEvent) + ":" + logical
				} else if index, ok := eventIndexes[boundary.LastEventID]; ok {
					item.LastEventLogicalID = fmt.Sprintf("event-index:%d", index)
				}
				if index, ok := eventIndexes[boundary.LastEventID]; ok {
					item.LastEventIndex = intPointer(index)
				} else {
					item.LastEventIndex = intPointer(-1)
				}
			}
		}
		result[filterKey] = item
	}
	return result
}

func (n Normalizer) normalizeTracks(
	sess *session.Session,
	ledger *IdentityLedger,
) map[string][]TrackEventSnapshot {
	result := make(map[string][]TrackEventSnapshot, len(sess.Tracks))
	for trackName, trackEvents := range sess.Tracks {
		if trackEvents == nil {
			result[string(trackName)] = nil
			continue
		}
		values := make([]TrackEventSnapshot, 0, len(trackEvents.Events))
		for _, trackEvent := range trackEvents.Events {
			payload := normalizeRawJSON(trackEvent.Payload)
			payload = normalizeKnownPayloadIdentifiers(payload, ledger)
			payload = normalizeJSON(payload, n.options.VolatilePayloadKeys)
			values = append(values, TrackEventSnapshot{
				Track: string(trackEvent.Track), Payload: payload,
			})
		}
		result[string(trackName)] = values
	}
	return result
}

func normalizeKnownPayloadIdentifiers(value any, ledger *IdentityLedger) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			switch strings.ToLower(key) {
			case "invocationid", "invocation_id", "parentinvocationid", "parent_invocation_id":
				if raw, ok := item.(string); ok {
					result[key] = aliasValue(IdentityInvocation, raw, ledger)
					continue
				}
			case "toolcallid", "tool_call_id", "toolid", "tool_id", "triggerid", "trigger_id":
				if raw, ok := item.(string); ok {
					result[key] = aliasValue(IdentityToolCall, raw, ledger)
					continue
				}
			}
			result[key] = normalizeKnownPayloadIdentifiers(item, ledger)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = normalizeKnownPayloadIdentifiers(typed[i], ledger)
		}
		return result
	default:
		return value
	}
}

func aliasMapString(
	value map[string]any,
	key string,
	namespace IdentityNamespace,
	ledger *IdentityLedger,
) {
	if value == nil {
		return
	}
	raw, ok := value[key].(string)
	if !ok || raw == "" {
		return
	}
	value[key] = aliasValue(namespace, raw, ledger)
}

func aliasValue(namespace IdentityNamespace, raw string, ledger *IdentityLedger) string {
	if logical, ok := ledger.Logical(namespace, raw); ok {
		return string(namespace) + ":" + logical
	}
	return string(namespace) + ":unmapped:" + shortHash(raw)
}

func aliasObjectKeys(value any, namespace IdentityNamespace, ledger *IdentityLedger) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result := make(map[string]any, len(object))
	for key, item := range object {
		result[aliasValue(namespace, key, ledger)] = item
	}
	return result
}

func lastEventAtOrBefore(events []event.Event, cutoff time.Time) *int {
	if cutoff.IsZero() {
		return nil
	}
	index := -1
	for i := range events {
		if events[i].Timestamp.After(cutoff) {
			continue
		}
		if index < 0 || events[index].Timestamp.Before(events[i].Timestamp) ||
			(events[index].Timestamp.Equal(events[i].Timestamp) && i > index) {
			index = i
		}
	}
	return intPointer(index)
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
		return typed
	default:
		return value
	}
}

func normalizeJSONMap(value map[string]any, omit map[string]struct{}) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		if _, skipped := omit[strings.ToLower(key)]; skipped {
			continue
		}
		result[key] = normalizeJSON(item, omit)
	}
	return result
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
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func semanticFingerprint(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:6])
}

func shortHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:6])
}

func roundFloat(value float64, precision int) float64 {
	scale := math.Pow10(precision)
	result := math.Round(value*scale) / scale
	if result == 0 {
		return 0
	}
	return result
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func intPointer(value int) *int { return &value }

func cloneGenericMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneStringSet(value map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(value))
	for key := range value {
		result[key] = struct{}{}
	}
	return result
}

func cloneUnsupported(value map[CapabilityName]string) map[CapabilityName]string {
	result := make(map[CapabilityName]string, len(value))
	for key, reason := range value {
		result[key] = reason
	}
	return result
}
