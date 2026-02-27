//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"errors"
	"fmt"
)

// ExecutionEngine controls how the graph is scheduled and executed.
//
// The default engine is ExecutionEngineBSP, which runs in Pregel-style
// BSP (bulk synchronous parallel) supersteps.
//
// ExecutionEngineDAG (also called "eager") removes the global superstep
// barrier and schedules nodes as soon as their dependencies become ready.
type ExecutionEngine string

const (
	// ExecutionEngineBSP is the default Pregel-style scheduling engine.
	ExecutionEngineBSP ExecutionEngine = "bsp"
	// ExecutionEngineDAG schedules nodes eagerly without supersteps.
	ExecutionEngineDAG ExecutionEngine = "dag"
)

var errUnknownExecutionEngine = errors.New("unknown execution engine")

func (e ExecutionEngine) validate() error {
	switch e {
	case ExecutionEngineBSP, ExecutionEngineDAG:
		return nil
	default:
		return fmt.Errorf("%w: %q", errUnknownExecutionEngine, e)
	}
}
