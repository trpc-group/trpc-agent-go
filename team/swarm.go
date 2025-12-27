//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import "time"

// SwarmConfig defines optional safety limits for swarm-style handoffs.
//
// All fields are optional. A zero value means "no limit" for that field.
type SwarmConfig struct {
	// MaxHandoffs limits how many transfers can happen in a single run.
	MaxHandoffs int

	// NodeTimeout limits how long a single member agent may run after a
	// transfer. A zero value means no per-node timeout.
	NodeTimeout time.Duration

	// RepetitiveHandoffWindow is the sliding window size used to detect
	// repetitive handoff loops. A zero value disables this check.
	RepetitiveHandoffWindow int

	// RepetitiveHandoffMinUnique is the minimum number of unique agents that
	// must appear in the window. If fewer appear, the transfer is rejected.
	// A zero value disables this check.
	RepetitiveHandoffMinUnique int
}

// DefaultSwarmConfig returns conservative defaults that prevent unbounded
// transfer loops while keeping behavior predictable.
func DefaultSwarmConfig() SwarmConfig {
	return SwarmConfig{
		MaxHandoffs:                20,
		RepetitiveHandoffWindow:    8,
		RepetitiveHandoffMinUnique: 3,
	}
}
