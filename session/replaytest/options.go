// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

// ComparisonMode selects how backend snapshots are compared.
type ComparisonMode string

const (
	// ComparisonReference compares every backend against one reference backend.
	ComparisonReference ComparisonMode = "reference"
	// ComparisonAllPairs compares every backend pair.
	ComparisonAllPairs ComparisonMode = "all_pairs"
)

// HarnessOpts configures harness execution.
type HarnessOpts struct {
	ComparisonMode   ComparisonMode
	ReferenceBackend string
	Mode             string // lightweight | integration
}

// DefaultHarnessOpts returns default harness options.
func DefaultHarnessOpts() HarnessOpts {
	return HarnessOpts{
		ComparisonMode:   ComparisonReference,
		ReferenceBackend: "inmemory",
		Mode:             "lightweight",
	}
}
