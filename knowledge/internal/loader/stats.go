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

// Package loader contains internal helper utilities for concurrent knowledge
// base loading. These helpers are internal-only and not part of the public
// API.
package loader

import "trpc.group/trpc-go/trpc-agent-go/log"

// This file provides Stats, a small helper to collect document-size statistics
// in a concurrency-safe manner when used in combination with the Aggregator.

// Stats tracks statistics for document sizes during a load run.
// This is a near-copy of the original sizeStats type but is placed in an
// internal package so it can be updated safely by a single goroutine that
// owns all state.
type Stats struct {
	TotalDocs  int
	TotalSize  int
	MinSize    int
	MaxSize    int
	bucketCnts []int
}

// NewStats returns a Stats initialised for the provided buckets.
func NewStats(buckets []int) *Stats {
	// Initialise with max-int for MinSize.
	maxInt := int(^uint(0) >> 1)
	return &Stats{
		MinSize:    maxInt,
		bucketCnts: make([]int, len(buckets)+1),
	}
}

// Add records the size of a document.
func (s *Stats) Add(size int, buckets []int) {
	s.TotalDocs++
	s.TotalSize += size
	if size < s.MinSize {
		s.MinSize = size
	}
	if size > s.MaxSize {
		s.MaxSize = size
	}

	placed := false
	for i, upper := range buckets {
		if size < upper {
			s.bucketCnts[i]++
			placed = true
			break
		}
	}
	if !placed {
		s.bucketCnts[len(s.bucketCnts)-1]++
	}
}

// Avg returns the average document size.
func (s *Stats) Avg() float64 {
	if s.TotalDocs == 0 {
		return 0
	}
	return float64(s.TotalSize) / float64(s.TotalDocs)
}

// Log outputs the collected statistics.
func (s *Stats) Log(buckets []int) {
	log.Infof(
		"Document statistics - total: %d, avg: %.1f B, min: %d B, max: %d B",
		s.TotalDocs, s.Avg(), s.MinSize, s.MaxSize,
	)

	lower := 0
	for i, upper := range buckets {
		if s.bucketCnts[i] == 0 {
			lower = upper
			continue
		}
		log.Infof("  [%d, %d): %d document(s)", lower, upper,
			s.bucketCnts[i])
		lower = upper
	}

	lastCnt := s.bucketCnts[len(s.bucketCnts)-1]
	if lastCnt > 0 {
		log.Infof("  [>= %d]: %d document(s)", buckets[len(buckets)-1],
			lastCnt)
	}
}
