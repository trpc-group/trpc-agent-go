//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package history

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

const (
	invStateKeyBudget = "tool:history:budget"
)

// budget keeps invocation-scoped limits for history tools.
// It is stored on agent.Invocation state so it is naturally per-run.
type budget struct {
	// SearchCallsRemaining is the remaining number of search_history calls.
	SearchCallsRemaining int `json:"searchCallsRemaining"`
	// GetCallsRemaining is the remaining number of get_history_events calls.
	GetCallsRemaining int `json:"getCallsRemaining"`
	// CharsRemaining is the remaining character budget across history tool calls.
	CharsRemaining int `json:"charsRemaining"`
}

func defaultBudget() *budget {
	return &budget{
		SearchCallsRemaining: 3,
		GetCallsRemaining:    2,
		CharsRemaining:       6000,
	}
}

func getOrInitBudget(inv *agent.Invocation) *budget {
	if inv == nil {
		return defaultBudget()
	}
	if v, ok := inv.GetState(invStateKeyBudget); ok {
		if b, ok2 := v.(*budget); ok2 && b != nil {
			return b
		}
	}
	b := defaultBudget()
	inv.SetState(invStateKeyBudget, b)
	return b
}

func spendChars(b *budget, n int) error {
	if b == nil {
		return nil
	}
	if n <= 0 {
		return nil
	}
	if b.CharsRemaining-n < 0 {
		return fmt.Errorf("history budget exceeded")
	}
	b.CharsRemaining -= n
	return nil
}
