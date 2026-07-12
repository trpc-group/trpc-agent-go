//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

type Event struct {
	ID         string         `json:"id"`
	Seq        int            `json:"seq"`
	Author     string         `json:"author"`
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	Tool       string         `json:"tool,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Response   any            `json:"response,omitempty"`
	Branch     string         `json:"branch,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	FilterKey  string         `json:"filter_key,omitempty"`
	StateDelta map[string]any `json:"state_delta,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
	Timestamp  string         `json:"timestamp,omitempty"`
}
type Memory struct {
	ID         string         `json:"id"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Scope      string         `json:"scope"`
	Similarity float64        `json:"similarity,omitempty"`
}
type Summary struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	FilterKey string `json:"filter_key"`
	Text      string `json:"text"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at,omitempty"`
}
type TrackEvent struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	InvocationID string  `json:"invocation_id"`
	Error        string  `json:"error,omitempty"`
	DurationMS   float64 `json:"duration_ms,omitempty"`
	Timestamp    string  `json:"timestamp,omitempty"`
}
type Snapshot struct {
	SessionID   string            `json:"session_id"`
	Events      []Event           `json:"events"`
	State       map[string]any    `json:"state"`
	Memories    []Memory          `json:"memories"`
	Summaries   []Summary         `json:"summaries"`
	Tracks      []TrackEvent      `json:"tracks"`
	Unsupported map[string]string `json:"unsupported,omitempty"`
}

type Backend interface {
	Name() string
	Save(Snapshot) error
	Load() (Snapshot, error)
}
type memoryBackend struct {
	name  string
	value Snapshot
}

func (b *memoryBackend) Name() string            { return b.name }
func (b *memoryBackend) Save(v Snapshot) error   { b.value = clone(v); return nil }
func (b *memoryBackend) Load() (Snapshot, error) { return clone(b.value), nil }

type JSONBackend struct {
	name, path string
	mu         sync.Mutex
}

func NewJSONBackend(path string) *JSONBackend {
	return &JSONBackend{name: "json-persistent", path: path}
}
func (b *JSONBackend) Name() string { return b.name }
func (b *JSONBackend) Save(v Snapshot) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return os.WriteFile(b.path, data, 0o600)
}
func (b *JSONBackend) Load() (Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var v Snapshot
	data, e := os.ReadFile(b.path)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(data, &v)
	return v, e
}
func clone(v Snapshot) Snapshot {
	data, _ := json.Marshal(v)
	var out Snapshot
	_ = json.Unmarshal(data, &out)
	return out
}

type ReplayCase struct {
	Name   string
	Build  func() Snapshot
	Mutate func(*Snapshot)
}

func Cases() []ReplayCase {
	return []ReplayCase{
		{"single-turn", func() Snapshot {
			return base("s1", Event{Seq: 1, Author: "user", Role: "user", Content: "hello"}, Event{Seq: 2, Author: "agent", Role: "assistant", Content: "hi"})
		}, func(s *Snapshot) { s.Events[1].Content = "wrong" }},
		{"multi-turn", func() Snapshot {
			return base("s2", Event{Seq: 1, Role: "user", Content: "one"}, Event{Seq: 2, Role: "assistant", Content: "1"}, Event{Seq: 3, Role: "user", Content: "two"})
		}, func(s *Snapshot) { s.Events[2].Seq = 2 }},
		{"tool-call", func() Snapshot {
			s := base("s3", Event{Seq: 1, Role: "assistant", Tool: "weather", Args: map[string]any{"city": "SZ"}, Extensions: map[string]any{"call_id": "c1"}}, Event{Seq: 2, Role: "tool", Tool: "weather", Response: map[string]any{"temp": 30}})
			return s
		}, func(s *Snapshot) { s.Events[0].Args["city"] = "BJ" }},
		{"state-updates", func() Snapshot { s := base("s4"); s.State = map[string]any{"count": 2, "keep": true}; return s }, func(s *Snapshot) { s.State["count"] = 1 }},
		{"memory", func() Snapshot {
			s := base("s5")
			s.Memories = []Memory{{ID: "m1", Content: "likes tea", Metadata: map[string]any{"kind": "preference"}, Scope: "user", Similarity: .912345}}
			return s
		}, func(s *Snapshot) { s.Memories[0].Content = "likes coffee" }},
		{"summary-update", func() Snapshot {
			s := base("s6")
			s.Summaries = []Summary{{ID: "sum1", SessionID: "s6", FilterKey: "all", Text: "latest summary", Version: 2}}
			return s
		}, func(s *Snapshot) { s.Summaries[0].Text = "old summary" }},
		{"summary-truncation", func() Snapshot {
			s := base("s7", Event{Seq: 10, Role: "assistant", Content: "retained"}, Event{Seq: 11, Role: "user", Content: "new"})
			s.Summaries = []Summary{{ID: "sum2", SessionID: "s7", FilterKey: "conversation", Text: "events 1-9", Version: 1}}
			return s
		}, func(s *Snapshot) { s.Summaries = nil }},
		{"track-events", func() Snapshot {
			s := base("s8")
			s.Tracks = []TrackEvent{{Name: "tool/weather", Type: "finish", InvocationID: "i1", DurationMS: 12.345}}
			return s
		}, func(s *Snapshot) { s.Tracks[0].Error = "timeout" }},
		{"concurrent-order", func() Snapshot {
			return base("s9", Event{Seq: 2, Branch: "tool-b", Content: "b"}, Event{Seq: 1, Branch: "tool-a", Content: "a"})
		}, func(s *Snapshot) { s.Events[0].Seq = 1 }},
		{"retry-recovery", func() Snapshot {
			s := base("s10", Event{ID: "stable", Seq: 1, Content: "once"})
			s.State = map[string]any{"committed": true}
			s.Memories = []Memory{{ID: "m10", Content: "once", Scope: "session"}}
			return s
		}, func(s *Snapshot) { s.Events = append(s.Events, s.Events[0]) }},
	}
}
func base(id string, events ...Event) Snapshot {
	return Snapshot{SessionID: id, Events: events, State: map[string]any{}, Unsupported: map[string]string{}}
}

