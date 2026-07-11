//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package normalize

// 归一化： 将不同后端拿出的东西 处理转化/映射成 在同一评价标准下

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/tool"
)

// 同一的格式
type Event struct {
	Index      int               // 事件序号
	ID         string            // 事件 ID
	Author     string            // 作者
	Role       string            // 消息角色
	Content    string            // 消息内容
	ToolID     string            // 工具响应 ID
	ToolName   string            // 工具名称
	ToolArgs   string            // 工具参数
	ToolCalls  []ToolCall        // 助手发起的工具调用
	FilterKey  string            // 过滤分支
	Branch     string            // Agent 分支
	Tag        string            // 业务标签
	StateDelta map[string]string // 事件携带的状态变化
	Extensions map[string]string // 扩展 JSON，已规范化
}
type Summary struct {
	FilterKey    string // 摘要过滤键
	Text         string // 摘要正文
	Version      int    // 边界版本
	UpdatedAtSet bool   // 是否设置了更新时间
	CutoffAtSet  bool   // 是否设置了截断时间
	LastEventID  string // 摘要覆盖的最后事件 ID
}

type ToolCall struct {
	ID   string // 工具调用 ID
	Name string // 工具名称
	Args string // 工具参数 JSON
}
type TrackEvent struct {
	Index     int    // Track 内序号
	TrackName string // Track 名称
	Payload   string // 事件 JSON，已规范化
}

type SnapShot struct {
	SessionId string                  // 会话 ID
	Events    []Event                 // 事件列表
	State     map[string]string       // 会话状态
	Summaries map[string]Summary      // 按 filterKey 索引的摘要
	Tracks    map[string][]TrackEvent // 按 track 名称索引的事件
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
	snapshot.Tracks = NormalizeTracks(sess)
	return snapshot
}

// 把 session.StateMap 转成 string map，方便比较。
func NormalizeState(state session.StateMap) map[string]string {
	normalizedState := make(map[string]string)
	for k, v := range state {
		if v == nil {
			continue
		}
		normalizedState[k] = string(v)
	}
	return normalizedState
}

// 把单个 event.Event 转成归一化事件，忽略无消息体的事件。
func NormalizeEvent(index int, evt event.Event) Event {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return Event{Index: index}
	}
	msg := evt.Response.Choices[0].Message
	return Event{
		Index:      index,
		ID:         evt.ID,
		Author:     evt.Author,
		Role:       string(msg.Role),
		Content:    msg.Content,
		ToolID:     msg.ToolID,
		ToolName:   msg.ToolName,
		ToolCalls:  NormalizeToolCalls(msg.ToolCalls),
		FilterKey:  evt.FilterKey,
		Branch:     evt.Branch,
		Tag:        evt.Tag,
		StateDelta: normalizeByteMap(evt.StateDelta),
		Extensions: normalizeRawMessageMap(evt.Extensions),
	}
}

// 把 model.ToolCall 转成可比较的 ToolCall 切片。
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

// 归一化 session 中的摘要，只保留是否设置时间戳等稳定字段。
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
		normalized := Summary{
			FilterKey:    filterKey,
			Text:         summary.Summary,
			UpdatedAtSet: !summary.UpdatedAt.IsZero(),
		}
		if summary.Boundary != nil {
			normalized.Version = summary.Boundary.Version
			normalized.CutoffAtSet = !summary.Boundary.CutoffAt.IsZero()
			normalized.LastEventID = summary.Boundary.LastEventID
		}
		normalziedsum[filterKey] = normalized
	}

	return normalziedsum
}

// 归一化 session 中的 Track 事件，并对 payload 做 JSON 规范化。
func NormalizeTracks(sess *session.Session) map[string][]TrackEvent {
	normalizedTracks := make(map[string][]TrackEvent)
	if sess == nil {
		return normalizedTracks
	}

	for trackName, history := range sess.Tracks {
		if history == nil {
			continue
		}
		events := make([]TrackEvent, 0, len(history.Events))
		for i, evt := range history.Events {
			events = append(events, TrackEvent{
				Index:     i,
				TrackName: string(trackName),
				Payload:   tool.NormalizeJSON(evt.Payload),
			})
		}
		normalizedTracks[string(trackName)] = events
	}

	return normalizedTracks
}

// 把 []byte map 转成 string map。
func normalizeByteMap(values map[string][]byte) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = string(value)
	}
	return out
}

// 把 RawMessage map 转成规范化 JSON 字符串 map。
func normalizeRawMessageMap(values map[string]json.RawMessage) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = tool.NormalizeJSON(value)
	}
	return out
}
