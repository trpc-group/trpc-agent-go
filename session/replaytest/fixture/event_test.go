//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fixture

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewUserEvent(t *testing.T) {
	evt := NewUserEvent("hello")
	if evt.Response.Choices[0].Message.Role != model.RoleUser ||
		evt.Response.Choices[0].Message.Content != "hello" {
		t.Fatalf("user event 内容错误: %+v", evt)
	}
}

func TestNewAssistantEvent(t *testing.T) {
	evt := NewAssistantEvent("reply")
	if evt.Response.Choices[0].Message.Role != model.RoleAssistant ||
		evt.Response.Choices[0].Message.Content != "reply" {
		t.Fatalf("assistant event 内容错误: %+v", evt)
	}
}

func TestNewAssistantToolCallEvent(t *testing.T) {
	evt := NewAssistantToolCallEvent("id-1", "weather", `{"city":"北京"}`)
	msg := evt.Response.Choices[0].Message
	if len(msg.ToolCalls) != 1 ||
		msg.ToolCalls[0].ID != "id-1" ||
		msg.ToolCalls[0].Function.Name != "weather" ||
		string(msg.ToolCalls[0].Function.Arguments) != `{"city":"北京"}` {
		t.Fatalf("tool call event 内容错误: %+v", evt)
	}
}

func TestNewToolResponseEvent(t *testing.T) {
	evt := NewToolResponseEvent("id-1", "weather", `{"weather":"晴"}`)
	msg := evt.Response.Choices[0].Message
	if msg.Role != model.RoleTool ||
		msg.ToolID != "id-1" ||
		msg.ToolName != "weather" ||
		msg.Content != `{"weather":"晴"}` {
		t.Fatalf("tool response event 内容错误: %+v", evt)
	}
}
