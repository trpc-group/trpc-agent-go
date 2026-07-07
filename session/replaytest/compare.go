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
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Run executes all cases against all backends and returns a diff report.
func Run(ctx context.Context, cases []ReplayCase, backends []Backend) (*Report, error) {
	report := &Report{
		GeneratedAt: time.Now().UTC(),
		Cases:       make([]CaseReport, 0, len(cases)),
	}
	if len(backends) == 0 {
		return report, nil
	}
	report.BaseBackend = backends[0].Name()
	for _, c := range cases {
		caseReport := CaseReport{
			Case:      c.Name,
			SessionID: c.Key.SessionID,
		}
		var base *Snapshot
		for i, backend := range backends {
			snapshot, err := backend.Apply(ctx, c)
			if err != nil {
				return nil, fmt.Errorf("run case %s on backend %s: %w", c.Name, backend.Name(), err)
			}
			caseReport.Compared = append(caseReport.Compared, backend.Name())
			if len(snapshot.Unsupported) > 0 {
				caseReport.Unsupported = append(caseReport.Unsupported, BackendUnsupported{
					Backend:     backend.Name(),
					Unsupported: snapshot.Unsupported,
				})
			}
			if i == 0 {
				base = snapshot
				continue
			}
			caseReport.Differences = append(
				caseReport.Differences,
				CompareSnapshots(base, snapshot)...,
			)
		}
		report.Cases = append(report.Cases, caseReport)
	}
	return report, nil
}

// CompareSnapshots returns all normalized differences between base and compare.
func CompareSnapshots(base, compare *Snapshot) []Difference {
	if base == nil || compare == nil {
		return nil
	}
	var diffs []Difference
	add := func(path, locator string, b, c any, explanation string) {
		if reflect.DeepEqual(b, c) {
			return
		}
		diffs = append(diffs, Difference{
			Case:         base.Case,
			Backend:      compare.Backend,
			SessionID:    base.SessionID,
			Locator:      locator,
			FieldPath:    path,
			BaseValue:    b,
			CompareValue: c,
			AllowedDiff:  false,
			Explanation:  explanation,
		})
	}
	add("$.session_id", "session", base.SessionID, compare.SessionID, "session ownership changed")
	compareSlices("$.events", base.Events, compare.Events, func(i int, field string, b, c any) {
		add(fmt.Sprintf("$.events[%d].%s", i, field), fmt.Sprintf("event[%d]", i), b, c, "event replay mismatch")
	})
	compareMaps("$.state", base.State, compare.State, func(key, field string, b, c any) {
		add(fmt.Sprintf("$.state[%q].%s", key, field), "state:"+key, b, c, "state final value mismatch")
	})
	compareSlices("$.memories", base.Memories, compare.Memories, func(i int, field string, b, c any) {
		locator := fmt.Sprintf("memory[%d]", i)
		if i >= 0 && i < len(base.Memories) {
			locator = "memory:" + base.Memories[i].StableID
		}
		add(fmt.Sprintf("$.memories[%d].%s", i, field), locator, b, c, "memory replay mismatch")
	})
	compareSlices("$.summaries", base.Summaries, compare.Summaries, func(i int, field string, b, c any) {
		locator := fmt.Sprintf("summary[%d]", i)
		if i >= 0 && i < len(base.Summaries) {
			locator = "summary:" + base.Summaries[i].FilterKey
		}
		add(fmt.Sprintf("$.summaries[%d].%s", i, field), locator, b, c, "summary replay mismatch")
	})
	compareSlices("$.tracks", base.Tracks, compare.Tracks, func(i int, field string, b, c any) {
		locator := fmt.Sprintf("track[%d]", i)
		if i >= 0 && i < len(base.Tracks) {
			locator = "track:" + base.Tracks[i].Name
		}
		add(fmt.Sprintf("$.tracks[%d].%s", i, field), locator, b, c, "track replay mismatch")
	})
	return diffs
}

func compareSlices[T any](path string, base, compare []T, add func(int, string, any, any)) {
	if len(base) != len(compare) {
		add(-1, "length", len(base), len(compare))
		return
	}
	for i := range base {
		compareStruct(path, base[i], compare[i], func(field string, b, c any) {
			add(i, field, b, c)
		})
	}
}

func compareMaps[T any](path string, base, compare map[string]T, add func(string, string, any, any)) {
	keys := map[string]struct{}{}
	for k := range base {
		keys[k] = struct{}{}
	}
	for k := range compare {
		keys[k] = struct{}{}
	}
	for k := range keys {
		b, bok := base[k]
		c, cok := compare[k]
		if !bok || !cok {
			add(k, "presence", bok, cok)
			continue
		}
		compareStruct(path, b, c, func(field string, bv, cv any) {
			add(k, field, bv, cv)
		})
	}
}

func compareStruct(path string, base, compare any, add func(string, any, any)) {
	bb, _ := json.Marshal(base)
	cb, _ := json.Marshal(compare)
	var bm map[string]any
	var cm map[string]any
	if json.Unmarshal(bb, &bm) != nil || json.Unmarshal(cb, &cm) != nil {
		if !reflect.DeepEqual(base, compare) {
			add("value", base, compare)
		}
		return
	}
	keys := map[string]struct{}{}
	for k := range bm {
		keys[k] = struct{}{}
	}
	for k := range cm {
		keys[k] = struct{}{}
	}
	for k := range keys {
		if !reflect.DeepEqual(bm[k], cm[k]) {
			add(k, bm[k], cm[k])
		}
	}
	_ = path
}

// MarshalReport renders report JSON with stable indentation.
func MarshalReport(report *Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// HasBlockingDiff reports whether any non-allowed diff exists.
func HasBlockingDiff(report *Report) bool {
	if report == nil {
		return false
	}
	for _, c := range report.Cases {
		for _, d := range c.Differences {
			if !d.AllowedDiff {
				return true
			}
		}
	}
	return false
}
