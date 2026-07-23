//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

// ComparisonMode selects how backend snapshots are compared.
type ComparisonMode string

const (
	// ComparisonRef compares every backend against one reference backend.
	ComparisonRef ComparisonMode = "reference"
	// ComparisonPairs compares every backend pair.
	ComparisonPairs ComparisonMode = "all_pairs"
)

// HarnessOpts configures replay harness execution.
type HarnessOpts struct {
	// ComparisonMode selects reference or all-pairs backend comparison.
	ComparisonMode ComparisonMode
	// ReferenceBackend names the backend used as the reference in reference mode.
	ReferenceBackend string
}

// DefaultHarnessOpts returns the default replay harness options.
func DefaultHarnessOpts() HarnessOpts {
	return HarnessOpts{
		ComparisonMode:   ComparisonRef,
		ReferenceBackend: "inmemory",
	}
}
