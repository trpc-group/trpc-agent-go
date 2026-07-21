//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Canonical is the normalized form of a Snapshot. Two backends are
// consistent iff their Canonical forms are equal outside the allowed-diff
// whitelist. All fields are JSON-marshable so a Canonical can be cloned via
// a JSON round trip (used by the mutation tests).
type Canonical struct {
	Backend string `json:"backend"`
	Case    string `json:"case"`

	Sessions  []*CSession       `json:"sessions,omitempty"`
	AppState  map[string]string `json:"app_state,omitempty"`
	UserState map[string]string `json:"user_state,omitempty"`

	// Memories are sorted by (user, content) — contents are unique per case
	// within one user scope — so the list doubles as a set keyed by scope;
	// the backend's returned order is kept in CMemory.Order and reported as
	// an allowed diff when it differs, since listing order is
	// implementation-dependent (timestamp precision).
	Memories    []*CMemory `json:"memories,omitempty"`
	Search      []*CMemory `json:"search,omitempty"`
	SearchQuery string     `json:"search_query,omitempty"`

	Errors      []CError `json:"errors,omitempty"`
	Unsupported []string `json:"unsupported,omitempty"`
}

// CSession is the normalized form of one session.
type CSession struct {
	SessionID string               `json:"session_id"`
	Events    []*CEvent            `json:"events,omitempty"`
	State     map[string]string    `json:"state,omitempty"`
	Summaries map[string]*CSummary `json:"summaries,omitempty"`
	Tracks    map[string][]string  `json:"tracks,omitempty"`
}

// CEvent is the normalized form of one event.
type CEvent struct {
	ID                 string            `json:"id"` // symbolized evt#N
	InvocationID       string            `json:"invocation_id,omitempty"`
	ParentInvocationID string            `json:"parent_invocation_id,omitempty"`
	RequestID          string            `json:"request_id,omitempty"`
	Author             string            `json:"author"`
	Role               string            `json:"role"`
	Content            string            `json:"content"`
	ToolCalls          []*CToolCall      `json:"tool_calls,omitempty"`
	ToolID             string            `json:"tool_id,omitempty"` // symbolized call#N
	ToolName           string            `json:"tool_name,omitempty"`
	Branch             string            `json:"branch,omitempty"`
	Tag                string            `json:"tag,omitempty"`
	FilterKey          string            `json:"filter_key,omitempty"`
	FinishReason       string            `json:"finish_reason,omitempty"`
	StateDelta         map[string]string `json:"state_delta,omitempty"`
	Extensions         map[string]string `json:"extensions,omitempty"`
	LongRunning        []string          `json:"long_running,omitempty"`
}

// CToolCall is the normalized form of one tool call.
type CToolCall struct {
	ID   string `json:"id"` // symbolized call#N
	Type string `json:"type"`
	Name string `json:"name"`
	Args string `json:"args"` // canonical JSON
}

// CSummary is the normalized form of one filter-key summary.
type CSummary struct {
	Text         string   `json:"text"`
	Topics       []string `json:"topics,omitempty"`
	Version      int      `json:"version"`
	FilterKey    string   `json:"filter_key"`
	LastEventID  string   `json:"last_event_id,omitempty"` // symbolized evt#N
	HasUpdatedAt bool     `json:"has_updated_at"`
}

// CMemory is the normalized form of one memory entry.
type CMemory struct {
	UserID  string   `json:"user_id"`
	ID      string   `json:"id"` // symbolized mem#N (sorted user/content order)
	Content string   `json:"content"`
	Topics  []string `json:"topics,omitempty"`
	Meta    string   `json:"meta"` // canonical JSON of episodic fields
	// Order is the entry's position in the backend's returned listing.
	Order int `json:"order"`
}

// CError is the normalized form of one error record.
type CError struct {
	Step  int    `json:"step"`
	Class string `json:"class"`
}

// scrubbedPayloadKeys are JSON object keys whose values are timing or
// otherwise backend-dependent; they are replaced by "*" during
// canonicalization (documented allowed-diff).
var scrubbedPayloadKeys = map[string]bool{
	"duration_ms": true,
	"durationMs":  true,
	"latency_ms":  true,
	"latencyMs":   true,
	"elapsed_ms":  true,
}

// isScrubbedPayloadKey reports whether a JSON object key carries a timing
// value that must be scrubbed during canonicalization: one of the exact
// keys above, any key ending in "_ms" (e.g. "cost_ms"), or a key whose
// lowercase form is "duration", "latency" or "elapsed" (e.g. "Duration").
// Unrelated keys such as "timeout" are not scrubbed.
func isScrubbedPayloadKey(key string) bool {
	if scrubbedPayloadKeys[key] {
		return true
	}
	if strings.HasSuffix(key, "_ms") {
		return true
	}
	switch strings.ToLower(key) {
	case "duration", "latency", "elapsed":
		return true
	}
	return false
}

