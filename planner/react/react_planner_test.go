//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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

func TestPlanner_BuildPlanInstr(t *testing.T) {
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

func TestPlanner_ProcessPlanResp_Nil(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	result := p.ProcessPlanningResponse(ctx, invocation, nil)
	if result != nil {
		t.Error("ProcessPlanningResponse() with nil response should return nil")
	}
}

func TestPlanner_ProcessPlanResp_Empty(t *testing.T) {
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

func TestPlanner_ToolCalls(t *testing.T) {
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

func TestPlanner_FinalAns(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	originalContent := PlanningTag + " Step 1: Do something\n" +
		ReasoningTag + " This is reasoning\n" +
		FinalAnswerTag + " This is the final answer."
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
	// Current implementation preserves original content without processing
	if choice.Message.Content != originalContent {
		t.Errorf("Expected content %q, got %q", originalContent, choice.Message.Content)
	}
}

func TestPlanner_ProcessPlanResp_Delta(t *testing.T) {
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

func TestPlanner_IntentDescriptionDetection(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	tests := []struct {
		name         string
		content      string
		done         bool
		expectedDone bool
		description  string
	}{
		{
			name:         "intent_description_i_will",
			content:      "I will fetch the Special:Log page for the Legume article to inspect log entries.",
			done:         true,
			expectedDone: false, // Should be marked as not done
			description:  "Intent description with 'I will' should not be final",
		},
		{
			name:         "intent_with_action_tag",
			content:      "/*ACTION*/\nI will search for the information.\nfunctions.web_search",
			done:         true,
			expectedDone: false,
			description:  "Content with ACTION tag but no actual tool call should not be final",
		},
		{
			name:         "intent_with_planning_tag",
			content:      "/*PLANNING*/\n1. First search for the author\n2. Then find their publications",
			done:         true,
			expectedDone: false,
			description:  "Content with PLANNING tag should not be final",
		},
		{
			name:         "final_answer_with_tag",
			content:      "/*FINAL_ANSWER*/ The answer is 42.",
			done:         true,
			expectedDone: true, // Should remain done because it has FINAL_ANSWER tag
			description:  "Content with FINAL_ANSWER tag should be final",
		},
		{
			name:         "empty_final_answer_tag",
			content:      FinalAnswerTag,
			done:         true,
			expectedDone: false,
			description:  "Empty FINAL_ANSWER tag should not be final",
		},
		{
			name: "final_answer_tag_then_action",
			content: strings.Join(
				[]string{FinalAnswerTag, ActionTag},
				"\n\n",
			),
			done:         true,
			expectedDone: false,
			description: "FINAL_ANSWER tag without answer should not " +
				"be final",
		},
		{
			name: "action_with_final_answer_prefix",
			content: strings.Join(
				[]string{
					ActionTag,
					finalAnswerPrefix + " 42",
				},
				"\n",
			),
			done:         true,
			expectedDone: true,
			description: "FINAL ANSWER line should allow a final " +
				"response",
		},
		{
			name:         "intent_with_final_answer",
			content:      "I will provide the answer now. /*FINAL_ANSWER*/ The result is 42.",
			done:         true,
			expectedDone: true, // Has FINAL_ANSWER, so it's final
			description:  "Content with both intent and FINAL_ANSWER should be final",
		},
		{
			name:         "normal_response_no_intent",
			content:      "The capital of France is Paris.",
			done:         true,
			expectedDone: true, // No intent patterns, should remain as is
			description:  "Normal response without intent patterns should remain final",
		},
		{
			name:         "empty_content",
			content:      "",
			done:         true,
			expectedDone: true, // Empty content is not an intent description
			description:  "Empty content should remain final",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &model.Response{
				Done: tt.done,
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: tt.content,
						},
					},
				},
			}

			result := p.ProcessPlanningResponse(ctx, invocation, response)
			if result == nil {
				t.Fatal("ProcessPlanningResponse() returned nil")
			}

			if result.Done != tt.expectedDone {
				t.Errorf("%s: expected Done=%v, got Done=%v", tt.description, tt.expectedDone, result.Done)
			}
		})
	}
}

