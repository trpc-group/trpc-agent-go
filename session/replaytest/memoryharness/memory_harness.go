//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package memoryharness 回放确定性的记忆写入、读取和搜索。
package memoryharness

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
)

// Result 保存一次记忆回放的可观察结果。
type Result struct {
	Read   []*memory.Entry // 全量读取
	Search []*memory.Entry // 关键词搜索
}

// Run 在指定后端执行记忆用例，返回读取和搜索结果。
func Run(ctx context.Context, svc memory.Service, c *scenario.MemoryCase) (*Result, error) {
	if svc == nil {
		return nil, fmt.Errorf("记忆服务为空")
	}
	if c == nil {
		return nil, fmt.Errorf("记忆用例为空")
	}
	userKey := memory.UserKey{AppName: "replaytest", UserID: "user01"}
	// 按用例顺序写入记忆。
	for i, write := range c.Writes {
		metadata := &memory.Metadata{Kind: write.Kind, EventTime: write.EventTime, Participants: write.Participants, Location: write.Location}
		if err := svc.AddMemory(ctx, userKey, write.Content, write.Topics, memory.WithMetadata(metadata)); err != nil {
			return nil, fmt.Errorf("写入[%d]记忆失败: %w", i, err)
		}
	}
	read, err := svc.ReadMemories(ctx, userKey, 100)
	if err != nil {
		return nil, fmt.Errorf("读取记忆失败: %w", err)
	}
	search, err := svc.SearchMemories(ctx, userKey, c.SearchQuery)
	if err != nil {
		return nil, fmt.Errorf("搜索记忆失败: %w", err)
	}
	return &Result{Read: read, Search: search}, nil
}
