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
// history summary) with topics and episodic metadata, one update touching
// content, topics and metadata together, one delete, then list plus search
// read-back. The final snapshot keeps the updated, metadata-bearing
// records, so update/topics/metadata serialization defects stay detectable;
// clear semantics are covered by MemoryScopeIsolation (case 11).
// It guards memory content/metadata fidelity, update and delete
// semantics, and search result-set consistency (ordering is an allowed
// note). MemorySearch is deliberately not required: a backend without
// search still validates add/update/delete/list here, and the search
// read-back (captured only on targets that support it) is excluded from
// the comparison by runCasePair.
func MemoryWriteRead() replaytest.Case {
	return replaytest.Case{
		Name:        "memory/write_read",
		Description: "memory add/update/delete with topics+metadata; list and search read-back",
		NeedCaps:    replaytest.Capability{Memory: true},
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
			// Update content, topics and metadata in one step so all three
			// stay observable in the final snapshot.
			{Op: replaytest.OpUpdateMemory, Memory: &replaytest.MemorySpec{
				MatchContent: "用户偏好：咖啡不加糖",
				Content:      "用户偏好：咖啡不加糖，加奶",
				Topics:       []string{"preference", "drink", "updated"},
				Metadata: &replaytest.MetadataSpec{
					Kind:         "episode",
					Participants: []string{"user"},
					Location:     "上海",
				},
			}},
			{Op: replaytest.OpDeleteMemory, Memory: &replaytest.MemorySpec{
				MatchContent: "经验：发布前必须先跑 replaytest",
			}},
		},
	}
}
