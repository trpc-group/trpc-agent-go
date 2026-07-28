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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Scenario is one replay case: a deterministic script plus the things to
// observe once it has run.
type Scenario struct {
	// Name identifies the case in reports and divergence records.
	Name string
	// Description states what the case is meant to expose. It is written into
	// the report so a failure is readable without the source at hand.
	Description string
	// Sessions lists the sessions to read back. A session is still listed when
	// the script deletes it, because "the backend still has it" is exactly the
	// kind of divergence worth catching.
	Sessions []SessionRef
	// MemoryUser selects the user whose memories are read back. Nil means the
	// case does not exercise memory.
	MemoryUser *SessionRef
	// Ops is the script, applied in order.
	Ops []Op
}

// Unsupported records a capability a backend does not implement.
//
// It is reported separately from divergences so that "this backend cannot do
// tracks" never reads as "this backend lost the track data".
type Unsupported struct {
	Backend string `json:"backend"`
	Feature string `json:"feature"`
	Reason  string `json:"reason"`
}

// CaseResult is the outcome of replaying one scenario across backends.
type CaseResult struct {
	Case        string `json:"case"`
	Description string `json:"description,omitempty"`
	// Baseline names the backend every other backend is compared against.
	Baseline    string        `json:"baseline"`
	Backends    []string      `json:"backends"`
	Unsupported []Unsupported `json:"unsupported,omitempty"`
	Divergences []Divergence  `json:"divergences,omitempty"`
	// Observations are retained so a report reader can see the compared values
	// in full rather than only the fields that differed.
	Observations []*Observation `json:"observations,omitempty"`
	// Duration is deliberately excluded from the serialized report: a wall
	// clock reading would make every regenerated report differ from the last.
	Duration time.Duration `json:"-"`
}

// Failed reports whether the case found a difference that is neither allowed
// nor already tracked as known.
func (r *CaseResult) Failed() bool {
	for _, d := range r.Divergences {
		if d.Fatal() {
			return true
		}
	}
	return false
}

// RunOption configures a replay run.
type RunOption func(*runConfig)

type runConfig struct {
	faults map[string]Fault
}

// WithFault injects a fault into the named backend before the script runs.
//
// This exists so the harness can prove it detects the failures it claims to
// detect: a case that reports no divergence under an injected fault is a case
// that would not have caught the real bug either.
func WithFault(backend string, f Fault) RunOption {
	return func(c *runConfig) {
		if c.faults == nil {
			c.faults = make(map[string]Fault)
		}
		c.faults[backend] = f
	}
}

// Run replays a scenario against every backend and compares each one against
// the first, which acts as the baseline.
func Run(ctx context.Context, sc Scenario, backends []Backend, opts ...RunOption) (*CaseResult, error) {
	if len(backends) < 2 {
		return nil, fmt.Errorf("scenario %q needs at least two backends, got %d", sc.Name, len(backends))
	}
	cfg := &runConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	base := newBaseTime()
	start := time.Now()
	result := &CaseResult{
		Case:        sc.Name,
		Description: sc.Description,
		Baseline:    backends[0].Name,
	}

	for _, b := range backends {
		obs, unsupported, err := runOne(ctx, sc, b, cfg.faults[b.Name], base)
		if err != nil {
			return nil, err
		}
		result.Backends = append(result.Backends, b.Name)
		result.Observations = append(result.Observations, obs)
		result.Unsupported = append(result.Unsupported, unsupported...)
	}

	baseline := result.Observations[0]
	for _, obs := range result.Observations[1:] {
		result.Divergences = append(result.Divergences, Compare(sc.Name, baseline, obs)...)
	}
	result.Duration = time.Since(start)
	return result, nil
}

// runOne replays the scenario against a single backend and projects the
// result.
func runOne(
	ctx context.Context, sc Scenario, b Backend, fault Fault, base time.Time,
) (*Observation, []Unsupported, error) {
	sum := &scriptedSummarizer{}
	svcs, err := b.Open(sum)
	if err != nil {
		return nil, nil, fmt.Errorf("open backend %q: %w", b.Name, err)
	}
	defer svcs.Close()

	// Capabilities are detected from the undecorated service. A fault wrapper
	// necessarily implements the optional interfaces it intercepts, so asking
	// the wrapper what it supports would let a fault mask itself as a missing
	// capability instead of surfacing as lost data.
	caps := detectCapabilities(svcs.Session, svcs.Memory)

	sessionSvc := svcs.Session
	if fault.Wrap != nil {
		sessionSvc = fault.Wrap(sessionSvc)
	}

	tgt := &target{
		name:       b.Name,
		session:    sessionSvc,
		memory:     svcs.Memory,
		summarizer: sum,
		caps:       caps,
		base:       base,
	}

	for i, op := range sc.Ops {
		if err := op.apply(ctx, tgt); err != nil {
			return nil, nil, fmt.Errorf(
				"scenario %q backend %q op %d (%s): %w", sc.Name, b.Name, i, op.Describe(), err)
		}
	}

	obs, err := observe(ctx, tgt, sc)
	if err != nil {
		return nil, nil, fmt.Errorf("scenario %q backend %q: %w", sc.Name, b.Name, err)
	}
	return obs, unsupportedFeatures(b.Name, tgt.caps), nil
}

// detectCapabilities inspects what the live services actually implement rather
// than trusting a hand-maintained table, so a backend that gains or loses an
// optional interface is picked up without editing this package.
func detectCapabilities(sessionSvc session.Service, memorySvc memory.Service) Capabilities {
	_, tracks := sessionSvc.(session.TrackService)
	return Capabilities{
		Tracks:  tracks,
		Summary: true,
		Memory:  memorySvc != nil,
	}
}

func unsupportedFeatures(backend string, caps Capabilities) []Unsupported {
	var out []Unsupported
	if !caps.Tracks {
		out = append(out, Unsupported{
			Backend: backend,
			Feature: "tracks",
			Reason:  "session service does not implement session.TrackService",
		})
	}
	if !caps.Memory {
		out = append(out, Unsupported{
			Backend: backend,
			Feature: "memory",
			Reason:  "no memory service is paired with this backend",
		})
	}
	return out
}
