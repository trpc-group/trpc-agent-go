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

func normalizeSession(
	caseName, backendName string,
	sess *session.Session,
	memories []*memory.Entry,
	unsupported []UnsupportedFeature,
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
	for i, evt := range events {
		if evt.ID != "" {
			s.RawEventIDs[evt.ID] = i
		}
		s.Events = append(s.Events, normalizeEvent(i, evt))
	}
	for k, v := range sess.SnapshotState() {
		s.State[k] = normalizeBytes(v)
	}
	s.Memories = normalizeMemories(memories)
	s.Summaries = normalizeSummaries(sess, s.RawEventIDs)
	s.Tracks = normalizeTracks(sess)
	return s
}

func normalizeEvent(index int, evt event.Event) NormalizedEvent {
	out := NormalizedEvent{
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

func normalizeBytes(v []byte) NormalizedValue {
	if v == nil {
		return NormalizedValue{Kind: "null"}
	}
	return NormalizedValue{Kind: "value", Value: canonicalJSONBytes(v)}
}

func normalizeMemories(entries []*memory.Entry) []NormalizedMemory {
	out := make([]NormalizedMemory, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		metadata := map[string]string{}
		if entry.Memory.Kind != "" {
			metadata["kind"] = string(entry.Memory.Kind)
		}
		if entry.Memory.EventTime != nil && !entry.Memory.EventTime.IsZero() {
			metadata["event_time"] = entry.Memory.EventTime.UTC().Format(time.RFC3339Nano)
		}
		if len(entry.Memory.Participants) > 0 {
			participants := append([]string{}, entry.Memory.Participants...)
			sort.Strings(participants)
			metadata["participants"] = strings.Join(participants, ",")
		}
		if entry.Memory.Location != "" {
			metadata["location"] = entry.Memory.Location
		}
		topics := append([]string{}, entry.Memory.Topics...)
		sort.Strings(topics)
		content := entry.Memory.Memory
		stable := stableID(entry.AppName, entry.UserID, content, strings.Join(topics, ","), metadata["kind"])
		out = append(out, NormalizedMemory{
			ID:        stable,
			StableID:  stable,
			Content:   content,
			Topics:    topics,
			Metadata:  metadata,
			Scope:     entry.AppName + "/" + entry.UserID,
			ScoreBand: scoreBand(entry.Score),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StableID == out[j].StableID {
			return out[i].Content < out[j].Content
		}
		return out[i].StableID < out[j].StableID
	})
	return out
}

func normalizeSummaries(sess *session.Session, eventIDs map[string]int) []NormalizedSummary {
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
				if idx, ok := eventIDs[boundary.LastEventID]; ok {
					cutoffRef = fmt.Sprintf("event[%d]", idx)
				} else {
					cutoffRef = "event[missing]"
				}
			}
		}
		topics := append([]string{}, sum.Topics...)
		sort.Strings(topics)
		updated := "unset"
		if !sum.UpdatedAt.IsZero() {
			updated = "set"
		}
		out = append(out, NormalizedSummary{
			FilterKey:      filterKey,
			Text:           sum.Summary,
			Version:        version,
			SessionID:      sess.ID,
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
				Index:   i,
				Type:    payloadType(payload),
				Payload: payload,
			})
		}
		out = append(out, track)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
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

func eventFromSpec(spec EventSpec) (*event.Event, error) {
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
	evt.Timestamp = deterministicEventTime(spec.LogicalID)
	evt.Branch = spec.Branch
	evt.Tag = spec.Tag
	evt.FilterKey = spec.FilterKey
	evt.StateDelta = stateDelta
	for k, v := range spec.Extensions {
		if err := event.SetExtension(evt, k, v); err != nil {
			return nil, fmt.Errorf("set extension %s: %w", k, err)
		}
	}
	return evt, nil
}

func deterministicEventTime(logicalID string) time.Time {
	sum := sha256.Sum256([]byte(logicalID))
	seconds := int64(sum[0])<<8 + int64(sum[1])
	return time.Unix(1700000000+seconds, 0).UTC()
}

func canonicalJSONBytes(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
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
