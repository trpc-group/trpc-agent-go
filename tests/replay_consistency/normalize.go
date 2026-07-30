// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replayconsistency

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// NormalizedSnapshot is the canonical, backend-agnostic representation of session state, memory, summaries, and tracks.
type NormalizedSnapshot struct {
	Events    []NormalizedEvent   `json:"events,omitempty"`
	State     []NormalizedState   `json:"state,omitempty"`
	Memories  []NormalizedMemory  `json:"memories,omitempty"`
	Summaries []NormalizedSummary `json:"summaries,omitempty"`
	Tracks    []NormalizedTrack   `json:"tracks,omitempty"`
}

// NormalizedEvent is the canonical representation of a session event for comparison.
type NormalizedEvent struct {
	Index      int            `json:"index"`
	ID         string         `json:"id,omitempty"`
	Author     string         `json:"author,omitempty"`
	Role       string         `json:"role,omitempty"`
	Content    string         `json:"content,omitempty"`
	ToolCall   string         `json:"tool_call,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolID     string         `json:"tool_id,omitempty"`
	Branch     string         `json:"branch,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	FilterKey  string         `json:"filter_key,omitempty"`
	StateDelta []NormalizedKV `json:"state_delta,omitempty"`
	Extensions []NormalizedKV `json:"extensions,omitempty"`
	Timestamp  string         `json:"timestamp,omitempty"`
	Object     string         `json:"object,omitempty"`
}

// NormalizedState holds a single state key-value pair with a deterministic value representation.
type NormalizedState struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// NormalizedMemory is the canonical representation of a memory entry for comparison.
type NormalizedMemory struct {
	ID        string   `json:"id"`
	AppName   string   `json:"app_name,omitempty"`
	UserID    string   `json:"user_id,omitempty"`
	Content   string   `json:"content,omitempty"`
	Topics    []string `json:"topics,omitempty"`
	Metadata  string   `json:"metadata,omitempty"`
	Score     string   `json:"score,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// NormalizedSummary is the canonical representation of a session summary for comparison.
type NormalizedSummary struct {
	SessionID string `json:"session_id,omitempty"`
	FilterKey string `json:"filter_key,omitempty"`
	Summary   string `json:"summary,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Boundary  string `json:"boundary,omitempty"`
	Version   string `json:"version,omitempty"`
}

// NormalizedTrack is the canonical representation of a track event for comparison.
type NormalizedTrack struct {
	Track     string `json:"track"`
	Timestamp string `json:"timestamp,omitempty"`
	Payload   string `json:"payload,omitempty"`
	Type      string `json:"type,omitempty"`
}

// NormalizedKV is a sorted, deterministic key-value pair used within normalized structures.
type NormalizedKV struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// NormalizeSnapshot sorts all collections within a snapshot into a deterministic order.
func NormalizeSnapshot(snapshot NormalizedSnapshot) NormalizedSnapshot {
	snapshot.Events = append([]NormalizedEvent(nil), snapshot.Events...)
	snapshot.State = append([]NormalizedState(nil), snapshot.State...)
	snapshot.Memories = append([]NormalizedMemory(nil), snapshot.Memories...)
	snapshot.Summaries = append([]NormalizedSummary(nil), snapshot.Summaries...)
	snapshot.Tracks = append([]NormalizedTrack(nil), snapshot.Tracks...)

	sort.SliceStable(snapshot.Events, func(i, j int) bool { return snapshot.Events[i].Index < snapshot.Events[j].Index })
	sort.SliceStable(snapshot.State, func(i, j int) bool { return snapshot.State[i].Key < snapshot.State[j].Key })
	sort.SliceStable(snapshot.Memories, func(i, j int) bool { return snapshot.Memories[i].ID < snapshot.Memories[j].ID })
	sort.SliceStable(snapshot.Summaries, func(i, j int) bool {
		if snapshot.Summaries[i].SessionID == snapshot.Summaries[j].SessionID {
			return snapshot.Summaries[i].FilterKey < snapshot.Summaries[j].FilterKey
		}
		return snapshot.Summaries[i].SessionID < snapshot.Summaries[j].SessionID
	})
	sort.SliceStable(snapshot.Tracks, func(i, j int) bool {
		if snapshot.Tracks[i].Track == snapshot.Tracks[j].Track {
			return snapshot.Tracks[i].Timestamp < snapshot.Tracks[j].Timestamp
		}
		return snapshot.Tracks[i].Track < snapshot.Tracks[j].Track
	})
	return snapshot
}

// NormalizeEvent converts a raw event.Event into its canonical NormalizedEvent form.
func NormalizeEvent(evt *event.Event) NormalizedEvent {
	if evt == nil {
		return NormalizedEvent{}
	}
	choice := firstChoice(evt)
	out := NormalizedEvent{
		ID:        evt.ID,
		Author:    evt.Author,
		Branch:    evt.Branch,
		Tag:       evt.Tag,
		FilterKey: evt.FilterKey,
		Timestamp: evt.Timestamp.UTC().Format(timeLayout),
		Object:    choice.object,
	}
	if choice.message != "" {
		out.Role = choice.role
		out.Content = choice.message
	}
	if choice.toolName != "" {
		out.ToolName = choice.toolName
	}
	if choice.toolID != "" {
		out.ToolID = choice.toolID
	}
	if choice.toolCall != "" {
		out.ToolCall = choice.toolCall
	}
	if len(evt.StateDelta) > 0 {
		out.StateDelta = normalizeMap(evt.StateDelta)
	}
	if len(evt.Extensions) > 0 {
		out.Extensions = normalizeRawMap(evt.Extensions)
	}
	return out
}

