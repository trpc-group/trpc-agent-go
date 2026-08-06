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
	"encoding/json"
	"fmt"
)

// Mutation injects one kind of inconsistency into a canonical snapshot.
// Mutation tests prove the differ detects every injected inconsistency:
// they are the executable evidence for the detection-rate acceptance
// criteria (100% for all public cases; 100% for summary loss, stale
// overwrite, wrong attribution and filter-key errors).
type Mutation struct {
	// Name is the stable mutation ID.
	Name string
	// Dimension is the expected diff dimension.
	Dimension string
	// Applies reports whether the mutation has something to corrupt.
	Applies func(c *Canonical) bool
	// Apply corrupts the snapshot in place.
	Apply func(c *Canonical)
}

// Mutations returns the standard mutation set: one mutation per comparison
// code path. Mutations that would exercise the same differ branch as an
// existing entry (e.g. dropping vs corrupting a memory) are omitted.
func Mutations() []Mutation {
	return []Mutation{
		{
			Name:      "drop_event",
			Dimension: DimEvent,
			Applies: func(c *Canonical) bool {
				return firstSessionWithEvents(c) != nil
			},
			Apply: func(c *Canonical) {
				s := firstSessionWithEvents(c)
				s.Events = append(s.Events[:0], s.Events[1:]...)
			},
		},
		{
			Name:      "swap_events",
			Dimension: DimEvent,
			Applies: func(c *Canonical) bool {
				s, _, _ := sameBranchPair(c)
				return s != nil
			},
			Apply: func(c *Canonical) {
				s, i, j := sameBranchPair(c)
				s.Events[i], s.Events[j] = s.Events[j], s.Events[i]
			},
		},
		{
			Name:      "corrupt_state",
			Dimension: DimState,
			Applies: func(c *Canonical) bool {
				return firstStateMap(c) != nil
			},
			Apply: func(c *Canonical) {
				m := firstStateMap(c)
				for k := range m {
					m[k] = `"CORRUPTED"`
					return
				}
			},
		},
		{
			Name:      "drop_summary",
			Dimension: DimSummary,
			Applies: func(c *Canonical) bool {
				return firstSessionWithSummaries(c) != nil
			},
			Apply: func(c *Canonical) {
				s := firstSessionWithSummaries(c)
				for fk := range s.Summaries {
					delete(s.Summaries, fk)
					return
				}
			},
		},
		{
			Name:      "stale_summary",
			Dimension: DimSummary,
			Applies: func(c *Canonical) bool {
				return firstSessionWithSummaries(c) != nil
			},
			Apply: func(c *Canonical) {
				s := firstSessionWithSummaries(c)
				for _, sum := range s.Summaries {
					sum.Text = "STALE-SUMMARY"
					return
				}
			},
		},
		{
			Name:      "wrong_filterkey",
			Dimension: DimSummary,
			Applies: func(c *Canonical) bool {
				return firstSessionWithSummaries(c) != nil
			},
			Apply: func(c *Canonical) {
				s := firstSessionWithSummaries(c)
				for fk, sum := range s.Summaries {
					delete(s.Summaries, fk)
					s.Summaries["wrong/filter-key"] = sum
					return
				}
			},
		},
		{
			Name:      "wrong_session_summary",
			Dimension: DimSummary,
			Applies: func(c *Canonical) bool {
				from, to := summaryMovePair(c)
				return from != nil && to != nil
			},
			Apply: func(c *Canonical) {
				from, to := summaryMovePair(c)
				for fk, sum := range from.Summaries {
					delete(from.Summaries, fk)
					if to.Summaries == nil {
						to.Summaries = map[string]*CSummary{}
					}
					to.Summaries[fk] = sum
					return
				}
			},
		},
		{
			Name:      "corrupt_memory_content",
			Dimension: DimMemory,
			Applies: func(c *Canonical) bool {
				return len(c.Memories) > 0
			},
			Apply: func(c *Canonical) {
				c.Memories[0].Content = "CORRUPTED-CONTENT"
			},
		},
		{
			Name:      "corrupt_track_payload",
			Dimension: DimTrack,
			Applies: func(c *Canonical) bool {
				return firstSessionWithTracks(c) != nil
			},
			Apply: func(c *Canonical) {
				s := firstSessionWithTracks(c)
				for _, payloads := range s.Tracks {
					if len(payloads) > 0 {
						payloads[0] = `{"corrupted":true}`
						return
					}
				}
			},
		},
	}
}

// CloneCanonical deep-copies a canonical snapshot via JSON round trip.
func CloneCanonical(c *Canonical) *Canonical {
	b, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Sprintf("clone canonical marshal: %v", err))
	}
	var out Canonical
	if err := json.Unmarshal(b, &out); err != nil {
		panic(fmt.Sprintf("clone canonical unmarshal: %v", err))
	}
	return &out
}

// firstSessionWithEvents returns the first session holding events.
func firstSessionWithEvents(c *Canonical) *CSession {
	for _, s := range c.Sessions {
		if len(s.Events) > 0 {
			return s
		}
	}
	return nil
}

// firstSessionWithSummaries returns the first session holding summaries.
func firstSessionWithSummaries(c *Canonical) *CSession {
	for _, s := range c.Sessions {
		if len(s.Summaries) > 0 {
			return s
		}
	}
	return nil
}

// firstSessionWithTracks returns the first session holding tracks.
func firstSessionWithTracks(c *Canonical) *CSession {
	for _, s := range c.Sessions {
		if len(s.Tracks) > 0 {
			return s
		}
	}
	return nil
}

// sameBranchPair locates two same-branch events in the first session that
// has a pair (swapping them is detectable in both comparison modes).
func sameBranchPair(c *Canonical) (s *CSession, i, j int) {
	for _, s := range c.Sessions {
		seen := map[string]int{}
		for i, e := range s.Events {
			if j, ok := seen[e.Branch]; ok {
				return s, j, i
			}
			seen[e.Branch] = i
		}
	}
	return nil, 0, 0
}

// summaryMovePair finds a session with a summary and another session to
// move it to.
func summaryMovePair(c *Canonical) (from, to *CSession) {
	for _, s := range c.Sessions {
		if len(s.Summaries) > 0 {
			from = s
			break
		}
	}
	if from == nil {
		return nil, nil
	}
	for _, s := range c.Sessions {
		if s != from {
			return from, s
		}
	}
	return nil, nil
}

// firstStateMap returns the first non-empty state map.
func firstStateMap(c *Canonical) map[string]string {
	for _, s := range c.Sessions {
		if len(s.State) > 0 {
			return s.State
		}
	}
	if len(c.AppState) > 0 {
		return c.AppState
	}
	if len(c.UserState) > 0 {
		return c.UserState
	}
	return nil
}
