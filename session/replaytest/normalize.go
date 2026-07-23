//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const presentMarker = "<present>"

type normalizedEvent struct {
	value    CanonicalMap
	orderKey string
	sequence int
}

func normalizeSnapshot(
	backendName string,
	caseName string,
	eventOrder EventOrderMode,
	eventOrderPlan *causalOrderPlan,
	required Capabilities,
	eventStateKeys map[string]struct{},
	sess *session.Session,
	appState session.StateMap,
	userState session.StateMap,
	memories []*memory.Entry,
	memorySearches map[string][]*memory.Entry,
) (Snapshot, error) {
	if sess == nil {
		return Snapshot{}, session.ErrNilSession
	}
	eventSnapshot := sess.GetEvents()
	events, order, physicalToLogical, err := normalizeEvents(
		eventSnapshot,
		eventOrder,
		eventOrderPlan,
		sess.CreatedAt,
	)
	if err != nil {
		return Snapshot{}, err
	}
	normalizedMemories := make([]CanonicalMap, 0)
	physicalToLogicalMemory := make(map[string]string)
	if required[CapabilityMemory] {
		normalizedMemories, physicalToLogicalMemory, err = normalizeMemoryCatalog(memories)
		if err != nil {
			return Snapshot{}, err
		}
	}
	normalizedMemorySearches := make(map[string][]CanonicalMap)
	if required[CapabilityMemorySearch] {
		normalizedMemorySearches, err = normalizeMemorySearches(
			memorySearches,
			physicalToLogicalMemory,
		)
		if err != nil {
			return Snapshot{}, err
		}
	}
	summaries := make(map[string]CanonicalMap)
	if required[CapabilitySummary] {
		summaries, err = normalizeSummaries(sess, eventSnapshot, physicalToLogical)
		if err != nil {
			return Snapshot{}, err
		}
	}
	tracks := make(map[string][]CanonicalMap)
	if required[CapabilityTrack] {
		tracks, err = normalizeTracks(sess, sess.CreatedAt)
		if err != nil {
			return Snapshot{}, err
		}
	}
	state := map[string]CanonicalMap{"app": {}, "user": {}, "session": {}}
	if required[CapabilityAppState] {
		state["app"] = normalizeState(appState, "")
	}
	if required[CapabilityUserState] {
		state["user"] = normalizeState(userState, "")
	}
	if required[CapabilitySessionState] {
		state["session"] = normalizeSessionState(sess.SnapshotState(), eventStateKeys)
	}
	return Snapshot{
		Backend: backendName,
		Case:    caseName,
		Session: CanonicalMap{
			"id":         sess.ID,
			"app_name":   sess.AppName,
			"user_id":    sess.UserID,
			"created_at": normalizeTime(sess.CreatedAt),
			"updated_at": normalizeTime(sess.UpdatedAt),
		},
		Events:         events,
		EventOrder:     order,
		State:          state,
		Memories:       normalizedMemories,
		MemorySearches: normalizedMemorySearches,
		Summaries:      summaries,
		Tracks:         tracks,
	}, nil
}

func normalizeEvents(
	events []event.Event,
	mode EventOrderMode,
	plan *causalOrderPlan,
	baseTime time.Time,
) ([]CanonicalMap, map[string][]string, map[string]string, error) {
	records := make([]normalizedEvent, 0, len(events))
	order := make(map[string][]string)
	physicalToLogical := make(map[string]string, len(events))
	physicalIDs := make(map[string]struct{}, len(events))
	logicalPositions := make(map[string]int, len(events))
	for index := range events {
		evt := &events[index]
		logicalID, err := recordEventIdentity(evt, index, physicalIDs, logicalPositions)
		if err != nil {
			return nil, nil, nil, err
		}
		physicalToLogical[evt.ID] = logicalID
		orderKey := normalizedEventOrderKey(evt, logicalID, mode, plan)
		sequence := len(order[orderKey])
		order[orderKey] = append(order[orderKey], logicalID)
		value, err := normalizeEventValue(evt, index, logicalID, baseTime)
		if err != nil {
			return nil, nil, nil, err
		}
		records = append(records, normalizedEvent{
			value:    value,
			orderKey: orderKey,
			sequence: sequence,
		})
	}
	if err := validateObservedCausalPlan(logicalPositions, plan); err != nil {
		return nil, nil, nil, err
	}
	if mode == EventOrderCausal {
		sort.SliceStable(records, func(i, j int) bool {
			if records[i].orderKey != records[j].orderKey {
				return records[i].orderKey < records[j].orderKey
			}
			return records[i].sequence < records[j].sequence
		})
	}
	output := make([]CanonicalMap, 0, len(records))
	for _, record := range records {
		output = append(output, record.value)
	}
	return output, order, physicalToLogical, nil
}