// NormalizeState converts a session.StateMap into a sorted slice of NormalizedState.
func NormalizeState(state session.StateMap) []NormalizedState {
	if len(state) == 0 {
		return nil
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]NormalizedState, 0, len(keys))
	for _, key := range keys {
		out = append(out, NormalizedState{Key: key, Value: normalizeBytes(state[key])})
	}
	return out
}

// NormalizeMemoryEntry converts a raw memory.Entry into its canonical NormalizedMemory form.
func NormalizeMemoryEntry(entry *memory.Entry) NormalizedMemory {
	if entry == nil {
		return NormalizedMemory{}
	}
	out := NormalizedMemory{
		ID:        entry.ID,
		AppName:   entry.AppName,
		UserID:    entry.UserID,
		UpdatedAt: entry.UpdatedAt.UTC().Format(timeLayout),
		Score:     stableFloat(entry.Score),
	}
	if entry.Memory != nil {
		out.Content = entry.Memory.Memory
		out.Topics = append([]string(nil), entry.Memory.Topics...)
		sort.Strings(out.Topics)
		out.Metadata = normalizeMemoryMetadata(entry.Memory)
	}
	return out
}

// NormalizeSummary converts a raw session.Summary into its canonical NormalizedSummary form.
func NormalizeSummary(sessionID string, filterKey string, sum *session.Summary) NormalizedSummary {
	if sum == nil {
		return NormalizedSummary{SessionID: sessionID, FilterKey: filterKey}
	}
	out := NormalizedSummary{
		SessionID: sessionID,
		FilterKey: filterKey,
		Summary:   sum.Summary,
		UpdatedAt: sum.UpdatedAt.UTC().Format(timeLayout),
		Version:   fmt.Sprintf("%d", summaryBoundaryVersion(sum)),
	}
	if boundary := sum.CutoffBoundary(); boundary != nil {
		out.Boundary = fmt.Sprintf("%d|%s|%s|%s", boundary.Version, boundary.FilterKey, boundary.CutoffAt.UTC().Format(timeLayout), boundary.LastEventID)
	}
	return out
}

// NormalizeTrackEvent converts a raw session.TrackEvent into its canonical NormalizedTrack form.
func NormalizeTrackEvent(trackEvent *session.TrackEvent) NormalizedTrack {
	if trackEvent == nil {
		return NormalizedTrack{}
	}
	return NormalizedTrack{
		Track:     string(trackEvent.Track),
		Timestamp: trackEvent.Timestamp.UTC().Format(timeLayout),
		Payload:   canonicalizeRawJSON(trackEvent.Payload),
	}
}

func normalizeMap(state map[string][]byte) []NormalizedKV {
	if len(state) == 0 {
		return nil
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]NormalizedKV, 0, len(keys))
	for _, key := range keys {
		out = append(out, NormalizedKV{Key: key, Value: normalizeBytes(state[key])})
	}
	return out
}

func normalizeRawMap(state map[string]json.RawMessage) []NormalizedKV {
	if len(state) == 0 {
		return nil
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]NormalizedKV, 0, len(keys))
	for _, key := range keys {
		out = append(out, NormalizedKV{Key: key, Value: canonicalizeRawJSON(state[key])})
	}
	return out
}

func normalizeBytes(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if json.Valid(raw) {
		return canonicalizeRawJSON(raw)
	}
	if utf8.Valid(raw) {
		text := string(raw)
		if isMostlyPrintable(text) {
			return text
		}
	}
	return "hex:" + hex.EncodeToString(raw)
}

func canonicalizeRawJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

func stableFloat(value float64) string {
	return fmt.Sprintf("%.4f", value)
}

func isMostlyPrintable(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

type choiceView struct {
	object   string
	role     string
	message  string
	toolName string
	toolID   string
	toolCall string
}

func firstChoice(evt *event.Event) choiceView {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return choiceView{object: evtObject(evt)}
	}
	choice := evt.Response.Choices[0]
	view := choiceView{object: evt.Response.Object}
	if choice.Message.Content != "" {
		view.role = string(choice.Message.Role)
		view.message = choice.Message.Content
		view.toolName = choice.Message.ToolName
		view.toolID = choice.Message.ToolID
		if len(choice.Message.ToolCalls) > 0 {
			view.toolCall = choice.Message.ToolCalls[0].Function.Name
		}
		return view
	}
	if choice.Delta.Content != "" {
		view.role = string(choice.Delta.Role)
		view.message = choice.Delta.Content
		view.toolName = choice.Delta.ToolName
		view.toolID = choice.Delta.ToolID
		if len(choice.Delta.ToolCalls) > 0 {
			view.toolCall = choice.Delta.ToolCalls[0].Function.Name
		}
	}
	return view
}

func evtObject(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	return evt.Response.Object
}

const timeLayout = time.RFC3339Nano

func summaryBoundaryVersion(sum *session.Summary) int {
	if sum == nil || sum.Boundary == nil {
		return 0
	}
	return sum.Boundary.Version
}

func normalizeMemoryMetadata(mem *memory.Memory) string {
	if mem == nil {
		return ""
	}
	type metadata struct {
		Kind         memory.Kind `json:"kind,omitempty"`
		EventTime    string      `json:"event_time,omitempty"`
		Participants []string    `json:"participants,omitempty"`
		Location     string      `json:"location,omitempty"`
	}
	value := metadata{Kind: mem.Kind, Location: mem.Location}
	if mem.EventTime != nil {
		value.EventTime = mem.EventTime.UTC().Format(timeLayout)
	}
	value.Participants = append([]string(nil), mem.Participants...)
	sort.Strings(value.Participants)
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
