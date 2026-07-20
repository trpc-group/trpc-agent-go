//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Step type constants (used in JSON scenario files).
const (
	StepCreateSession      = "create_session"
	StepAppendEvent        = "append_event"
	StepUpdateAppState     = "update_app_state"
	StepUpdateUserState    = "update_user_state"
	StepUpdateSessionState = "update_session_state"
	StepAddMemory          = "add_memory"
	StepUpdateMemory       = "update_memory"
	StepDeleteMemory       = "delete_memory"
	StepCreateSummary      = "create_summary"
	StepAppendTrack        = "append_track"
	StepConcurrentEvents   = "concurrent_events"
	StepGetSession         = "get_session"
)

var validStepTypes = map[string]bool{
	StepCreateSession:      true,
	StepAppendEvent:        true,
	StepUpdateAppState:     true,
	StepUpdateUserState:    true,
	StepUpdateSessionState: true,
	StepAddMemory:          true,
	StepUpdateMemory:       true,
	StepDeleteMemory:       true,
	StepCreateSummary:      true,
	StepAppendTrack:        true,
	StepConcurrentEvents:   true,
	StepGetSession:         true,
}

// ReplayCase is a complete replay test case loaded from a JSON file.
type ReplayCase struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	AppName      string            `json:"app_name"`
	UserID       string            `json:"user_id"`
	SessionID    string            `json:"session_id"`
	Steps        []ReplayStep      `json:"steps"`
	Verify       *VerifySpec       `json:"verify,omitempty"`
	AllowedDiffs []AllowedDiffRule `json:"allowed_diffs,omitempty"`

	// BaseTime is the reference time used for event and track timestamps.
	// When zero, RunReplayCase falls back to time.Now().  Set this to
	// share a deterministic clock across backends so timestamps are
	// identical in cross-backend comparisons.
	BaseTime time.Time `json:"-"`
}

// ReplayStep is a single operation in the replay sequence.
type ReplayStep struct {
	Type       string         `json:"type"`
	Event      *actionEvent   `json:"event,omitempty"`
	State      map[string]any `json:"state,omitempty"`
	Memory     *actionMemory  `json:"memory,omitempty"`
	Summary    *actionSummary `json:"summary,omitempty"`
	Track      *actionTrack   `json:"track,omitempty"`
	Fault      *FaultConfig   `json:"fault,omitempty"`
	Concurrent []ReplayStep   `json:"concurrent,omitempty"`
}

// FaultConfig describes a fault to inject before or after a step's
// backend operation.  Both fields are ignored when Fault is nil; when
// set only one of them should be true (they are mutually exclusive in
// practice).  Fault injection is used by error_recovery scenarios to
// verify that backends handle transient failures correctly.
type FaultConfig struct {
	FailBefore bool `json:"fail_before,omitempty"`
	FailAfter  bool `json:"fail_after,omitempty"`
}

// actionEvent describes a single event to be appended.
type actionEvent struct {
	ID         string         `json:"id,omitempty"`
	Author     string         `json:"author"`
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []toolCall     `json:"tool_calls,omitempty"`
	ToolID     string         `json:"tool_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Branch     string         `json:"branch,omitempty"`
	FilterKey  string         `json:"filter_key,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	StateDelta map[string]any `json:"state_delta,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
	Actions    *eventActions  `json:"actions,omitempty"`
}

type toolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type eventActions struct {
	SkipSummarization bool `json:"skip_summarization,omitempty"`
}

// actionMemory describes a memory operation.
type actionMemory struct {
	Op          string      `json:"op"`
	Ref         string      `json:"ref,omitempty"`
	ResultAlias string      `json:"result_alias,omitempty"`
	Content     string      `json:"content"`
	Topics      []string    `json:"topics,omitempty"`
	Meta        *memoryMeta `json:"metadata,omitempty"`
}

