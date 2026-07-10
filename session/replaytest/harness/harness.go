package harness

import (
	"context"
	"fmt"

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
		return nil, fmt.Errorf("case is nil")
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
	for i, op := range c.Ops {
		switch op.Kind {
		case scenario.OpCreateSession:
			sess, err = svc.CreateSession(ctx, key, nil)
			if err != nil {
				return nil, fmt.Errorf("op[%d] create session: %w", i, err)
			}

		case scenario.OpAppendEvent:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] append event before create session", i)
			}
			evt, buildErr := buildEvent(op)
			if buildErr != nil {
				return nil, fmt.Errorf("op[%d] build event: %w", i, buildErr)
			}
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				return nil, fmt.Errorf("op[%d] append event: %w", i, err)
			}
		case scenario.OpUpdateState:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] append event before create session", i)
			}
			// 将状态转换为session.StateMap 格式
			state := make(session.StateMap)
			for k, v := range op.State {
				state[k] = []byte(v)
			}
			if err = svc.UpdateSessionState(ctx, key, state); err != nil {
				return nil, fmt.Errorf("op[%d] update state: %w", i, err)
			}
		case scenario.OpAppendToolCall:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] append event before create session", i)
			}
			evt := fixture.NewAssistantToolCallEvent(op.ToolID, op.ToolName, op.ToolArgs)
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				return nil, fmt.Errorf("op[%d] append tool call: %w", i, err)
			}
		case scenario.OpAppendToolResponse:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] append event before create session", i)
			}
			evt := fixture.NewToolResponseEvent(op.ToolID, op.ToolName, op.Content)
			if err = svc.AppendEvent(ctx, sess, evt); err != nil {
				return nil, fmt.Errorf("op[%d] append tool response: %w", i, err)
			}

		case scenario.OpUpdateSummary:
			if sess == nil {
				return nil, fmt.Errorf("op[%d] update summary before create session", i)
			}
			if err = svc.CreateSessionSummary(ctx, sess, op.FilterKey, op.Force); err != nil {
				return nil, fmt.Errorf("op[%d] update summary: %w", i, err)
			}
			// 修改了session 需要重新获取session
			sess, err = svc.GetSession(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("op[%d] get session after summary: %w", i, err)
			}
		default:
			return nil, fmt.Errorf("op[%d] unsupported kind %q", i, op.Kind)
		}
	}
	if sess == nil {
		return nil, fmt.Errorf("case %q never created session", c.Name)
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
		return nil, fmt.Errorf("unsupported role %q", op.Role)
	}

	evt.FilterKey = op.FilterKey
	return evt, nil
}
