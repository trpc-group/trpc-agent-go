//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultMaxAuditContentBytes = 16 * 1024
	redactedValue               = "[REDACTED]"
)

var inlineSecretPattern = regexp.MustCompile(
	`(?i)\b(api[_-]?key|authorization|access[_-]?token|refresh[_-]?token|token|secret|password|cookie)\b\s*[:=]\s*[^,;\r\n]+`,
)

var bearerTokenPattern = regexp.MustCompile(`(?i)\bbearer\s+[-A-Za-z0-9._~+/]+=*`)

func sanitizeProfile(source *promptiter.Profile, policy AuditPolicy) *promptiter.Profile {
	result := promptiter.CloneProfile(source)
	if result == nil {
		return nil
	}
	for overrideIndex := range result.Overrides {
		value := &result.Overrides[overrideIndex].Value
		if value.Text != nil {
			text := sanitizeContent(policy, *value.Text)
			value.Text = &text
		}
		for exampleIndex := range value.FewShot {
			for messageIndex := range value.FewShot[exampleIndex].Messages {
				message := &value.FewShot[exampleIndex].Messages[messageIndex]
				message.Content = sanitizeContent(policy, message.Content)
			}
		}
		if value.Model != nil {
			value.Model.Provider = sanitizeContent(policy, value.Model.Provider)
			value.Model.Name = sanitizeContent(policy, value.Model.Name)
			value.Model.Variant = sanitizeContent(policy, value.Model.Variant)
			value.Model.BaseURL = sanitizeModelURL(policy, value.Model.BaseURL)
			if value.Model.APIKey != "" {
				value.Model.APIKey = redactedValue
			}
			for key, headerValue := range value.Model.Headers {
				if sensitiveKey(key) {
					value.Model.Headers[key] = redactedValue
					continue
				}
				value.Model.Headers[key] = sanitizeContent(policy, headerValue)
			}
		}
		for toolIndex := range value.Tools {
			toolRef := &value.Tools[toolIndex]
			toolRef.Description = sanitizeContent(policy, toolRef.Description)
			sanitizeSchema(toolRef.InputSchema, policy)
			sanitizeSchema(toolRef.OutputSchema, policy)
		}
		for skillIndex := range value.Skills {
			value.Skills[skillIndex].Description = sanitizeContent(
				policy, value.Skills[skillIndex].Description,
			)
		}
	}
	return result
}

func sanitizeModelURL(policy AuditPolicy, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return sanitizeContent(policy, value)
	}
	if parsed.User != nil {
		parsed.User = url.User(redactedValue)
	}
	query := parsed.Query()
	for key, values := range query {
		if sensitiveKey(key) {
			query[key] = []string{redactedValue}
			continue
		}
		for index := range values {
			values[index] = sanitizeContent(policy, values[index])
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = sanitizeContent(policy, parsed.Fragment)
	return sanitizeContent(policy, parsed.String())
}

func sanitizeSchema(schema *tool.Schema, policy AuditPolicy) {
	if schema == nil {
		return
	}
	schema.Description = sanitizeContent(policy, schema.Description)
	for name, property := range schema.Properties {
		if sensitiveKey(name) && property != nil {
			property.Default = redactedValue
			if len(property.Enum) > 0 {
				property.Enum = []any{redactedValue}
			}
		}
		sanitizeSchema(property, policy)
	}
	for _, definition := range schema.Defs {
		sanitizeSchema(definition, policy)
	}
	sanitizeSchema(schema.Items, policy)
	schema.AdditionalProperties = sanitizeArbitraryValue(schema.AdditionalProperties, policy)
	schema.Default = sanitizeArbitraryValue(schema.Default, policy)
	for index := range schema.Enum {
		schema.Enum[index] = sanitizeArbitraryValue(schema.Enum[index], policy)
	}
}

func sanitizeStructuredContent(policy AuditPolicy, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return sanitizeContent(policy, value)
	}
	decoded = redactStructuredValue(decoded, "", policy)
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return sanitizeContent(policy, value)
	}
	return sanitizeContent(policy, string(encoded))
}

func sanitizeArbitraryValue(value any, policy AuditPolicy) any {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return unsupportedAuditValue(value)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return unsupportedAuditValue(value)
	}
	return redactStructuredValue(decoded, "", policy)
}

func unsupportedAuditValue(value any) string {
	return fmt.Sprintf("[UNSERIALIZABLE:%T]", value)
}

func redactStructuredValue(value any, key string, policy AuditPolicy) any {
	if sensitiveKey(key) {
		return redactedValue
	}
	switch current := value.(type) {
	case map[string]any:
		for childKey, childValue := range current {
			current[childKey] = redactStructuredValue(childValue, childKey, policy)
		}
		return current
	case []any:
		for index := range current {
			current[index] = redactStructuredValue(current[index], key, policy)
		}
		return current
	case string:
		return sanitizeContent(policy, current)
	default:
		return current
	}
}

