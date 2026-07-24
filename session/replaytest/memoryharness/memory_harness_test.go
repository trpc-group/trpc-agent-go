//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memoryharness

import (
	"context"
	"testing"

	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
)

func TestRunMemoryCase(t *testing.T) {
	ctx := context.Background()
	svc := memoryinmemory.NewMemoryService()
	t.Cleanup(func() { _ = svc.Close() })

	result, err := Run(ctx, svc, scenario.Case05_Memory)
	if err != nil {
		t.Fatalf("记忆回放失败: %v", err)
	}
	if len(result.Read) == 0 || len(result.Search) == 0 {
		t.Fatalf("记忆结果为空: read=%d search=%d", len(result.Read), len(result.Search))
	}
}

func TestRunNilService(t *testing.T) {
	_, err := Run(context.Background(), nil, scenario.Case05_Memory)
	if err == nil {
		t.Fatal("nil service 应返回错误")
	}
}

func TestRunNilCase(t *testing.T) {
	svc := memoryinmemory.NewMemoryService()
	t.Cleanup(func() { _ = svc.Close() })
	_, err := Run(context.Background(), svc, nil)
	if err == nil {
		t.Fatal("nil case 应返回错误")
	}
}
