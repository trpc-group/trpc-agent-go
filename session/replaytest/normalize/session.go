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

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/tool"
)

// Event is the normalized representation of a session event.
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

// Summary is the normalized representation of a session summary.
type Summary struct {
	FilterKey    string // 摘要过滤键
	Text         string // 摘要正文
	Version      int    // 边界版本
	UpdatedAtSet bool   // 是否设置了更新时间
	CutoffAtSet  bool   // 是否设置了截断时间
	LastEventID  string // 摘要覆盖的最后事件 ID
}

// ToolCall is the normalized representation of a model tool call.
type ToolCall struct {
	ID   string // 工具调用 ID
	Name string // 工具名称
	Args string // 工具参数 JSON
}

// TrackEvent is the normalized representation of a track event.
type TrackEvent struct {
	Index     int    // Track 内序号
	TrackName string // Track 名称
	Payload   string // 事件 JSON，已规范化
}

// SnapShot is the normalized representation of a full session state.
type SnapShot struct {
	SessionId string                  // 会话 ID
	Events    []Event                 // 事件列表
	State     map[string]string       // 会话状态
	Summaries map[string]Summary      // 按 filterKey 索引的摘要
	Tracks    map[string][]TrackEvent // 按 track 名称索引的事件
}

// FromSession converts a session into a normalized snapshot.
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

// NormalizeState converts session.StateMap into a string map for comparison.
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

// NormalizeEvent converts a single event.Event into a normalized event.
func NormalizeEvent(index int, evt event.Event) Event {
	normalized := Event{
		Index:      index,
		ID:         evt.ID,
		Author:     evt.Author,
		FilterKey:  evt.FilterKey,
		Branch:     evt.Branch,
		Tag:        evt.Tag,
		StateDelta: normalizeByteMap(evt.StateDelta),
		Extensions: normalizeRawMessageMap(evt.Extensions),
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return normalized
	}
	msg := evt.Response.Choices[0].Message
	normalized.Role = string(msg.Role)
	normalized.Content = msg.Content
	normalized.ToolID = msg.ToolID
	normalized.ToolName = msg.ToolName
	normalized.ToolCalls = NormalizeToolCalls(msg.ToolCalls)
	return normalized
}

// NormalizeToolCalls converts model tool calls into comparable ToolCall values.
// Arguments are canonicalized as JSON so two semantically identical payloads that
// only differ in field order or whitespace compare as equal.
func NormalizeToolCalls(calls []model.ToolCall) []ToolCall {
	normalizedToolcalls := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		normalizedToolcalls = append(normalizedToolcalls, ToolCall{
			ID:   call.ID,
			Name: call.Function.Name,
			Args: tool.NormalizeJSON(call.Function.Arguments),
		})
	}
	return normalizedToolcalls
}

// NormalizeSummaries converts session summaries into comparable Summary values.
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

// NormalizeTracks converts session tracks into comparable TrackEvent values.
func NormalizeTracks(sess *session.Session) map[string][]TrackEvent {
	normalizedTracks := make(map[string][]TrackEvent)
	if sess == nil {
		return normalizedTracks
	}

	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()

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
