//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
