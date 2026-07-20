//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import "fmt"

// OperationKind identifies one replay action.
type OperationKind string

// InjectedFailurePoint identifies whether a deterministic failure occurs around a write.
type InjectedFailurePoint string

// FailureBeforeWrite and FailureAfterWrite identify supported injection boundaries.
const (
	FailureBeforeWrite InjectedFailurePoint = "before_write"
	FailureAfterWrite  InjectedFailurePoint = "after_write"
)

// OperationCreateSession and the following constants identify replay actions.
const (
	OperationCreateSession   OperationKind = "create_session"
	OperationAppendEvent     OperationKind = "append_event"
	OperationUpdateState     OperationKind = "update_state"
	OperationWriteMemory     OperationKind = "write_memory"
	OperationSearchMemory    OperationKind = "search_memory"
	OperationUpdateSummary   OperationKind = "update_summary"
	OperationSetReplayWindow OperationKind = "set_replay_window"
	OperationAppendTrack     OperationKind = "append_track"
	OperationParallel        OperationKind = "parallel"
)

// Operation is a backend-neutral action in a replay case.
type Operation struct {
	Kind                  OperationKind
	Name                  string
	After                 []string
	SessionID             string
	Event                 *EventSnapshot
	StateUpdates          map[string]any
	StateDeletes          []string
	Memory                *MemorySnapshot
	Summary               *SummarySnapshot
	TrackName             string
	TrackEvent            *TrackEventSnapshot
	ReplayWindowFilterKey string
	SearchQuery           string
	SearchLimit           int
	SearchMinScore        float64
	SearchAppName         string
	SearchUserID          string
	Parallel              []Operation
	InjectedFailure       string
	FailurePoint          InjectedFailurePoint
	ExpectFailure         bool
}

// Validate checks that an operation contains exactly the payload required by its kind.
func (operation Operation) Validate() error {
	if operation.Kind == "" {
		return fmt.Errorf("operation kind is empty")
	}
	if err := operation.validateFailureSettings(); err != nil {
		return err
	}
	specification, err := operationSpecificationFor(operation.Kind)
	if err != nil {
		return err
	}
	if err := specification.validate(operation); err != nil {
		return err
	}
	return operation.requirePayload(specification.payload)
}

func (operation Operation) validateFailureSettings() error {
	if operation.ExpectFailure && operation.InjectedFailure == "" {
		return fmt.Errorf("expected failure requires injected failure")
	}
	if operation.InjectedFailure != "" && !operation.ExpectFailure {
		return fmt.Errorf("injected failure must be expected")
	}
	if operation.InjectedFailure != "" && operation.FailurePoint != FailureBeforeWrite &&
		operation.FailurePoint != FailureAfterWrite {
		return fmt.Errorf("injected failure requires a valid failure point")
	}
	if operation.InjectedFailure == "" && operation.FailurePoint != "" {
		return fmt.Errorf("failure point requires injected failure")
	}
	if operation.Kind != OperationParallel && len(operation.Parallel) > 0 {
		return fmt.Errorf("operation %s cannot contain parallel operations", operation.Kind)
	}
	if operation.Kind == OperationParallel && operation.InjectedFailure != "" {
		return fmt.Errorf("parallel failure must be injected on a child operation")
	}
	return nil
}

type operationSpecification struct {
	payload  operationPayload
	validate func(Operation) error
}

func operationSpecificationFor(kind OperationKind) (operationSpecification, error) {
	switch kind {
	case OperationCreateSession:
		return operationSpecification{payloadSession, validateCreateSession}, nil
	case OperationAppendEvent:
		return operationSpecification{payloadSession | payloadEvent, validateAppendEvent}, nil
	case OperationUpdateState:
		return operationSpecification{payloadSession | payloadState, validateUpdateState}, nil
	case OperationWriteMemory:
		return operationSpecification{payloadMemory, validateWriteMemory}, nil
	case OperationSearchMemory:
		return operationSpecification{payloadSearch, validateSearchMemory}, nil
	case OperationUpdateSummary:
		return operationSpecification{payloadSession | payloadSummary, validateUpdateSummary}, nil
	case OperationSetReplayWindow:
		return operationSpecification{payloadSession | payloadReplayWindow, validateSetReplayWindow}, nil
	case OperationAppendTrack:
		return operationSpecification{payloadSession | payloadTrack, validateAppendTrack}, nil
	case OperationParallel:
		return operationSpecification{payloadParallel, validateParallel}, nil
	default:
		return operationSpecification{}, fmt.Errorf("unsupported operation kind %q", kind)
	}
}

