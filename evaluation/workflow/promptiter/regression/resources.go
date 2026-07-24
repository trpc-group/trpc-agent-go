//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"sync"
)

// ResourceMeter returns cumulative resource usage. Implementations must return
// monotonically increasing measurements for every available field.
type ResourceMeter interface {
	Snapshot() ResourceUsage
}

// ResourceObserver receives every entry appended to a pipeline ledger.
type ResourceObserver func(ResourceEntry)

// UsageMeter is a concurrency-safe cumulative resource meter.
type UsageMeter struct {
	mu       sync.Mutex
	usage    ResourceUsage
	recorded bool
}

// NewUsageMeter creates a meter with known zero call, token, and latency
// counters. Monetary cost remains unavailable until a recorder supplies it.
func NewUsageMeter() *UsageMeter {
	return &UsageMeter{usage: ResourceUsage{
		ModelCalls:   Count{Available: true},
		InputTokens:  Count{Available: true},
		OutputTokens: Count{Available: true},
		LatencyMS:    Count{Available: true},
	}}
}

// Record adds one usage increment to the cumulative meter.
func (m *UsageMeter) Record(usage ResourceUsage) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.recorded {
		m.usage.ModelCalls = addCount(m.usage.ModelCalls, usage.ModelCalls)
		m.usage.InputTokens = addCount(m.usage.InputTokens, usage.InputTokens)
		m.usage.OutputTokens = addCount(m.usage.OutputTokens, usage.OutputTokens)
		m.usage.LatencyMS = addCount(m.usage.LatencyMS, usage.LatencyMS)
		m.usage.MonetaryCost = usage.MonetaryCost
		m.recorded = true
		return
	}
	m.usage = addResourceUsage(m.usage, usage)
}

// Snapshot returns an isolated copy of the cumulative measurement.
func (m *UsageMeter) Snapshot() ResourceUsage {
	if m == nil {
		return ResourceUsage{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage
}

func appendResourceEntry(
	ledger *ResourceLedger,
	entry ResourceEntry,
	observer ResourceObserver,
) {
	if ledger == nil {
		return
	}
	if len(ledger.Entries) == 0 {
		ledger.Cumulative = entry.Usage
	} else {
		ledger.Cumulative = addResourceUsage(ledger.Cumulative, entry.Usage)
	}
	ledger.Entries = append(ledger.Entries, entry)
	if observer != nil {
		observer(entry)
	}
}

func addResourceUsage(left, right ResourceUsage) ResourceUsage {
	return ResourceUsage{
		ModelCalls:   addCount(left.ModelCalls, right.ModelCalls),
		InputTokens:  addCount(left.InputTokens, right.InputTokens),
		OutputTokens: addCount(left.OutputTokens, right.OutputTokens),
		LatencyMS:    addCount(left.LatencyMS, right.LatencyMS),
		MonetaryCost: addAmount(left.MonetaryCost, right.MonetaryCost),
	}
}

func addCount(left, right Count) Count {
	if !left.Available || !right.Available {
		return Count{}
	}
	return Count{Available: true, Value: left.Value + right.Value}
}

func addAmount(left, right Amount) Amount {
	if !left.Available || !right.Available {
		return Amount{}
	}
	unit := left.Unit
	if unit == "" {
		unit = right.Unit
	}
	if left.Unit != "" && right.Unit != "" && left.Unit != right.Unit {
		return Amount{}
	}
	return Amount{Available: true, Value: left.Value + right.Value, Unit: unit}
}

func resourceUsageDelta(before, after ResourceUsage) ResourceUsage {
	return ResourceUsage{
		ModelCalls:   subtractCount(before.ModelCalls, after.ModelCalls),
		InputTokens:  subtractCount(before.InputTokens, after.InputTokens),
		OutputTokens: subtractCount(before.OutputTokens, after.OutputTokens),
		LatencyMS:    subtractCount(before.LatencyMS, after.LatencyMS),
		MonetaryCost: subtractAmount(before.MonetaryCost, after.MonetaryCost),
	}
}

func subtractCount(before, after Count) Count {
	if !before.Available || !after.Available || after.Value < before.Value {
		return Count{}
	}
	return Count{Available: true, Value: after.Value - before.Value}
}

func subtractAmount(before, after Amount) Amount {
	if !after.Available {
		return Amount{}
	}
	if !before.Available {
		if before.Value != 0 || before.Unit != "" {
			return Amount{}
		}
		return after
	}
	if after.Value < before.Value {
		return Amount{}
	}
	if before.Unit != "" && after.Unit != "" && before.Unit != after.Unit {
		return Amount{}
	}
	unit := after.Unit
	if unit == "" {
		unit = before.Unit
	}
	return Amount{Available: true, Value: after.Value - before.Value, Unit: unit}
}
