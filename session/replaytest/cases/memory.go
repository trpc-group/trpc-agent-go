//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cases

import (
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// MemoryScopeIsolation is case 11: two users under the same app write,
// update, delete and clear memories; per-user read-back must stay isolated
// — user B's entries must never leak into the default user's list, and
// clearing user B must not touch the default user. It guards the memory
// (app, user) scope dimension.
func MemoryScopeIsolation() replaytest.Case {
	return replaytest.Case{
		Name: "memory/scope_isolation",
		Description: "two users in one app write/update/delete/clear memories; " +
			"per-user read-back must stay isolated (no cross-user leakage, " +
			"scoped clear)",
		NeedCaps: replaytest.Capability{Memory: true},
		Steps: []replaytest.Step{
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "默认用户偏好：咖啡不加糖",
				Topics:  []string{"preference"},
			}},
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				UserID:  "replay-user-b",
				Content: "用户B的私密事实：从不喝咖啡",
				Topics:  []string{"fact"},
			}},
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				UserID:  "replay-user-b",
				Content: "用户B的经验：先写测试再提交",
				Topics:  []string{"experience"},
			}},
			{Op: replaytest.OpUpdateMemory, Memory: &replaytest.MemorySpec{
				UserID:       "replay-user-b",
				MatchContent: "用户B的经验：先写测试再提交",
				Content:      "用户B的经验：先跑 replaytest 再提交",
				Topics:       []string{"experience", "updated"},
			}},
			{Op: replaytest.OpDeleteMemory, Memory: &replaytest.MemorySpec{
				UserID:       "replay-user-b",
				MatchContent: "用户B的私密事实：从不喝咖啡",
			}},
			// Clearing user B must leave the default user's memory intact.
			{Op: replaytest.OpClearMemories, Memory: &replaytest.MemorySpec{
				UserID: "replay-user-b",
			}},
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				UserID:  "replay-user-b",
				Content: "用户B重建：清空后只影响本用户",
				Topics:  []string{"rebuilt"},
			}},
		},
	}
}

// MemoryWriteRead is case 5: four memories (preference, fact, experience,
// history summary) with topics and episodic metadata, one update, one
// delete, a clear-to-empty, one re-add after the clear, then list plus
// search read-back.
// It guards memory content/metadata fidelity, update, delete and clear
// semantics, and search result-set consistency (ordering is an allowed
// note).
func MemoryWriteRead() replaytest.Case {
	return replaytest.Case{
		Name:        "memory/write_read",
		Description: "memory add/update/delete/clear with topics+metadata; list and search read-back",
		NeedCaps:    replaytest.Capability{Memory: true, MemorySearch: true},
		SearchQuery: "咖啡",
		Steps: []replaytest.Step{
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "用户偏好：咖啡不加糖",
				Topics:  []string{"preference", "drink"},
				Metadata: &replaytest.MetadataSpec{
					Kind:         "episode",
					Participants: []string{"user"},
					Location:     "深圳",
				},
			}},
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "事实：用户的项目使用 Go 1.21",
				Topics:  []string{"fact", "project"},
			}},
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "经验：发布前必须先跑 replaytest",
				Topics:  []string{"experience", "release"},
			}},
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "历史摘要：上周讨论了会话持久化方案",
				Topics:  []string{"summary", "history"},
			}},
			{Op: replaytest.OpUpdateMemory, Memory: &replaytest.MemorySpec{
				MatchContent: "用户偏好：咖啡不加糖",
				Content:      "用户偏好：咖啡不加糖，加奶",
				Topics:       []string{"preference", "drink", "updated"},
			}},
			{Op: replaytest.OpDeleteMemory, Memory: &replaytest.MemorySpec{
				MatchContent: "经验：发布前必须先跑 replaytest",
			}},
			// Clear-to-empty: both backends must clear consistently.
			{Op: replaytest.OpClearMemories},
			// Re-add after the clear proves the store is still writable and
			// keeps the search read-back below non-trivial.
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "清空后重建：用户改喝拿铁咖啡",
				Topics:  []string{"preference", "rebuilt"},
			}},
		},
	}
}
