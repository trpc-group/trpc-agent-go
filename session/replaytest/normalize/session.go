package normalize

// 归一化： 将不同后端拿出的东西 处理转化/映射成 在同一评价标准下

import (
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// 同一的格式
type Event struct {
	Index     int
	Role      string
	Content   string
	ToolID    string
	ToolName  string
	ToolArgs  string
	ToolCalls []ToolCall
	FilterKey string
}
type Summary struct {
	FilterKey string
	Text      string
	Version   int
	UpdateAt  bool
}

type ToolCall struct {
	ID   string
	Name string
	Args string
}

type SnapShot struct {
	SessionId string
	Events    []Event
	State     map[string]string
	Summaries map[string]Summary
}

//从某一session 转化得到 snapshot
// 获得snapshot的最后接口 其他 归一化在这个函数内 以及之前进行

func FromSession(sess *session.Session) *SnapShot {

	if sess == nil {
		return &SnapShot{}
	}

	snapshot := &SnapShot{
		SessionId: sess.ID,
		Events:    make([]Event, 0, len(sess.Events)),
		State:     make(map[string]string),
	}

	for i, evt := range sess.Events {
		snapshot.Events = append(snapshot.Events, NormalizeEvent(i, evt))
	}
	// 处理状态
	snapshot.State = NormalizeState(sess.State)
	snapshot.Summaries = NormalizeSummaries(sess)
	return snapshot
}

func NormalizeState(state session.StateMap) map[string]string {
	normalizedState := make(map[string]string)
	for k, v := range state {
		normalizedState[k] = string(v)
	}
	return normalizedState
}

func NormalizeEvent(index int, evt event.Event) Event {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return Event{Index: index}
	}
	msg := evt.Response.Choices[0].Message
	return Event{
		Index:     index,
		Role:      string(msg.Role),
		Content:   msg.Content,
		ToolID:    msg.ToolID,
		ToolName:  msg.ToolName,
		ToolCalls: NormalizeToolCalls(msg.ToolCalls),
		FilterKey: evt.FilterKey,
	}
}

func NormalizeToolCalls(calls []model.ToolCall) []ToolCall {
	normalizedToolcalls := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		normalizedToolcalls = append(normalizedToolcalls, ToolCall{
			ID:   call.ID,
			Name: call.Function.Name,
			Args: string(call.Function.Arguments),
		})
	}
	return normalizedToolcalls
}
func NormalizeSummaries(sess *session.Session) map[string]Summary {
	normalziedsum := make(map[string]Summary)
	if sess == nil {
		return normalziedsum
	}

	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()

	for filterKey, summary := range sess.Summaries {
		if summary == nil {
			continue
		}
		normalziedsum[filterKey] = Summary{
			FilterKey: filterKey,
			Text:      summary.Summary,
			Version:   summary.Boundary.Version,
			UpdateAt:  !summary.UpdatedAt.IsZero(),
		}
	}

	return normalziedsum
}
