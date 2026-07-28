//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// faultTarget is the backend faults are injected into.
//
// Faults never go into the baseline, because a corrupted baseline would make
// every other backend look wrong. They also avoid redis, whose two entries in
// the known-divergence list would mask a fault landing on the same path and
// turn a missed detection into a silent pass.
const faultTarget = "sqlite"

// twoSessionSummary is a scenario with a second session, so that a summary
// written against the wrong session has somewhere wrong to go.
func twoSessionSummary() Scenario {
	primary := ref("u-own", "s-own-primary")
	other := ref("u-own", "s-own-other")
	return Scenario{
		Name:        "summary-ownership",
		Description: "two sessions of one user, each summarized independently",
		Sessions:    []SessionRef{primary, other},
		Ops: []Op{
			CreateSession{Ref: primary},
			CreateSession{Ref: other},
			AppendEvent{Ref: primary, Event: EventSpec{
				ID: "o1", InvocationID: "inv-own", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "primary conversation",
			}},
			AppendEvent{Ref: other, Event: EventSpec{
				ID: "o2", InvocationID: "inv-own-2", Author: "user",
				Offset: sec(1), Role: model.RoleUser, Content: "unrelated conversation",
			}},
			CreateSummary{Ref: primary, Summary: SummarySpec{
				Text: "primary summary", Force: true,
			}},
		},
	}
}

// TestFaultDetection injects a known corruption into one backend and requires
// the comparator to report it.
//
// Without this test the suite could only demonstrate that backends currently
// agree, which is also what a comparator that inspects nothing would report.
// Each case here fails if the harness stops noticing the fault it names.
func TestFaultDetection(t *testing.T) {
	summaryOwnership := twoSessionSummary()

	tests := []struct {
		name string
		// scenario is the case the fault is injected into.
		scenario Scenario
		fault    Fault
		// wantPath is a fragment every acceptance criterion requires the
		// divergence to locate: the session, the summary filter key, the track
		// name or the field itself.
		wantPath string
	}{
		{
			name:     "dropped event",
			scenario: singleTurn(),
			fault:    DropNthEvent(2),
			wantPath: `sessions[ref="replay/u-single/s-single"].events`,
		},
		{
			name:     "duplicated event on retry",
			scenario: singleTurn(),
			fault:    DuplicateNthEvent(1),
			wantPath: `sessions[ref="replay/u-single/s-single"].events`,
		},
		{
			name:     "events returned out of order",
			scenario: multiTurn(),
			fault:    SwapEventsOnRead(0, 1),
			wantPath: `sessions[ref="replay/u-multi/s-multi"].events[0]`,
		},
		{
			name:     "summary lost",
			scenario: summaryLifecycle(),
			fault:    DropSummary(),
			wantPath: `sessions[ref="replay/u-summary/s-summary"].summaries[filterKey=""]`,
		},
		{
			name:     "summary not overwritten on regeneration",
			scenario: summaryLifecycle(),
			fault:    StaleSummaryOnRegenerate(),
			wantPath: `sessions[ref="replay/u-summary/s-summary"].summaries[filterKey=""].text`,
		},
		{
			name:     "summary filed under the wrong filter key",
			scenario: summaryLifecycle(),
			fault:    MisfileSummaryFilterKey("not-the-requested-branch"),
			wantPath: `summaries[filterKey=`,
		},
		{
			name:     "summary attributed to the wrong session",
			scenario: summaryOwnership,
			fault:    MisattributeSummary(ref("u-own", "s-own-other")),
			wantPath: `sessions[ref="replay/u-own/s-own-`,
		},
		{
			name:     "state key lost",
			scenario: stateLifecycle(),
			fault:    HideStateKeyOnRead("stage"),
			wantPath: `state[key="stage"]`,
		},
		{
			name:     "state key leaked",
			scenario: stateLifecycle(),
			fault:    InjectStateKeyOnRead("ghost", "leaked"),
			wantPath: `state[key="ghost"]`,
		},
		{
			name:     "track events lost",
			scenario: trackEvents(),
			fault:    DropTrackEvents(),
			wantPath: `tracks[track=`,
		},
	}

	backends := LightweightBackends()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := Run(
				context.Background(), tt.scenario, backends,
				WithFault(faultTarget, tt.fault),
			)
			if err != nil {
				t.Fatalf("run %q with fault %q: %v", tt.scenario.Name, tt.fault.Name, err)
			}

			if !res.Failed() {
				t.Fatalf("fault %q (%s) went undetected in case %q; the comparator reported no fatal divergence",
					tt.fault.Name, tt.fault.Description, tt.scenario.Name)
			}

			var located bool
			for _, d := range res.Divergences {
				if !d.Fatal() || d.Backend != faultTarget {
					continue
				}
				if strings.Contains(d.Path, tt.wantPath) {
					located = true
					break
				}
			}
			if !located {
				t.Errorf("fault %q was detected but not located at %q; paths reported:\n%s",
					tt.fault.Name, tt.wantPath, fatalPaths(res, faultTarget))
			}
		})
	}
}

// TestFaultDetectionCoversEveryCase requires every public case to notice a
// dropped write, so that no case in the published list is decorative.
func TestFaultDetectionCoversEveryCase(t *testing.T) {
	backends := LightweightBackends()
	for _, sc := range Scenarios() {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			// Dropping the first append reaches every case: each one starts by
			// writing something the projection reads back.
			res, err := Run(
				context.Background(), sc, backends,
				WithFault(faultTarget, DropNthEvent(1)),
			)
			if err != nil {
				t.Fatalf("run %q: %v", sc.Name, err)
			}
			if !res.Failed() {
				t.Errorf("case %q did not detect a dropped event; it cannot be relied on to catch a regression",
					sc.Name)
			}
		})
	}
}

func fatalPaths(res *CaseResult, backend string) string {
	var b strings.Builder
	for _, d := range res.Divergences {
		if d.Fatal() && d.Backend == backend {
			b.WriteString("  " + d.Path + "\n")
		}
	}
	if b.Len() == 0 {
		return "  (none)\n"
	}
	return b.String()
}
