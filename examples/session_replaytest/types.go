//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

// OpType defines replay operations.
type OpType string

const (
	OpAddEvent      OpType = "ADD_EVENT"
	OpSetState      OpType = "SET_STATE"
	OpDeleteState   OpType = "DELETE_STATE"
	OpWriteMemory   OpType = "WRITE_MEMORY"
	OpUpdateSummary OpType = "UPDATE_SUMMARY"
	OpAddTrack      OpType = "ADD_TRACK"
	OpInjectTrap    OpType = "INJECT_TRAP"
)

// ReplayOp represents a single atomic session operation.
type ReplayOp struct {
	Type      OpType            `json:"type"`
	SessionID string            `json:"session_id"`
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	StateKey  string            `json:"state_key,omitempty"`
	StateVal  string            `json:"state_val,omitempty"`
	MemoryID  string            `json:"memory_id,omitempty"`
	FilterKey string            `json:"filter_key,omitempty"`
	TrackName string            `json:"track_name,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// ReplayCase defines a test scenario.
type ReplayCase struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Ops         []ReplayOp `json:"ops"`
}

// DiffEntry records a detected discrepancy across backends.
type DiffEntry struct {
	CaseID      string      `json:"case_id"`
	SessionID   string      `json:"session_id"`
	FieldPath   string      `json:"field_path"`
	BackendA    string      `json:"backend_a"`
	BackendB    string      `json:"backend_b"`
	ValueA      interface{} `json:"value_a"`
	ValueB      interface{} `json:"value_b"`
	AllowedDiff bool        `json:"allowed_diff"`
	Reason      string      `json:"reason"`
}

// DiffReport wraps all diffs for a test execution.
type DiffReport struct {
	TotalCases int         `json:"total_cases"`
	Diffs      []DiffEntry `json:"diffs"`
	Passed     bool        `json:"passed"`
}