func sanitizeContent(policy AuditPolicy, value string) string {
	if value == "" {
		return ""
	}
	value = inlineSecretPattern.ReplaceAllString(value, "$1="+redactedValue)
	value = bearerTokenPattern.ReplaceAllString(value, "Bearer "+redactedValue)
	limit := policy.MaxContentBytes
	if limit <= 0 {
		limit = defaultMaxAuditContentBytes
	}
	if len(value) <= limit {
		return value
	}
	truncated := value[:limit]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "…[TRUNCATED]"
}

func sensitiveKey(value string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
	for _, candidate := range []string{
		"apikey", "authorization", "accesstoken", "refreshtoken",
		"token", "secret", "password", "passwd", "cookie", "setcookie",
	} {
		if normalized == candidate || strings.HasSuffix(normalized, candidate) {
			return true
		}
	}
	return false
}

func sanitizeAttribution(
	source *AttributionResult,
	policy AuditPolicy,
) *AttributionResult {
	if source == nil {
		return nil
	}
	result := *source
	result.CandidateID = sanitizeContent(policy, source.CandidateID)
	result.EvalSetID = sanitizeContent(policy, source.EvalSetID)
	result.CaseID = sanitizeContent(policy, source.CaseID)
	result.Reason = sanitizeContent(policy, source.Reason)
	if len(source.Evidence) > 0 {
		result.Evidence = make([]Evidence, len(source.Evidence))
		for index, evidence := range source.Evidence {
			result.Evidence[index] = Evidence{
				Source: sanitizeContent(policy, evidence.Source),
				Path:   sanitizeContent(policy, evidence.Path),
				Reason: sanitizeContent(policy, evidence.Reason),
			}
		}
	}
	return &result
}

func sanitizeGateDecision(
	source *GateDecision,
	policy AuditPolicy,
) *GateDecision {
	if source == nil {
		return nil
	}
	result := *source
	result.Reasons = sanitizeStrings(source.Reasons, policy)
	result.Warnings = sanitizeStrings(source.Warnings, policy)
	if len(source.Rules) > 0 {
		result.Rules = make([]GateRuleResult, len(source.Rules))
		for index, rule := range source.Rules {
			result.Rules[index] = rule
			result.Rules[index].Rule = sanitizeContent(policy, rule.Rule)
			result.Rules[index].Reason = sanitizeContent(policy, rule.Reason)
			result.Rules[index].Observed = sanitizeArbitraryValue(rule.Observed, policy)
			result.Rules[index].Threshold = sanitizeArbitraryValue(rule.Threshold, policy)
		}
	}
	return &result
}

func sanitizeStrings(source []string, policy AuditPolicy) []string {
	if len(source) == 0 {
		return nil
	}
	result := make([]string, len(source))
	for index, value := range source {
		result[index] = sanitizeContent(policy, value)
	}
	return result
}

func sanitizeMetadata(source map[string]string, policy AuditPolicy) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		if sensitiveKey(key) {
			result[key] = redactedValue
			continue
		}
		result[key] = sanitizeContent(policy, value)
	}
	return result
}

// SanitizeRunResult returns a defensive report copy with secret-like values
// redacted, oversized fields truncated, and raw execution payloads omitted
// unless the run's audit policy explicitly enables them.
func SanitizeRunResult(source *RunResult) (*RunResult, error) {
	if source == nil {
		return nil, errors.New("run result is nil")
	}
	prepared := prepareRunResultForClone(source)
	encoded, err := json.Marshal(prepared)
	if err != nil {
		return nil, fmt.Errorf("clone run result for reporting: %w", err)
	}
	var result RunResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("decode report copy: %w", err)
	}
	policy := AuditPolicy{}
	if result.Spec != nil {
		policy = result.Spec.Audit
		result.Spec.Metadata = sanitizeMetadata(result.Spec.Metadata, policy)
	}
	result.ErrorMessage = sanitizeContent(policy, result.ErrorMessage)
	result.Usage.TelemetrySource = sanitizeContent(policy, result.Usage.TelemetrySource)
	result.Usage.PricingSource = sanitizeContent(policy, result.Usage.PricingSource)
	result.BaselineProfile = sanitizeProfile(result.BaselineProfile, policy)
	sanitizeSnapshot(result.BaselineTrain, policy)
	sanitizeSnapshot(result.BaselineValidation, policy)
	for index := range result.Attributions {
		sanitized := sanitizeAttribution(&result.Attributions[index], policy)
		result.Attributions[index] = *sanitized
	}
	for index := range result.Candidates {
		sanitizeCandidateResult(&result.Candidates[index], policy)
	}
	return &result, nil
}

// prepareRunResultForClone replaces the two extensible value graphs that can
// contain non-JSON Go values before the JSON round-trip creates an owned copy:
// profile schemas and custom Gate observations. The remaining report model is
// composed only of JSON-native fields.
func prepareRunResultForClone(source *RunResult) *RunResult {
	result := *source
	policy := AuditPolicy{}
	if source.Spec != nil {
		policy = source.Spec.Audit
		result.Spec = cloneRunSpec(source.Spec)
	}
	result.BaselineProfile = sanitizeProfile(source.BaselineProfile, policy)
	if len(source.Candidates) > 0 {
		result.Candidates = make([]CandidateResult, len(source.Candidates))
		for index := range source.Candidates {
			result.Candidates[index] = source.Candidates[index]
			result.Candidates[index].Candidate.Profile = sanitizeProfile(
				source.Candidates[index].Candidate.Profile,
				policy,
			)
			result.Candidates[index].Gate = sanitizeGateDecision(
				source.Candidates[index].Gate,
				policy,
			)
		}
	}
	return &result
}

