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
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Normalize reads a backend and returns its canonical replay snapshot.
func Normalize(
	ctx context.Context,
	backend Backend,
	cfg RunConfig,
	tc ReplayCase,
) (Snapshot, error) {
	key := session.Key{AppName: cfg.AppName, UserID: cfg.UserID, SessionID: cfg.SessionID}
	var opts []session.Option
	if tc.SnapshotEventNum > 0 {
		opts = append(opts, session.WithEventNum(tc.SnapshotEventNum))
	}
	sess, err := backend.Session.GetSession(ctx, key, opts...)
	if err != nil {
		return Snapshot{}, err
	}
	if sess == nil {
		return Snapshot{}, fmt.Errorf("session %s not found", cfg.SessionID)
	}

	idMap := eventIDMap(sess.Events)
	snap := Snapshot{
		CaseName:     tc.Name,
		Backend:      backend.Name,
		SessionID:    sess.ID,
		Events:       normalizeEvents(sess.Events),
		State:        normalizeState(sess.SnapshotState()),
		Summaries:    normalizeSummaries(sess.ID, sess.Summaries, idMap, summaryOwners(ctx, backend, key)),
		Tracks:       normalizeTracks(sess.Tracks),
		Capabilities: cloneCapabilities(backend.Capabilities),
		Unsupported:  unsupportedCapabilities(backend),
	}

	if backend.Memory != nil {
		memories, err := backend.Memory.ReadMemories(
			ctx,
			memory.UserKey{AppName: cfg.AppName, UserID: cfg.UserID},
			0,
		)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Memories = normalizeMemories(memories)
		searches, err := normalizeMemorySearches(ctx, backend.Memory, cfg, tc.MemorySearchQueries)
		if err != nil {
			return Snapshot{}, err
		}
		snap.MemorySearches = searches
	}
	return snap, nil
}

func eventIDMap(events []event.Event) map[string]string {
	out := make(map[string]string, len(events))
	for i, evt := range events {
		if evt.ID != "" {
			out[evt.ID] = fmt.Sprintf("event#%d", i)
		}
	}
	return out
}

func normalizeEvents(events []event.Event) []NormalizedEvent {
	out := make([]NormalizedEvent, 0, len(events))
	for i, evt := range events {
		normalized := NormalizedEvent{
			Index:      i,
			Author:     evt.Author,
			Branch:     evt.Branch,
			Tag:        normalizeTag(evt.Tag),
			FilterKey:  evt.FilterKey,
			StateDelta: normalizeState(evt.StateDelta),
			Extensions: normalizeRawMap(evt.Extensions),
		}
		msg, ok := eventMessage(evt)
		if ok {
			normalized.Role = string(msg.Role)
			normalized.Content = msg.Content
			normalized.ToolCalls = normalizeToolCalls(msg.ToolCalls)
			if msg.ToolID != "" || msg.ToolName != "" {
				normalized.ToolResponse = &NormalizedToolResponse{
					ToolID:   msg.ToolID,
					ToolName: msg.ToolName,
					Content:  msg.Content,
				}
			}
		}
		out = append(out, normalized)
	}
	return out
}

func eventMessage(evt event.Event) (model.Message, bool) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return model.Message{}, false
	}
	choice := evt.Response.Choices[0]
	if choice.Message.Role != "" || choice.Message.Content != "" ||
		len(choice.Message.ToolCalls) > 0 || choice.Message.ToolID != "" {
		return choice.Message, true
	}
	return choice.Delta, true
}

