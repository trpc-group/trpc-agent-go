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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var replayEventBaseTime = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

type normalizedEventRef struct {
	Index int
	Time  time.Time
}

func normalizeSession(
	caseName, backendName string,
	sess *session.Session,
	memories []*memory.Entry,
	unsupported []UnsupportedFeature,
	normalizeEventOrder bool,
	caseDef ReplayCase,
) *Snapshot {
	s := &Snapshot{
		Case:        caseName,
		Backend:     backendName,
		Unsupported: unsupported,
		State:       make(map[string]NormalizedValue),
		RawEventIDs: make(map[string]int),
	}
	if sess == nil {
		return s
	}
	s.SessionID = sess.ID
	s.AppName = sess.AppName
	s.UserID = sess.UserID
	events := sess.GetEvents()
	for _, evt := range events {
		s.EventOrder = append(s.EventOrder, stableEventOrderID(evt))
	}
	if normalizeEventOrder {
		sort.SliceStable(events, func(i, j int) bool {
			left := events[i].Timestamp
			right := events[j].Timestamp
			if left.Equal(right) {
				return events[i].ID < events[j].ID
			}
			return left.Before(right)
		})
	}
	eventRefs := make(map[string]normalizedEventRef, len(events))
	for i, evt := range events {
		if evt.ID != "" {
			s.RawEventIDs[evt.ID] = i
			eventRefs[evt.ID] = normalizedEventRef{Index: i, Time: evt.Timestamp}
		}
		s.Events = append(s.Events, normalizeEvent(i, evt))
	}
	for k, v := range sess.SnapshotState() {
		s.State[k] = normalizeStateValue(k, v)
	}
	memoryLogicalIDs := expectedMemoryLogicalIDs(caseDef)
	s.Memories = normalizeMemories(memories, memoryLogicalIDs)
	s.Summaries = normalizeSummaries(sess, eventRefs)
	s.Tracks = normalizeTracks(sess)
	return s
}

func normalizeEvent(index int, evt event.Event) NormalizedEvent {
	out := NormalizedEvent{
		ID:        stableEventOrderID(evt),
		Index:     index,
		Author:    evt.Author,
		Branch:    evt.Branch,
		Tag:       evt.Tag,
		FilterKey: evt.FilterKey,
	}
	if evt.Response != nil && len(evt.Response.Choices) > 0 {
		msg := evt.Response.Choices[0].Message
		if msg.Role == "" {
			msg = evt.Response.Choices[0].Delta
		}
		out.Role = msg.Role.String()
		out.Content = msg.Content
		out.ToolID = msg.ToolID
		out.ToolName = msg.ToolName
		for _, tc := range msg.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, NormalizedToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: canonicalJSONBytes(tc.Function.Arguments),
			})
		}
	}
	if len(evt.StateDelta) > 0 {
		out.StateDelta = make(map[string]NormalizedValue, len(evt.StateDelta))
		for k, v := range evt.StateDelta {
			out.StateDelta[k] = normalizeBytes(v)
		}
	}
	if len(evt.Extensions) > 0 {
		out.Extensions = make(map[string]string, len(evt.Extensions))
		for k, v := range evt.Extensions {
			out.Extensions[k] = canonicalJSONBytes(v)
		}
	}
	return out
}

func stableEventOrderID(evt event.Event) string {
	if evt.ID != "" {
		return evt.ID
	}
	return stableID(
		evt.InvocationID,
		evt.Author,
		evt.Branch,
		evt.Tag,
		evt.FilterKey,
		evt.Timestamp.UTC().Format(time.RFC3339Nano),
	)
}

func normalizeBytes(v []byte) NormalizedValue {
	if v == nil {
		return NormalizedValue{Kind: "null"}
	}
	return NormalizedValue{Kind: "value", Value: canonicalJSONBytes(v)}
}

func normalizeStateValue(key string, v []byte) NormalizedValue {
	if key != "tracks" || v == nil {
		return normalizeBytes(v)
	}
	var tracks []string
	if err := json.Unmarshal(v, &tracks); err != nil {
		return normalizeBytes(v)
	}
	sort.Strings(tracks)
	encoded, err := json.Marshal(tracks)
	if err != nil {
		return normalizeBytes(v)
	}
	return normalizeBytes(encoded)
}

func normalizeMemories(entries []*memory.Entry, logicalIDs map[string]string) []NormalizedMemory {
	out := make([]NormalizedMemory, 0, len(entries))
	for _, entry := range entries {
		normalized, ok := normalizeMemoryEntry(entry, logicalIDs)
		if ok {
			out = append(out, normalized)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StableID == out[j].StableID {
			return out[i].Content < out[j].Content
		}
		return out[i].StableID < out[j].StableID
	})
	return out
}

