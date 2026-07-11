//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package scenario

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// 描述一次确定性的记忆写入。
type MemoryWrite struct {
	Content      string
	Topics       []string
	Kind         memory.Kind
	EventTime    *time.Time // 可选事件时间
	Participants []string   // 可选参与者
	Location     string     // 可选地点
}

// MemoryCase 描述一组写入以及后续的读取和搜索验证。
type MemoryCase struct {
	Name        string
	Writes      []MemoryWrite // 写入的记忆
	SearchQuery string        // 搜索关键词
}

// 固定任务经验时间，避免比较时受当前时间影响。
var memoryTaskTime = time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)

// Case05_Memory 覆盖用户偏好、事实、任务经验和历史摘要。
var Case05_Memory = &MemoryCase{
	Name: "case05_memory",
	Writes: []MemoryWrite{
		{
			Content: "用户偏好简洁中文回答",
			Topics:  []string{"language", "preference"},
			Kind:    memory.KindFact,
		},
		{
			Content: "用户正在开发一个测试框架",
			Topics:  []string{"project", "fact"},
			Kind:    memory.KindFact,
		},
		{
			Content:      "Windows SQLite 测试需要 MinGW GCC",
			Topics:       []string{"sqlite", "experience"},
			Kind:         memory.KindEpisode,
			EventTime:    &memoryTaskTime,
			Participants: []string{"Liam"},
			Location:     "Windows",
		},
		{
			Content: "已完成 Session replay 的事件、状态和 Track 测试",
			Topics:  []string{"summary", "history"},
			Kind:    memory.KindFact,
		},
	},
	SearchQuery: "SQLite GCC",
}
