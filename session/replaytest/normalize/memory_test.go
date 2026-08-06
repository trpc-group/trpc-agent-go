//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package normalize

import (
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestFromMemoryEntries(t *testing.T) {
	eventTime := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	read := []*memory.Entry{{
		AppName: "app", UserID: "user",
		Memory: &memory.Memory{
			Memory:       "用户偏好中文",
			Topics:       []string{"b", "a"},
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Bob", "Alice"},
			Location:     "home",
		},
	}, nil}
	search := []*memory.Entry{{
		AppName: "app", UserID: "user",
		Memory: &memory.Memory{
			Memory: "SQLite GCC",
			Topics: []string{"sqlite"},
		},
	}}

	snapshot := FromMemoryEntries(read, search)
	if len(snapshot.Read) != 1 || len(snapshot.Search) != 1 {
		t.Fatalf("应跳过 nil entry: %+v", snapshot)
	}
	entry := snapshot.Read[0]
	if entry.Content != "用户偏好中文" ||
		entry.Topics[0] != "a" || entry.Topics[1] != "b" ||
		entry.Kind != string(memory.KindEpisode) ||
		entry.Participants[0] != "Alice" ||
		entry.Location != "home" ||
		entry.EventTime == "" {
		t.Fatalf("memory 归一化错误: %+v", entry)
	}
	if entry.ID == "" || entry.ID == "memory-1" {
		t.Fatalf("应生成语义化 ID: %q", entry.ID)
	}
}

func TestFromMemoryEntriesDefaultKind(t *testing.T) {
	entries := []*memory.Entry{{
		AppName: "app", UserID: "user",
		Memory: &memory.Memory{Memory: "fact only"},
	}}
	snapshot := FromMemoryEntries(entries, nil)
	if snapshot.Read[0].Kind != string(memory.KindFact) {
		t.Fatalf("空 kind 应默认为 fact: %+v", snapshot.Read[0])
	}
}
