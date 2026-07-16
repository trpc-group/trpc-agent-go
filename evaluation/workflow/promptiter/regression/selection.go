//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// ErrNoSelectedCandidate means the Regression release gate did not select a
// publishable candidate.
var ErrNoSelectedCandidate = errors.New("regression release gate selected no candidate")

// SelectedProfile returns an owned copy of the only profile authorized for
// write-back by the Regression release gate. It never falls back to the
// PromptIter Engine's AcceptedProfile.
func (r *RunResult) SelectedProfile() (*promptiter.Profile, error) {
	if r == nil || r.Decision != DecisionAccepted || r.SelectedCandidateID == "" {
		return nil, ErrNoSelectedCandidate
	}
	var selected *promptiter.Profile
	for index := range r.Candidates {
		candidate := &r.Candidates[index].Candidate
		if candidate.ID != r.SelectedCandidateID {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("duplicate selected candidate id %q", r.SelectedCandidateID)
		}
		if candidate.Profile == nil {
			return nil, fmt.Errorf("selected candidate %q has no profile", r.SelectedCandidateID)
		}
		selected = candidate.Profile
	}
	if selected == nil {
		return nil, fmt.Errorf("selected candidate %q is absent", r.SelectedCandidateID)
	}
	return promptiter.CloneProfile(selected), nil
}
