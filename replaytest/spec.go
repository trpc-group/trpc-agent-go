//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replaytest provides a replay consistency test framework for Session, Memory, Summary, and Track backends.
// It drives the same set of operations through multiple backends, normalizes the results, and produces a diff report.
package replaytest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Spec is the top-level test case definition, serialized as JSON.
type Spec struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Tags         []string      `json:"tags,omitempty"`
	Backends     BackendConfig `json:"backends"`
	Setup        SetupSpec     `json:"setup"`
	Operations   []Operation   `json:"operations"`
	Verifies     []VerifySpec  `json:"verifies"`
	AllowedDiffs []DiffRule    `json:"allowed_diffs,omitempty"`
}

// BackendConfig specifies which backends to spin up for a test case.
type BackendConfig struct {
	Session []string `json:"session"`
	Memory  []string `json:"memory"`
}

// SetupSpec defines the initial conditions for a replay case.
type SetupSpec struct {
	AppName   string            `json:"app_name"`
	UserID    string            `json:"user_id"`
	SessionID string            `json:"session_id,omitempty"`
	InitState map[string]string `json:"init_state,omitempty"`
}

// Operation represents a single action in a replay case.
type Operation struct {
	Op      string          `json:"op"`
	Backend string          `json:"backend"` // "session" or "memory"
	Params  json.RawMessage `json:"params"`
}

// VerifySpec describes a cross-backend consistency check.
type VerifySpec struct {
	What   string          `json:"what"`
	Params json.RawMessage `json:"params,omitempty"`
	Expect json.RawMessage `json:"expect,omitempty"`
}

// LoadSpec loads a Spec from a JSON file on disk.
func LoadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec file %s: %w", path, err)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec file %s: %w", path, err)
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid spec %s: %w", path, err)
	}
	return &spec, nil
}

// LoadSpecsFromDir loads all JSON spec files from a directory.
func LoadSpecsFromDir(dir string) ([]*Spec, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read spec dir %s: %w", dir, err)
	}
	var specs []*Spec
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		spec, err := LoadSpec(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// HasTag returns whether the spec has a given tag.
func (s *Spec) HasTag(tag string) bool {
	return slices.Contains(s.Tags, tag)
}

// validOps and validWhats are maps of known operation names and verify targets.
var validOps = map[string]bool{
	OpCreateSession: true, OpGetSession: true, OpDeleteSession: true,
	OpListSessions: true, OpAppendUserEvent: true, OpAppendAssistantEvent: true,
	OpAppendToolCallEvent: true, OpAppendToolResponseEvent: true,
	OpUpdateAppState: true, OpUpdateUserState: true, OpUpdateSessionState: true,
	OpDeleteAppStateKey: true, OpDeleteUserStateKey: true,
	OpCreateSummary: true, OpEnqueueSummary: true, OpAppendTrackEvent: true,
	OpAddMemory: true, OpUpdateMemory: true, OpDeleteMemory: true,
	OpClearMemories: true, OpSearchMemories: true, OpAddMemoryWithMetadata: true,
	OpAppendConcurrentEvents: true,
}

var validWhats = map[string]bool{
	VerifySessionFull: true, VerifyEvents: true, VerifyState: true,
	VerifySummary: true, VerifyTracks: true, VerifyMemories: true,
	VerifyMemorySearch: true,
}

var validBackendTypes = map[string]bool{"session": true, "memory": true}

// Validate checks that the Spec has all required fields, and that operations and verifications reference known names so that misconfiguration is caught at load time.
func (s *Spec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	if s.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(s.Backends.Session) == 0 {
		return fmt.Errorf("at least one session backend is required")
	}
	if len(s.Backends.Memory) == 0 {
		return fmt.Errorf("at least one memory backend is required")
	}
	if s.Setup.AppName == "" {
		return fmt.Errorf("setup.app_name is required")
	}
	if s.Setup.UserID == "" {
		return fmt.Errorf("setup.user_id is required")
	}
	if s.Setup.SessionID == "" {
		return fmt.Errorf("setup.session_id is required")
	}
	if len(s.Operations) == 0 {
		return fmt.Errorf("at least one operation is required")
	}
	for i, op := range s.Operations {
		if !validOps[op.Op] {
			return fmt.Errorf("operation %d: unknown op %q", i, op.Op)
		}
		if !validBackendTypes[op.Backend] {
			return fmt.Errorf("operation %d: unknown backend %q (expected \"session\" or \"memory\")", i, op.Backend)
		}
	}
	if len(s.Verifies) == 0 {
		return fmt.Errorf("at least one verify is required")
	}
	for i, v := range s.Verifies {
		if !validWhats[v.What] {
			return fmt.Errorf("verify %d: unknown what %q", i, v.What)
		}
	}
	return nil
}
