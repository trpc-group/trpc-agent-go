//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/summary"
)

func TestRunSingleTurnCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService(inmemory.WithSummarizer(summary.FakeSummarizer{}))
	sess, err := Run(ctx, svc, scenario.Case01_SingleTurn)
	if err != nil {
		t.Fatalf("回放失败: %v", err)
	}
	if len(sess.Events) != 2 {
		t.Fatalf("事件数量错误: %+v", sess.Events)
	}
}

func TestRunNilCase(t *testing.T) {
	_, err := Run(context.Background(), inmemory.NewSessionService(), nil)
	if err == nil {
		t.Fatal("nil case 应返回错误")
	}
}

func TestRunUnsupportedOperation(t *testing.T) {
	c := &scenario.Case{
		Name: "unsupported",
		Ops:  []scenario.Op{{Kind: scenario.OpKind("unknown")}},
	}
	_, err := Run(context.Background(), inmemory.NewSessionService(), c)
	if err == nil {
		t.Fatal("不支持的操作应返回错误")
	}
}

func TestRunWithoutCreateSession(t *testing.T) {
	c := &scenario.Case{
		Name: "no_create",
		Ops:  []scenario.Op{{Kind: scenario.OpAppendEvent, Role: "user", Content: "hi"}},
	}
	_, err := Run(context.Background(), inmemory.NewSessionService(), c)
	if err == nil {
		t.Fatal("未创建 session 就追加事件应失败")
	}
}

func TestRunUnsupportedRole(t *testing.T) {
	c := &scenario.Case{
		Name: "bad_role",
		Ops: []scenario.Op{
			{Kind: scenario.OpCreateSession},
			{Kind: scenario.OpAppendEvent, Role: "system", Content: "hi"},
		},
	}
	_, err := Run(context.Background(), inmemory.NewSessionService(), c)
	if err == nil {
		t.Fatal("不支持的角色应返回错误")
	}
}

func TestRunUpdateStateCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	sess, err := Run(ctx, svc, scenario.Case03_UpdateState)
	if err != nil {
		t.Fatalf("状态用例回放失败: %v", err)
	}
	if sess.State["final"] == nil || string(sess.State["final"]) != "clean" {
		t.Fatalf("状态结果错误: %+v", sess.State)
	}
}

func TestRunToolCallCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	sess, err := Run(ctx, svc, scenario.Case04_ToolCall)
	if err != nil {
		t.Fatalf("工具调用回放失败: %v", err)
	}
	if len(sess.Events) < 3 {
		t.Fatalf("工具调用事件数量错误: %+v", sess.Events)
	}
}

func TestRunSummaryCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService(inmemory.WithSummarizer(summary.FakeSummarizer{}))
	sess, err := Run(ctx, svc, scenario.Case06_Summary)
	if err != nil {
		t.Fatalf("摘要回放失败: %v", err)
	}
	if len(sess.Summaries) == 0 {
		t.Fatalf("摘要未生成: %+v", sess.Summaries)
	}
}

func TestRunSummaryFilterKeyCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService(inmemory.WithSummarizer(summary.FakeSummarizer{}))
	sess, err := Run(ctx, svc, scenario.Case06_SummaryFilterKey)
	if err != nil {
		t.Fatalf("按 filter key 摘要回放失败: %v", err)
	}
	if _, ok := sess.Summaries["weather"]; !ok {
		t.Fatalf("weather 摘要未生成: %+v", sess.Summaries)
	}
}

func TestRunSummaryWithTruncationCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService(inmemory.WithSummarizer(summary.FakeSummarizer{}))
	sess, err := Run(ctx, svc, scenario.Case07_SummaryWithTruncation)
	if err != nil {
		t.Fatalf("截断读取回放失败: %v", err)
	}
	if len(sess.Events) != 2 {
		t.Fatalf("截断后事件数量错误: %+v", sess.Events)
	}
}

func TestRunTrackCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	sess, err := Run(ctx, svc, scenario.Case08_Track)
	if err != nil {
		t.Fatalf("track 回放失败: %v", err)
	}
	if len(sess.Tracks) == 0 {
		t.Fatalf("track 未写入: %+v", sess.Tracks)
	}
}

func TestRunConcurrentAppendCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	sess, err := Run(ctx, svc, scenario.Case09_ConcurrentAppend)
	if err != nil {
		t.Fatalf("并发回放失败: %v", err)
	}
	if len(sess.Events) != 3 {
		t.Fatalf("并发事件数量错误: %+v", sess.Events)
	}
}

func TestRunRecoveryCase(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	sess, err := Run(ctx, svc, scenario.Case10_Recovery)
	if err != nil {
		t.Fatalf("恢复回放失败: %v", err)
	}
	if string(sess.State["recovered"]) != "ok" {
		t.Fatalf("恢复状态错误: %+v", sess.State)
	}
}
