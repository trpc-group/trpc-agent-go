package react

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Error("New() returned nil")
	}

	// Verify interface implementation.
	var _ planner.Planner = p
}

func TestPlanner_BuildPlanningInstruction(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-001",
	}
	request := &model.Request{}

	instruction := p.BuildPlanningInstruction(ctx, invocation, request)

	// Verify instruction is not empty.
	if instruction == "" {
		t.Error("BuildPlanningInstruction() returned empty string")
	}

	// Verify instruction contains required tags.
	expectedTags := []string{
		PlanningTag,
		ReasoningTag,
		ActionTag,
		FinalAnswerTag,
		ReplanningTag,
	}

	for _, tag := range expectedTags {
		if !strings.Contains(instruction, tag) {
			t.Errorf("BuildPlanningInstruction() missing tag: %s", tag)
		}
	}

	// Verify instruction contains key concepts.
	expectedConcepts := []string{
		"plan",
		"tools",
		"reasoning",
		"final answer",
		"step",
	}

	for _, concept := range expectedConcepts {
		if !strings.Contains(strings.ToLower(instruction), concept) {
			t.Errorf("BuildPlanningInstruction() missing concept: %s", concept)
		}
	}
}

func TestPlanner_ProcessPlanningResponse_NilResponse(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	result := p.ProcessPlanningResponse(ctx, invocation, nil)
	if result != nil {
		t.Error("ProcessPlanningResponse() with nil response should return nil")
	}
}

func TestPlanner_ProcessPlanningResponse_EmptyChoices(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}
	response := &model.Response{
		Choices: []model.Choice{},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result != nil {
		t.Error("ProcessPlanningResponse() with empty choices should return nil")
	}
}

func TestPlanner_ProcessPlanningResponse_WithToolCalls(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	response := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Function: model.FunctionDefinitionParam{
								Name: "valid_tool",
							},
						},
						{
							Function: model.FunctionDefinitionParam{
								Name: "", // Empty name should be filtered
							},
						},
						{
							Function: model.FunctionDefinitionParam{
								Name: "another_tool",
							},
						},
					},
				},
			},
		},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result == nil {
		t.Fatal("ProcessPlanningResponse() returned nil")
	}

	// Verify only valid tool calls are preserved.
	if len(result.Choices) != 1 {
		t.Errorf("Expected 1 choice, got %d", len(result.Choices))
	}

	choice := result.Choices[0]
	if len(choice.Message.ToolCalls) != 2 {
		t.Errorf("Expected 2 tool calls after filtering, got %d", len(choice.Message.ToolCalls))
	}

	// Verify the remaining tool calls have valid names.
	for _, toolCall := range choice.Message.ToolCalls {
		if toolCall.Function.Name == "" {
			t.Error("Tool call with empty name was not filtered")
		}
	}
}

func TestPlanner_ProcessPlanningResponse_WithFinalAnswer(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	originalContent := PlanningTag + " Step 1: Do something\n" + ReasoningTag + " This is reasoning\n" + FinalAnswerTag + " This is the final answer."
	response := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: originalContent,
				},
			},
		},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result == nil {
		t.Fatal("ProcessPlanningResponse() returned nil")
	}

	choice := result.Choices[0]
	expectedContent := " This is the final answer."
	if choice.Message.Content != expectedContent {
		t.Errorf("Expected content %q, got %q", expectedContent, choice.Message.Content)
	}
}

func TestPlanner_ProcessPlanningResponse_WithDeltaContent(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	originalDelta := ReasoningTag + " This is reasoning content."
	response := &model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{
					Role:    model.RoleAssistant,
					Content: originalDelta,
				},
			},
		},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result == nil {
		t.Fatal("ProcessPlanningResponse() returned nil")
	}

	choice := result.Choices[0]
	// Since there's no final answer tag, content should remain as-is.
	if choice.Delta.Content != originalDelta {
		t.Errorf("Expected delta content %q, got %q", originalDelta, choice.Delta.Content)
	}
}

