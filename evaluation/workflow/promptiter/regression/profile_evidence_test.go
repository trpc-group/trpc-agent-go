//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestProfileOnlyChangesTargetValidatesProfileScope(t *testing.T) {
	baseline := testProfile("target", "before", "other", "stable")
	tests := []struct {
		name      string
		candidate *promptiter.Profile
		valid     bool
	}{
		{name: "nil candidate", candidate: nil},
		{name: "different structure", candidate: &promptiter.Profile{StructureID: "other"}},
		{name: "target omitted", candidate: testProfile("other", "stable")},
		{name: "non target changed", candidate: testProfile("target", "after", "other", "changed")},
		{name: "non target added", candidate: testProfile("target", "after", "other", "stable", "added", "value")},
		{name: "target only changed", candidate: testProfile("target", "after", "other", "stable"), valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			valid, reason := profileOnlyChangesTarget(baseline, test.candidate, "target")
			assert.Equal(t, test.valid, valid)
			assert.NotEmpty(t, reason)
		})
	}
}

func TestOverrideJSONAndInitialProfileHandleInvalidAndFallbackSources(t *testing.T) {
	_, err := overrideJSON(&promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{}}})
	require.ErrorContains(t, err, "surface id is empty")
	_, err = overrideJSON(&promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{SurfaceID: "target"}, {SurfaceID: "target"}}})
	require.ErrorContains(t, err, "duplicate profile override")

	assert.Nil(t, initialProfile(nil))
	initial := testProfile("target", "initial")
	fromInitial := initialProfile(&engine.RunResult{InitialProfile: initial})
	require.NotNil(t, fromInitial)
	assert.NotSame(t, initial, fromInitial)

	fromRound := initialProfile(&engine.RunResult{Rounds: []engine.RoundResult{{InputProfile: testProfile("target", "round")}}})
	assert.Equal(t, "round", *fromRound.Overrides[0].Value.Text)
	fromAccepted := initialProfile(&engine.RunResult{AcceptedProfile: testProfile("target", "accepted")})
	assert.Equal(t, "accepted", *fromAccepted.Overrides[0].Value.Text)
}

func TestRoundCandidateTrainUsesFutureFallbackWhenTerminalEvidenceIsAbsent(t *testing.T) {
	snapshot := &EvaluationSnapshot{EvalSetID: "train"}
	actual, err := roundCandidateTrain(engine.RoundResult{Round: 1}, []trainEvidence{{round: 2, snapshot: snapshot}}, nil, AuditPolicy{}, nil, 1)
	require.NoError(t, err)
	assert.Same(t, snapshot, actual)

	actual, err = roundCandidateTrain(engine.RoundResult{Round: 2}, []trainEvidence{{round: 2, snapshot: snapshot}}, nil, AuditPolicy{}, nil, 1)
	require.NoError(t, err)
	assert.Nil(t, actual)
}

func testProfile(values ...string) *promptiter.Profile {
	profile := &promptiter.Profile{StructureID: "structure"}
	for index := 0; index < len(values); index += 2 {
		text := values[index+1]
		profile.Overrides = append(profile.Overrides, promptiter.SurfaceOverride{
			SurfaceID: values[index], Value: astructure.SurfaceValue{Text: &text},
		})
	}
	return profile
}
