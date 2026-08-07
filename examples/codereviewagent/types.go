//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "time"

type changedLine struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type finding struct {
	File       string  `json:"file"`
	StartLine  int     `json:"startLine"`
	EndLine    int     `json:"endLine"`
	Severity   string  `json:"severity"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
	RuleID     string  `json:"ruleId"`
	Status     string  `json:"status"`
	Message    string  `json:"message"`
	Suggestion string  `json:"suggestion"`
}

type permissionRecord struct {
	Action  string   `json:"action"`
	Reason  string   `json:"reason"`
	Command []string `json:"command"`
}

type sandboxRun struct {
	Status       string   `json:"status"`
	Command      []string `json:"command"`
	ExitCode     int      `json:"exitCode"`
	TimedOut     bool     `json:"timedOut"`
	Output       string   `json:"output,omitempty"`
	Error        string   `json:"error,omitempty"`
	DurationMS   int64    `json:"durationMillis"`
	OutputCapped bool     `json:"outputCapped"`
}

type artifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type reviewMetrics struct {
	DurationMS       int64          `json:"durationMillis"`
	SandboxDuration  int64          `json:"sandboxDurationMillis"`
	ToolCalls        int            `json:"toolCalls"`
	PermissionChecks int            `json:"permissionChecks"`
	FindingCount     int            `json:"findingCount"`
	BySeverity       map[string]int `json:"bySeverity"`
	ByCategory       map[string]int `json:"byCategory"`
	Warnings         int            `json:"warnings"`
}

type reviewReport struct {
	TaskID      string           `json:"taskId"`
	Status      string           `json:"status"`
	Mode        string           `json:"mode"`
	InputSource string           `json:"inputSource"`
	DiffSHA256  string           `json:"diffSha256"`
	Skill       string           `json:"skill"`
	CreatedAt   time.Time        `json:"createdAt"`
	Findings    []finding        `json:"findings"`
	Permission  permissionRecord `json:"permission"`
	Sandbox     sandboxRun       `json:"sandbox"`
	Metrics     reviewMetrics    `json:"metrics"`
	Artifacts   []artifact       `json:"artifacts,omitempty"`
	Summary     string           `json:"summary"`
}

type storedTask struct {
	ID            string
	Status        string
	FindingCount  int
	SandboxCount  int
	DecisionCount int
	ReportCount   int
}
