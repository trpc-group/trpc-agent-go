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

// RunFaultDemo replays c cleanly on the baseline backend (bs[0]) and against a
// fault-wrapped compare backend (bs[1]), then classifies the diffs into a
// CaseReport. It gives the sample report at least one locally-reachable
// "inconsistent" row, demonstrating the harness catches genuinely persisted bad
// data without needing an external backend. The case must declare a
// FaultInjection that WrapFaulty understands.
func RunFaultDemo(ctx context.Context, bs []*backends.Backend, c *ReplayCase) (CaseReport, error) {
	if len(bs) < 2 {
		return CaseReport{}, errors.New("RunFaultDemo requires a baseline and at least one compare backend")
	}
	baseSnap, err := Run(ctx, bs[0], c)
	if err != nil {
		return CaseReport{}, err
	}
	Normalize(baseSnap)

	compare := bs[1]
	badSnap, err := RunFaulty(ctx, compare, c)
	if err != nil {
		return CaseReport{}, err
	}
	Normalize(badSnap)

	cr := CaseReport{Case: c.Name, SessionID: c.Key.SessionID}
	caps := Capabilities{SupportsEventPage: compare.SupportsEventPage, SupportsTTL: compare.SupportsTTL}
	for _, d := range Compare(c.Name, compare.Name, baseSnap, badSnap) {
		verdict, expl := Classify(compare.Name, caps, d)
		cr.Results = append(cr.Results, ResultEntry{
			Backend:       compare.Name,
			Category:      d.Category,
			Locator:       d.Locator,
			FieldPath:     d.FieldPath,
			BaselineValue: d.BaselineValue,
			CompareValue:  d.CompareValue,
			Verdict:       verdict,
			Explanation:   expl,
		})
	}
	return cr, nil
}
