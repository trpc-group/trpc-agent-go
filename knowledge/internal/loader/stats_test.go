//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package loader

import "testing"

func TestStatsAddAndAvg(t *testing.T) {
	buckets := []int{256, 512, 1024}
	s := NewStats(buckets)

	s.Add(100, buckets)  // bucket 0
	s.Add(300, buckets)  // bucket 1
	s.Add(1500, buckets) // last bucket

	if s.TotalDocs != 3 {
		t.Errorf("expected 3 docs, got %d", s.TotalDocs)
	}

	if s.MinSize != 100 {
		t.Errorf("expected min 100, got %d", s.MinSize)
	}

	if s.MaxSize != 1500 {
		t.Errorf("expected max 1500, got %d", s.MaxSize)
	}

	expectedAvg := float64(100+300+1500) / 3
	if s.Avg() != expectedAvg {
		t.Errorf("expected avg %.2f, got %.2f", expectedAvg, s.Avg())
	}

	// bucket counts assertions
	if s.bucketCnts[0] != 1 || s.bucketCnts[1] != 1 {
		t.Errorf("bucket counts incorrect: %+v", s.bucketCnts)
	}
	if s.bucketCnts[len(s.bucketCnts)-1] != 1 {
		t.Errorf("last bucket count incorrect: %+v", s.bucketCnts)
	}
}

// TestStats_AvgZero tests Avg function with zero documents.
func TestStats_AvgZero(t *testing.T) {
	buckets := []int{100, 200}
	s := NewStats(buckets)

	// Avg should return 0 when there are no documents
	if avg := s.Avg(); avg != 0 {
		t.Errorf("expected avg 0 for empty stats, got %.2f", avg)
	}
}

// TestStats_Log tests Log function to ensure it doesn't panic.
func TestStats_Log(t *testing.T) {
	buckets := []int{256, 512, 1024}
	s := NewStats(buckets)

	// Add some data
	s.Add(100, buckets)
	s.Add(300, buckets)
	s.Add(600, buckets)

	// Log should not panic (we can't easily test log output, but at least ensure no crash)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Log() panicked: %v", r)
		}
	}()

	s.Log(buckets)
}

// TestStats_LogEmpty tests Log function with empty stats.
func TestStats_LogEmpty(t *testing.T) {
	buckets := []int{100, 200}
	s := NewStats(buckets)

	// Log should not panic even with no data
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Log() panicked with empty stats: %v", r)
		}
	}()

	s.Log(buckets)
}
