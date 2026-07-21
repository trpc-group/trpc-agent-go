//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCovercore_LoadPolicyMissingFile covers the read error path.
func TestCovercore_LoadPolicyMissingFile(t *testing.T) {
	_, err := LoadPolicy(t.TempDir() + "/does-not-exist.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "load policy")
}

// TestCovercore_LoadPolicyInvalidYAML covers the decode error path.
func TestCovercore_LoadPolicyInvalidYAML(t *testing.T) {
	_, err := LoadPolicyFromBytes([]byte("version: [unclosed"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode policy")
}

// TestCovercore_LoadPolicyUnknownDecisionAggregates covers the error
// aggregation in normalizePolicyDecisions.
func TestCovercore_LoadPolicyUnknownDecisionAggregates(t *testing.T) {
	_, err := LoadPolicyFromBytes([]byte(`
version: 1
decision_threshold:
  high: explode
rules:
  network: {enabled: true, action: detonate}
`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "decision_threshold.high")
	require.Contains(t, err.Error(), "rules.network.action")
}

// TestCovercore_LoadPolicyNormalizesRuleActionAlias verifies the
// needs_human_review alias inside a rule action.
func TestCovercore_LoadPolicyNormalizesRuleActionAlias(t *testing.T) {
	policy, err := LoadPolicyFromBytes([]byte(`
version: 1
rules:
  resource_abuse: {enabled: true, action: needs_human_review}
`))
	require.NoError(t, err)
	require.Equal(t, DecisionAsk, policy.Rules.ResourceAbuse.Action)
}

// TestCovercore_PolicyValidateFieldErrors covers the remaining Validate
// branches.
func TestCovercore_PolicyValidateFieldErrors(t *testing.T) {
	base := DefaultPolicy()

	p := base
	p.Version = 2
	require.ErrorContains(t, p.Validate(), "version must be 1")

	p = base
	p.MaxOutputSize = -1
	require.ErrorContains(t, p.Validate(), "max_output_size")

	p = base
	p.MaxSleepSeconds = -1
	require.ErrorContains(t, p.Validate(), "max_sleep_seconds")

	p = base
	p.DecisionThreshold.High = ""
	require.ErrorContains(t, p.Validate(), "is empty")

	p = base
	p.DecisionThreshold.Low = Decision("explode")
	require.ErrorContains(t, p.Validate(), "unknown decision")

	p = base
	p.DecisionThreshold.Medium = DecisionNeedsHumanReview
	require.ErrorContains(t, p.Validate(), "reserved for input only")

	p = base
	p.DecisionThreshold.Critical = DecisionAsk
	require.ErrorContains(t, p.Validate(), "critical must be deny")

	p = base
	p.Rules.SecretLeak.Action = DecisionAllow
	require.ErrorContains(t, p.Validate(), "secret_leak")
}

// TestCovercore_ValidateDomain covers every domain-validation branch.
func TestCovercore_ValidateDomain(t *testing.T) {
	require.NoError(t, validateDomain("example.com"))
	require.NoError(t, validateDomain("*.example.com"))

	require.ErrorContains(t, validateDomain("   "), "empty domain")
	require.ErrorContains(t, validateDomain("bad domain.com"), "spaces")
	require.ErrorContains(t, validateDomain("*.*.example.com"), "too many wildcards")
	require.ErrorContains(t, validateDomain("*."), "invalid wildcard placement")
	require.ErrorContains(t, validateDomain("*.*"), "too many wildcards")
	require.ErrorContains(t, validateDomain("exam*le.com"), "must use *.example.com form")
}

// TestCovercore_NormalizeDecision covers all decision-normalization cases.
func TestCovercore_NormalizeDecision(t *testing.T) {
	d, err := normalizeDecision(DecisionNeedsHumanReview)
	require.NoError(t, err)
	require.Equal(t, DecisionAsk, d)

	for _, valid := range []Decision{DecisionAllow, DecisionDeny, DecisionAsk} {
		d, err = normalizeDecision(valid)
		require.NoError(t, err)
		require.Equal(t, valid, d)
	}

	_, err = normalizeDecision("")
	require.Error(t, err)

	_, err = normalizeDecision(Decision("bogus"))
	require.Error(t, err)
}

// TestCovercore_ThresholdFor covers every risk level plus the default.
func TestCovercore_ThresholdFor(t *testing.T) {
	p := DefaultPolicy()
	require.Equal(t, p.DecisionThreshold.Critical, p.thresholdFor(RiskCritical))
	require.Equal(t, p.DecisionThreshold.High, p.thresholdFor(RiskHigh))
	require.Equal(t, p.DecisionThreshold.Medium, p.thresholdFor(RiskMedium))
	require.Equal(t, p.DecisionThreshold.Low, p.thresholdFor(RiskLow))
	require.Equal(t, DecisionDeny, p.thresholdFor(RiskLevel("bogus")))
}

// TestCovercore_LoadPolicyDurationOverride verifies a YAML duration field
// round-trips through the partial-override loader.
func TestCovercore_LoadPolicyDurationOverride(t *testing.T) {
	policy, err := LoadPolicyFromBytes([]byte("version: 1\nmax_timeout: 45s\n"))
	require.NoError(t, err)
	require.Equal(t, 45*time.Second, policy.MaxTimeout)
	// Omitted fields keep their defaults.
	require.Equal(t, int64(1<<20), policy.MaxOutputSize)
}
