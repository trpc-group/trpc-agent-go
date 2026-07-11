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
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// MemoryEntry 保存与具体后端无关的记忆字段。
type MemoryEntry struct {
	ID           string
	AppName      string
	UserID       string
	Content      string
	Topics       []string
	Kind         string
	EventTime    string   // 事件时间
	Participants []string // 参与者，
	Location     string   // 发生地点
}

type MemorySnapshot struct {
	Read   []MemoryEntry
	Search []MemoryEntry
}

func FromMemoryEntries(
	read []*memory.Entry,
	search []*memory.Entry,
) *MemorySnapshot {
	return &MemorySnapshot{
		Read:   normalizeMemoryEntries(read),
		Search: normalizeMemoryEntries(search),
	}
}

func normalizeMemoryEntries(entries []*memory.Entry) []MemoryEntry {
	out := make([]MemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		// 避免后端迭代顺序影响比较结果。
		// 统一顺序
		topics := append([]string(nil), entry.Memory.Topics...)
		sort.Strings(topics)
		participants := append([]string(nil), entry.Memory.Participants...)
		sort.Strings(participants)

		kind := entry.Memory.Kind
		if kind == "" {
			kind = memory.KindFact
		}
		var eventTime string
		if entry.Memory.EventTime != nil {
			eventTime = entry.Memory.EventTime.UTC().Format(
				"2006-01-02T15:04:05.999999999Z07:00",
			)
		}
		out = append(out, MemoryEntry{
			ID:           entry.ID,
			AppName:      entry.AppName,
			UserID:       entry.UserID,
			Content:      entry.Memory.Memory,
			Topics:       topics,
			Kind:         string(kind),
			EventTime:    eventTime,
			Participants: participants,
			Location:     entry.Memory.Location,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}