func normalizeMemoryEntry(entry *memory.Entry, logicalIDs map[string]string) (NormalizedMemory, bool) {
	if entry == nil || entry.Memory == nil {
		return NormalizedMemory{}, false
	}
	metadata := normalizedMemoryMetadata(
		entry.Memory.Kind,
		entry.Memory.EventTime,
		entry.Memory.Participants,
		entry.Memory.Location,
	)
	topics := append([]string{}, entry.Memory.Topics...)
	sort.Strings(topics)
	content := entry.Memory.Memory
	stable := stableMemoryID(entry.AppName, entry.UserID, content, topics, metadata)
	id := stable
	if logicalID := logicalIDs[stable]; logicalID != "" {
		id = logicalID
	}
	return NormalizedMemory{
		ID:        id,
		BackendID: entry.ID,
		StableID:  stable,
		Content:   content,
		Topics:    topics,
		Metadata:  metadata,
		Scope:     entry.AppName + "/" + entry.UserID,
		ScoreBand: scoreBand(entry.Score),
	}, true
}

func normalizeMemorySearchResults(entries []*memory.Entry) []NormalizedMemory {
	out := make([]NormalizedMemory, 0, len(entries))
	for _, entry := range entries {
		normalized, ok := normalizeMemoryEntry(entry, nil)
		if ok {
			out = append(out, normalized)
		}
	}
	clearMemoryScoreBands(out)
	sortNormalizedMemories(out)
	return out
}

func expectedMemoryLogicalIDs(c ReplayCase) map[string]string {
	out := map[string]string{}
	applyExpectedMemoryLogicalIDs(out, c.Key, c.Operations)
	return out
}

func applyExpectedMemoryLogicalIDs(out map[string]string, key session.Key, ops []Operation) {
	for _, op := range ops {
		switch op.Kind {
		case OpAddMemory, OpUpdateMemory:
			if op.Memory == nil || op.Memory.ID == "" {
				continue
			}
			stable := stableMemorySpecID(key, op.Memory)
			out[stable] = op.Memory.ID
		case OpDeleteMemory:
			if op.Memory == nil || op.Memory.ID == "" {
				continue
			}
			for stable, logicalID := range out {
				if logicalID == op.Memory.ID {
					delete(out, stable)
				}
			}
		case OpClearMemory:
			for stable := range out {
				delete(out, stable)
			}
		case OpConcurrent:
			applyExpectedMemoryLogicalIDs(out, key, op.Concurrent)
		}
	}
}

func stableMemorySpecID(key session.Key, spec *MemorySpec) string {
	if spec == nil {
		return ""
	}
	topics := append([]string{}, spec.Topics...)
	sort.Strings(topics)
	kind := string(memory.KindFact)
	if spec.Metadata != nil && spec.Metadata.Kind != "" {
		kind = string(spec.Metadata.Kind)
	}
	var eventTime *time.Time
	var participants []string
	var location string
	if spec.Metadata != nil {
		eventTime = spec.Metadata.EventTime
		participants = spec.Metadata.Participants
		location = spec.Metadata.Location
	}
	metadata := normalizedMemoryMetadata(memory.Kind(kind), eventTime, participants, location)
	return stableMemoryID(key.AppName, key.UserID, spec.Content, topics, metadata)
}

func normalizeMemoryQueries(
	ctx context.Context,
	svc memory.Service,
	key session.Key,
	queries []MemoryQuerySpec,
	caseDef ReplayCase,
) ([]NormalizedMemoryQuery, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	out := make([]NormalizedMemoryQuery, 0, len(queries))
	for _, query := range queries {
		entries, err := svc.SearchMemories(ctx, userKey(key), query.Query)
		if err != nil {
			return nil, err
		}
		results := normalizeMemorySearchResultsWithCase(entries, caseDef)
		if query.Limit > 0 && len(results) > query.Limit {
			results = results[:query.Limit]
		}
		out = append(out, NormalizedMemoryQuery{
			Name:    query.Name,
			Query:   query.Query,
			Results: results,
		})
	}
	return out, nil
}

