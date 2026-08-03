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
	"unicode/utf8"

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
	if required[CapabilityAppState] {
		if err := validateStateMapKeys("app state", appState); err != nil {
			return Snapshot{}, err
		}
	}
	if required[CapabilityUserState] {
		if err := validateStateMapKeys("user state", userState); err != nil {
			return Snapshot{}, err
		}
	}
	var sessionState session.StateMap
	if required[CapabilitySessionState] {
		sessionState = sess.SnapshotState()
		if err := validateStateMapKeys("session state", sessionState); err != nil {
			return Snapshot{}, err
		}
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
	memoryIdentities := make(map[string]normalizedMemoryIdentity)
	if required[CapabilityMemory] {
		normalizedMemories, memoryIdentities, err = normalizeMemoryCatalog(memories)
		if err != nil {
			return Snapshot{}, err
		}
	}
	normalizedMemorySearches := make(map[string][]CanonicalMap)
	if required[CapabilityMemorySearch] {
		normalizedMemorySearches, err = normalizeMemorySearches(
			memorySearches,
			memoryIdentities,
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
		state["session"] = normalizeSessionState(sessionState, eventStateKeys)
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
	if err := validateEventComparisonStrings(evt, fmt.Sprintf("event %d", index)); err != nil {
		return nil, err
	}
	if err := validateEventToolCallArguments(evt); err != nil {
		return nil, fmt.Errorf("event %d: %w", index, err)
	}
	if err := validateStateMapKeys(fmt.Sprintf("event %d state delta", index), evt.StateDelta); err != nil {
		return nil, err
	}
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
	if evt.Response != nil && !evt.Response.IsPartial {
		if err := normalizeToolCallArguments(value, index); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func normalizeToolCallArguments(value CanonicalMap, eventIndex int) error {
	choices, _ := value["choices"].([]any)
	for choiceIndex, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		for _, field := range []string{"message", "delta"} {
			message, _ := choice[field].(map[string]any)
			calls, _ := message["tool_calls"].([]any)
			for callIndex, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				function, _ := call["function"].(map[string]any)
				arguments, ok := function["arguments"].(string)
				if !ok {
					continue
				}
				canonical, err := canonicalJSONString(arguments)
				if err != nil {
					return fmt.Errorf(
						"event %d choice %d %s tool call %d arguments: %w",
						eventIndex,
						choiceIndex,
						field,
						callIndex,
						err,
					)
				}
				function["arguments"] = canonical
			}
		}
	}
	return nil
}

func canonicalJSONString(input string) (string, error) {
	var value any
	if err := decodeJSON([]byte(input), &value); err != nil {
		return "", err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
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
		if decodeLosslessJSON(value, &decoded) {
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

func decodeLosslessJSON(raw []byte, output any) bool {
	return decodeJSON(raw, output) == nil
}

// raw has passed encoding/json validation, which replaces unpaired UTF-16
// surrogate escapes with U+FFFD instead of rejecting them.
func validJSONSurrogatePairs(raw []byte) bool {
	for index := 0; index < len(raw); index++ {
		if raw[index] != '\\' {
			continue
		}
		index++
		if raw[index] != 'u' {
			continue
		}
		code, _ := strconv.ParseUint(string(raw[index+1:index+5]), 16, 16)
		index += 4
		switch {
		case code >= 0xd800 && code <= 0xdbff:
			if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
				return false
			}
			low, _ := strconv.ParseUint(string(raw[index+3:index+7]), 16, 16)
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			index += 6
		case code >= 0xdc00 && code <= 0xdfff:
			return false
		}
	}
	return true
}

func normalizeMemories(entries []*memory.Entry) ([]CanonicalMap, error) {
	output, _, err := normalizeMemoryCatalog(entries)
	return output, err
}

type normalizedMemoryRecord struct {
	physicalID  string
	value       CanonicalMap
	sortKey     string
	fingerprint string
}

type normalizedMemoryIdentity struct {
	logicalID   string
	fingerprint string
}

func normalizeMemoryCatalog(
	entries []*memory.Entry,
) ([]CanonicalMap, map[string]normalizedMemoryIdentity, error) {
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
		fingerprint, err := memoryIdentityFingerprint(value)
		if err != nil {
			return nil, nil, fmt.Errorf("fingerprint normalized memory %d: %w", index, err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal normalized memory %d: %w", index, err)
		}
		records = append(records, normalizedMemoryRecord{
			physicalID:  entry.ID,
			value:       value,
			sortKey:     string(raw),
			fingerprint: fingerprint,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].sortKey < records[j].sortKey
	})
	output := make([]CanonicalMap, 0, len(records))
	identities := make(map[string]normalizedMemoryIdentity, len(records))
	for index := range records {
		if index > 0 && records[index-1].sortKey == records[index].sortKey {
			return nil, nil, errors.New("duplicate normalized memory entry")
		}
		logicalID := "memory-" + strconv.Itoa(index)
		records[index].value["id"] = logicalID
		identities[records[index].physicalID] = normalizedMemoryIdentity{
			logicalID:   logicalID,
			fingerprint: records[index].fingerprint,
		}
		output = append(output, records[index].value)
	}
	return output, identities, nil
}

func normalizeMemorySearches(
	searches map[string][]*memory.Entry,
	identities map[string]normalizedMemoryIdentity,
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
			identity, exists := identities[entry.ID]
			if !exists {
				return nil, fmt.Errorf("memory search %q returned unknown id %q", name, entry.ID)
			}
			fingerprint, err := memoryIdentityFingerprint(value)
			if err != nil {
				return nil, fmt.Errorf("fingerprint memory search %q result %d: %w", name, index, err)
			}
			if fingerprint != identity.fingerprint {
				return nil, fmt.Errorf("memory search %q result id %q does not match catalog entry", name, entry.ID)
			}
			value["id"] = identity.logicalID
			value["score"] = entry.Score
			results = append(results, value)
		}
		output[name] = results
	}
	return output, nil
}

func memoryIdentityFingerprint(value CanonicalMap) (string, error) {
	identity := make(CanonicalMap, len(value))
	for key, field := range value {
		if key == "id" || key == "score" {
			continue
		}
		identity[key] = field
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return string(raw), nil
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
	if err := validateMemoryEntryStrings(entry, owner); err != nil {
		return nil, err
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

func validateMemoryEntryStrings(entry *memory.Entry, owner string) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "id", value: entry.ID},
		{name: "app name", value: entry.AppName},
		{name: "user id", value: entry.UserID},
		{name: "content", value: entry.Memory.Memory},
		{name: "kind", value: string(entry.Memory.Kind)},
		{name: "location", value: entry.Memory.Location},
	}
	for _, field := range fields {
		if err := validateUTF8String(owner+" "+field.name, field.value); err != nil {
			return err
		}
	}
	for index, topic := range entry.Memory.Topics {
		if err := validateUTF8String(fmt.Sprintf("%s topic %d", owner, index), topic); err != nil {
			return err
		}
	}
	for index, participant := range entry.Memory.Participants {
		if err := validateUTF8String(
			fmt.Sprintf("%s participant %d", owner, index),
			participant,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateUTF8String(owner, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s contains invalid UTF-8", owner)
	}
	return nil
}

func validateStateMapKeys(owner string, state session.StateMap) error {
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := validateUTF8String(owner+" key", key); err != nil {
			return err
		}
	}
	return nil
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
		if err := validateUTF8String("summary filter key", filterKey); err != nil {
			return nil, err
		}
		if summary == nil {
			output[filterKey] = nil
			continue
		}
		if err := validateSummaryStrings(summary, filterKey); err != nil {
			return nil, err
		}
		value := CanonicalMap{
			"text":               summary.Summary,
			"topics":             append([]string(nil), summary.Topics...),
			"updated_at":         normalizeTime(summary.UpdatedAt),
			"retained_event_ids": retainedEventIDs(events, summary, filterKey, physicalToLogical),
		}
		if boundary := summary.CutoffBoundary(); boundary != nil {
			lastEventID := boundary.LastEventID
			cutoffAt := normalizeTimeOffset(boundary.CutoffAt, sess.CreatedAt)
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

func validateSummaryStrings(summary *session.Summary, filterKey string) error {
	if err := validateUTF8String(fmt.Sprintf("summary %q text", filterKey), summary.Summary); err != nil {
		return err
	}
	for index, topic := range summary.Topics {
		if err := validateUTF8String(
			fmt.Sprintf("summary %q topic %d", filterKey, index),
			topic,
		); err != nil {
			return err
		}
	}
	if summary.Boundary == nil {
		return nil
	}
	if err := validateUTF8String(
		fmt.Sprintf("summary %q boundary filter key", filterKey),
		summary.Boundary.FilterKey,
	); err != nil {
		return err
	}
	return validateUTF8String(
		fmt.Sprintf("summary %q boundary event id", filterKey),
		summary.Boundary.LastEventID,
	)
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
		if err := validateUTF8String("track name", string(trackName)); err != nil {
			return nil, err
		}
		if history == nil {
			output[string(trackName)] = nil
			continue
		}
		if history.Track != trackName {
			return nil, fmt.Errorf(
				"track %q contains history for %q",
				trackName,
				history.Track,
			)
		}
		events := make([]CanonicalMap, 0, len(history.Events))
		for index, trackEvent := range history.Events {
			if err := validateUTF8String(
				fmt.Sprintf("track %q event %d name", trackName, index),
				string(trackEvent.Track),
			); err != nil {
				return nil, err
			}
			if trackEvent.Track != trackName {
				return nil, fmt.Errorf(
					"track %q event %d belongs to %q",
					trackName,
					index,
					trackEvent.Track,
				)
			}
			var payload any
			if trackEvent.Payload != nil {
				if err := validateUTF8String(
					fmt.Sprintf("track %q event %d payload", trackName, index),
					string(trackEvent.Payload),
				); err != nil {
					return nil, err
				}
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
	if !utf8.Valid(raw) {
		return errors.New("json contains invalid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
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
	if !validJSONSurrogatePairs(raw) {
		return errors.New("json contains an unpaired UTF-16 surrogate escape")
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return consumeUniqueJSONValue(decoder)
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("json object key is not a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate json object key %q", key)
			}
			keys[key] = struct{}{}
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		return consumeJSONDelimiter(decoder, '}')
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		return consumeJSONDelimiter(decoder, ']')
	default:
		return fmt.Errorf("unexpected json delimiter %q", delimiter)
	}
}

func consumeJSONDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("unexpected json delimiter %q", token)
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
