//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package eval

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEvaluatePublicFixturesMeetsAcceptanceThresholds(t *testing.T) {
	result, err := EvaluatePublicFixtures(context.Background(), filepath.Join("..", "..", "testdata", "fixtures"), nil)
	if err != nil {
		t.Fatalf("EvaluatePublicFixtures() error = %v", err)
	}
	if result.FixtureCount != 9 {
		t.Fatalf("FixtureCount = %d, want 9", result.FixtureCount)
	}
	if result.HighRiskRecall < 0.80 {
		t.Fatalf("HighRiskRecall = %.2f, want >= 0.80", result.HighRiskRecall)
	}
	if result.FalsePositiveRate > 0.15 {
		t.Fatalf("FalsePositiveRate = %.2f, want <= 0.15", result.FalsePositiveRate)
	}
	if result.RedactionRecall < 0.95 {
		t.Fatalf("RedactionRecall = %.2f, want >= 0.95", result.RedactionRecall)
	}
}