func recordEventIdentity(
	evt *event.Event,
	index int,
	physicalIDs map[string]struct{},
	logicalPositions map[string]int,
) (string, error) {
	if evt.ID == "" {
		return "", fmt.Errorf("event %d has no physical id", index)
	}
	if _, exists := physicalIDs[evt.ID]; exists {
		return "", fmt.Errorf("duplicate physical event id %q", evt.ID)
	}
	physicalIDs[evt.ID] = struct{}{}
	logicalID, ok, err := event.GetExtension[string](evt, logicalEventIDExtension)
	if err != nil {
		return "", fmt.Errorf("event %d logical id: %w", index, err)
	}
	if !ok || logicalID == "" {
		return "", fmt.Errorf("event %d has no logical id", index)
	}
	if _, exists := logicalPositions[logicalID]; exists {
		return "", fmt.Errorf("duplicate logical event id %q", logicalID)
	}
	logicalPositions[logicalID] = index
	return logicalID, nil
}

func normalizedEventOrderKey(
	evt *event.Event,
	logicalID string,
	mode EventOrderMode,
	plan *causalOrderPlan,
) string {
	if mode != EventOrderCausal {
		return "global"
	}
	if plan != nil && plan.lanes[logicalID] != "" {
		return "concurrent:" + plan.lanes[logicalID]
	}
	if evt.FilterKey != "" {
		return evt.FilterKey
	}
	if evt.Branch != "" {
		return evt.Branch
	}
	return "<root>"
}

func normalizeEventValue(
	evt *event.Event,
	index int,
	logicalID string,
	baseTime time.Time,
) (CanonicalMap, error) {
	raw, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("marshal event %d: %w", index, err)
	}
	var value CanonicalMap
	if err := decodeJSON(raw, &value); err != nil {
		return nil, fmt.Errorf("decode event %d: %w", index, err)
	}
	value["id"] = logicalID
	value["timestamp"] = normalizeTimeOffset(evt.Timestamp, baseTime)
	response, _ := value["response"].(map[string]any)
	if evt.Response != nil && response != nil {
		response["timestamp"] = normalizeTimeOffset(evt.Response.Timestamp, baseTime)
	}
	if extensions, ok := value["extensions"].(map[string]any); ok {
		delete(extensions, logicalEventIDExtension)
		if len(extensions) == 0 {
			delete(value, "extensions")
		}
	}
	if len(evt.StateDelta) > 0 {
		// StateDelta itself is observable event data. Do not apply the session
		// snapshot filter here: scoped prefixes and even unexpected backend keys
		// must remain visible to comparison.
		value["stateDelta"] = normalizeState(evt.StateDelta, "")
	}
	return value, nil
}

func validateObservedCausalPlan(
	positions map[string]int,
	plan *causalOrderPlan,
) error {
	if plan == nil {
		return nil
	}
	if len(positions) != len(plan.predecessors) {
		return fmt.Errorf(
			"observed %d replay events, want %d",
			len(positions),
			len(plan.predecessors),
		)
	}
	for logicalID, predecessors := range plan.predecessors {
		position, exists := positions[logicalID]
		if !exists {
			return fmt.Errorf("planned event %q is missing", logicalID)
		}
		for _, predecessor := range predecessors {
			predecessorPosition, exists := positions[predecessor]
			if !exists {
				return fmt.Errorf("planned predecessor %q is missing", predecessor)
			}
			if predecessorPosition >= position {
				return fmt.Errorf(
					"event %q appears before predecessor %q",
					logicalID,
					predecessor,
				)
			}
		}
	}
	return nil
}

func normalizeState(input session.StateMap, scope string) CanonicalMap {
	return normalizeStatePreserving(input, scope, nil)
}

func normalizeSessionState(
	input session.StateMap,
	eventStateKeys map[string]struct{},
) CanonicalMap {
	return normalizeStatePreserving(input, "session", eventStateKeys)
}