func normalizeToolCalls(toolCalls []model.ToolCall) []NormalizedToolCall {
	out := make([]NormalizedToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		out = append(out, NormalizedToolCall{
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: normalizeBytes(tc.Function.Arguments),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeState(state session.StateMap) map[string]NormalizedValue {
	if len(state) == 0 {
		return nil
	}
	out := make(map[string]NormalizedValue, len(state))
	for k, v := range state {
		out[k] = normalizeBytes(v)
	}
	return out
}

func normalizeRawMap(raw map[string]json.RawMessage) map[string]NormalizedValue {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]NormalizedValue, len(raw))
	for k, v := range raw {
		out[k] = normalizeBytes(v)
	}
	return out
}

func normalizeBytes(raw []byte) NormalizedValue {
	if raw == nil {
		return NormalizedValue{Value: nil}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return NormalizedValue{Value: ""}
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&v); err == nil {
		return NormalizedValue{Value: normalizeAny(v)}
	}
	return NormalizedValue{Value: string(raw)}
}

func normalizeAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalizeAny(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeAny(v)
		}
		return out
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return roundFloat(f)
		}
		return x.String()
	case float64:
		return roundFloat(x)
	default:
		return x
	}
}

func roundFloat(f float64) float64 {
	return math.Round(f*1_000_000) / 1_000_000
}

func normalizeMemories(entries []*memory.Entry) []NormalizedMemory {
	return normalizeMemoryEntries(entries, true)
}

func normalizeMemoryEntries(entries []*memory.Entry, sortEntries bool) []NormalizedMemory {
	out := make([]NormalizedMemory, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		mem := NormalizedMemory{
			ID:       normalizeMemoryID(entry),
			Content:  entry.Memory.Memory,
			Topics:   sortedStrings(entry.Memory.Topics),
			Metadata: normalizeMemoryMetadata(entry.Memory),
			Scope:    entry.AppName + "/" + entry.UserID,
		}
		if entry.Score != 0 {
			score := roundFloat(entry.Score)
			mem.Score = &score
		}
		out = append(out, mem)
	}
	if sortEntries {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Content == out[j].Content {
				return out[i].ID < out[j].ID
			}
			return out[i].Content < out[j].Content
		})
	}
	return out
}

func normalizeMemoryID(entry *memory.Entry) string {
	if entry == nil || entry.Memory == nil {
		return ""
	}
	canonical := struct {
		Scope    string                     `json:"scope"`
		Content  string                     `json:"content"`
		Topics   []string                   `json:"topics,omitempty"`
		Metadata map[string]NormalizedValue `json:"metadata,omitempty"`
	}{
		Scope:    entry.AppName + "/" + entry.UserID,
		Content:  entry.Memory.Memory,
		Topics:   sortedStrings(entry.Memory.Topics),
		Metadata: normalizeMemoryMetadata(entry.Memory),
	}
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("memory#%x", sum[:6])
}

func normalizeMemorySearches(
	ctx context.Context,
	svc memory.Service,
	cfg RunConfig,
	queries []string,
) (map[string][]NormalizedMemory, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	out := make(map[string][]NormalizedMemory, len(queries))
	userKey := memory.UserKey{AppName: cfg.AppName, UserID: cfg.UserID}
	for _, query := range queries {
		entries, err := svc.SearchMemories(ctx, userKey, query)
		if err != nil {
			return nil, err
		}
		out[query] = normalizeMemoryEntries(entries, false)
	}
	return out, nil
}

