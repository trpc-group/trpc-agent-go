//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"fmt"
)

// PublicCaseRunner executes one standard replay scenario against a backend.
type PublicCaseRunner func(context.Context, Backend) error

// PublicCases returns the reusable standard replay matrix. The caller supplies
// storage-specific scenario operations while replaytest owns case names,
// capability requirements, and comparison options.
func PublicCases(runners map[string]PublicCaseRunner) ([]Case, error) {
	specs := []struct {
		name                   string
		requiredCapabilities   []string
		orderEventsByTimestamp bool
	}{
		{name: "single_turn", requiredCapabilities: []string{CapabilityEvents}},
		{name: "multi_turn", requiredCapabilities: []string{CapabilityEvents}},
		{name: "tool_call_and_response", requiredCapabilities: []string{CapabilityEvents}},
		{name: "state_update_overwrite_delete", requiredCapabilities: []string{CapabilityEvents, CapabilityState}},
		{name: "session_state_direct_round_trip", requiredCapabilities: []string{CapabilityState}},
		{name: "memory_search_order_and_score", requiredCapabilities: []string{CapabilityMemory}},
		{name: "memory_update_and_delete", requiredCapabilities: []string{CapabilityMemory}},
		{name: "summary_filter_and_update", requiredCapabilities: []string{CapabilityEvents, CapabilitySummary}},
		{name: "summary_event_window_recovery", requiredCapabilities: []string{CapabilityEvents, CapabilitySummary}},
		{name: "track_status_and_error", requiredCapabilities: []string{CapabilityTracks}},
		{
			name: "concurrent_tool_event_interleaving", requiredCapabilities: []string{CapabilityEvents},
			orderEventsByTimestamp: true,
		},
		{
			name:                 "failure_recovery_without_duplicates",
			requiredCapabilities: []string{CapabilityEvents, CapabilityState, CapabilityMemory, CapabilitySummary},
		},
	}
	cases := make([]Case, 0, len(specs))
	for _, spec := range specs {
		run := runners[spec.name]
		if run == nil {
			return nil, fmt.Errorf("public replay case %q has no runner", spec.name)
		}
		cases = append(cases, Case{
			Name: spec.name, Run: run,
			RequiredCapabilities:   spec.requiredCapabilities,
			OrderEventsByTimestamp: spec.orderEventsByTimestamp,
		})
	}
	return cases, nil
}
