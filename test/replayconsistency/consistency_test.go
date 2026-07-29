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
	"testing"
)

// TestReplayConsistency replays every public case against the lightweight
// backends and fails on any difference that is not declared allowed.
func TestReplayConsistency(t *testing.T) {
	backends := LightweightBackends()
	for _, sc := range Scenarios() {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			res, err := Run(context.Background(), sc, backends)
			if err != nil {
				t.Fatalf("run %q: %v", sc.Name, err)
			}
			for _, u := range res.Unsupported {
				t.Logf("unsupported: backend=%s feature=%s reason=%s", u.Backend, u.Feature, u.Reason)
			}
			for _, d := range res.Divergences {
				switch {
				case d.AllowedDiff:
					t.Logf("allowed difference:\n%s", d)
				case d.Known:
					t.Logf("known divergence:\n%s", d)
				default:
					t.Errorf("unexpected divergence:\n%s", d)
				}
			}
		})
	}
}
