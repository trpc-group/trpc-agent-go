//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// SessionPersistenceMode controls how long sandbox workspace state is reused.
type SessionPersistenceMode int

const (
	// SessionPersistencePerTurn removes the workspace during cleanup so later
	// turns do not observe files written through the session workspace.
	SessionPersistencePerTurn SessionPersistenceMode = iota
	// SessionPersistencePerSession reuses one deterministic workspace for all
	// turns in the same session.
	SessionPersistencePerSession
)

// SessionRunConcurrencyMode controls how program runs sharing one session
// workspace are scheduled.
type SessionRunConcurrencyMode int

const (
	// SessionRunConcurrencyParallel allows runs sharing a workspace to execute
	// concurrently. Callers are responsible for avoiding file races.
	SessionRunConcurrencyParallel SessionRunConcurrencyMode = iota
	// SessionRunConcurrencySerial runs one program at a time per workspace.
	SessionRunConcurrencySerial
)

// SessionPolicy controls sandbox session lifecycle.
type SessionPolicy struct {
	Persistence    SessionPersistenceMode
	RunConcurrency SessionRunConcurrencyMode
}
