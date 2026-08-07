//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// MockBackend represents a simulated storage engine (InMemory or SQLite).
type MockBackend struct {
	Name      string
	Events    map[string][]string
	States    map[string]map[string]string
	Memories  map[string]map[string]string
	Summaries map[string]map[string]string
	Tracks    map[string][]string
}

// NewMockBackend initializes a fresh MockBackend.
func NewMockBackend(name string) *MockBackend {
	b := &MockBackend{Name: name}
	b.Reset()
	return b
}

// Reset clears all stored session data for case isolation.
func (b *MockBackend) Reset() {
	b.Events = make(map[string][]string)
	b.States = make(map[string]map[string]string)
	b.Memories = make(map[string]map[string]string)
	b.Summaries = make(map[string]map[string]string)
	b.Tracks = make(map[string][]string)
}

// ReplayHarness orchestrates operations across multiple backends and compares results.
type ReplayHarness struct {
	backends []*MockBackend
}

// NewReplayHarness creates a harness with specified backends.
func NewReplayHarness(backends ...*MockBackend) *ReplayHarness {
	return &ReplayHarness{backends: backends}
}

// RunCase executes a ReplayCase on all backends and compares outputs with case isolation.
func (h *ReplayHarness) RunCase(c ReplayCase) []DiffEntry {
	diffs := make([]DiffEntry, 0)

	// Isolated case execution: reset backends before applying case operations
	for _, b := range h.backends {
		b.Reset()
		for _, op := range c.Ops {
			h.applyOp(b, op)
		}
	}

	// Compare Backend A vs Backend B
	if len(h.backends) >= 2 {
		bA := h.backends[0]
		bB := h.backends[1]

		// 1. Compare Events (Count, Content, Order)
		allSessionIDs := make(map[string]bool)
		for sid := range bA.Events {
			allSessionIDs[sid] = true
		}
		for sid := range bB.Events {
			allSessionIDs[sid] = true
		}

		for sid := range allSessionIDs {
			evA := bA.Events[sid]
			evB := bB.Events[sid]
			if len(evA) != len(evB) {
				diffs = append(diffs, DiffEntry{
					CaseID:    c.ID,
					SessionID: sid,
					FieldPath: "events.count",
					BackendA:  bA.Name,
					BackendB:  bB.Name,
					ValueA:    len(evA),
					ValueB:    len(evB),
					Reason:    "Event sequence count mismatch across backends",
				})
			} else {
				for i := 0; i < len(evA); i++ {
					if evA[i] != evB[i] {
						diffs = append(diffs, DiffEntry{
							CaseID:    c.ID,
							SessionID: sid,
							FieldPath: fmt.Sprintf("events[%d]", i),
							BackendA:  bA.Name,
							BackendB:  bB.Name,
							ValueA:    evA[i],
							ValueB:    evB[i],
							Reason:    "Event content or order mismatch across backends",
						})
					}
				}
			}
		}

		// 2. Compare States
		allStateSIDs := make(map[string]bool)
		for sid := range bA.States {
			allStateSIDs[sid] = true
		}
		for sid := range bB.States {
			allStateSIDs[sid] = true
		}

		for sid := range allStateSIDs {
			stA := bA.States[sid]
			stB := bB.States[sid]
			allKeys := make(map[string]bool)
			for k := range stA {
				allKeys[k] = true
			}
			for k := range stB {
				allKeys[k] = true
			}
			for k := range allKeys {
				vA := stA[k]
				vB := stB[k]
				if vA != vB {
					diffs = append(diffs, DiffEntry{
						CaseID:    c.ID,
						SessionID: sid,
						FieldPath: "state." + k,
						BackendA:  bA.Name,
						BackendB:  bB.Name,
						ValueA:    vA,
						ValueB:    vB,
						Reason:    "State key value mismatch",
					})
				}
			}
		}

		// 3. Compare Memories
		for sid, memA := range bA.Memories {
			memB := bB.Memories[sid]
			for mid, contentA := range memA {
				contentB := memB[mid]
				if contentA != contentB {
					diffs = append(diffs, DiffEntry{
						CaseID:    c.ID,
						SessionID: sid,
						FieldPath: "memory." + mid,
						BackendA:  bA.Name,
						BackendB:  bB.Name,
						ValueA:    contentA,
						ValueB:    contentB,
						Reason:    "Memory content mismatch",
					})
				}
			}
		}

		// 4. Compare Summaries (Filter-key aware)
		for sid, sumA := range bA.Summaries {
			sumB := bB.Summaries[sid]
			for fk, valA := range sumA {
				valB := sumB[fk]
				if valA != valB {
					diffs = append(diffs, DiffEntry{
						CaseID:    c.ID,
						SessionID: sid,
						FieldPath: fmt.Sprintf("summary[%s]", fk),
						BackendA:  bA.Name,
						BackendB:  bB.Name,
						ValueA:    valA,
						ValueB:    valB,
						Reason:    "Summary filter-key text mismatch",
					})
				}
			}
		}

		// 5. Compare Tracks
		for sid, trA := range bA.Tracks {
			trB := bB.Tracks[sid]
			if len(trA) != len(trB) {
				diffs = append(diffs, DiffEntry{
					CaseID:    c.ID,
					SessionID: sid,
					FieldPath: "tracks.count",
					BackendA:  bA.Name,
					BackendB:  bB.Name,
					ValueA:    len(trA),
					ValueB:    len(trB),
					Reason:    "Track telemetry count mismatch across backends",
				})
			}
		}
	}

	return diffs
}

func (h *ReplayHarness) applyOp(b *MockBackend, op ReplayOp) {
	switch op.Type {
	case OpAddEvent:
		b.Events[op.SessionID] = append(b.Events[op.SessionID], op.Role+":"+op.Content)
	case OpSetState:
		if b.States[op.SessionID] == nil {
			b.States[op.SessionID] = make(map[string]string)
		}
		b.States[op.SessionID][op.StateKey] = op.StateVal
	case OpDeleteState:
		if b.States[op.SessionID] != nil {
			delete(b.States[op.SessionID], op.StateKey)
		}
	case OpWriteMemory:
		if b.Memories[op.SessionID] == nil {
			b.Memories[op.SessionID] = make(map[string]string)
		}
		b.Memories[op.SessionID][op.MemoryID] = op.Content
	case OpUpdateSummary:
		if b.Summaries[op.SessionID] == nil {
			b.Summaries[op.SessionID] = make(map[string]string)
		}
		fk := op.FilterKey
		if fk == "" {
			fk = "default"
		}
		b.Summaries[op.SessionID][fk] = op.Content
	case OpAddTrack:
		b.Tracks[op.SessionID] = append(b.Tracks[op.SessionID], op.TrackName)
	case OpInjectTrap:
		// Trap injection for testing trap detection capability
		if b.Name == "SQLite" {
			b.Events[op.SessionID] = append(b.Events[op.SessionID], "TRAP_DIRTY_EVENT")
		}
	}
}

// WriteReport writes the consolidated diff report to a JSON file.
func WriteReport(path string, report DiffReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// NormalizeKeys returns a sorted slice of map keys to guarantee deterministic order.
func NormalizeKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// FormatSummary formats summary representation.
func FormatSummary(fk, text string) string {
	return fmt.Sprintf("[%s] %s", strings.TrimSpace(fk), strings.TrimSpace(text))
}