func validateCreateSession(operation Operation) error {
	if operation.SessionID == "" {
		return fmt.Errorf("create session requires session id")
	}
	return nil
}

func validateAppendEvent(operation Operation) error {
	if operation.SessionID == "" || operation.Event == nil {
		return fmt.Errorf("append event requires session id and event")
	}
	return nil
}

func validateUpdateState(operation Operation) error {
	if operation.SessionID == "" ||
		(len(operation.StateUpdates) == 0 && len(operation.StateDeletes) == 0) {
		return fmt.Errorf("update state requires session id and state changes")
	}
	return nil
}

func validateWriteMemory(operation Operation) error {
	if operation.Memory == nil {
		return fmt.Errorf("write memory requires memory")
	}
	return nil
}

func validateSearchMemory(operation Operation) error {
	if operation.SearchQuery == "" || operation.SearchLimit <= 0 ||
		operation.SearchAppName == "" || operation.SearchUserID == "" {
		return fmt.Errorf("search memory requires scope, query, and positive limit")
	}
	if operation.SearchMinScore < 0 || operation.SearchMinScore > 1 {
		return fmt.Errorf("search memory score threshold must be between zero and one")
	}
	return nil
}

func validateUpdateSummary(operation Operation) error {
	if operation.SessionID == "" || operation.Summary == nil {
		return fmt.Errorf("update summary requires session id and summary")
	}
	if operation.Summary.SessionID == "" || operation.Summary.SessionID != operation.SessionID {
		return fmt.Errorf("update summary session id must match summary ownership")
	}
	if operation.Summary.Version != 0 || operation.Summary.Boundary != nil ||
		!operation.Summary.UpdatedAt.IsZero() {
		return fmt.Errorf("update summary version, boundary, and updated time are read-only")
	}
	return nil
}

func validateSetReplayWindow(operation Operation) error {
	if operation.SessionID == "" || operation.ReplayWindowFilterKey == "" {
		return fmt.Errorf("set replay window requires session id and summary filter key")
	}
	return nil
}

func validateAppendTrack(operation Operation) error {
	if operation.SessionID == "" || operation.TrackName == "" || operation.TrackEvent == nil {
		return fmt.Errorf("append track requires session id, track name, and event")
	}
	return nil
}

func validateParallel(operation Operation) error {
	if len(operation.Parallel) == 0 {
		return fmt.Errorf("parallel operation requires child operations")
	}
	return nil
}

type operationPayload uint16

const (
	payloadSession operationPayload = 1 << iota
	payloadEvent
	payloadState
	payloadMemory
	payloadSummary
	payloadTrack
	payloadReplayWindow
	payloadSearch
	payloadParallel
)

func (operation Operation) requirePayload(want operationPayload) error {
	got := operation.payload()
	if got != want {
		return fmt.Errorf("operation %s contains incompatible payload", operation.Kind)
	}
	return nil
}

func (operation Operation) payload() operationPayload {
	return optionalPayload(operation.SessionID != "", payloadSession) |
		optionalPayload(operation.Event != nil, payloadEvent) |
		optionalPayload(len(operation.StateUpdates) > 0 || len(operation.StateDeletes) > 0, payloadState) |
		optionalPayload(operation.Memory != nil, payloadMemory) |
		optionalPayload(operation.Summary != nil, payloadSummary) |
		optionalPayload(operation.TrackName != "" || operation.TrackEvent != nil, payloadTrack) |
		optionalPayload(operation.ReplayWindowFilterKey != "", payloadReplayWindow) |
		optionalPayload(operation.hasSearchPayload(), payloadSearch) |
		optionalPayload(len(operation.Parallel) > 0, payloadParallel)
}

func (operation Operation) hasSearchPayload() bool {
	return operation.SearchQuery != "" || operation.SearchLimit != 0 ||
		operation.SearchMinScore != 0 ||
		operation.SearchAppName != "" || operation.SearchUserID != ""
}

func optionalPayload(present bool, payload operationPayload) operationPayload {
	if present {
		return payload
	}
	return 0
}

// ReplayCase describes one deterministic replay scenario.
type ReplayCase struct {
	Name         string
	Description  string
	Capabilities []Capability
	Operations   []Operation
	Invariants   []SnapshotInvariant
}

// SnapshotInvariant validates backend-independent replay semantics.
type SnapshotInvariant struct {
	Name  string
	Check func(Snapshot) error
}
