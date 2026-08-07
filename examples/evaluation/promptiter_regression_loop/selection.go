//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

type candidateSelection struct {
	summary CandidateSummary
	delta   DeltaSummary
	gate    GateDecision
	ok      bool
}

func (s *candidateSelection) consider(
	summary CandidateSummary,
	delta DeltaSummary,
	gate GateDecision,
) bool {
	if gate.Accepted {
		s.summary = summary
		s.delta = delta
		s.gate = gate
		s.ok = true
		return true
	}
	if !s.ok || (!s.gate.Accepted && delta.CandidateScore > s.delta.CandidateScore) {
		s.summary = summary
		s.delta = delta
		s.gate = gate
		s.ok = true
	}
	return false
}