func normalizeFileMemoryQueries(
	key session.Key,
	entries []*memory.Entry,
	queries []MemoryQuerySpec,
	caseDef ReplayCase,
) []NormalizedMemoryQuery {
	if len(queries) == 0 {
		return nil
	}
	out := make([]NormalizedMemoryQuery, 0, len(queries))
	for _, query := range queries {
		matched := make([]*memory.Entry, 0, len(entries))
		terms := strings.Fields(strings.ToLower(query.Query))
		for _, entry := range entries {
			if entry == nil || entry.Memory == nil {
				continue
			}
			if entry.AppName != key.AppName || entry.UserID != key.UserID {
				continue
			}
			haystack := strings.ToLower(entry.Memory.Memory + " " + strings.Join(entry.Memory.Topics, " "))
			ok := true
			for _, term := range terms {
				if !strings.Contains(haystack, term) {
					ok = false
					break
				}
			}
			if ok {
				cloned := *entry
				cloned.Score = 1
				matched = append(matched, &cloned)
			}
		}
		results := normalizeMemorySearchResultsWithCase(matched, caseDef)
		if query.Limit > 0 && len(results) > query.Limit {
			results = results[:query.Limit]
		}
		out = append(out, NormalizedMemoryQuery{
			Name:    query.Name,
			Query:   query.Query,
			Results: results,
		})
	}
	return out
}

func normalizeMemorySearchResultsWithCase(entries []*memory.Entry, caseDef ReplayCase) []NormalizedMemory {
	out := make([]NormalizedMemory, 0, len(entries))
	logicalIDs := expectedMemoryLogicalIDs(caseDef)
	for _, entry := range entries {
		normalized, ok := normalizeMemoryEntry(entry, logicalIDs)
		if ok {
			out = append(out, normalized)
		}
	}
	clearMemoryScoreBands(out)
	sortNormalizedMemories(out)
	return out
}

func clearMemoryScoreBands(results []NormalizedMemory) {
	for i := range results {
		results[i].ScoreBand = ""
	}
}

func sortNormalizedMemories(results []NormalizedMemory) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].StableID == results[j].StableID {
			if results[i].Content == results[j].Content {
				return results[i].ID < results[j].ID
			}
			return results[i].Content < results[j].Content
		}
		return results[i].StableID < results[j].StableID
	})
}

func normalizedMemoryMetadata(
	kind memory.Kind,
	eventTime *time.Time,
	participants []string,
	location string,
) map[string]string {
	metadata := map[string]string{}
	if kind != "" {
		metadata["kind"] = string(kind)
	}
	if eventTime != nil && !eventTime.IsZero() {
		metadata["event_time"] = eventTime.UTC().Format(time.RFC3339Nano)
	}
	if len(participants) > 0 {
		sortedParticipants := append([]string{}, participants...)
		sort.Strings(sortedParticipants)
		metadata["participants"] = strings.Join(sortedParticipants, ",")
	}
	if location != "" {
		metadata["location"] = location
	}
	return metadata
}

func stableMemoryID(
	appName string,
	userID string,
	content string,
	topics []string,
	metadata map[string]string,
) string {
	kind := metadata["kind"]
	if kind == "" {
		kind = string(memory.KindFact)
	}
	return stableID(
		appName,
		userID,
		content,
		strings.Join(topics, ","),
		kind,
		metadata["event_time"],
		metadata["participants"],
		metadata["location"],
	)
}

func normalizeSummaries(sess *session.Session, eventRefs map[string]normalizedEventRef) []NormalizedSummary {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	out := make([]NormalizedSummary, 0, len(sess.Summaries))
	for filterKey, sum := range sess.Summaries {
		if sum == nil {
			continue
		}
		boundary := sum.CutoffBoundary()
		version := 0
		cutoffRef := ""
		if boundary != nil {
			version = boundary.Version
			if boundary.LastEventID != "" {
				if ref, ok := eventRefs[boundary.LastEventID]; ok {
					cutoffRef = fmt.Sprintf("event[%d]", ref.Index)
				} else {
					cutoffRef = "event[missing]"
				}
			}
		}
		topics := append([]string{}, sum.Topics...)
		sort.Strings(topics)
		updated := normalizeSummaryUpdatedAt(sum.UpdatedAt, eventRefs)
		out = append(out, NormalizedSummary{
			FilterKey:      filterKey,
			Text:           sum.Summary,
			Version:        version,
			SessionID:      normalizeSummaryOwner(sum.Summary, sess.ID),
			Topics:         topics,
			UpdatedAt:      updated,
			CutoffEventRef: cutoffRef,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].FilterKey < out[j].FilterKey
	})
	return out
}

