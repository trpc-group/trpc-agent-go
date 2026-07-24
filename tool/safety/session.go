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

	"github.com/google/uuid"
)

// sessionTracker records opaque hostexec/workspace session ids so the
// guard can correlate write_stdin/kill_session calls with the session-
// creating exec_command. Only hashes are persisted in audit events.
type sessionTracker struct {
	mu          sync.Mutex
	known       map[string]bool
	knownOrder  []string
	killed      map[string]bool
	killedOrder []string
}

const maxKilledSessions = 1024
const maxKnownSessions = 1024

// newSessionTracker returns an empty sessionTracker.
func newSessionTracker() *sessionTracker {
	return &sessionTracker{
		known:  make(map[string]bool),
		killed: make(map[string]bool),
	}
}

// register marks a session id as known.
func (s *sessionTracker) register(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known[id] {
		s.knownOrder = append(s.knownOrder, id)
	}
	s.known[id] = true
	if len(s.knownOrder) > maxKnownSessions {
		oldest := s.knownOrder[0]
		s.knownOrder = s.knownOrder[1:]
		delete(s.known, oldest)
	}
	delete(s.killed, id)
	for i := 0; i < len(s.killedOrder); {
		if s.killedOrder[i] != id {
			i++
			continue
		}
		s.killedOrder = append(
			s.killedOrder[:i],
			s.killedOrder[i+1:]...,
		)
	}
}

// kill marks a session id as killed. Subsequent kill/interaction calls
// produce a residual-session finding.
func (s *sessionTracker) kill(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.known, id)
	s.removeKnownOrder(id)
	if s.killed[id] {
		return
	}
	s.killed[id] = true
	s.killedOrder = append(s.killedOrder, id)
	if len(s.killedOrder) > maxKilledSessions {
		oldest := s.killedOrder[0]
		s.killedOrder = s.killedOrder[1:]
		delete(s.killed, oldest)
	}
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

// reset drops all tracking state. Guard.Close calls it so the maps do
// not grow without bound over the guard's lifetime.
func (s *sessionTracker) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.known = make(map[string]bool)
	s.knownOrder = nil
	s.killed = make(map[string]bool)
	s.killedOrder = nil
}

func (s *sessionTracker) removeKnownOrder(id string) {
	for i := 0; i < len(s.knownOrder); {
		if s.knownOrder[i] != id {
			i++
			continue
		}
		s.knownOrder = append(
			s.knownOrder[:i],
			s.knownOrder[i+1:]...,
		)
	}
}

// newScanID returns a unique identifier for one scan.
func newScanID() string {
	return uuid.NewString()
}

// scanEvent is a compact representation of a ScanReport used as the
// post-tool audit source. The guard stashes it in a side table keyed by
// tool call id during wrapper preflight (allow decisions only) and
// wrapper completion pops it to emit a correlated post_execute audit
// record that reuses the preflight scan id, decision, risk level, and
// rule ids. Entries are evicted by completion or by Guard.Close.
type scanEvent struct {
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

// fromReport converts a ScanReport to a scanEvent.
func fromReport(r ScanReport) scanEvent {
	return scanEvent{
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
