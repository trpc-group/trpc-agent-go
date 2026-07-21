//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Capability describes the optional backend features a target supports.
// Cases declare the capabilities they require; a target that misses some of
// them reports the case as "unsupported" instead of failing it.
type Capability struct {
	// Session indicates session create/append/read support.
	Session bool
	// Memory indicates memory add/update/delete/read support.
	Memory bool
	// Summary indicates session summary write/read support.
	Summary bool
	// Tracks indicates track event support.
	Tracks bool
	// State indicates app/user/session state support.
	State bool
	// MemorySearch indicates memory search support.
	MemorySearch bool
}

// CapAll is the full capability set.
var CapAll = Capability{
	Session:      true,
	Memory:       true,
	Summary:      true,
	Tracks:       true,
	State:        true,
	MemorySearch: true,
}

// Missing returns the capabilities set in want that c does not have.
func (c Capability) Missing(want Capability) Capability {
	return Capability{
		Session:      want.Session && !c.Session,
		Memory:       want.Memory && !c.Memory,
		Summary:      want.Summary && !c.Summary,
		Tracks:       want.Tracks && !c.Tracks,
		State:        want.State && !c.State,
		MemorySearch: want.MemorySearch && !c.MemorySearch,
	}
}

// Names returns the human readable names of the enabled capabilities.
func (c Capability) Names() []string {
	var out []string
	if c.Session {
		out = append(out, "session")
	}
	if c.Memory {
		out = append(out, "memory")
	}
	if c.Summary {
		out = append(out, "summary")
	}
	if c.Tracks {
		out = append(out, "track")
	}
	if c.State {
		out = append(out, "state")
	}
	if c.MemorySearch {
		out = append(out, "memory_search")
	}
	return out
}

// Target is a backend under test. It pairs one session service with one
// memory service; either may be nil when unsupported (reflected in Caps).
//
// Implementations must live in the backend's own Go module: persistent
// backends are separate modules that depend on the root module, so this
// package cannot import them without creating an import cycle.
type Target interface {
	// Name returns the backend name, e.g. "inmemory" or "sqlite".
	Name() string
	// Caps returns the supported capability set.
	Caps() Capability
	// SessionService returns the session service, or nil.
	SessionService() session.Service
	// MemoryService returns the memory service, or nil.
	MemoryService() memory.Service
	// Reset removes all data so the next case starts from a clean slate.
	Reset(ctx context.Context) error
}
