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
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/fixture"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
)

// 执行器  利用现有的接口实现每个ops中操作
func Run(
	ctx context.Context,
	svc session.Service,
	c *scenario.Case,
) (*session.Session, error) {

	if c == nil {
		return nil, fmt.Errorf("用例为空")
	}

	key := session.Key{
		AppName:   "replaytest",
		UserID:    "user01",
		SessionID: "session_id_01",
	}

	var (
		sess *session.Session
		err  error
	)
	// 对某一case 遍历 需要进行的操作
	// 一个case下有多次 操作 都是对于同一session
	for i, op := range c.Ops {
		switch op.Kind {
		case scenario.OpCreateSession:
			sess, err = svc.CreateSession(ctx, key, nil)
			if err != nil {
				return nil, fmt.Errorf("op[%d] 创建 session 失败: %w", i, err)
			}

		case scenario.OpAppendEvent:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就追加事件", i)
			}
			evt, buildErr := buildEvent(op)
			if buildErr != nil {
				return nil, fmt.Errorf("op[%d] 构建事件失败: %w", i, buildErr)
			}
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				return nil, fmt.Errorf("op[%d] 追加事件失败: %w", i, err)
			}
		case scenario.OpAppendEventWithRetry:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就追加事件", i)
			}
			evt, buildErr := buildEvent(op)
			if buildErr != nil {
				return nil, fmt.Errorf("op[%d] 构建重试事件失败: %w", i, buildErr)
			}
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				// 故意重试同一事件；若后端先落库再报错，这里会暴露重复写入。
				if err = svc.AppendEvent(ctx, sess, evt); err != nil {
					return nil, fmt.Errorf("op[%d] 重试追加事件失败: %w", i, err)
				}
			}
		case scenario.OpUpdateState:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就更新状态", i)
			}
			// 将状态转换为session.StateMap 格式
			state := make(session.StateMap)
			for k, v := range op.State {
				state[k] = []byte(v)
			}
			if err = svc.UpdateSessionState(ctx, key, state); err != nil {
				return nil, fmt.Errorf("op[%d] 更新状态失败: %w", i, err)
			}
		case scenario.OpDeleteState, scenario.OpClearState:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就删除状态", i)
			}
			state := make(session.StateMap)
			if op.Kind == scenario.OpClearState {
				current, getErr := svc.GetSession(ctx, key)
				if getErr != nil {
					return nil, fmt.Errorf("op[%d] 读取状态用于清空失败: %w", i, getErr)
				}
				for stateKey := range current.State {
					state[stateKey] = nil
				}
			} else {
				for _, stateKey := range op.DeleteState {
					state[stateKey] = nil
				}
			}
			if err = svc.UpdateSessionState(ctx, key, state); err != nil {
				return nil, fmt.Errorf("op[%d] 删除/清空状态失败: %w", i, err)
			}
		case scenario.OpAppendToolCall:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就追加工具调用", i)
			}
			evt := fixture.NewAssistantToolCallEvent(op.ToolID, op.ToolName, op.ToolArgs)
			applyEventMetadata(evt, op)
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				return nil, fmt.Errorf("op[%d] 追加工具调用失败: %w", i, err)
			}
		case scenario.OpAppendToolResponse:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就追加工具响应", i)
			}
			evt := fixture.NewToolResponseEvent(op.ToolID, op.ToolName, op.Content)
			applyEventMetadata(evt, op)
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				return nil, fmt.Errorf("op[%d] 追加工具响应失败: %w", i, err)
			}

		case scenario.OpUpdateSummary:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就更新摘要", i)
			}
			if err = svc.CreateSessionSummary(ctx, sess, op.FilterKey, op.Force); err != nil {
				return nil, fmt.Errorf("op[%d] 更新摘要失败: %w", i, err)
			}
			// 修改了session 需要重新获取session
			sess, err = svc.GetSession(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("op[%d] 摘要后重新获取 session 失败: %w", i, err)
			}

		case scenario.OpAppendTrack:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就追加 track", i)
			}
			trackSvc, ok := svc.(session.TrackService)
			if !ok {
				return nil, fmt.Errorf("op[%d] 后端不支持 track", i)
			}
			trackEvt := &session.TrackEvent{
				Track:     session.Track(op.TrackName),
				Payload:   []byte(op.TrackPayload),
				Timestamp: time.Now(),
			}
			if err = trackSvc.AppendTrackEvent(ctx, sess, trackEvt); err != nil {
				return nil, fmt.Errorf("op[%d] 追加 track 失败: %w", i, err)
			}
			sess, err = svc.GetSession(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("op[%d] track 后重新获取 session 失败: %w", i, err)
			}

		case scenario.OpConcurrentAppend:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就并发追加", i)
			}
			if err = appendConcurrently(ctx, svc, sess, op.Concurrent); err != nil {
				return nil, fmt.Errorf("op[%d] 并发追加失败: %w", i, err)
			}

		case scenario.OpAppendStateDelta:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] 未创建 session 就追加状态变化", i)
			}
			evt := &event.Event{}
			applyEventMetadata(evt, op)
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				return nil, fmt.Errorf("op[%d] 追加状态变化失败: %w", i, err)
			}

		default:
			return nil, fmt.Errorf("op[%d] 不支持的操作类型 %q", i, op.Kind)
		}
	}
	if sess == nil {
		return nil, fmt.Errorf("用例 %q 从未创建 session", c.Name)
	}
	if c.FinalEventNum > 0 {
		// 只保留最近 N 条事件，用于验证摘要截断后的读取行为。
		return svc.GetSession(ctx, key, session.WithEventNum(c.FinalEventNum))
	}
	return svc.GetSession(ctx, key)
}

