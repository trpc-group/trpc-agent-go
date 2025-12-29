//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type swarmRuntime struct {
	mu sync.Mutex

	cfg      SwarmConfig
	handoffs int
	recent   []string
}

func (sr *swarmRuntime) OnTransfer(
	_ context.Context,
	fromAgent string,
	toAgent string,
) (time.Duration, error) {
	_ = fromAgent

	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.handoffs++
	if sr.cfg.MaxHandoffs > 0 && sr.handoffs > sr.cfg.MaxHandoffs {
		return 0, fmt.Errorf(
			"max handoffs exceeded: %d",
			sr.cfg.MaxHandoffs,
		)
	}

	window := sr.cfg.RepetitiveHandoffWindow
	minUnique := sr.cfg.RepetitiveHandoffMinUnique
	if window > 0 && minUnique > 0 {
		sr.recent = append(sr.recent, toAgent)
		if len(sr.recent) > window {
			sr.recent = sr.recent[len(sr.recent)-window:]
		}
		if len(sr.recent) == window && uniqueCount(sr.recent) < minUnique {
			return 0, errRepetitiveHandoff
		}
	}

	return sr.cfg.NodeTimeout, nil
}

func ensureSwarmRuntime(inv *agent.Invocation, cfg SwarmConfig) {
	if inv == nil {
		return
	}
	if inv.RunOptions.RuntimeState == nil {
		inv.RunOptions.RuntimeState = make(map[string]any)
	}
	key := agent.RuntimeStateKeyTransferController
	if _, ok := inv.RunOptions.RuntimeState[key]; ok {
		return
	}
	inv.RunOptions.RuntimeState[key] = &swarmRuntime{cfg: cfg}
}

var (
	errRepetitiveHandoff = errors.New("repetitive handoff detected")
)

func uniqueCount(values []string) int {
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		seen[v] = struct{}{}
	}
	return len(seen)
}
