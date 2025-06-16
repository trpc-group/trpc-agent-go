package event

import (
	"testing"
)

func TestNewTextContent(t *testing.T) {
	text := "Hello, world!"
	content := NewTextContent(text)

	if content == nil {
		t.Fatal("NewTextContent returned nil")
	}

	if len(content.Parts) != 1 {
		t.Fatalf("Expected 1 part, got %d", len(content.Parts))
	}

	if content.Parts[0].Text != text {
		t.Fatalf("Expected text %q, got %q", text, content.Parts[0].Text)
	}
}

func TestNewContentWithParts(t *testing.T) {
	parts := []*Part{
		NewTextPart("Hello"),
		NewTextPart("World"),
	}

	content := NewContentWithParts(parts)

	if content == nil {
		t.Fatal("NewContentWithParts returned nil")
	}

	if len(content.Parts) != 2 {
		t.Fatalf("Expected 2 parts, got %d", len(content.Parts))
	}

	if content.Parts[0].Text != "Hello" {
		t.Fatalf("Expected first part text 'Hello', got %q", content.Parts[0].Text)
	}

	if content.Parts[1].Text != "World" {
		t.Fatalf("Expected second part text 'World', got %q", content.Parts[1].Text)
	}
}

func TestNewFunctionCallPart(t *testing.T) {
	name := "calculator"
	args := map[string]interface{}{
		"a":         2,
		"b":         3,
		"operation": "add",
	}

	part := NewFunctionCallPart(name, args)

	if part == nil {
		t.Fatal("NewFunctionCallPart returned nil")
	}

	if part.FunctionCall == nil {
		t.Fatal("FunctionCall is nil")
	}

	if part.FunctionCall.Name != name {
		t.Fatalf("Expected function name %q, got %q", name, part.FunctionCall.Name)
	}

	if len(part.FunctionCall.Arguments) != 3 {
		t.Fatalf("Expected 3 arguments, got %d", len(part.FunctionCall.Arguments))
	}

	if part.FunctionCall.Arguments["a"] != 2 {
		t.Fatalf("Expected argument 'a' to be 2, got %v", part.FunctionCall.Arguments["a"])
	}
}

func TestNewFunctionResponsePart(t *testing.T) {
	name := "calculator"
	result := 5

	part := NewFunctionResponsePart(name, result)

	if part == nil {
		t.Fatal("NewFunctionResponsePart returned nil")
	}

	if part.FunctionResponse == nil {
		t.Fatal("FunctionResponse is nil")
	}

	if part.FunctionResponse.Name != name {
		t.Fatalf("Expected function name %q, got %q", name, part.FunctionResponse.Name)
	}

	if part.FunctionResponse.Result != result {
		t.Fatalf("Expected result %v, got %v", result, part.FunctionResponse.Result)
	}
}

func TestContentGetText(t *testing.T) {
	content := NewContentWithParts([]*Part{
		NewTextPart("Hello"),
		NewFunctionCallPart("tool", map[string]interface{}{}),
		NewTextPart("World"),
	})

	text := content.GetText()
	expected := "Hello World"

	if text != expected {
		t.Fatalf("Expected text %q, got %q", expected, text)
	}
}

func TestContentGetFunctionCalls(t *testing.T) {
	call1 := &FunctionCall{Name: "tool1", Arguments: map[string]interface{}{"x": 1}}
	call2 := &FunctionCall{Name: "tool2", Arguments: map[string]interface{}{"y": 2}}

	content := NewContentWithParts([]*Part{
		NewTextPart("Hello"),
		{FunctionCall: call1},
		{FunctionCall: call2},
	})

	calls := content.GetFunctionCalls()

	if len(calls) != 2 {
		t.Fatalf("Expected 2 function calls, got %d", len(calls))
	}

	if calls[0].Name != "tool1" {
		t.Fatalf("Expected first call name 'tool1', got %q", calls[0].Name)
	}

	if calls[1].Name != "tool2" {
		t.Fatalf("Expected second call name 'tool2', got %q", calls[1].Name)
	}
}