func normalizeStatePreserving(
	input session.StateMap,
	scope string,
	preserved map[string]struct{},
) CanonicalMap {
	output := make(CanonicalMap)
	for key, value := range input {
		if scope == "session" {
			if key == replayTrackStateKey {
				continue
			}
			if strings.HasPrefix(key, session.StateAppPrefix) ||
				strings.HasPrefix(key, session.StateUserPrefix) {
				if _, ok := preserved[key]; !ok {
					continue
				}
			}
		}
		if value == nil {
			output[key] = CanonicalMap{"kind": "nil"}
			continue
		}
		var decoded any
		if decodeJSON(value, &decoded) == nil {
			raw, _ := json.Marshal(decoded)
			output[key] = CanonicalMap{
				"kind": "json",
				"json": string(raw),
			}
			continue
		}
		output[key] = CanonicalMap{
			"kind":   "bytes",
			"base64": base64.StdEncoding.EncodeToString(value),
		}
	}
	return output
}

func normalizeMemories(entries []*memory.Entry) ([]CanonicalMap, error) {
	output, _, err := normalizeMemoryCatalog(entries)
	return output, err
}

type normalizedMemoryRecord struct {
	physicalID string
	value      CanonicalMap
	sortKey    string
}

func normalizeMemoryCatalog(
	entries []*memory.Entry,
) ([]CanonicalMap, map[string]string, error) {
	records := make([]normalizedMemoryRecord, 0, len(entries))
	ids := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		value, err := normalizeMemoryEntry(entry, fmt.Sprintf("memory %d", index))
		if err != nil {
			return nil, nil, err
		}
		if _, exists := ids[entry.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate memory id %q", entry.ID)
		}
		ids[entry.ID] = struct{}{}
		delete(value, "id")
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal normalized memory %d: %w", index, err)
		}
		records = append(records, normalizedMemoryRecord{
			physicalID: entry.ID,
			value:      value,
			sortKey:    string(raw),
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].sortKey < records[j].sortKey
	})
	output := make([]CanonicalMap, 0, len(records))
	physicalToLogical := make(map[string]string, len(records))
	for index := range records {
		if index > 0 && records[index-1].sortKey == records[index].sortKey {
			return nil, nil, errors.New("duplicate normalized memory entry")
		}
		logicalID := "memory-" + strconv.Itoa(index)
		records[index].value["id"] = logicalID
		physicalToLogical[records[index].physicalID] = logicalID
		output = append(output, records[index].value)
	}
	return output, physicalToLogical, nil
}

func normalizeMemorySearches(
	searches map[string][]*memory.Entry,
	physicalToLogical map[string]string,
) (map[string][]CanonicalMap, error) {
	output := make(map[string][]CanonicalMap, len(searches))
	for name, entries := range searches {
		if name == "" {
			return nil, errors.New("memory search has no name")
		}
		seen := make(map[string]struct{}, len(entries))
		results := make([]CanonicalMap, 0, len(entries))
		for index, entry := range entries {
			value, err := normalizeMemoryEntry(
				entry,
				fmt.Sprintf("memory search %q result %d", name, index),
			)
			if err != nil {
				return nil, err
			}
			if _, exists := seen[entry.ID]; exists {
				return nil, fmt.Errorf("memory search %q repeats id %q", name, entry.ID)
			}
			seen[entry.ID] = struct{}{}
			logicalID := physicalToLogical[entry.ID]
			if logicalID == "" {
				return nil, fmt.Errorf("memory search %q returned unknown id %q", name, entry.ID)
			}
			value["id"] = logicalID
			value["score"] = entry.Score
			results = append(results, value)
		}
		output[name] = results
	}
	return output, nil
}

func normalizeMemoryEntry(entry *memory.Entry, owner string) (CanonicalMap, error) {
	if entry == nil {
		return nil, fmt.Errorf("%s is nil", owner)
	}
	if entry.Memory == nil {
		return nil, fmt.Errorf("%s has nil content", owner)
	}
	if entry.ID == "" {
		return nil, fmt.Errorf("%s has no id", owner)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", owner, err)
	}
	var value CanonicalMap
	if err := decodeJSON(raw, &value); err != nil {
		return nil, fmt.Errorf("decode %s: %w", owner, err)
	}
	normalizeTimestamps(value, "created_at", "updated_at")
	memoryValue, _ := value["memory"].(map[string]any)
	normalizeTimestamps(memoryValue, "last_updated")
	if entry.Memory.EventTime != nil && memoryValue != nil {
		memoryValue["event_time"] = entry.Memory.EventTime.UTC().Format(time.RFC3339Nano)
	}
	return value, nil
}

