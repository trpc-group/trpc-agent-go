//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// sessionTracker records opaque hostexec/workspace session ids so the
// guard can correlate write_stdin/kill_session calls with the session-
// creating exec_command. Only hashes are persisted in audit events.
type sessionTracker struct {
	mu          sync.Mutex
	known       map[string]sessionInfo
	knownOrder  []string
	killed      map[string]bool
	killedOrder []string
	inputGates  map[string]chan struct{}
}

type sessionInputMode string

const (
	sessionInputUnknown sessionInputMode = ""
	sessionInputData    sessionInputMode = "data"
	sessionInputShell   sessionInputMode = "shell"
	sessionInputCode    sessionInputMode = "code"
)

type sessionInfo struct {
	Backend   Backend
	InputMode sessionInputMode
	Language  string
	Pending   string
}

const maxKilledSessions = 1024
const maxKnownSessions = 1024
const maxSessionInputBuffer = 64 * 1024
const maxShellSessionInputBuffer = 16 * 1024

func sessionInputBufferLimit(info sessionInfo) int {
	if info.InputMode == sessionInputShell {
		// Keep this aligned with internal/shellsafe's command length
		// bound so oversized shell input gets the session-specific
		// finding instead of a generic parse failure.
		return maxShellSessionInputBuffer
	}
	return maxSessionInputBuffer
}

// newSessionTracker returns an empty sessionTracker.
func newSessionTracker() *sessionTracker {
	return &sessionTracker{
		known:      make(map[string]sessionInfo),
		killed:     make(map[string]bool),
		inputGates: make(map[string]chan struct{}),
	}
}

// register marks a session id as known.
func (s *sessionTracker) register(id string) {
	s.registerWithInfo(id, sessionInfo{InputMode: sessionInputData})
}

func (s *sessionTracker) registerWithInfo(id string, info sessionInfo) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.known[id]; !ok {
		s.knownOrder = append(s.knownOrder, id)
	}
	s.known[id] = info
	if _, ok := s.inputGates[id]; !ok {
		gate := make(chan struct{}, 1)
		gate <- struct{}{}
		s.inputGates[id] = gate
	}
	if len(s.knownOrder) > maxKnownSessions {
		oldest := s.knownOrder[0]
		s.knownOrder = s.knownOrder[1:]
		delete(s.known, oldest)
		delete(s.inputGates, oldest)
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
	delete(s.inputGates, id)
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

// quarantine preserves a live session for cleanup while forcing future
// stdin through the unclassified-session approval path.
func (s *sessionTracker) quarantine(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.killed[id] {
		return
	}
	info, ok := s.known[id]
	if !ok {
		return
	}
	info.InputMode = sessionInputUnknown
	info.Language = ""
	info.Pending = ""
	s.known[id] = info
}

// isKnown returns true when id was registered.
func (s *sessionTracker) isKnown(id string) bool {
	_, ok := s.lookup(id)
	return ok
}

func (s *sessionTracker) lookup(id string) (sessionInfo, bool) {
	if id == "" {
		return sessionInfo{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.known[id]
	return info, ok
}

func (s *sessionTracker) previewInput(
	id string,
	chars string,
	submit bool,
) (sessionInfo, string, bool, bool) {
	if id == "" {
		return sessionInfo{}, "", false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.known[id]
	if !ok {
		return sessionInfo{}, "", false, false
	}
	combined := info.Pending + chars
	if submit {
		combined += "\n"
	}
	return info, combined, true,
		len(combined) <= sessionInputBufferLimit(info)
}

func (s *sessionTracker) commitInput(
	id string,
	chars string,
	submit bool,
) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.known[id]
	if !ok {
		return
	}
	combined := info.Pending + chars
	if submit {
		combined += "\n"
	}
	if info.InputMode == sessionInputData ||
		info.InputMode == sessionInputShell {
		if index := strings.LastIndexByte(combined, '\n'); index >= 0 {
			combined = combined[index+1:]
		}
	}
	limit := sessionInputBufferLimit(info)
	if len(combined) > limit {
		combined = combined[len(combined)-limit:]
	}
	info.Pending = combined
	s.known[id] = info
}

func (s *sessionTracker) acquireInput(
	ctx context.Context,
	id string,
) (func(), error) {
	if id == "" {
		return nil, nil
	}
	s.mu.Lock()
	if _, ok := s.known[id]; !ok {
		s.mu.Unlock()
		return nil, nil
	}
	gate := s.inputGates[id]
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate:
		var once sync.Once
		return func() {
			once.Do(func() {
				gate <- struct{}{}
			})
		}, nil
	}
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
	s.known = make(map[string]sessionInfo)
	s.knownOrder = nil
	s.killed = make(map[string]bool)
	s.killedOrder = nil
	s.inputGates = make(map[string]chan struct{})
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
