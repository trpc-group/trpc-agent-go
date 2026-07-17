//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Event struct {
	ID           string         `json:"id"`
	Seq          int            `json:"seq"`
	Author       string         `json:"author"`
	Role         string         `json:"role"`
	Content      string         `json:"content"`
	Tool         string         `json:"tool,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
	ToolResultID string         `json:"tool_result_id,omitempty"`
	Args         map[string]any `json:"args,omitempty"`
	Response     any            `json:"response,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	Tag          string         `json:"tag,omitempty"`
	FilterKey    string         `json:"filter_key,omitempty"`
	StateDelta   map[string]any `json:"state_delta,omitempty"`
	Extensions   map[string]any `json:"extensions,omitempty"`
	Timestamp    string         `json:"timestamp,omitempty"`
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
	Begin(Snapshot) error
	AppendEvent(Event) error
	AppendEventIdempotent(Event) error
	UpdateState(string, any) error
	AddMemory(Memory) error
	CreateSummary(Summary) error
	AppendTrack(TrackEvent) error
	Load() (Snapshot, error)
	Close() error
}

func clone(v Snapshot) Snapshot {
	data, _ := json.Marshal(v)
	var out Snapshot
	_ = json.Unmarshal(data, &out)
	return out
}

type ReplayCase struct {
	Name      string
	Expected  func() Snapshot
	Run       func(Backend) error
	FaultPath string
	Mutate    func(*Snapshot)
}

func Cases() []ReplayCase {
	return []ReplayCase{
		snapshotCase("single-turn", func() Snapshot {
			return base("s1", Event{Seq: 1, Author: "user", Role: "user", Content: "hello"}, Event{Seq: 2, Author: "agent", Role: "assistant", Content: "hi"})
		}, "/events/1/content", func(s *Snapshot) { s.Events[1].Content = "wrong" }),
		snapshotCase("multi-turn", func() Snapshot {
			return base("s2", Event{Seq: 1, Role: "user", Content: "one"}, Event{Seq: 2, Role: "assistant", Content: "1"}, Event{Seq: 3, Role: "user", Content: "two"})
		}, "/events", func(s *Snapshot) { s.Events[2].Seq = 2 }),
		snapshotCase("tool-call", func() Snapshot {
			s := base("s3",
				Event{Seq: 1, Author: "user", Role: "user", Content: "weather in Shenzhen"},
				Event{Seq: 2, Author: "agent", Role: "assistant", Tool: "weather", ToolCallID: "call-weather", Args: map[string]any{"city": "SZ"}, Extensions: map[string]any{"call_id": "c1"}},
				Event{Seq: 3, Author: "tool", Role: "tool", Tool: "weather", ToolResultID: "call-weather", Response: map[string]any{"temp": 30}},
			)
			return s
		}, "/events/1/args/city", func(s *Snapshot) {
			if s.Events[1].Args == nil {
				s.Events[1].Args = map[string]any{}
			}
			s.Events[1].Args["city"] = "BJ"
		}),
		snapshotCase("state-updates", func() Snapshot { s := base("s4"); s.State = map[string]any{"count": 2, "keep": true}; return s }, "/state/count", func(s *Snapshot) { s.State["count"] = 1 }),
		snapshotCase("memory", func() Snapshot {
			s := base("s5")
			s.Memories = []Memory{{ID: "m1", Content: "likes tea", Metadata: map[string]any{"kind": "preference"}, Scope: "user", Similarity: .912345}}
			return s
		}, "/memories/0/content", func(s *Snapshot) { s.Memories[0].Content = "likes coffee" }),
		summaryUpdateCase(),
		snapshotCase("summary-truncation", func() Snapshot {
			s := base("s7", Event{Seq: 10, Author: "user", Role: "user", Content: "retained", FilterKey: "conversation"}, Event{Seq: 11, Author: "agent", Role: "assistant", Content: "new", FilterKey: "conversation"})
			s.Summaries = []Summary{{ID: "sum2", SessionID: "s7", FilterKey: "conversation", Text: "events 1-9", Version: 1}}
			return s
		}, "/summaries", func(s *Snapshot) { s.Summaries = nil }),
		snapshotCase("track-events", func() Snapshot {
			s := base("s8")
			s.Tracks = []TrackEvent{{Name: "tool/weather", Type: "finish", InvocationID: "i1", DurationMS: 12.345}}
			return s
		}, "/tracks/0/error", func(s *Snapshot) { s.Tracks[0].Error = "timeout" }),
		concurrentCase(),
		retryCase(),
	}
}

func summaryUpdateCase() ReplayCase {
	build := func() Snapshot {
		s := base("s6", Event{Seq: 1, Author: "user", Role: "user", Content: "summarize this", FilterKey: "all"})
		s.Summaries = []Summary{{ID: "sum1", SessionID: "s6", FilterKey: "all", Text: "latest summary", Version: 2}}
		return s
	}
	return ReplayCase{
		Name: "summary-update", Expected: build, FaultPath: "/summaries/0/text",
		Run: func(backend Backend) error {
			expected := build()
			if err := backend.Begin(expected); err != nil {
				return err
			}
			if err := backend.AppendEvent(expected.Events[0]); err != nil {
				return err
			}
			old := expected.Summaries[0]
			old.Text, old.Version = "old summary", 1
			if err := backend.CreateSummary(old); err != nil {
				return err
			}
			return backend.CreateSummary(expected.Summaries[0])
		},
		Mutate: func(s *Snapshot) { s.Summaries[0].Text = "old summary" },
	}
}

