//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var windowsReservedRunIDNames = map[string]struct{}{
	"AUX": {}, "CON": {}, "NUL": {}, "PRN": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// Validate checks the audit contract before PromptIter evidence is consumed.
func (s *RunSpec) Validate() error {
	if s == nil {
		return errors.New("run spec is nil")
	}
	required := []struct {
		value string
		name  string
	}{
		{s.RunID, "run id"},
		{s.TargetSurfaceID, "target surface id"},
		{s.InputFingerprint, "input fingerprint"},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if err := ValidateRunID(s.RunID); err != nil {
		return err
	}
	if s.Runtime.NumRuns <= 0 {
		return errors.New("num runs must be greater than zero")
	}
	if !finite(s.Gate.MinValidationGain) || !finite(s.Gate.MaxCaseRegression) ||
		!finite(s.Gate.MaxGeneralizationGap) || !finite(s.Gate.MaxScoreStdDev) ||
		s.Gate.MinValidationGain < 0 || s.Gate.MaxCaseRegression < 0 ||
		s.Gate.MaxGeneralizationGap < 0 || s.Gate.MaxScoreStdDev < 0 {
		return errors.New("gate limits must be finite and non-negative")
	}
	if s.Budget.MaxCalls < 0 || s.Budget.MaxTokens < 0 ||
		!finite(s.Budget.MaxEstimatedCost) || s.Budget.MaxEstimatedCost < 0 ||
		s.Budget.MaxPromptIterLatency < 0 {
		return errors.New("budget limits must be finite and non-negative")
	}
	if s.Audit.MaxContentBytes < 0 {
		return errors.New("audit max content bytes must be non-negative")
	}
	if len(s.MetricPolicies) == 0 {
		return errors.New("metric policies are required")
	}
	for name, policy := range s.MetricPolicies {
		if err := validateMetricPolicy(name, policy); err != nil {
			return err
		}
	}
	seenCases := make(map[string]struct{}, len(s.CriticalCaseIDs))
	for _, caseID := range s.CriticalCaseIDs {
		caseID = strings.TrimSpace(caseID)
		if caseID == "" {
			return errors.New("critical case id must not be empty")
		}
		if _, exists := seenCases[caseID]; exists {
			return fmt.Errorf("duplicate critical case id %q", caseID)
		}
		seenCases[caseID] = struct{}{}
	}
	return nil
}

// ValidateRunID checks that a run identifier is safe for report content and a
// single immutable artifact directory.
func ValidateRunID(value string) error {
	if !runIDPattern.MatchString(value) {
		return errors.New("run id must be 1-128 characters using letters, digits, dot, underscore, or hyphen")
	}
	if strings.HasSuffix(value, ".") {
		return errors.New("run id must not end with a dot")
	}
	name, _, _ := strings.Cut(value, ".")
	if _, reserved := windowsReservedRunIDNames[strings.ToUpper(name)]; reserved {
		return errors.New("run id must not use a Windows reserved device name")
	}
	return nil
}

func validateMetricPolicy(name string, policy MetricPolicy) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("metric policy name must not be empty")
	}
	if !finite(policy.Weight) || policy.Weight <= 0 {
		return fmt.Errorf("metric %q weight must be finite and greater than zero", name)
	}
	if !finite(policy.Floor) || policy.Floor < 0 || policy.Floor > 1 {
		return fmt.Errorf("metric %q floor must be between zero and one", name)
	}
	return nil
}

func profileHash(profile *promptiter.Profile) (string, error) {
	if profile == nil {
		return "", errors.New("profile is nil")
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("encode profile: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
