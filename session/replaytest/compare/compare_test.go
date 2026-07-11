//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package compare

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
)

func TestCompareDeep(t *testing.T) {
	base := sampleSessionSnapshot()
	if !CompareDeep(base, cloneSessionSnapshot(t, base)) {
		t.Fatal("相同快照应判定为一致")
	}
	candidate := cloneSessionSnapshot(t, base)
	candidate.State["weather"] = "rain"
	if CompareDeep(base, candidate) {
		t.Fatal("不同快照应判定为不一致")
	}
}

func TestMakeDiff(t *testing.T) {
	base := sampleSessionSnapshot()

	if diff := MakeDiff(base, base); len(diff) != 0 {
		t.Fatalf("相同快照不应产生差异: %+v", diff)
	}
	if diff := MakeDiff(nil, base); diff["snapshot"] != "a is nil" {
		t.Fatalf("nil 基准快照: %+v", diff)
	}
	if diff := MakeDiff(base, nil); diff["snapshot"] != "b is nil" {
		t.Fatalf("nil 候选快照: %+v", diff)
	}

	candidate := cloneSessionSnapshot(t, base)
	candidate.SessionId = "other"
	candidate.Events[0].Content = "changed"
	candidate.Events = append(candidate.Events, normalize.Event{Index: 1, Content: "extra"})
	candidate.State["weather"] = "rain"
	candidate.Summaries["profile"] = normalize.Summary{FilterKey: "profile", Text: "new"}
	candidate.Tracks["tool"][0].Payload = `{"status":"error"}`

	diff := MakeDiff(base, candidate)
	for _, key := range []string{
		"session_id", "events_len", "state", "event_0", "summaries", "tracks",
	} {
		if _, ok := diff[key]; !ok {
			t.Fatalf("缺少差异字段 %q: %+v", key, diff)
		}
	}
}

func TestMakeMemoryDiff(t *testing.T) {
	base := &normalize.MemorySnapshot{
		Read: []normalize.MemoryEntry{{
			ID: "memory-1", Content: "hello",
		}},
		Search: []normalize.MemoryEntry{{
			ID: "memory-1", Content: "hello",
		}},
	}

	if diff := MakeMemoryDiff(base, base); len(diff) != 0 {
		t.Fatalf("相同记忆快照不应产生差异: %+v", diff)
	}
	if diff := MakeMemoryDiff(nil, base); diff["memory_snapshot"] == "" {
		t.Fatalf("nil 记忆快照应产生差异: %+v", diff)
	}

	candidate := &normalize.MemorySnapshot{
		Read:   []normalize.MemoryEntry{{ID: "memory-1", Content: "world"}},
		Search: []normalize.MemoryEntry{{ID: "memory-1", Content: "world"}},
	}
	diff := MakeMemoryDiff(base, candidate)
	for _, key := range []string{"memory.read", "memory.search"} {
		if _, ok := diff[key]; !ok {
			t.Fatalf("缺少差异字段 %q: %+v", key, diff)
		}
	}
}

func TestCompareSessionNilSnapshots(t *testing.T) {
	report := CompareSession(testContext(ScopeSession), nil, sampleSessionSnapshot(), nil)
	if report.Passed || len(report.Diffs) != 1 || report.Diffs[0].FieldPath != "snapshot" {
		t.Fatalf("nil 快照应失败: %+v", report)
	}
}

func TestCompareMemoryNilSnapshots(t *testing.T) {
	report := CompareMemory(testContext(ScopeMemory), nil, &normalize.MemorySnapshot{}, nil)
	if report.Passed || len(report.Diffs) != 1 || report.Diffs[0].FieldPath != "memory" {
		t.Fatalf("nil 记忆快照应失败: %+v", report)
	}
}

func TestCompareSessionEventLengthMismatch(t *testing.T) {
	base := sampleSessionSnapshot()
	candidate := cloneSessionSnapshot(t, base)
	candidate.Events = append(candidate.Events, normalize.Event{Index: 1, Content: "extra"})
	report := CompareSession(testContext(ScopeSession), base, candidate, nil)
	if !containsPath(report.Diffs, "events.length") {
		t.Fatalf("应检测到事件数量差异: %+v", report)
	}
}