func sanitizeCandidateResult(result *CandidateResult, policy AuditPolicy) {
	if result == nil {
		return
	}
	result.PromptIterReason = sanitizeContent(policy, result.PromptIterReason)
	result.Candidate.Profile = sanitizeProfile(result.Candidate.Profile, policy)
	sanitizeSnapshot(result.Train, policy)
	sanitizeSnapshot(result.Validation, policy)
	sanitizeDeltaReport(result.TrainDelta, policy)
	sanitizeDeltaReport(result.ValidationDelta, policy)
	result.Gate = sanitizeGateDecision(result.Gate, policy)
}

func sanitizeSnapshot(snapshot *EvaluationSnapshot, policy AuditPolicy) {
	if snapshot == nil {
		return
	}
	for caseIndex := range snapshot.Cases {
		caseResult := &snapshot.Cases[caseIndex]
		caseResult.EvalSetID = sanitizeContent(policy, caseResult.EvalSetID)
		caseResult.CaseID = sanitizeContent(policy, caseResult.CaseID)
		if policy.IncludeRawContent {
			caseResult.Input = sanitizeContent(policy, caseResult.Input)
		} else {
			caseResult.Input = ""
		}
		for metricIndex := range caseResult.Metrics {
			metric := &caseResult.Metrics[metricIndex]
			metric.Name = sanitizeContent(policy, metric.Name)
			metric.Reason = sanitizeContent(policy, metric.Reason)
			for rubricIndex := range metric.Rubrics {
				rubric := &metric.Rubrics[rubricIndex]
				rubric.ID = sanitizeContent(policy, rubric.ID)
				rubric.Reason = sanitizeContent(policy, rubric.Reason)
			}
		}
		for runIndex := range caseResult.Runs {
			sanitizeObservation(&caseResult.Runs[runIndex], policy)
		}
	}
}

func sanitizeObservation(observation *Observation, policy AuditPolicy) {
	if observation == nil {
		return
	}
	observation.Route = sanitizeContent(policy, observation.Route)
	observation.ExpectedRoute = sanitizeContent(policy, observation.ExpectedRoute)
	observation.Error = sanitizeContent(policy, observation.Error)
	if policy.IncludeRawContent {
		observation.FinalResponse = sanitizeContent(policy, observation.FinalResponse)
		observation.ExpectedFinalResponse = sanitizeContent(policy, observation.ExpectedFinalResponse)
	} else {
		observation.FinalResponse = ""
		observation.ExpectedFinalResponse = ""
	}
	sanitizeToolObservations(observation.Tools, policy)
	sanitizeToolObservations(observation.ExpectedTools, policy)
	for traceIndex := range observation.Trace {
		step := &observation.Trace[traceIndex]
		step.StepID = sanitizeContent(policy, step.StepID)
		step.NodeID = sanitizeContent(policy, step.NodeID)
		step.Branch = sanitizeContent(policy, step.Branch)
		step.Error = sanitizeContent(policy, step.Error)
		for surfaceIndex := range step.AppliedSurfaceIDs {
			step.AppliedSurfaceIDs[surfaceIndex] = sanitizeContent(
				policy, step.AppliedSurfaceIDs[surfaceIndex],
			)
		}
		if policy.IncludeRawContent {
			step.Input = sanitizeContent(policy, step.Input)
			step.Output = sanitizeContent(policy, step.Output)
		} else {
			step.Input = ""
			step.Output = ""
		}
	}
}

func sanitizeToolObservations(observations []ToolObservation, policy AuditPolicy) {
	for toolIndex := range observations {
		toolObservation := &observations[toolIndex]
		toolObservation.Name = sanitizeContent(policy, toolObservation.Name)
		toolObservation.Error = sanitizeContent(policy, toolObservation.Error)
		if policy.IncludeRawContent {
			toolObservation.Arguments = sanitizeStructuredContent(policy, toolObservation.Arguments)
			toolObservation.Result = sanitizeStructuredContent(policy, toolObservation.Result)
		} else {
			toolObservation.Arguments = ""
			toolObservation.Result = ""
		}
	}
}

func sanitizeDeltaReport(report *DeltaReport, policy AuditPolicy) {
	if report == nil {
		return
	}
	for caseIndex := range report.Cases {
		caseDelta := &report.Cases[caseIndex]
		caseDelta.EvalSetID = sanitizeContent(policy, caseDelta.EvalSetID)
		caseDelta.CaseID = sanitizeContent(policy, caseDelta.CaseID)
		for metricIndex := range caseDelta.Metrics {
			caseDelta.Metrics[metricIndex].MetricName = sanitizeContent(
				policy, caseDelta.Metrics[metricIndex].MetricName,
			)
		}
	}
}
