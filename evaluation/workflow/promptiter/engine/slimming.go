//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package engine

// RunResultSlimming controls which PromptIter run result fields are omitted
// from persisted or returned copies.
//
// The zero value keeps every field so existing callers keep the full result.
type RunResultSlimming struct {
	// OmitStructure removes the exported agent structure snapshot.
	OmitStructure bool
	// OmitEvaluationCases removes per-case evaluation details from all phases.
	OmitEvaluationCases bool
	// OmitBackward removes per-case backward results from each round.
	OmitBackward bool
	// OmitAggregation removes aggregated surface gradients from each round.
	OmitAggregation bool
	// OmitPatches removes optimizer patch proposals from each round.
	OmitPatches bool
	// OmitProfiles removes input, output, and accepted profiles.
	OmitProfiles bool
	// OmitLosses removes round loss details.
	OmitLosses bool
}