type memoryMeta struct {
	Kind         string   `json:"kind,omitempty"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
}

// actionSummary describes a summary generation step.
type actionSummary struct {
	FilterKey string `json:"filter_key"`
	Text      string `json:"text"`
	Force     bool   `json:"force"`
}

// actionTrack describes a track event to append.
type actionTrack struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

// VerifySpec declares expected properties of the final snapshot.
type VerifySpec struct {
	EventsCount            *int `json:"events_count,omitempty"`
	MemoriesCount          *int `json:"memories_count,omitempty"`
	NoDuplicateEvents      bool `json:"no_duplicate_events,omitempty"`
	NoDuplicateMemories    bool `json:"no_duplicate_memories,omitempty"`
	EventsOrderPreserved   bool `json:"events_order_preserved,omitempty"`
	EventsOrderIndependent bool `json:"events_order_independent,omitempty"`
}

// AllowedDiffRule describes a cross-backend difference that is expected.
type AllowedDiffRule struct {
	Section  string `json:"section"`
	Path     string `json:"path"`
	BackendA string `json:"backend_a"`
	BackendB string `json:"backend_b"`
	Reason   string `json:"reason"`
}

// LoadReplayCase reads a ReplayCase from a JSON file.
func LoadReplayCase(path string) (*ReplayCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read replay case file: %w", err)
	}
	var rc ReplayCase
	if err := json.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("unmarshal replay case: %w", err)
	}
	if err := rc.Validate(); err != nil {
		return nil, fmt.Errorf("validate replay case %q: %w", rc.Name, err)
	}
	return &rc, nil
}

// LoadReplayCasesFromDir loads all .json replay case files from a directory.
func LoadReplayCasesFromDir(dir string) ([]*ReplayCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read replay case dir: %w", err)
	}
	var cases []*ReplayCase
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rc, err := LoadReplayCase(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		cases = append(cases, rc)
	}
	return cases, nil
}

// Validate checks that required fields are present and step types are valid.
func (rc *ReplayCase) Validate() error {
	if strings.TrimSpace(rc.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if rc.AppName == "" {
		rc.AppName = "replaytest"
	}
	if rc.UserID == "" {
		rc.UserID = "user-" + rc.Name
	}
	if rc.SessionID == "" {
		rc.SessionID = "session-" + rc.Name
	}
	if len(rc.Steps) == 0 {
		return fmt.Errorf("steps must not be empty")
	}
	for i, step := range rc.Steps {
		if err := validateSteps(step, i); err != nil {
			return err
		}
	}
	return nil
}

// validateSteps recursively validates step types, including steps nested
// inside Concurrent blocks.
func validateSteps(step ReplayStep, index int) error {
	if !validStepTypes[step.Type] {
		return fmt.Errorf("step %d: unknown type %q", index, step.Type)
	}

	// Validate required payloads are present so malformed scenarios are
	// caught at load time with a descriptive error rather than panicking
	// deep inside execution.
	switch step.Type {
	case StepAppendEvent:
		if step.Event == nil {
			return fmt.Errorf("step %d (%s): event is required", index, step.Type)
		}
	case StepAddMemory, StepUpdateMemory, StepDeleteMemory:
		if step.Memory == nil {
			return fmt.Errorf("step %d (%s): memory is required", index, step.Type)
		}
		if !validMemoryOps[step.Memory.Op] {
			return fmt.Errorf("step %d (%s): unknown memory op %q", index, step.Type, step.Memory.Op)
		}
	case StepCreateSummary:
		if step.Summary == nil {
			return fmt.Errorf("step %d (%s): summary is required", index, step.Type)
		}
		if step.Summary.FilterKey == "" {
			return fmt.Errorf("step %d (%s): summary filter_key is required", index, step.Type)
		}
	case StepAppendTrack:
		if step.Track == nil {
			return fmt.Errorf("step %d (%s): track is required", index, step.Type)
		}
		if step.Track.Name == "" {
			return fmt.Errorf("step %d (%s): track name is required", index, step.Type)
		}
	case StepConcurrentEvents:
		if len(step.Concurrent) == 0 {
			return fmt.Errorf("step %d (%s): concurrent_events must have at least one child step", index, step.Type)
		}
	}

	for i, nested := range step.Concurrent {
		if err := validateSteps(nested, i); err != nil {
			return err
		}
	}
	return nil
}

// validMemoryOps lists the allowed memory operation values.
var validMemoryOps = map[string]bool{
	"add":    true,
	"update": true,
	"delete": true,
}
