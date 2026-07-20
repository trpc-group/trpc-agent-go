//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const presentMarker = "<present>"

func normalizeSnapshot(
	backendName string,
	caseName string,
	eventOrder EventOrderMode,
	sess *session.Session,
	appState session.StateMap,
	userState session.StateMap,
	memories []*memory.Entry,
) (Snapshot, error) {
	if sess == nil {
		return Snapshot{}, session.ErrNilSession
	}
	events, order, physicalToLogical, err := normalizeEvents(sess.Events, eventOrder)
	if err != nil {
		return Snapshot{}, err
	}
	normalizedMemories, err := normalizeMemories(memories)
	if err != nil {
		return Snapshot{}, err
	}
	summaries, err := normalizeSummaries(sess, physicalToLogical)
	if err != nil {
		return Snapshot{}, err
	}
	tracks, err := normalizeTracks(sess)
	if err != nil {
		return Snapshot{}, err
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
		Events:     events,
		EventOrder: order,
		State: map[string]map[string]string{
			"app":     normalizeState(appState, ""),
			"user":    normalizeState(userState, ""),
			"session": normalizeState(sess.State, "session"),
		},
		Memories:  normalizedMemories,
		Summaries: summaries,
		Tracks:    tracks,
	}, nil
}

func normalizeEvents(
	events []event.Event,
	mode EventOrderMode,
) ([]CanonicalMap, map[string][]string, map[string]string, error) {
	type normalizedEvent struct {
		value    CanonicalMap
		orderKey string
		sequence int
	}
	records := make([]normalizedEvent, 0, len(events))
	order := make(map[string][]string)
	physicalToLogical := make(map[string]string, len(events))
	for index := range events {
		evt := &events[index]
		logicalID, ok, err := event.GetExtension[string](evt, logicalEventIDExtension)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("event %d logical id: %w", index, err)
		}
		if !ok || logicalID == "" {
			logicalID = "event-" + strconv.Itoa(index)
		}
		physicalToLogical[evt.ID] = logicalID
		orderKey := "global"
		if mode == EventOrderCausal {
			orderKey = evt.FilterKey
			if orderKey == "" {
				orderKey = evt.Branch
			}
			if orderKey == "" {
				orderKey = "<root>"
			}
		}
		sequence := len(order[orderKey])
		order[orderKey] = append(order[orderKey], logicalID)
		raw, err := json.Marshal(evt)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("marshal event %d: %w", index, err)
		}
		var value CanonicalMap
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, nil, fmt.Errorf("decode event %d: %w", index, err)
		}
		value["id"] = logicalID
		normalizeVolatile(value)
		if extensions, ok := value["extensions"].(map[string]any); ok {
			delete(extensions, logicalEventIDExtension)
			if len(extensions) == 0 {
				delete(value, "extensions")
			}
		}
		records = append(records, normalizedEvent{
			value:    value,
			orderKey: orderKey,
			sequence: sequence,
		})
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

func normalizeState(input session.StateMap, scope string) map[string]string {
	output := make(map[string]string)
	for key, value := range input {
		if scope == "session" {
			if key == "tracks" || strings.HasPrefix(key, session.StateAppPrefix) ||
				strings.HasPrefix(key, session.StateUserPrefix) {
				continue
			}
		}
		if value == nil {
			output[key] = "<nil>"
			continue
		}
		var decoded any
		if json.Unmarshal(value, &decoded) == nil {
			raw, _ := json.Marshal(decoded)
			output[key] = string(raw)
			continue
		}
		output[key] = string(value)
	}
	return output
}

func normalizeMemories(entries []*memory.Entry) ([]CanonicalMap, error) {
	output := make([]CanonicalMap, 0, len(entries))
	for index, entry := range entries {
		if entry == nil {
			continue
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("marshal memory %d: %w", index, err)
		}
		var value CanonicalMap
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode memory %d: %w", index, err)
		}
		normalizeVolatile(value)
		if entry.Memory != nil && entry.Memory.EventTime != nil {
			memoryValue, ok := value["memory"].(map[string]any)
			if ok {
				memoryValue["event_time"] = entry.Memory.EventTime.UTC().Format(time.RFC3339Nano)
			}
		}
		delete(value, "id")
		output = append(output, value)
	}
	sort.Slice(output, func(i, j int) bool {
		left, _ := json.Marshal(output[i])
		right, _ := json.Marshal(output[j])
		return string(left) < string(right)
	})
	for index := range output {
		output[index]["id"] = "memory-" + strconv.Itoa(index)
	}
	return output, nil
}

func normalizeSummaries(
	sess *session.Session,
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
			"session_id":         sess.ID,
			"text":               summary.Summary,
			"topics":             append([]string(nil), summary.Topics...),
			"updated_at":         normalizeTime(summary.UpdatedAt),
			"retained_event_ids": retainedEventIDs(sess.Events, summary, filterKey, physicalToLogical),
		}
		if boundary := summary.CutoffBoundary(); boundary != nil {
			lastEventID := boundary.LastEventID
			if logicalID := physicalToLogical[lastEventID]; logicalID != "" {
				lastEventID = logicalID
			} else if lastEventID != "" {
				lastEventID = "<unknown-event>"
			}
			value["boundary"] = CanonicalMap{
				"version":       boundary.Version,
				"filter_key":    boundary.FilterKey,
				"cutoff_at":     normalizeTime(boundary.CutoffAt),
				"last_event_id": lastEventID,
			}
		}
		output[filterKey] = value
	}
	return output, nil
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

func normalizeTracks(sess *session.Session) (map[string][]CanonicalMap, error) {
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
			if len(trackEvent.Payload) > 0 {
				if err := json.Unmarshal(trackEvent.Payload, &payload); err != nil {
					return nil, fmt.Errorf("decode track %s event %d: %w", trackName, index, err)
				}
				normalizeDynamicPayload(payload)
			}
			events = append(events, CanonicalMap{
				"track":     string(trackEvent.Track),
				"payload":   payload,
				"timestamp": normalizeTime(trackEvent.Timestamp),
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

func normalizeVolatile(value any) {
	switch typed := value.(type) {
	case CanonicalMap:
		normalizeVolatileMap(map[string]any(typed))
	case map[string]any:
		normalizeVolatileMap(typed)
	case []any:
		for _, child := range typed {
			normalizeVolatile(child)
		}
	}
}

func normalizeVolatileMap(value map[string]any) {
	for childKey, child := range value {
		lower := strings.ToLower(childKey)
		switch lower {
		case "timestamp", "created_at", "updated_at", "last_updated", "event_time":
			value[childKey] = normalizeTimestampPresence(child)
		default:
			normalizeVolatile(child)
		}
	}
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
	}
	return presentMarker
}

func normalizeDynamicPayload(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			lower := strings.ToLower(childKey)
			if isDurationKey(lower) || isTimestampKey(lower) {
				if child != nil {
					typed[childKey] = presentMarker
				}
				continue
			}
			normalizeDynamicPayload(child)
		}
	case []any:
		for _, child := range typed {
			normalizeDynamicPayload(child)
		}
	}
}

func isDurationKey(key string) bool {
	return strings.Contains(key, "duration") || strings.Contains(key, "latency") ||
		strings.Contains(key, "elapsed")
}

func isTimestampKey(key string) bool {
	return key == "time" || strings.Contains(key, "timestamp") ||
		strings.HasSuffix(key, "_at")
}