func normalizeMemoryMetadata(mem *memory.Memory) map[string]NormalizedValue {
	out := map[string]NormalizedValue{}
	if mem.Kind != "" {
		out["kind"] = NormalizedValue{Value: string(mem.Kind)}
	}
	if mem.EventTime != nil {
		out["event_time"] = NormalizedValue{Value: normalizeTime(*mem.EventTime)}
	}
	if len(mem.Participants) > 0 {
		out["participants"] = NormalizedValue{Value: sortedStrings(mem.Participants)}
	}
	if mem.Location != "" {
		out["location"] = NormalizedValue{Value: mem.Location}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeSummaries(
	sessionID string,
	summaries map[string]*session.Summary,
	idMap map[string]string,
	ownerIDs map[string]string,
) map[string]NormalizedSummary {
	if len(summaries) == 0 {
		return nil
	}
	out := make(map[string]NormalizedSummary, len(summaries))
	for filterKey, sum := range summaries {
		if sum == nil {
			continue
		}
		ownerID := sessionID
		if ownerIDs != nil {
			if owner, ok := ownerIDs[filterKey]; ok {
				ownerID = owner
			}
		}
		normalized := NormalizedSummary{
			FilterKey: filterKey,
			Text:      sum.Summary,
			SessionID: ownerID,
			UpdatedAt: normalizeTime(sum.UpdatedAt),
		}
		if sum.Boundary != nil {
			lastEventID := sum.Boundary.LastEventID
			if mapped, ok := idMap[lastEventID]; ok {
				lastEventID = mapped
			}
			normalized.Version = sum.Boundary.Version
			normalized.Boundary = &NormalizedSummaryBoundary{
				Version:     sum.Boundary.Version,
				FilterKey:   sum.Boundary.FilterKey,
				CutoffAt:    normalizeTime(sum.Boundary.CutoffAt),
				LastEventID: lastEventID,
			}
		}
		out[filterKey] = normalized
	}
	return out
}

func summaryOwners(ctx context.Context, backend Backend, key session.Key) map[string]string {
	provider, ok := backend.Session.(SummaryOwnerProvider)
	if !ok {
		return nil
	}
	owners, err := provider.SummaryOwnerIDs(ctx, key)
	if err != nil {
		return nil
	}
	return owners
}

func normalizeTracks(tracks map[session.Track]*session.TrackEvents) map[string][]NormalizedTrack {
	if len(tracks) == 0 {
		return nil
	}
	out := make(map[string][]NormalizedTrack, len(tracks))
	for track, history := range tracks {
		if history == nil {
			continue
		}
		events := make([]NormalizedTrack, 0, len(history.Events))
		for i, evt := range history.Events {
			payload := normalizeBytes(evt.Payload)
			events = append(events, NormalizedTrack{
				Index:      i,
				TrackName:  string(track),
				Timestamp:  normalizeTime(evt.Timestamp),
				EventType:  payloadString(payload, "event_type"),
				Invocation: payloadString(payload, "invocation"),
				Error:      payloadString(payload, "error"),
				DurationMs: payloadFloat(payload, "duration_ms", "durationMs", "elapsed_ms"),
				Payload:    payload,
			})
		}
		out[string(track)] = events
	}
	return out
}

func payloadString(v NormalizedValue, key string) string {
	m, ok := v.Value.(map[string]any)
	if !ok {
		return ""
	}
	value, _ := m[key].(string)
	return value
}

func payloadFloat(v NormalizedValue, keys ...string) *float64 {
	m, ok := v.Value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range keys {
		switch x := m[key].(type) {
		case float64:
			y := roundFloat(x)
			return &y
		case int64:
			y := float64(x)
			return &y
		case int:
			y := float64(x)
			return &y
		}
	}
	return nil
}

func normalizeTag(tag string) string {
	if tag == "" {
		return ""
	}
	parts := bytes.Split([]byte(tag), []byte(event.TagDelimiter))
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := string(bytes.TrimSpace(part)); trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	sort.Strings(tags)
	out, _ := json.Marshal(tags)
	return string(out)
}

func normalizeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func sortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func cloneCapabilities(in map[string]CapabilityStatus) map[string]CapabilityStatus {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]CapabilityStatus, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func unsupportedCapabilities(backend Backend) []UnsupportedCapability {
	caps := cloneCapabilities(backend.Capabilities)
	if caps == nil {
		caps = make(map[string]CapabilityStatus)
	}
	if backend.Memory == nil {
		caps[CapabilityMemory] = CapabilityStatus{
			Supported:   false,
			AllowedDiff: true,
			Explanation: "memory service is not configured",
		}
	}
	if _, ok := backend.Session.(session.TrackService); !ok {
		caps[CapabilityTrack] = CapabilityStatus{
			Supported:   false,
			AllowedDiff: true,
			Explanation: "session service does not implement session.TrackService",
		}
	}
	out := []UnsupportedCapability{}
	for name, status := range caps {
		if status.Supported {
			continue
		}
		out = append(out, UnsupportedCapability{
			Capability:  name,
			AllowedDiff: status.AllowedDiff,
			Explanation: status.Explanation,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Capability < out[j].Capability
	})
	return out
}
