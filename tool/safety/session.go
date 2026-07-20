//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// sessionTracker records opaque hostexec/workspace session ids so the
// guard can correlate write_stdin/kill_session calls with the session-
// creating exec_command. Only hashes are persisted in audit events.
type sessionTracker struct {
	mu      sync.Mutex
	known   map[string]bool
	killed  map[string]bool
	created map[string]time.Time
}

func newSessionTracker() *sessionTracker {
	return &sessionTracker{
		known:   make(map[string]bool),
		killed:  make(map[string]bool),
		created: make(map[string]time.Time),
	}
}

// register marks a session id as known.
func (s *sessionTracker) register(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.known[id] = true
	s.created[id] = time.Now()
}

// kill marks a session id as killed. Subsequent kill/interaction calls
// produce a residual-session finding.
func (s *sessionTracker) kill(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.killed[id] = true
}

// isKnown returns true when id was registered.
func (s *sessionTracker) isKnown(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.known[id]
}

// isKilled returns true when id was killed.
func (s *sessionTracker) isKilled(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.killed[id]
}

// clear removes tracking state for id (used by Close).
func (s *sessionTracker) clear(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.known, id)
	delete(s.killed, id)
	delete(s.created, id)
}

// newScanID returns a unique identifier for one scan.
func newScanID() string {
	return uuid.NewString()
}

// ScanEvent is a compact representation of a ScanReport carried through
// context to the after-tool callback. It is the post-tool audit source.
type ScanEvent struct {
	ScanID      string
	ToolName    string
	Backend     Backend
	Decision    Decision
	RiskLevel   RiskLevel
	RuleIDs     []string
	DurationMs  float64
	Redacted    bool
	Intercepted bool
	CommandHash string
	SessionHash string
}

// fromReport converts a ScanReport to a ScanEvent.
func fromReport(r ScanReport) ScanEvent {
	return ScanEvent{
		ScanID:      r.ScanID,
		ToolName:    r.ToolName,
		Backend:     r.Backend,
		Decision:    r.Decision,
		RiskLevel:   r.RiskLevel,
		RuleIDs:     ruleIDsFromFindings(r.Findings),
		DurationMs:  r.DurationMs,
		Redacted:    r.Redacted,
		Intercepted: r.Intercepted,
		CommandHash: r.CommandHash,
	}
}
