//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finding

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDedupEngine_NoDuplicates(t *testing.T) {
	e := NewDedupEngine()
	findings := []Finding{
		{File: "main.go", Line: 10, RuleID: "R1", Title: "bug", Confidence: ConfidenceHigh},
		{File: "main.go", Line: 20, RuleID: "R2", Title: "style", Confidence: ConfidenceHigh},
	}
	result := e.Dedup(findings)
	assert.Len(t, result.Findings, 2)
	assert.Len(t, result.Warnings, 0)
	assert.Equal(t, 0, result.Suppressed)
}

func TestDedupEngine_SameLineSameRule(t *testing.T) {
	e := NewDedupEngine()
	findings := []Finding{
		{File: "main.go", Line: 10, RuleID: "R1", Title: "bug1", Confidence: ConfidenceHigh},
		{File: "main.go", Line: 10, RuleID: "R1", Title: "bug2", Confidence: ConfidenceHigh},
	}
	result := e.Dedup(findings)
	assert.Len(t, result.Findings, 1)
	assert.Equal(t, 1, result.Suppressed)
}

func TestDedupEngine_LowConfidenceToWarnings(t *testing.T) {
	e := NewDedupEngine()
	findings := []Finding{
		{File: "main.go", Line: 5, RuleID: "R1", Title: "maybe", Confidence: ConfidenceLow},
		{File: "main.go", Line: 10, RuleID: "R2", Title: "definite", Confidence: ConfidenceHigh},
	}
	result := e.Dedup(findings)
	assert.Len(t, result.Findings, 1)
	assert.Len(t, result.Warnings, 1)
	assert.Equal(t, 0, result.Suppressed)
}

func TestDedupEngine_MaxWarnings(t *testing.T) {
	e := NewDedupEngine()
	e.MaxWarnings = 2

	var findings []Finding
	for i := 0; i < 5; i++ {
		findings = append(findings, Finding{
			File: "main.go", Line: i, RuleID: "R1",
			Title: "warn", Confidence: ConfidenceLow,
		})
	}
	result := e.Dedup(findings)
	assert.Len(t, result.Findings, 0)
	assert.Len(t, result.Warnings, 2) // capped
}

func TestDedupEngine_EmptyInput(t *testing.T) {
	e := NewDedupEngine()
	result := e.Dedup(nil)
	assert.Len(t, result.Findings, 0)
	assert.Len(t, result.Warnings, 0)
	assert.Equal(t, 0, result.Suppressed)

	result = e.Dedup([]Finding{})
	assert.Len(t, result.Findings, 0)
}

func TestDedupEngine_AllSuppressed(t *testing.T) {
	e := NewDedupEngine()
	findings := []Finding{
		{File: "a.go", Line: 1, RuleID: "X", Title: "dup", Confidence: ConfidenceHigh},
		{File: "a.go", Line: 1, RuleID: "X", Title: "dup2", Confidence: ConfidenceHigh},
		{File: "a.go", Line: 1, RuleID: "X", Title: "dup3", Confidence: ConfidenceHigh},
	}
	result := e.Dedup(findings)
	assert.Len(t, result.Findings, 1)
	assert.Equal(t, 2, result.Suppressed)
}

func TestFindings_SortByLine(t *testing.T) {
	f := Findings{
		{File: "a.go", Line: 30},
		{File: "a.go", Line: 10},
		{File: "a.go", Line: 20},
	}
	f.Swap(0, 2)
	assert.Equal(t, 20, f[0].Line)
	assert.Equal(t, 10, f[1].Line)
	assert.Equal(t, 30, f[2].Line)
}

func TestFindings_Less(t *testing.T) {
	f := Findings{
		{File: "a.go", Line: 10},
		{File: "b.go", Line: 5},
	}
	// Less compares by line number only.
	assert.False(t, f.Less(0, 1)) // 10 < 5 = false
	assert.True(t, f.Less(1, 0))  // 5 < 10 = true
}

func TestDedupEngine_ConfigOff(t *testing.T) {
	e := &DedupEngine{SameLineSameRule: false, SameFileSameMsg: false}
	findings := []Finding{
		{File: "main.go", Line: 10, RuleID: "R1", Title: "bug", Confidence: ConfidenceHigh},
		{File: "main.go", Line: 10, RuleID: "R1", Title: "bug", Confidence: ConfidenceHigh},
	}
	// Fallback mode still dedups by exact match (file+line+rule+title).
	result := e.Dedup(findings)
	assert.Len(t, result.Findings, 1)
	assert.Equal(t, 1, result.Suppressed)
}

func TestDedupEngine_SameFileSameMsg_DifferentLine(t *testing.T) {
	e := &DedupEngine{SameLineSameRule: false, SameFileSameMsg: true}
	findings := []Finding{
		{File: "main.go", Line: 10, RuleID: "R1", Title: "same msg", Confidence: ConfidenceHigh},
		{File: "main.go", Line: 20, RuleID: "R2", Title: "same msg", Confidence: ConfidenceHigh},
	}
	result := e.Dedup(findings)
	assert.Len(t, result.Findings, 1)
	assert.Equal(t, 1, result.Suppressed)
}
