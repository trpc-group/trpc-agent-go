//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolmock defines evalset tool mock configuration.
package toolmock

// ToolMock configures tool mocks for one evalset invocation.
type ToolMock struct {
	// Actual applies to the tested runner.
	Actual []*Tool `json:"actual,omitempty"`
	// Expected applies to the expected runner.
	Expected []*Tool `json:"expected,omitempty"`
}

// Tool configures static or generated mock behavior for one tool name.
type Tool struct {
	// Name is the tool name to mock.
	Name string `json:"name,omitempty"`
	// Arguments configures argument matching. When omitted, the tool name is enough to match.
	Arguments *ArgumentsMatch `json:"arguments,omitempty"`
	// Result is the static mock result returned when this tool item matches.
	Result any `json:"result,omitempty"`
	// LLMGenerator configures LLM-based result generation for this tool.
	LLMGenerator *LLMGenerator `json:"llmGenerator,omitempty"`
}

// ArgumentsMatch configures argument matching for a tool mock rule.
type ArgumentsMatch struct {
	// Ignore ignores tool arguments and matches by tool name only.
	Ignore bool `json:"ignore,omitempty"`
	// Expected is the expected tool arguments.
	Expected any `json:"expected,omitempty"`
	// OnlyTree compares only selected fields.
	OnlyTree map[string]any `json:"onlyTree,omitempty"`
	// IgnoreTree ignores selected fields.
	IgnoreTree map[string]any `json:"ignoreTree,omitempty"`
	// NumberTolerance configures numeric comparison tolerance. When omitted, numbers must match exactly.
	NumberTolerance *float64 `json:"numberTolerance,omitempty"`
}

// LLMGenerator configures LLM-based tool mock result generation.
type LLMGenerator struct {
	// Prompt is used as the tool mock runner instruction.
	Prompt string `json:"prompt,omitempty"`
}