func TestCompareSessionAdditionalEventFields(t *testing.T) {
	base := sampleSessionSnapshot()
	candidate := cloneSessionSnapshot(t, base)
	candidate.Events[0].Author = "wrong"
	candidate.Events[0].Role = "wrong"
	candidate.Events[0].ToolID = "wrong"
	candidate.Events[0].ToolName = "wrong"
	candidate.Events[0].FilterKey = "wrong"
	candidate.Events[0].Branch = "wrong"
	candidate.Events[0].Tag = "wrong"
	candidate.Events[0].StateDelta = map[string]string{"k": "v"}
	candidate.Events[0].ToolCalls = append(candidate.Events[0].ToolCalls, normalize.ToolCall{
		ID: "call-2", Name: "other", Args: `{}`,
	})

	report := CompareSession(testContext(ScopeSession), base, candidate, nil)
	for _, path := range []string{
		"events[0].author",
		"events[0].role",
		"events[0].tool_id",
		"events[0].tool_name",
		"events[0].filter_key",
		"events[0].branch",
		"events[0].tag",
		"events[0].state_delta.k",
		"events[0].tool_calls.length",
	} {
		if !containsPath(report.Diffs, path) {
			t.Fatalf("未检测到字段 %q: %+v", path, report.Diffs)
		}
	}
}

func TestCompareMemorySearchAndFields(t *testing.T) {
	base := &normalize.MemorySnapshot{
		Read: []normalize.MemoryEntry{{
			ID: "memory-1", AppName: "app", UserID: "user",
			Content: "中文", Topics: []string{"a"}, Kind: "fact",
			EventTime: "2026-01-01T00:00:00Z", Participants: []string{"u"},
			Location: "home",
		}},
		Search: []normalize.MemoryEntry{{
			ID: "memory-1", AppName: "app", UserID: "user",
			Content: "中文", Topics: []string{"a"}, Kind: "fact",
		}},
	}
	candidate := &normalize.MemorySnapshot{
		Read: append([]normalize.MemoryEntry(nil), base.Read...),
		Search: []normalize.MemoryEntry{{
			ID: "memory-2", AppName: "app", UserID: "user",
			Content: "英文", Topics: []string{"b"}, Kind: "episode",
			EventTime: "2026-02-01T00:00:00Z", Participants: []string{"v"},
			Location: "office",
		}},
	}

	report := CompareMemory(testContext(ScopeMemory), base, candidate, nil)
	for _, path := range []string{
		`memory.search[id="memory-1"]`,
		`memory.search[id="memory-2"]`,
	} {
		if !containsPath(report.Diffs, path) {
			t.Fatalf("未检测到字段 %q: %+v", path, report.Diffs)
		}
	}
}

func TestCompareMemoryFieldLevelDiffs(t *testing.T) {
	base := &normalize.MemorySnapshot{
		Search: []normalize.MemoryEntry{{
			ID: "memory-1", AppName: "app", UserID: "user",
			Content: "中文", Topics: []string{"a"}, Kind: "fact",
			EventTime: "2026-01-01T00:00:00Z", Participants: []string{"u"},
			Location: "home",
		}},
	}
	candidate := &normalize.MemorySnapshot{
		Search: []normalize.MemoryEntry{{
			ID: "memory-1", AppName: "app", UserID: "user",
			Content: "英文", Topics: []string{"b"}, Kind: "episode",
			EventTime: "2026-02-01T00:00:00Z", Participants: []string{"v"},
			Location: "office",
		}},
	}
	report := CompareMemory(testContext(ScopeMemory), base, candidate, nil)
	for _, path := range []string{
		`memory.search[id="memory-1"].content`,
		`memory.search[id="memory-1"].topics`,
		`memory.search[id="memory-1"].kind`,
		`memory.search[id="memory-1"].event_time`,
		`memory.search[id="memory-1"].participants`,
		`memory.search[id="memory-1"].location`,
	} {
		if !containsPath(report.Diffs, path) {
			t.Fatalf("未检测到字段 %q: %+v", path, report.Diffs)
		}
	}
}

func TestCompareJSONInvalidPayload(t *testing.T) {
	base := sampleSessionSnapshot()
	candidate := cloneSessionSnapshot(t, base)
	candidate.Tracks["tool"][0].Payload = `not-json`
	report := CompareSession(testContext(ScopeSession), base, candidate, nil)
	if !containsPath(report.Diffs, `tracks["tool"][0].payload`) {
		t.Fatalf("非法 JSON payload 应产生差异: %+v", report.Diffs)
	}
}
