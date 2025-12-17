//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package barrier provides a mechanism to indicate graph barrier requirements.
package barrier

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// StateKeyBarrier is the invocation state key used by internal barrier flag.
const StateKeyBarrier = "__graph_barrier__"

// Enable enables the graph barrier for the invocation.
func Enable(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.SetState(StateKeyBarrier, true)
}

// Enabled reports whether the graph barrier is enabled for the invocation.
func Enabled(inv *agent.Invocation) bool {
	enabled, ok := agent.GetStateValue[bool](inv, StateKeyBarrier)
	if !ok {
		return false
	}
	return enabled
}