// 根据不同角色 构建不同事件
func buildEvent(op scenario.Op) (*event.Event, error) {

	var evt *event.Event
	switch op.Role {
	case "user":
		evt = fixture.NewUserEvent(op.Content)
	case "assistant":
		evt = fixture.NewAssistantEvent(op.Content)

	default:
		return nil, fmt.Errorf("不支持的角色 %q", op.Role)
	}

	applyEventMetadata(evt, op)
	return evt, nil
}

// 把 scenario.Op 中的元数据写回 event.Event。
func applyEventMetadata(evt *event.Event, op scenario.Op) {
	evt.ID = op.EventID
	evt.Author = op.Author
	evt.Branch = op.Branch
	evt.Tag = op.Tag
	evt.FilterKey = op.FilterKey
	if !op.Timestamp.IsZero() {
		evt.Timestamp = op.Timestamp
	}
	if len(op.StateDelta) > 0 {
		evt.StateDelta = make(map[string][]byte, len(op.StateDelta))
		for key, value := range op.StateDelta {
			evt.StateDelta[key] = []byte(value)
		}
	}
	if len(op.Extensions) > 0 {
		evt.Extensions = make(map[string]json.RawMessage, len(op.Extensions))
		for key, value := range op.Extensions {
			evt.Extensions[key] = []byte(value)
		}
	}
}

// 并发追加事件，每个 goroutine 使用独立 session 副本避免竞态。
func appendConcurrently(
	ctx context.Context,
	svc session.Service,
	sess *session.Session,
	ops []scenario.Op,
) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(ops))
	sessionClones := make([]*session.Session, len(ops))
	for i := range ops {
		// Clone 后各 goroutine 只改自己的副本，最终仍写入同一后端 session。
		sessionClones[i] = sess.Clone()
	}
	for i, op := range ops {
		op := op
		localSession := sessionClones[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if op.DelayMS > 0 {
				time.Sleep(time.Duration(op.DelayMS) * time.Millisecond)
			}
			evt, err := buildEvent(op)
			if err != nil {
				errCh <- err
				return
			}
			if err := svc.AppendEvent(ctx, localSession, evt); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}