func Normalize(s Snapshot) Snapshot {
	for i := range s.Events {
		s.Events[i].ID = ""
		s.Events[i].Timestamp = ""
	}
	for i := range s.Memories {
		if strings.HasPrefix(s.Memories[i].ID, "generated-") {
			s.Memories[i].ID = ""
		}
		s.Memories[i].Similarity = float64(int(s.Memories[i].Similarity*1000+.5)) / 1000
	}
	for i := range s.Summaries {
		s.Summaries[i].UpdatedAt = ""
	}
	for i := range s.Tracks {
		s.Tracks[i].Timestamp = ""
		s.Tracks[i].DurationMS = float64(int(s.Tracks[i].DurationMS + .5))
	}
	sort.SliceStable(s.Events, func(i, j int) bool { return s.Events[i].Seq < s.Events[j].Seq })
	sort.Slice(s.Memories, func(i, j int) bool { return s.Memories[i].ID < s.Memories[j].ID })
	sort.Slice(s.Summaries, func(i, j int) bool {
		if s.Summaries[i].FilterKey == s.Summaries[j].FilterKey {
			return s.Summaries[i].ID < s.Summaries[j].ID
		}
		return s.Summaries[i].FilterKey < s.Summaries[j].FilterKey
	})
	sort.SliceStable(s.Tracks, func(i, j int) bool { return s.Tracks[i].Name < s.Tracks[j].Name })
	return s
}

type Difference struct {
	Case        string `json:"case"`
	Backend     string `json:"backend"`
	SessionID   string `json:"session_id"`
	Locator     string `json:"locator"`
	Path        string `json:"field_path"`
	Baseline    any    `json:"baseline"`
	Compared    any    `json:"compared"`
	Allowed     bool   `json:"allowed_diff"`
	Explanation string `json:"explanation"`
}
type Report struct {
	DurationMS       int64        `json:"duration_ms"`
	Cases            int          `json:"cases"`
	DetectedInjected int          `json:"detected_injected"`
	Differences      []Difference `json:"differences"`
	Backends         []string     `json:"backends"`
}

func Compare(caseName, backend string, a, b Snapshot) []Difference {
	a = Normalize(a)
	b = Normalize(b)
	var out []Difference
	walk(caseName, backend, a.SessionID, "session", "", toAny(a), toAny(b), &out)
	return out
}
func toAny(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}
func walk(c, backend, sid, locator, path string, a, b any, out *[]Difference) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		list := make([]string, 0, len(keys))
		for k := range keys {
			list = append(list, k)
		}
		sort.Strings(list)
		for _, k := range list {
			next := path + "/" + k
			walk(c, backend, sid, identify(locator, k, am[k], bm[k]), next, am[k], bm[k], out)
		}
		return
	}
	aa, aok := a.([]any)
	bb, bok := b.([]any)
	if aok && bok {
		n := len(aa)
		if len(bb) > n {
			n = len(bb)
		}
		for i := 0; i < n; i++ {
			var av, bv any
			if i < len(aa) {
				av = aa[i]
			}
			if i < len(bb) {
				bv = bb[i]
			}
			walk(c, backend, sid, elementLocator(locator, i, av, bv), fmt.Sprintf("%s/%d", path, i), av, bv, out)
		}
		return
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		*out = append(*out, Difference{Case: c, Backend: backend, SessionID: sid, Locator: locator, Path: path, Baseline: a, Compared: b, Explanation: "normalized values differ"})
	}
}
func elementLocator(kind string, index int, a, b any) string {
	m, _ := a.(map[string]any)
	if m == nil {
		m, _ = b.(map[string]any)
	}
	switch kind {
	case "event":
		if seq, ok := m["seq"]; ok {
			return fmt.Sprintf("event[seq=%v]", seq)
		}
	case "summary":
		return fmt.Sprintf("summary[id=%v,filter_key=%v]", m["id"], m["filter_key"])
	case "memory":
		return fmt.Sprintf("memory[id=%v]", m["id"])
	case "track":
		return fmt.Sprintf("track[name=%v]", m["name"])
	}
	return fmt.Sprintf("%s[%d]", kind, index)
}
func identify(current, key string, a, b any) string {
	if key == "events" {
		return "event"
	}
	if key == "summaries" {
		return "summary"
	}
	if key == "memories" {
		return "memory"
	}
	if key == "tracks" {
		return "track"
	}
	return current
}
