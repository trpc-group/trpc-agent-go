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
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

func TestRunMultiTurnCase(t *testing.T) {
	sess, err := Run(context.Background(), inmemory.NewSessionService(), scenario.Case02_MultiTurn)
	if err != nil {
		t.Fatalf("多轮回放失败: %v", err)
	}
	if len(sess.Events) != 6 {
		t.Fatalf("多轮事件数量错误: %+v", sess.Events)
	}
}

func TestRunAppendStateDelta(t *testing.T) {
	c := &scenario.Case{
		Name: "state_delta",
		Ops: []scenario.Op{
			{Kind: scenario.OpCreateSession},
			{
				Kind:       scenario.OpAppendStateDelta,
				EventID:    "delta-1",
				Author:     "system",
				StateDelta: map[string]string{"k": "v"},
			},
		},
	}
	if _, err := Run(context.Background(), inmemory.NewSessionService(), c); err != nil {
		t.Fatalf("state delta 回放失败: %v", err)
	}
}

func TestRunAppendEventWithRetry(t *testing.T) {
	c := &scenario.Case{
		Name: "retry_append",
		Ops: []scenario.Op{
			{Kind: scenario.OpCreateSession},
			{Kind: scenario.OpAppendEventWithRetry, Role: "user", Content: "retry me"},
		},
	}
	svc := &failOnceAppendService{Service: inmemory.NewSessionService()}
	sess, err := Run(context.Background(), svc, c)
	if err != nil {
		t.Fatalf("重试追加回放失败: %v", err)
	}
	if len(sess.Events) != 1 {
		t.Fatalf("重试后事件数量错误: %+v", sess.Events)
	}
}

// TestRunAppendEventRetryAfterPersistedFailure 断言：当 AppendEvent 已完成
// 持久化却又返回错误时，回放框架不得无脑重试造成重复事件。
func TestRunAppendEventRetryAfterPersistedFailure(t *testing.T) {
	c := &scenario.Case{
		Name: "retry_after_persist",
		Ops: []scenario.Op{
			{Kind: scenario.OpCreateSession},
			{Kind: scenario.OpAppendEventWithRetry, Role: "user", Content: "write then fail"},
		},
	}
	key := session.Key{AppName: "replaytest", UserID: "user01", SessionID: "session_id_01"}
	wrapped := &writeThenFailAppendService{
		Service: inmemory.NewSessionService(),
		key:     key,
	}

	if _, err := Run(context.Background(), wrapped, c); err == nil {
		t.Fatal("已持久化却返回错误时应返回错误，而不是静默重试")
	}

	stored, err := wrapped.Service.GetSession(context.Background(), key)
	if err != nil {
		t.Fatalf("读取持久化 session 失败: %v", err)
	}
	if len(stored.Events) != 1 {
		t.Fatalf("已持久化事件不应因重试产生重复，期望 1 条，实际 %d: %+v",
			len(stored.Events), stored.Events)
	}
}

func TestRunAppendTrackUnsupportedBackend(t *testing.T) {
	c := &scenario.Case{
		Name: "track_unsupported",
		Ops: []scenario.Op{
			{Kind: scenario.OpCreateSession},
			{Kind: scenario.OpAppendTrack, TrackName: "tool", TrackPayload: `{}`},
		},
	}
	_, err := Run(context.Background(), nonTrackService{}, c)
	if err == nil {
		t.Fatal("不支持 track 的后端应返回错误")
	}
}

type failOnceAppendService struct {
	session.Service
	failed bool
}

func (s *failOnceAppendService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if !s.failed {
		s.failed = true
		return errors.New("fail once")
	}
	return s.Service.AppendEvent(ctx, sess, evt, opts...)
}

// writeThenFailAppendService 先真正把事件写入后端，再返回注入错误，
// 模拟“持久化已完成却返回错误”的重试场景。
type writeThenFailAppendService struct {
	session.Service
	key session.Key
}

func (s *writeThenFailAppendService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if err := s.Service.AppendEvent(ctx, sess, evt, opts...); err != nil {
		return err
	}
	// 事件已落库，但向上层返回错误。
	return errors.New("persisted then failed")
}

type nonTrackService struct {
	session.Service
}

func (nonTrackService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	return inmemory.NewSessionService().CreateSession(ctx, key, state, opts...)
}