// Normalize converts a raw snapshot into its canonical form.
func Normalize(snap *Snapshot) *Canonical {
	c := &Canonical{
		Backend:     snap.Backend,
		Case:        snap.Case,
		SearchQuery: snap.SearchQuery,
	}
	invSym := newSymbolizer("inv")
	for _, ss := range snap.Sessions {
		for i := range ss.Events {
			invSym.preload(ss.Events[i].InvocationID)
			invSym.preload(ss.Events[i].ParentInvocationID)
		}
	}
	invSym.freeze()
	for _, ss := range snap.Sessions {
		c.Sessions = append(c.Sessions, normalizeSession(ss, invSym))
	}
	c.AppState = canonicalizeRawMap(snap.AppState)
	c.UserState = canonicalizeRawMap(snap.UserState)
	c.Memories = normalizeMemories(snap.Memories)
	c.Search = normalizeMemories(snap.Search)
	for _, e := range snap.Errors {
		c.Errors = append(c.Errors, CError{Step: e.Step, Class: e.Class})
	}
	c.Unsupported = append([]string(nil), snap.Unsupported...)
	sort.Strings(c.Unsupported)
	return c
}

// normalizeSession normalizes one session.
func normalizeSession(ss *SessionSnap, invSym *symbolizer) *CSession {
	cs := &CSession{SessionID: ss.SessionID}

	evtSym := newSymbolizer("evt")
	callSym := newSymbolizer("call")
	for i := range ss.Events {
		evtSym.preload(ss.Events[i].ID)
		if ss.Events[i].Response != nil {
			for _, ch := range ss.Events[i].Response.Choices {
				for _, tc := range ch.Message.ToolCalls {
					callSym.preload(tc.ID)
				}
			}
		}
	}
	evtSym.freeze()
	callSym.freeze()

	for i := range ss.Events {
		cs.Events = append(cs.Events, normalizeEvent(&ss.Events[i], evtSym, invSym, callSym))
	}

	cs.State = canonicalizeRawMap(ss.State)
	cs.Summaries = normalizeSummaries(ss.Summaries, evtSym)
	cs.Tracks = normalizeTracks(ss.Tracks)
	return cs
}

// normalizeEvent normalizes one event.
func normalizeEvent(e *event.Event, evtSym, invSym, callSym *symbolizer) *CEvent {
	ce := &CEvent{
		ID:                 evtSym.sym(e.ID),
		InvocationID:       invSym.sym(e.InvocationID),
		ParentInvocationID: invSym.sym(e.ParentInvocationID),
		RequestID:          e.RequestID,
		Author:             e.Author,
		Branch:             e.Branch,
		Tag:                e.Tag,
		FilterKey:          e.FilterKey,
	}
	if r := e.Response; r != nil && len(r.Choices) > 0 {
		msg := r.Choices[0].Message
		ce.Role = string(msg.Role)
		ce.Content = msg.Content
		ce.ToolName = msg.ToolName
		if msg.ToolID != "" {
			ce.ToolID = callSym.sym(msg.ToolID)
		}
		for _, tc := range msg.ToolCalls {
			ce.ToolCalls = append(ce.ToolCalls, &CToolCall{
				ID:   callSym.sym(tc.ID),
				Type: tc.Type,
				Name: tc.Function.Name,
				Args: canonicalJSON(tc.Function.Arguments),
			})
		}
		if r.Choices[0].FinishReason != nil {
			ce.FinishReason = *r.Choices[0].FinishReason
		}
	}
	ce.StateDelta = canonicalizeStateDelta(e.StateDelta)
	ce.Extensions = canonicalizeRawMap(e.Extensions)
	for id := range e.LongRunningToolIDs {
		ce.LongRunning = append(ce.LongRunning, id)
	}
	sort.Strings(ce.LongRunning)
	return ce
}

// normalizeSummaries normalizes the filter-keyed summary map.
func normalizeSummaries(in map[string]*session.Summary, evtSym *symbolizer) map[string]*CSummary {
	if in == nil {
		return nil
	}
	out := make(map[string]*CSummary, len(in))
	for fk, s := range in {
		if s == nil {
			continue
		}
		cs := &CSummary{
			Text:         s.Summary,
			Topics:       append([]string(nil), s.Topics...),
			HasUpdatedAt: !s.UpdatedAt.IsZero(),
			FilterKey:    fk,
		}
		if s.Boundary != nil {
			cs.Version = s.Boundary.Version
			if s.Boundary.FilterKey != "" {
				cs.FilterKey = s.Boundary.FilterKey
			}
			cs.LastEventID = evtSym.sym(s.Boundary.LastEventID)
		}
		sort.Strings(cs.Topics)
		out[fk] = cs
	}
	return out
}