func normalizeSummaryUpdatedAt(updatedAt time.Time, eventRefs map[string]normalizedEventRef) string {
	if updatedAt.IsZero() {
		return "unset"
	}
	updatedAt = updatedAt.UTC()
	for _, ref := range eventRefs {
		if !ref.Time.IsZero() && ref.Time.UTC().Equal(updatedAt) {
			return fmt.Sprintf("event[%d]", ref.Index)
		}
	}
	return updatedAt.Format(time.RFC3339Nano)
}

func normalizeSummaryOwner(text, fallback string) string {
	for _, part := range strings.Split(text, " | ") {
		if owner, ok := strings.CutPrefix(part, "session="); ok && owner != "" {
			return owner
		}
	}
	return fallback
}

func normalizeTracks(sess *session.Session) []NormalizedTrack {
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	out := make([]NormalizedTrack, 0, len(sess.Tracks))
	for name, history := range sess.Tracks {
		if history == nil {
			continue
		}
		track := NormalizedTrack{Name: string(name)}
		for i, evt := range history.Events {
			payload := normalizeTrackPayload(evt.Payload)
			track.Events = append(track.Events, NormalizedTrackEvent{
				Index:     i,
				Type:      payloadType(payload),
				Timestamp: normalizeTrackTimestamp(evt.Timestamp),
				Payload:   payload,
			})
		}
		out = append(out, track)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func normalizeTrackTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return "unset"
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

func normalizeTrackPayload(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(bytes.TrimSpace(raw))
	}
	v = scrubVolatileNumbers(v, "")
	return canonicalJSON(v)
}

func scrubVolatileNumbers(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, child := range x {
			out[k] = scrubVolatileNumbers(child, k)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = scrubVolatileNumbers(child, key)
		}
		return out
	case float64:
		lower := strings.ToLower(key)
		if strings.Contains(lower, "duration") ||
			strings.Contains(lower, "elapsed") ||
			strings.Contains(lower, "latency") {
			return "<duration>"
		}
		return math.Round(x*1000) / 1000
	default:
		return v
	}
}

func payloadType(payload string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return ""
	}
	if typ, ok := m["type"].(string); ok {
		return typ
	}
	if typ, ok := m["event_type"].(string); ok {
		return typ
	}
	return ""
}

func eventFromSpec(spec EventSpec, sequence int) (*event.Event, error) {
	msg := model.Message{
		Role:     spec.Role,
		Content:  spec.Content,
		ToolID:   spec.ToolID,
		ToolName: spec.ToolName,
	}
	for _, tc := range spec.ToolCalls {
		args, err := json.Marshal(tc.Arguments)
		if err != nil {
			return nil, fmt.Errorf("marshal tool args: %w", err)
		}
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			Type: "function",
			ID:   tc.ID,
			Function: model.FunctionDefinitionParam{
				Name:      tc.Name,
				Arguments: args,
			},
		})
	}
	object := spec.Object
	if object == "" {
		object = model.ObjectTypeChatCompletion
	}
	rsp := &model.Response{
		ID:      "rsp-" + spec.LogicalID,
		Object:  object,
		Created: 1700000000,
		Model:   "replay-deterministic",
		Done:    spec.Done,
		Choices: []model.Choice{{Index: 0, Message: msg}},
	}
	stateDelta := make(map[string][]byte, len(spec.StateDelta))
	for k, v := range spec.StateDelta {
		stateDelta[k] = append([]byte(nil), v...)
	}
	evt := event.NewResponseEvent(spec.InvocationID, spec.Author, rsp)
	evt.ID = "event-" + spec.LogicalID
	evt.Timestamp = deterministicEventTime(sequence)
	evt.Branch = spec.Branch
	evt.Tag = spec.Tag
	evt.FilterKey = spec.FilterKey
	evt.StateDelta = stateDelta
	evt.IsPartial = spec.Partial
	for k, v := range spec.Extensions {
		if err := event.SetExtension(evt, k, v); err != nil {
			return nil, fmt.Errorf("set extension %s: %w", k, err)
		}
	}
	return evt, nil
}

func deterministicEventTime(sequence int) time.Time {
	return replayEventBaseTime.Add(time.Duration(sequence) * time.Second)
}

func canonicalJSONBytes(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return string(bytes.TrimSpace(raw))
	}
	return canonicalJSON(v)
}

func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func stableID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func scoreBand(score float64) string {
	if score == 0 {
		return ""
	}
	switch {
	case score >= 0.95:
		return "0.95-1.00"
	case score >= 0.80:
		return "0.80-0.95"
	case score >= 0.50:
		return "0.50-0.80"
	default:
		return "0.00-0.50"
	}
}
