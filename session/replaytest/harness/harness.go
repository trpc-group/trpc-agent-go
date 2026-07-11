//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package harness executes replay scenarios against session backends.
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

// Run executes every operation in a replay case against the given session service.
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

	runner := &runner{ctx: ctx, svc: svc, key: key}
	var (
		sess *session.Session
		err  error
	)
	for i, op := range c.Ops {
		sess, err = runner.execute(i, op, sess)
		if err != nil {
			return nil, err
		}
	}
	if sess == nil {
		return nil, fmt.Errorf("用例 %q 从未创建 session", c.Name)
	}
	if c.FinalEventNum > 0 {
		return svc.GetSession(ctx, key, session.WithEventNum(c.FinalEventNum))
	}
	return svc.GetSession(ctx, key)
}

type runner struct {
	ctx context.Context
	svc session.Service
	key session.Key
}

func (r *runner) execute(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	switch op.Kind {
	case scenario.OpCreateSession:
		return r.createSession(i)
	case scenario.OpAppendEvent:
		return r.appendEvent(i, op, sess)
	case scenario.OpAppendEventWithRetry:
		return r.appendEventWithRetry(i, op, sess)
	case scenario.OpUpdateState:
		return r.updateState(i, op, sess)
	case scenario.OpDeleteState, scenario.OpClearState:
		return r.deleteOrClearState(i, op, sess)
	case scenario.OpAppendToolCall:
		return r.appendToolCall(i, op, sess)
	case scenario.OpAppendToolResponse:
		return r.appendToolResponse(i, op, sess)
	case scenario.OpUpdateSummary:
		return r.updateSummary(i, op, sess)
	case scenario.OpAppendTrack:
		return r.appendTrack(i, op, sess)
	case scenario.OpConcurrentAppend:
		return r.concurrentAppend(i, op, sess)
	case scenario.OpAppendStateDelta:
		return r.appendStateDelta(i, op, sess)
	default:
		return nil, fmt.Errorf("op[%d] 不支持的操作类型 %q", i, op.Kind)
	}
}

func (r *runner) createSession(i int) (*session.Session, error) {
	sess, err := r.svc.CreateSession(r.ctx, r.key, nil)
	if err != nil {
		return nil, fmt.Errorf("op[%d] 创建 session 失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) appendEvent(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就追加事件", i)
	}
	evt, buildErr := buildEvent(op)
	if buildErr != nil {
		return nil, fmt.Errorf("op[%d] 构建事件失败: %w", i, buildErr)
	}
	if err := r.svc.AppendEvent(r.ctx, sess, evt); err != nil {
		return nil, fmt.Errorf("op[%d] 追加事件失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) appendEventWithRetry(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就追加事件", i)
	}
	evt, buildErr := buildEvent(op)
	if buildErr != nil {
		return nil, fmt.Errorf("op[%d] 构建重试事件失败: %w", i, buildErr)
	}
	if err := r.svc.AppendEvent(r.ctx, sess, evt); err != nil {
		if err = r.svc.AppendEvent(r.ctx, sess, evt); err != nil {
			return nil, fmt.Errorf("op[%d] 重试追加事件失败: %w", i, err)
		}
	}
	return sess, nil
}

func (r *runner) updateState(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就更新状态", i)
	}
	state := make(session.StateMap)
	for k, v := range op.State {
		state[k] = []byte(v)
	}
	if err := r.svc.UpdateSessionState(r.ctx, r.key, state); err != nil {
		return nil, fmt.Errorf("op[%d] 更新状态失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) deleteOrClearState(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就删除状态", i)
	}
	state := make(session.StateMap)
	if op.Kind == scenario.OpClearState {
		current, getErr := r.svc.GetSession(r.ctx, r.key)
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
	if err := r.svc.UpdateSessionState(r.ctx, r.key, state); err != nil {
		return nil, fmt.Errorf("op[%d] 删除/清空状态失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) appendToolCall(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就追加工具调用", i)
	}
	evt := fixture.NewAssistantToolCallEvent(op.ToolID, op.ToolName, op.ToolArgs)
	applyEventMetadata(evt, op)
	if err := r.svc.AppendEvent(r.ctx, sess, evt); err != nil {
		return nil, fmt.Errorf("op[%d] 追加工具调用失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) appendToolResponse(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就追加工具响应", i)
	}
	evt := fixture.NewToolResponseEvent(op.ToolID, op.ToolName, op.Content)
	applyEventMetadata(evt, op)
	if err := r.svc.AppendEvent(r.ctx, sess, evt); err != nil {
		return nil, fmt.Errorf("op[%d] 追加工具响应失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) updateSummary(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就更新摘要", i)
	}
	if err := r.svc.CreateSessionSummary(r.ctx, sess, op.FilterKey, op.Force); err != nil {
		return nil, fmt.Errorf("op[%d] 更新摘要失败: %w", i, err)
	}
	sess, err := r.svc.GetSession(r.ctx, r.key)
	if err != nil {
		return nil, fmt.Errorf("op[%d] 摘要后重新获取 session 失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) appendTrack(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就追加 track", i)
	}
	trackSvc, ok := r.svc.(session.TrackService)
	if !ok {
		return nil, fmt.Errorf("op[%d] 后端不支持 track", i)
	}
	trackEvt := &session.TrackEvent{
		Track:     session.Track(op.TrackName),
		Payload:   []byte(op.TrackPayload),
		Timestamp: time.Now(),
	}
	if err := trackSvc.AppendTrackEvent(r.ctx, sess, trackEvt); err != nil {
		return nil, fmt.Errorf("op[%d] 追加 track 失败: %w", i, err)
	}
	sess, err := r.svc.GetSession(r.ctx, r.key)
	if err != nil {
		return nil, fmt.Errorf("op[%d] track 后重新获取 session 失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) concurrentAppend(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就并发追加", i)
	}
	if err := appendConcurrently(r.ctx, r.svc, sess, op.Concurrent); err != nil {
		return nil, fmt.Errorf("op[%d] 并发追加失败: %w", i, err)
	}
	return sess, nil
}

func (r *runner) appendStateDelta(
	i int,
	op scenario.Op,
	sess *session.Session,
) (*session.Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("op[%d] 未创建 session 就追加状态变化", i)
	}
	evt := &event.Event{}
	applyEventMetadata(evt, op)
	if err := r.svc.AppendEvent(r.ctx, sess, evt); err != nil {
		return nil, fmt.Errorf("op[%d] 追加状态变化失败: %w", i, err)
	}
	return sess, nil
}

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