func TestContentGetFunctionResponses(t *testing.T) {
	resp1 := &FunctionResponse{Name: "tool1", Result: "result1"}
	resp2 := &FunctionResponse{Name: "tool2", Result: "result2"}

	content := NewContentWithParts([]*Part{
		NewTextPart("Hello"),
		{FunctionResponse: resp1},
		{FunctionResponse: resp2},
	})

	responses := content.GetFunctionResponses()

	if len(responses) != 2 {
		t.Fatalf("Expected 2 function responses, got %d", len(responses))
	}

	if responses[0].Name != "tool1" {
		t.Fatalf("Expected first response name 'tool1', got %q", responses[0].Name)
	}

	if responses[1].Name != "tool2" {
		t.Fatalf("Expected second response name 'tool2', got %q", responses[1].Name)
	}
}

func TestContentHasFunctionCalls(t *testing.T) {
	// Content with function calls
	contentWithCalls := NewContentWithParts([]*Part{
		NewTextPart("Hello"),
		NewFunctionCallPart("tool", map[string]interface{}{}),
	})

	if !contentWithCalls.HasFunctionCalls() {
		t.Fatal("Expected HasFunctionCalls to be true")
	}

	// Content without function calls
	contentWithoutCalls := NewContentWithParts([]*Part{
		NewTextPart("Hello"),
		NewTextPart("World"),
	})

	if contentWithoutCalls.HasFunctionCalls() {
		t.Fatal("Expected HasFunctionCalls to be false")
	}
}

func TestContentHasText(t *testing.T) {
	// Content with text
	contentWithText := NewContentWithParts([]*Part{
		NewTextPart("Hello"),
		NewFunctionCallPart("tool", map[string]interface{}{}),
	})

	if !contentWithText.HasText() {
		t.Fatal("Expected HasText to be true")
	}

	// Content without text
	contentWithoutText := NewContentWithParts([]*Part{
		NewFunctionCallPart("tool", map[string]interface{}{}),
	})

	if contentWithoutText.HasText() {
		t.Fatal("Expected HasText to be false")
	}
}

func TestContentAddMethods(t *testing.T) {
	content := &Content{}

	// Add text
	content.AddText("Hello")
	if len(content.Parts) != 1 || content.Parts[0].Text != "Hello" {
		t.Fatal("AddText failed")
	}

	// Add function call
	content.AddFunctionCall("tool", map[string]interface{}{"x": 1})
	if len(content.Parts) != 2 || content.Parts[1].FunctionCall == nil {
		t.Fatal("AddFunctionCall failed")
	}

	// Add function response
	content.AddFunctionResponse("tool", "result")
	if len(content.Parts) != 3 || content.Parts[2].FunctionResponse == nil {
		t.Fatal("AddFunctionResponse failed")
	}
}

func TestContentString(t *testing.T) {
	content := NewContentWithParts([]*Part{
		NewTextPart("Hello"),
		NewFunctionCallPart("calculator", map[string]interface{}{"x": 1}),
		NewFunctionResponsePart("calculator", 42),
	})

	str := content.String()
	expected := "Content[Text: Hello, FunctionCall: calculator, FunctionResponse: calculator]"

	if str != expected {
		t.Fatalf("Expected string %q, got %q", expected, str)
	}
}

func TestEmptyContent(t *testing.T) {
	// Test nil content
	var content *Content = nil

	if content.GetText() != "" {
		t.Fatal("Expected empty text for nil content")
	}

	if content.HasText() {
		t.Fatal("Expected HasText to be false for nil content")
	}

	if content.HasFunctionCalls() {
		t.Fatal("Expected HasFunctionCalls to be false for nil content")
	}

	// Test empty content
	emptyContent := &Content{}

	if emptyContent.GetText() != "" {
		t.Fatal("Expected empty text for empty content")
	}

	if emptyContent.HasText() {
		t.Fatal("Expected HasText to be false for empty content")
	}

	if emptyContent.HasFunctionCalls() {
		t.Fatal("Expected HasFunctionCalls to be false for empty content")
	}
}
