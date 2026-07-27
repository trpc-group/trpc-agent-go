//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type pricing struct {
	InputPerM  *float64
	OutputPerM *float64
}

type modelCall struct {
	Stage            string
	Role             string
	PromptTokens     int
	CompletionTokens int
	UsageKnown       bool
	Pricing          pricing
	Duration         time.Duration
	Err              string
}

type callError struct {
	Stage   string `json:"stage"`
	Role    string `json:"role"`
	Message string `json:"message"`
}

type ledger struct {
	mu sync.Mutex

	modelCalls int
	toolCalls  int
	tokens     int
	cost       float64
	latency    time.Duration
	pending    int

	tokensUnknown bool
	costUnknown   bool
	callErrors    []callError
}

func newLedger() *ledger {
	return &ledger{}
}

func (l *ledger) record(call modelCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.modelCalls++
	usageKnown := call.UsageKnown || call.PromptTokens != 0 || call.CompletionTokens != 0
	l.addUsageLocked(call, usageKnown)
}

func (l *ledger) beginCall(stage, role string, prices pricing, durationOverride *time.Duration) *callTicket {
	l.mu.Lock()
	l.modelCalls++
	l.pending++
	l.mu.Unlock()
	return &callTicket{
		ledger:           l,
		stage:            stage,
		role:             role,
		pricing:          prices,
		start:            time.Now(),
		durationOverride: durationOverride,
	}
}

func (l *ledger) recordToolCall() {
	l.mu.Lock()
	l.toolCalls++
	l.mu.Unlock()
}

func (l *ledger) addUsageLocked(call modelCall, usageKnown bool) {
	l.latency += call.Duration
	if !usageKnown {
		l.tokensUnknown = true
		l.costUnknown = true
	} else {
		l.tokens += call.PromptTokens + call.CompletionTokens
		if call.Pricing.InputPerM == nil || call.Pricing.OutputPerM == nil {
			l.costUnknown = true
		} else {
			l.cost += float64(call.PromptTokens) * *call.Pricing.InputPerM / 1_000_000
			l.cost += float64(call.CompletionTokens) * *call.Pricing.OutputPerM / 1_000_000
		}
	}
	if call.Err != "" {
		l.callErrors = append(l.callErrors, callError{Stage: call.Stage, Role: call.Role, Message: call.Err})
	}
}

func (l *ledger) snapshot() usageSummary {
	l.mu.Lock()
	defer l.mu.Unlock()
	complete := l.pending == 0
	return usageSummary{
		ModelCalls:    measurement[int]{Known: true, Value: l.modelCalls},
		ToolCalls:     measurement[int]{Known: true, Value: l.toolCalls},
		Tokens:        measurement[int]{Known: complete && !l.tokensUnknown, Value: l.tokens},
		EstimatedCost: measurement[float64]{Known: complete && !l.costUnknown, Value: l.cost},
		LatencyMillis: measurement[int64]{Known: complete, Value: l.latency.Milliseconds()},
	}
}

func (l *ledger) errors() []callError {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]callError(nil), l.callErrors...)
}

func (l *ledger) canReserve(reservation usageSummary, policy gatePolicy) error {
	current := l.snapshot()
	var result error
	result = errors.Join(result, reserveInt("model calls", current.ModelCalls, reservation.ModelCalls, policy.MaxModelCalls))
	result = errors.Join(result, reserveInt("tool calls", current.ToolCalls, reservation.ToolCalls, policy.MaxToolCalls))
	result = errors.Join(result, reserveInt("tokens", current.Tokens, reservation.Tokens, policy.MaxTokens))
	result = errors.Join(result, reserveFloat("estimated cost", current.EstimatedCost, reservation.EstimatedCost, policy.MaxEstimatedCost))
	result = errors.Join(result, reserveInt64("latency", current.LatencyMillis, reservation.LatencyMillis, policy.MaxLatencyMillis))
	return result
}

type callTicket struct {
	ledger           *ledger
	stage            string
	role             string
	pricing          pricing
	start            time.Time
	durationOverride *time.Duration
	done             bool
}

func (t *callTicket) finish(promptTokens, completionTokens int, usageKnown bool, callErr string) {
	if t.done {
		return
	}
	t.done = true
	t.ledger.mu.Lock()
	defer t.ledger.mu.Unlock()
	t.ledger.pending--
	duration := time.Since(t.start)
	if t.durationOverride != nil {
		duration = *t.durationOverride
	}
	t.ledger.addUsageLocked(modelCall{
		Stage:            t.stage,
		Role:             t.role,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Pricing:          t.pricing,
		Duration:         duration,
		Err:              callErr,
	}, usageKnown)
}

func reserveInt(name string, current, reservation measurement[int], limit *int) error {
	if limit == nil {
		return nil
	}
	if !current.Known || !reservation.Known {
		return fmt.Errorf("reserve %s: measurement is unknown", name)
	}
	if current.Value+reservation.Value > *limit {
		return fmt.Errorf("reserve %s: projected value %d exceeds limit %d", name, current.Value+reservation.Value, *limit)
	}
	return nil
}

func reserveInt64(name string, current, reservation measurement[int64], limit *int64) error {
	if limit == nil {
		return nil
	}
	if !current.Known || !reservation.Known {
		return fmt.Errorf("reserve %s: measurement is unknown", name)
	}
	if current.Value+reservation.Value > *limit {
		return fmt.Errorf("reserve %s: projected value %d exceeds limit %d", name, current.Value+reservation.Value, *limit)
	}
	return nil
}

func reserveFloat(name string, current, reservation measurement[float64], limit *float64) error {
	if limit == nil {
		return nil
	}
	if !current.Known || !reservation.Known {
		return fmt.Errorf("reserve %s: measurement is unknown", name)
	}
	if current.Value+reservation.Value > *limit {
		return fmt.Errorf("reserve %s: projected value %.6f exceeds limit %.6f", name, current.Value+reservation.Value, *limit)
	}
	return nil
}
