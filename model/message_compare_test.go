package model

import (
	"testing"
)

func TestMessagesEqual_Simple(t *testing.T) {
	a := NewUserMessage("hello")
	b := NewUserMessage("hello")
	if !MessagesEqual(a, b) {
		t.Fatalf("expected equal for identical user messages")
	}

	c := NewAssistantMessage("hello")
	if MessagesEqual(a, c) {
		t.Fatalf("expected not equal when roles differ")
	}

	d := NewUserMessage("hello!")
	if MessagesEqual(a, d) {
		t.Fatalf("expected not equal when content differs")
	}
}

func TestMessagesEqual_ContentParts(t *testing.T) {
	text := "part"
	m1 := Message{Role: RoleUser, Content: "", ContentParts: []ContentPart{{Type: ContentTypeText, Text: &text}}}
	m2 := Message{Role: RoleUser, Content: "", ContentParts: []ContentPart{{Type: ContentTypeText, Text: &text}}}
	if !MessagesEqual(m1, m2) {
		t.Fatalf("expected equal when content parts match")
	}

	diff := "part2"
	m3 := Message{Role: RoleUser, Content: "", ContentParts: []ContentPart{{Type: ContentTypeText, Text: &diff}}}
	if MessagesEqual(m1, m3) {
		t.Fatalf("expected not equal when content parts differ")
	}
}

func TestMessagesEqual_ToolFields(t *testing.T) {
	// Tool result comparison
	toolMsg1 := Message{Role: RoleTool, ToolID: "call_1", ToolName: "fn", Content: "res"}
	toolMsg2 := Message{Role: RoleTool, ToolID: "call_1", ToolName: "fn", Content: "res"}
	if !MessagesEqual(toolMsg1, toolMsg2) {
		t.Fatalf("expected equal tool result messages")
	}
	toolMsg3 := Message{Role: RoleTool, ToolID: "call_2", ToolName: "fn", Content: "res"}
	if MessagesEqual(toolMsg1, toolMsg3) {
		t.Fatalf("expected not equal when tool id differs")
	}

	// Tool calls comparison
	args1 := []byte(`{"x":1}`)
	args2 := []byte(`{"x":2}`)
	callMsg1 := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Type: "function", ID: "t1", Function: FunctionDefinitionParam{Name: "echo", Arguments: args1}}}}
	callMsg2 := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Type: "function", ID: "t1", Function: FunctionDefinitionParam{Name: "echo", Arguments: args1}}}}
	if !MessagesEqual(callMsg1, callMsg2) {
		t.Fatalf("expected equal tool call messages")
	}
	callMsg3 := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Type: "function", ID: "t1", Function: FunctionDefinitionParam{Name: "echo", Arguments: args2}}}}
	if MessagesEqual(callMsg1, callMsg3) {
		t.Fatalf("expected not equal when tool call args differ")
	}
}

func TestMessagesEqual_ReasoningContent(t *testing.T) {
	a := Message{Role: RoleAssistant, Content: "ok", ReasoningContent: "think1"}
	b := Message{Role: RoleAssistant, Content: "ok", ReasoningContent: "think1"}
	if !MessagesEqual(a, b) {
		t.Fatalf("expected equal when reasoning content same")
	}
	c := Message{Role: RoleAssistant, Content: "ok", ReasoningContent: "think2"}
	if MessagesEqual(a, c) {
		t.Fatalf("expected not equal when reasoning content differs")
	}
}