func TestPlanner_IntentDescriptionWithToolCalls(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	// When there are valid tool calls, Done should remain unchanged
	// even if content looks like intent description
	response := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I will search for the answer now.",
					ToolCalls: []model.ToolCall{
						{
							Function: model.FunctionDefinitionParam{
								Name: "web_search",
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

	// When there are tool calls, we don't modify Done
	// (the presence of tool calls will cause IsFinalResponse to return false anyway)
	if result.Done != true {
		t.Error("Response with tool calls should keep Done=true (tool calls handle continuation)")
	}
}

func TestPlanner_IsIntentDescription(t *testing.T) {
	p := New()

	tests := []struct {
		content  string
		expected bool
	}{
		{"I will search for the answer", true},
		{"I'll fetch the data", true},
		{"I am going to process this", true},
		{"I'm going to look this up", true},
		{"/*ACTION*/ Running search", true},
		{"/*PLANNING*/ Step 1: Search", true},
		{"/*REPLANNING*/ New approach", true},
		{"The answer is 42", false},
		{"Paris is the capital of France", false},
		{"", false},
	}

	for _, tt := range tests {
		result := p.isIntentDescription(tt.content)
		if result != tt.expected {
			t.Errorf("isIntentDescription(%q) = %v, want %v", tt.content, result, tt.expected)
		}
	}
}

// TestPlanner_IsIntentDescription_OnlyAtContentStart tests that intent prefixes
// only match when they appear at the very beginning of the content (first sentence),
// not when they appear in later lines or in the middle of a sentence.
func TestPlanner_IsIntentDescription_OnlyAtContentStart(t *testing.T) {
	p := New()

	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		// Should match: intent prefix at start of content
		{
			name:     "I will at content start",
			content:  "I will search for the answer",
			expected: true,
		},
		{
			name:     "I'll at content start",
			content:  "I'll fetch the data now",
			expected: true,
		},
		{
			name:     "I am going to at content start",
			content:  "I am going to process this request",
			expected: true,
		},
		{
			name:     "I'm going to at content start",
			content:  "I'm going to look this up",
			expected: true,
		},
		// Should match: intent prefix at content start with leading whitespace
		{
			name:     "I will with leading spaces",
			content:  "  I will search for the answer",
			expected: true,
		},
		{
			name:     "I'll with leading tabs",
			content:  "\tI'll fetch the data",
			expected: true,
		},
		// Should NOT match: intent prefix at start of later lines (not first sentence)
		{
			name:     "I will at line start in multiline",
			content:  "Here is my analysis:\nI will search for more details.",
			expected: false,
		},
		{
			name:     "I'll at line start in multiline",
			content:  "Based on the context:\nI'll proceed with the search.",
			expected: false,
		},
		{
			name:     "I am going to at line start in multiline",
			content:  "After reviewing:\nI am going to fetch the data.",
			expected: false,
		},
		{
			name:     "I'm going to at line start in multiline",
			content:  "The plan is:\nI'm going to execute step 1.",
			expected: false,
		},
		// Should NOT match: intent prefix in the middle of a sentence
		{
			name:     "I will in middle of sentence",
			content:  "Let me know if I will need to search again.",
			expected: false,
		},
		{
			name:     "I'll in middle of sentence",
			content:  "Please tell me what I'll need to do next.",
			expected: false,
		},
		{
			name:     "I am going to in middle of sentence",
			content:  "The user asked if I am going to help them.",
			expected: false,
		},
		{
			name:     "I'm going to in middle of sentence",
			content:  "They wondered if I'm going to provide an answer.",
			expected: false,
		},
		// Should NOT match: intent phrases as part of quoted text
		{
			name:     "I will in quoted text",
			content:  "The document says \"I will return tomorrow\".",
			expected: false,
		},
		{
			name:     "I'll in quoted text",
			content:  "She mentioned \"I'll be there soon\".",
			expected: false,
		},
		// Should NOT match: intent phrases after conjunction
		{
			name:     "I will after and",
			content:  "You should wait and I will respond shortly.",
			expected: false,
		},
		{
			name:     "I'll after but",
			content:  "That's incorrect, but I'll help you fix it.",
			expected: false,
		},
		// Should NOT match: normal final answers containing these phrases incidentally
		{
			name:     "final answer mentioning I will",
			content:  "Based on my analysis, the process shows that I will need more data to confirm, but the preliminary answer is 42.",
			expected: false,
		},
		{
			name:     "explanation with I'll",
			content:  "The result is correct. Note that I'll highlight the key points: first item is A, second is B.",
			expected: false,
		},
		// Edge cases
		{
			name:     "empty content",
			content:  "",
			expected: false,
		},
		{
			name:     "only whitespace",
			content:  "   \n\t  ",
			expected: false,
		},
		{
			name:     "I will without space (should not match)",
			content:  "Iwill search",
			expected: false,
		},
		{
			name:     "I'll without space (should not match)",
			content:  "I'llfetch",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.isIntentDescription(tt.content)
			if result != tt.expected {
				t.Errorf("isIntentDescription(%q) = %v, want %v", tt.content, result, tt.expected)
			}
		})
	}
}

func TestPlanner_HasFinalAnswerTag(t *testing.T) {
	p := New()

	tests := []struct {
		content  string
		expected bool
	}{
		{"/*FINAL_ANSWER*/ The answer is 42", true},
		{"Some text /*FINAL_ANSWER*/ with answer", true},
		{"No final answer here", false},
		{"", false},
		{"/*PLANNING*/ Plan here", false},
	}

	for _, tt := range tests {
		result := p.hasFinalAnswerTag(tt.content)
		if result != tt.expected {
			t.Errorf("hasFinalAnswerTag(%q) = %v, want %v", tt.content, result, tt.expected)
		}
	}
}

func TestPlanner_HasValidToolCalls(t *testing.T) {
	p := New()

	tests := []struct {
		name     string
		response *model.Response
		expected bool
	}{
		{
			name:     "nil response",
			response: nil,
			expected: false,
		},
		{
			name: "empty choices",
			response: &model.Response{
				Choices: []model.Choice{},
			},
			expected: false,
		},
		{
			name: "no tool calls",
			response: &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{Content: "hello"}},
				},
			},
			expected: false,
		},
		{
			name: "with tool calls",
			response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{
								{Function: model.FunctionDefinitionParam{Name: "test"}},
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.hasValidToolCalls(tt.response)
			if result != tt.expected {
				t.Errorf("hasValidToolCalls() = %v, want %v", result, tt.expected)
			}
		})
	}
}