func TestPlanner_ProcessTextContent(t *testing.T) {
	p := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "content with final answer",
			input:    PlanningTag + " Plan something\n" + FinalAnswerTag + " Final result",
			expected: " Final result",
		},
		{
			name:     "content without final answer",
			input:    PlanningTag + " Plan something\n" + ReasoningTag + " Some reasoning",
			expected: PlanningTag + " Plan something\n" + ReasoningTag + " Some reasoning",
		},
		{
			name:     "empty content",
			input:    "",
			expected: "",
		},
		{
			name:     "content with multiple final answer tags",
			input:    FinalAnswerTag + " First" + FinalAnswerTag + " Second",
			expected: " Second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.processTextContent(tt.input)
			if result != tt.expected {
				t.Errorf("processTextContent() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPlanner_SplitByLastPattern(t *testing.T) {
	p := New()

	tests := []struct {
		name      string
		text      string
		separator string
		before    string
		after     string
	}{
		{
			name:      "normal split",
			text:      "Hello SPLIT World",
			separator: "SPLIT",
			before:    "Hello ",
			after:     " World",
		},
		{
			name:      "no separator",
			text:      "Hello World",
			separator: "SPLIT",
			before:    "Hello World",
			after:     "",
		},
		{
			name:      "multiple separators",
			text:      "A SPLIT B SPLIT C",
			separator: "SPLIT",
			before:    "A SPLIT B ",
			after:     " C",
		},
		{
			name:      "empty text",
			text:      "",
			separator: "SPLIT",
			before:    "",
			after:     "",
		},
		{
			name:      "separator at end",
			text:      "Hello SPLIT",
			separator: "SPLIT",
			before:    "Hello ",
			after:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := p.splitByLastPattern(tt.text, tt.separator)
			if before != tt.before {
				t.Errorf("splitByLastPattern() before = %q, want %q", before, tt.before)
			}
			if after != tt.after {
				t.Errorf("splitByLastPattern() after = %q, want %q", after, tt.after)
			}
		})
	}
}

func TestPlanner_BuildPlannerInstruction(t *testing.T) {
	p := New()

	instruction := p.buildPlannerInstruction()

	// Verify instruction is comprehensive.
	if len(instruction) < 1000 {
		t.Error("buildPlannerInstruction() returned too short instruction")
	}

	// Verify it contains all required sections.
	requiredSections := []string{
		"planning",
		"reasoning",
		"final answer",
		"tool",
		"format",
	}

	for _, section := range requiredSections {
		if !strings.Contains(strings.ToLower(instruction), section) {
			t.Errorf("buildPlannerInstruction() missing section: %s", section)
		}
	}

	// Verify it references all tags.
	allTags := []string{
		PlanningTag,
		ReplanningTag,
		ReasoningTag,
		ActionTag,
		FinalAnswerTag,
	}

	for _, tag := range allTags {
		if !strings.Contains(instruction, tag) {
			t.Errorf("buildPlannerInstruction() missing tag: %s", tag)
		}
	}
}

func TestConstants(t *testing.T) {
	// Verify all constants are properly defined.
	expectedTags := map[string]string{
		"PlanningTag":    "/*PLANNING*/",
		"ReplanningTag":  "/*REPLANNING*/",
		"ReasoningTag":   "/*REASONING*/",
		"ActionTag":      "/*ACTION*/",
		"FinalAnswerTag": "/*FINAL_ANSWER*/",
	}

	actualTags := map[string]string{
		"PlanningTag":    PlanningTag,
		"ReplanningTag":  ReplanningTag,
		"ReasoningTag":   ReasoningTag,
		"ActionTag":      ActionTag,
		"FinalAnswerTag": FinalAnswerTag,
	}

	for name, expected := range expectedTags {
		if actualTags[name] != expected {
			t.Errorf("Constant %s = %q, want %q", name, actualTags[name], expected)
		}
	}
}