// normalizeTracks normalizes tracks to ordered canonical payload lists.
func normalizeTracks(in map[string]*session.TrackEvents) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, te := range in {
		if te == nil {
			continue
		}
		for _, ev := range te.Events {
			out[name] = append(out[name], canonicalJSON(ev.Payload))
		}
	}
	return out
}

// normalizeMemories normalizes memories into a (user, content) sorted
// list. The original listing position is preserved in CMemory.Order so the
// differ can report return-order differences as an allowed note.
func normalizeMemories(in []*MemorySnap) []*CMemory {
	type indexed struct {
		snap *MemorySnap
		pos  int
	}
	wrapped := make([]indexed, 0, len(in))
	for i, m := range in {
		wrapped = append(wrapped, indexed{snap: m, pos: i})
	}
	sort.SliceStable(wrapped, func(i, j int) bool {
		if wrapped[i].snap.UserID != wrapped[j].snap.UserID {
			return wrapped[i].snap.UserID < wrapped[j].snap.UserID
		}
		if wrapped[i].snap.Content != wrapped[j].snap.Content {
			return wrapped[i].snap.Content < wrapped[j].snap.Content
		}
		return wrapped[i].snap.ID < wrapped[j].snap.ID
	})
	var out []*CMemory
	for i, w := range wrapped {
		m := w.snap
		meta := map[string]any{
			"kind":         m.Kind,
			"participants": m.Participants,
			"location":     m.Location,
			"event_time":   m.EventTime,
		}
		out = append(out, &CMemory{
			UserID:  m.UserID,
			ID:      fmt.Sprintf("mem#%d", i+1),
			Content: m.Content,
			Topics:  m.Topics,
			Meta:    canonicalValue(meta),
			Order:   w.pos,
		})
	}
	return out
}

// symbolizer maps raw IDs to stable symbolic names. IDs are assigned in
// sorted-raw order so the mapping is independent of event interleaving.
type symbolizer struct {
	prefix string
	raw    []string
	m      map[string]string
}

// newSymbolizer creates a symbolizer.
func newSymbolizer(prefix string) *symbolizer {
	return &symbolizer{prefix: prefix, m: make(map[string]string)}
}

// preload registers a raw ID before freeze.
func (s *symbolizer) preload(id string) {
	if id == "" {
		return
	}
	for _, r := range s.raw {
		if r == id {
			return
		}
	}
	s.raw = append(s.raw, id)
}

// freeze assigns symbolic names in sorted-raw order.
func (s *symbolizer) freeze() {
	sort.Strings(s.raw)
	for i, r := range s.raw {
		s.m[r] = fmt.Sprintf("%s#%d", s.prefix, i+1)
	}
}

// sym maps one raw ID; unknown non-empty IDs get a deterministic fallback.
func (s *symbolizer) sym(id string) string {
	if id == "" {
		return ""
	}
	if v, ok := s.m[id]; ok {
		return v
	}
	// IDs discovered after freeze (e.g. boundary references to events that
	// are not in the list) map deterministically by raw value.
	return s.prefix + "?:" + id
}

// canonicalizeRawMap canonicalizes every value of a raw JSON map.
func canonicalizeRawMap(in map[string]json.RawMessage) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = canonicalJSON(v)
	}
	return out
}

// canonicalizeStateDelta canonicalizes a state delta map.
func canonicalizeStateDelta(in map[string][]byte) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = canonicalJSON(v)
	}
	return out
}

// canonicalJSON returns the canonical form of a raw JSON document:
// object keys sorted, numbers unified (1 and 1.0 are equal), scrubbed keys
// replaced by "*". Invalid JSON is returned verbatim.
func canonicalJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	return canonicalValue(v)
}

// canonicalValue marshals v canonically.
func canonicalValue(v any) string {
	b, err := json.Marshal(normalizeJSONValue(v))
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// normalizeJSONValue walks v, scrubbing timing keys and normalizing numbers.
func normalizeJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if isScrubbedPayloadKey(k) {
				out[k] = "*"
				continue
			}
			out[k] = normalizeJSONValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeJSONValue(val)
		}
		return out
	case json.Number:
		return normalizeNumber(t)
	default:
		return v
	}
}

// normalizeNumber unifies integer-valued numbers: 1 and 1.0 become the same
// int64; other floats stay float64.
func normalizeNumber(n json.Number) any {
	if i, err := n.Int64(); err == nil {
		return i
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 && !strings.ContainsAny(n.String(), "eE") {
		return int64(f)
	}
	return f
}
