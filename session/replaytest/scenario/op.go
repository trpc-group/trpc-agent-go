//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package scenario defines replay cases and operations for session backends.
package scenario

import "time"

// Case 定义一条回放用例。
type Case struct {
	Name          string // case 名称
	Ops           []Op   // case 操作
	FinalEventNum int    // 最终读取时保留的最近事件数，0 表示全部
}

// OpKind 表示回放操作类型。
type OpKind string

// Op 定义一条可回放操作及其参数。
type Op struct {
	// 通用消息和状态参数。
	Kind        OpKind            // 具体操作类型
	Role        string            // user / assistant
	Content     string            // 消息内容
	State       map[string]string // session 状态更新
	DeleteState []string          // 用 nil 墓碑删除的 key
	ClearState  bool              // 清空当前所有状态 key

	// 工具调用和工具响应参数。
	ToolID   string // 工具调用 ID
	ToolName string // 工具名称
	ToolArgs string // 工具参数 JSON

	// Summary 参数。
	FilterKey string // 事件或 Summary 的过滤分支
	Force     bool   // 是否强制重新生成 Summary

	// Track 参数。
	TrackName    string // Track 名称
	TrackPayload string // Track 事件 JSON

	// Event 元数据，用于验证顺序、分支和扩展字段。
	EventID    string            // 固定事件 ID
	Author     string            // 事件作者
	Branch     string            // Agent 执行分支
	Tag        string            // 业务标签
	Timestamp  time.Time         // 固定事件时间
	StateDelta map[string]string // 事件携带的状态变化
	Extensions map[string]string // 事件扩展 JSON

	// 并发回放参数。
	Concurrent []Op // 并发执行的子操作
}

// 回放支持的操作类型。
const (
	OpCreateSession        OpKind = "create_session"
	OpAppendEvent          OpKind = "append_event"
	OpAppendEventWithRetry OpKind = "append_event_with_retry"
	OpUpdateState          OpKind = "update_state"
	OpDeleteState          OpKind = "delete_state"
	OpClearState           OpKind = "clear_state"
	OpWriteInMemory        OpKind = "write_in_memory"
	OpUpdateSummary        OpKind = "update_summary"
	OpAppendToolCall       OpKind = "append_tool_call"
	OpAppendToolResponse   OpKind = "append_tool_response"
	OpCreateSummary        OpKind = "create_summary"
	OpAppendTrack          OpKind = "append_track"
	OpConcurrentAppend     OpKind = "concurrent_append"
	OpAppendStateDelta     OpKind = "append_state_delta"
)
