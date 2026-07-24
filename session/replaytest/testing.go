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
	"context"
	"encoding/json"
	"testing"
	"time"
)

// RunPairT is the testing.T flavor of RunPair: it runs the full pair
// comparison and fails the test on any non-allowed diff, printing every
// located difference. It always returns the report for further assertions.
func RunPairT(t *testing.T, cases []Case, ref, cand Target, opts ...PairOption) *Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rep, err := RunPair(ctx, cases, ref, cand, opts...)
	if err != nil {
		t.Fatalf("run pair %s vs %s: %v", ref.Name(), cand.Name(), err)
	}
	for _, cr := range rep.Cases {
		if cr.Status != StatusFail {
			continue
		}
		for _, df := range cr.Diffs {
			b, _ := json.Marshal(df)
			t.Errorf("case %s diff: %s", cr.Case, b)
		}
	}
	return rep
}
