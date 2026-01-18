//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractFinalAnswer(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantFound  bool
		wantAnswer string
	}{
		{
			name:       "format1: /*FINAL_ANSWER*/ with trailing marker",
			content:    "Some reasoning...\n/*FINAL_ANSWER*/\n89706.00\n/*END*/",
			wantFound:  true,
			wantAnswer: "89706.00",
		},
		{
			name:       "format1: /*FINAL_ANSWER*/ at end of content",
			content:    "Some reasoning...\n/*FINAL_ANSWER*/\n42.00",
			wantFound:  true,
			wantAnswer: "42.00",
		},
		{
			name:       "format1: /*FINAL_ANSWER*/ with spaces",
			content:    "/*FINAL_ANSWER*/   answer with spaces   /*OTHER*/",
			wantFound:  true,
			wantAnswer: "answer with spaces",
		},
		{
			name:       "format2: FINAL ANSWER: inline",
			content:    "The result is ready.\nFINAL ANSWER: 12345\nDone.",
			wantFound:  true,
			wantAnswer: "12345",
		},
		{
			name:       "format2: FINAL ANSWER: at end",
			content:    "Calculation complete.\nFINAL ANSWER: hello world",
			wantFound:  true,
			wantAnswer: "hello world",
		},
		{
			name:       "format2: case insensitive",
			content:    "final answer: CaseTest",
			wantFound:  true,
			wantAnswer: "CaseTest",
		},
		{
			name:       "format2: with extra spaces",
			content:    "FINAL   ANSWER  :   spaced answer",
			wantFound:  true,
			wantAnswer: "spaced answer",
		},
		{
			name:       "no final answer",
			content:    "This is just regular content without any final answer.",
			wantFound:  false,
			wantAnswer: "",
		},
		{
			name:       "empty content",
			content:    "",
			wantFound:  false,
			wantAnswer: "",
		},
		{
			name:       "partial match - FINAL without ANSWER",
			content:    "FINAL result is 100",
			wantFound:  false,
			wantAnswer: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, answer := extractFinalAnswer(tt.content)
			assert.Equal(t, tt.wantFound, found, "found mismatch")
			assert.Equal(t, tt.wantAnswer, answer, "answer mismatch")
		})
	}
}

func TestExtractExecutionOutput(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   string
	}{
		{
			name:   "standard format with prefix",
			result: "Code execution result:\n89706.00\n",
			want:   "89706.00",
		},
		{
			name:   "without prefix",
			result: "42.5",
			want:   "42.5",
		},
		{
			name:   "multiple lines - take first",
			result: "Code execution result:\nline1\nline2\nline3",
			want:   "line1",
		},
		{
			name:   "empty lines before result",
			result: "Code execution result:\n\n\nactual_result",
			want:   "actual_result",
		},
		{
			name:   "whitespace handling",
			result: "Code execution result:\n   trimmed   \n",
			want:   "trimmed",
		},
		{
			name:   "empty result",
			result: "",
			want:   "",
		},
		{
			name:   "only prefix",
			result: "Code execution result:",
			want:   "",
		},
		{
			name:   "complex output",
			result: "Code execution result:\n{'key': 'value'}\nsome other output",
			want:   "{'key': 'value'}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractExecutionOutput(tt.result)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReplaceFinalAnswer(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		newValue string
		want     string
	}{
		{
			name:     "format1: replace /*FINAL_ANSWER*/ value",
			content:  "Reasoning...\n/*FINAL_ANSWER*/\nold_value\n/*END*/",
			newValue: "new_value",
			want:     "Reasoning...\n/*FINAL_ANSWER*/\nnew_value\n/*END*/",
		},
		{
			name:     "format1: replace at end of content",
			content:  "/*FINAL_ANSWER*/\noriginal",
			newValue: "replaced",
			want:     "/*FINAL_ANSWER*/\nreplaced\n",
		},
		{
			name:     "format2: replace FINAL ANSWER: value",
			content:  "Some text\nFINAL ANSWER: old\nMore text",
			newValue: "new",
			want:     "Some text\nFINAL ANSWER: new\nMore text",
		},
		{
			name:     "format2: replace at end",
			content:  "FINAL ANSWER: old_answer",
			newValue: "new_answer",
			want:     "FINAL ANSWER: new_answer",
		},
		{
			name:     "no match - return original",
			content:  "No final answer here",
			newValue: "ignored",
			want:     "No final answer here",
		},
		{
			name:     "empty content",
			content:  "",
			newValue: "value",
			want:     "",
		},
		{
			name:     "numeric replacement",
			content:  "FINAL ANSWER: 90123.00",
			newValue: "89706.00",
			want:     "FINAL ANSWER: 89706.00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceFinalAnswer(tt.content, tt.newValue)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewCodeExecutionResponseProcessor(t *testing.T) {
	p := NewCodeExecutionResponseProcessor()
	assert.NotNil(t, p)
}