func normalizeSummaries(
	sess *session.Session,
	events []event.Event,
	physicalToLogical map[string]string,
) (map[string]CanonicalMap, error) {
	output := make(map[string]CanonicalMap)
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	for filterKey, summary := range sess.Summaries {
		if summary == nil {
			output[filterKey] = nil
			continue
		}
		value := CanonicalMap{
			"text":               summary.Summary,
			"topics":             append([]string(nil), summary.Topics...),
			"updated_at":         normalizeTime(summary.UpdatedAt),
			"retained_event_ids": retainedEventIDs(events, summary, filterKey, physicalToLogical),
		}
		if boundary := summary.CutoffBoundary(); boundary != nil {
			lastEventID := boundary.LastEventID
			cutoffAt := normalizeTime(boundary.CutoffAt)
			if lastEventID != "" {
				logicalID := physicalToLogical[lastEventID]
				anchor := eventByPhysicalID(events, lastEventID)
				if logicalID == "" || anchor == nil {
					return nil, fmt.Errorf("summary %q references unknown event %q", filterKey, lastEventID)
				}
				if boundary.CutoffAt.IsZero() || !boundary.CutoffAt.Equal(anchor.Timestamp) {
					return nil, fmt.Errorf(
						"summary %q cutoff does not match event %q timestamp",
						filterKey,
						lastEventID,
					)
				}
				lastEventID = logicalID
				cutoffAt = normalizeTimeOffset(boundary.CutoffAt, sess.CreatedAt)
			}
			value["boundary"] = CanonicalMap{
				"version":       boundary.Version,
				"filter_key":    boundary.FilterKey,
				"cutoff_at":     cutoffAt,
				"last_event_id": lastEventID,
			}
		}
		output[filterKey] = value
	}
	return output, nil
}

func eventByPhysicalID(events []event.Event, id string) *event.Event {
	for index := range events {
		if events[index].ID == id {
			return &events[index]
		}
	}
	return nil
}

func retainedEventIDs(
	events []event.Event,
	summary *session.Summary,
	filterKey string,
	physicalToLogical map[string]string,
) []string {
	boundary := summary.CutoffBoundary()
	if boundary == nil {
		return nil
	}
	start := 0
	if boundary.LastEventID != "" {
		for index := range events {
			if events[index].ID == boundary.LastEventID {
				start = index + 1
				break
			}
		}
	}
	retained := make([]string, 0)
	for index := start; index < len(events); index++ {
		evt := &events[index]
		if boundary.LastEventID == "" && !boundary.CutoffAt.IsZero() && !evt.Timestamp.After(boundary.CutoffAt) {
			continue
		}
		if !evt.Filter(filterKey) {
			continue
		}
		logicalID := physicalToLogical[evt.ID]
		if logicalID == "" {
			logicalID = "event-" + strconv.Itoa(index)
		}
		retained = append(retained, logicalID)
	}
	return retained
}

func normalizeTracks(sess *session.Session, baseTime time.Time) (map[string][]CanonicalMap, error) {
	output := make(map[string][]CanonicalMap)
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	for trackName, history := range sess.Tracks {
		if history == nil {
			output[string(trackName)] = nil
			continue
		}
		events := make([]CanonicalMap, 0, len(history.Events))
		for index, trackEvent := range history.Events {
			var payload any
			if trackEvent.Payload != nil {
				if err := decodeJSON(trackEvent.Payload, &payload); err != nil {
					return nil, fmt.Errorf("decode track %s event %d: %w", trackName, index, err)
				}
			}
			events = append(events, CanonicalMap{
				"track":     string(trackEvent.Track),
				"payload":   payload,
				"timestamp": normalizeTimeOffset(trackEvent.Timestamp, baseTime),
			})
		}
		output[string(trackName)] = events
	}
	return output, nil
}

func normalizeTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return presentMarker
}

func normalizeTimeOffset(value, base time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.Sub(base).Nanoseconds()
}

func normalizeTimestamps(value map[string]any, keys ...string) {
	for _, key := range keys {
		if timestamp, ok := value[key]; ok {
			value[key] = normalizeTimestampPresence(timestamp)
		}
	}
}

func decodeJSON(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
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

func normalizeTimestampPresence(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if typed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil && parsed.IsZero() {
			return nil
		}
	case float64:
		if typed == 0 {
			return nil
		}
	case json.Number:
		if number, err := typed.Float64(); err == nil && number == 0 {
			return nil
		}
	}
	return presentMarker
}