func concurrentCase() ReplayCase {
	build := func() Snapshot {
		return base("s9",
			Event{Seq: 0, Author: "user", Role: "user", Content: "start concurrent tools"},
			Event{Seq: 1, Author: "agent-a", Role: "assistant", Branch: "tool-a", Content: "a"},
			Event{Seq: 2, Author: "agent-b", Role: "assistant", Branch: "tool-b", Content: "b"},
		)
	}
	return ReplayCase{
		Name: "concurrent-order", Expected: build, FaultPath: "/events",
		Run: func(backend Backend) error {
			expected := build()
			if err := backend.Begin(expected); err != nil {
				return err
			}
			if err := backend.AppendEvent(expected.Events[0]); err != nil {
				return err
			}
			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			for _, event := range expected.Events[1:] {
				event := event
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					errs <- backend.AppendEvent(event)
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					return err
				}
			}
			return nil
		},
		Mutate: func(s *Snapshot) { s.Events[2].Seq = 1 },
	}
}

var errInjectedAfterCommit = errors.New("injected failure after event commit")

func retryCase() ReplayCase {
	build := func() Snapshot {
		s := base("s10", Event{ID: "stable", Seq: 1, Author: "user", Role: "user", Content: "once"})
		s.State = map[string]any{"committed": true}
		s.Memories = []Memory{{ID: "m10", Content: "once", Scope: "session"}}
		return s
	}
	return ReplayCase{
		Name: "retry-recovery", Expected: build, FaultPath: "/events",
		Run: func(backend Backend) error {
			expected := build()
			if err := backend.Begin(expected); err != nil {
				return err
			}
			if err := appendThenFail(backend, expected.Events[0]); !errors.Is(err, errInjectedAfterCommit) {
				return fmt.Errorf("expected injected post-commit failure, got %w", err)
			}
			if err := backend.AppendEventIdempotent(expected.Events[0]); err != nil {
				return fmt.Errorf("retry stable event: %w", err)
			}
			if err := backend.UpdateState("committed", true); err != nil {
				return err
			}
			return backend.AddMemory(expected.Memories[0])
		},
		Mutate: func(s *Snapshot) { s.Events = append(s.Events, s.Events[0]) },
	}
}

func appendThenFail(backend Backend, event Event) error {
	if err := backend.AppendEvent(event); err != nil {
		return err
	}
	return errInjectedAfterCommit
}

func snapshotCase(name string, build func() Snapshot, faultPath string, mutate func(*Snapshot)) ReplayCase {
	return ReplayCase{
		Name: name, Expected: build, FaultPath: faultPath, Mutate: mutate,
		Run: func(backend Backend) error { return ReplaySnapshot(backend, build()) },
	}
}

func ReplaySnapshot(backend Backend, input Snapshot) error {
	if err := backend.Begin(input); err != nil {
		return err
	}
	for _, event := range input.Events {
		if err := backend.AppendEvent(event); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(input.State))
	for key := range input.State {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := backend.UpdateState(key, input.State[key]); err != nil {
			return err
		}
	}
	for _, memory := range input.Memories {
		if err := backend.AddMemory(memory); err != nil {
			return err
		}
	}
	for _, track := range input.Tracks {
		if err := backend.AppendTrack(track); err != nil {
			return err
		}
	}
	for _, summary := range input.Summaries {
		if err := backend.CreateSummary(summary); err != nil {
			return err
		}
	}
	return nil
}
func base(id string, events ...Event) Snapshot {
	return Snapshot{SessionID: id, Events: events, State: map[string]any{}, Unsupported: map[string]string{}}
}

func Normalize(s Snapshot) Snapshot {
	for i := range s.Events {
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
	unsupported := b.Unsupported
	// Unsupported describes backend capabilities rather than replayed data, so it
	// controls diff classification and is not itself part of the comparison.
	a.Unsupported = nil
	b.Unsupported = nil
	var out []Difference
	walk(caseName, backend, a.SessionID, "session", "", toAny(a), toAny(b), unsupported, &out)
	return out
}
func toAny(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}
func walk(c, backend, sid, locator, path string, a, b any, unsupported map[string]string, out *[]Difference) {
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
			walk(c, backend, sid, identify(locator, k, am[k], bm[k]), next, am[k], bm[k], unsupported, out)
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
			walk(c, backend, sid, elementLocator(locator, i, av, bv), fmt.Sprintf("%s/%d", path, i), av, bv, unsupported, out)
		}
		return
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		allowed, explanation := unsupportedDifference(path, unsupported)
		*out = append(*out, Difference{Case: c, Backend: backend, SessionID: sid, Locator: locator, Path: path, Baseline: a, Compared: b, Allowed: allowed, Explanation: explanation})
	}
}

func unsupportedDifference(path string, unsupported map[string]string) (bool, string) {
	for prefix, reason := range unsupported {
		prefix = "/" + strings.Trim(strings.TrimSpace(prefix), "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true, reason
		}
	}
	return false, "normalized values differ"
}

func HasNewNonAllowedDiff(before, after []Difference, pathPrefix string) bool {
	known := make(map[string]bool, len(before))
	for _, diff := range before {
		if !diff.Allowed {
			known[differenceKey(diff)] = true
		}
	}
	pathPrefix = "/" + strings.Trim(pathPrefix, "/")
	for _, diff := range after {
		if diff.Allowed || (diff.Path != pathPrefix && !strings.HasPrefix(diff.Path, pathPrefix+"/")) {
			continue
		}
		if !known[differenceKey(diff)] {
			return true
		}
	}
	return false
}

func differenceKey(diff Difference) string {
	baseline, _ := json.Marshal(diff.Baseline)
	compared, _ := json.Marshal(diff.Compared)
	return diff.Backend + "\x00" + diff.Path + "\x00" + string(baseline) + "\x00" + string(compared)
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
