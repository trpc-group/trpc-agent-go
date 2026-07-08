//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
)

// RunAll loads the cases in dir and replays every clean case across all
// backends, comparing each non-baseline backend to the baseline (bs[0]) and
// classifying each diff. Faulty variants (those declaring a FaultInjection) are
// skipped here; they are exercised by the fault-detection test. Integration-only
// cases are skipped unless an external backend is present.
func RunAll(ctx context.Context, dir, mode string, bs []*backends.Backend) (*Report, error) {
	if len(bs) == 0 {
		return nil, errors.New("RunAll requires at least one backend (baseline)")
	}
	cases, err := LoadCases(dir)
	if err != nil {
		return nil, err
	}
	names := backendNames(bs)
	report := NewReport(mode, bs[0].Name, names)
	hasExternal := len(bs) > 2

	for _, c := range cases {
		if c.FaultInjection != "" {
			continue
		}
		if c.Mode == "integration" && !hasExternal {
			continue
		}
		cr, err := runCase(ctx, c, bs)
		if err != nil {
			return nil, err
		}
		report.AddCase(cr)
	}
	return report, nil
}

func runCase(ctx context.Context, c *ReplayCase, bs []*backends.Backend) (CaseReport, error) {
	baseSnap, err := Run(ctx, bs[0], c)
	if err != nil {
		return CaseReport{}, err
	}
	Normalize(baseSnap)

	cr := CaseReport{Case: c.Name, SessionID: c.Key.SessionID}
	for _, b := range bs[1:] {
		otherSnap, err := Run(ctx, b, c)
		if err != nil {
			return CaseReport{}, err
		}
		Normalize(otherSnap)

		caps := Capabilities{SupportsEventPage: b.SupportsEventPage, SupportsTTL: b.SupportsTTL}
		for _, d := range Compare(c.Name, b.Name, baseSnap, otherSnap) {
			verdict, expl := Classify(b.Name, caps, d)
			cr.Results = append(cr.Results, ResultEntry{
				Backend:       b.Name,
				Category:      d.Category,
				Locator:       d.Locator,
				FieldPath:     d.FieldPath,
				BaselineValue: d.BaselineValue,
				CompareValue:  d.CompareValue,
				Verdict:       verdict,
				Explanation:   expl,
			})
		}
	}
	return cr, nil
}

func backendNames(bs []*backends.Backend) []string {
	names := make([]string, 0, len(bs))
	for _, b := range bs {
		names = append(names, b.Name)
	}
	return names
}
