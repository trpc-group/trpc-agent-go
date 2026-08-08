//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report finalizes deterministic review report artifacts.
package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	maxFindings      = 5000
	maxSandboxRuns   = 1000
	maxDecisions     = 10000
	maxArtifacts     = 1000
	maxChangedFiles  = 10000
	maxStringBytes   = 64 << 10
	maxAggregateText = 16 << 20
)

// Document contains canonical JSON and its deterministic Markdown projection.
type Document struct {
	Report   review.Report
	JSON     []byte
	Markdown []byte
}

// Finalize sanitizes and validates report, then emits canonical JSON and
// Markdown. Report artifacts are evidence artifacts; publication records are
// returned separately by Publish.
func Finalize(source review.Report) (Document, error) {
	if err := validateBounds(source); err != nil {
		return Document{}, err
	}
	if err := validateIdentityAndScope(source); err != nil {
		return Document{}, err
	}
	sanitized, err := sanitizeReport(source)
	if err != nil {
		return Document{}, err
	}
	canonicalizeReport(&sanitized)
	if err := sanitized.Validate(); err != nil {
		return Document{}, fmt.Errorf("finalize report: %w", redact.Error(err))
	}
	jsonBytes, err := encodeCanonicalJSON(sanitized)
	if err != nil {
		return Document{}, fmt.Errorf("finalize report json: %w", redact.Error(err))
	}
	markdown := renderMarkdown(sanitized)
	return Document{Report: sanitized, JSON: jsonBytes, Markdown: markdown}, nil
}

func sanitizeReport(source review.Report) (review.Report, error) {
	result := source
	result.Task.TerminalError = redact.String(source.Task.TerminalError)
	result.Input.ChangedFiles = append([]string(nil), source.Input.ChangedFiles...)

	result.SandboxRuns = append([]review.SandboxRun(nil), source.SandboxRuns...)
	for index := range result.SandboxRuns {
		result.SandboxRuns[index].Stdout = redact.String(result.SandboxRuns[index].Stdout)
		result.SandboxRuns[index].Stderr = redact.String(result.SandboxRuns[index].Stderr)
	}
	result.GovernanceDecisions = append(
		[]review.GovernanceDecision(nil), source.GovernanceDecisions...)
	for index := range result.GovernanceDecisions {
		result.GovernanceDecisions[index].Reason = redact.String(
			result.GovernanceDecisions[index].Reason)
		result.GovernanceDecisions[index].Rule = redact.String(
			result.GovernanceDecisions[index].Rule)
	}
	result.Findings = append([]review.Finding(nil), source.Findings...)
	for index := range result.Findings {
		finding := &result.Findings[index]
		finding.Category = redact.String(finding.Category)
		finding.Title = redact.String(finding.Title)
		finding.Evidence = redact.String(finding.Evidence)
		finding.Recommendation = redact.String(finding.Recommendation)
	}
	result.Artifacts = append([]review.ArtifactRecord(nil), source.Artifacts...)
	result.Metrics.SeverityCounts = cloneSeverityCounts(source.Metrics.SeverityCounts)
	result.Metrics.ErrorTypeCounts = make(map[string]int, len(source.Metrics.ErrorTypeCounts))
	for key, count := range source.Metrics.ErrorTypeCounts {
		result.Metrics.ErrorTypeCounts[redact.String(key)] += count
	}
	result.Conclusion = redact.String(source.Conclusion)
	return result, nil
}

func validateBounds(value review.Report) error {
	switch {
	case len(value.Findings) > maxFindings:
		return errors.New("finalize report: finding limit exceeded")
	case len(value.SandboxRuns) > maxSandboxRuns:
		return errors.New("finalize report: sandbox run limit exceeded")
	case len(value.GovernanceDecisions) > maxDecisions:
		return errors.New("finalize report: governance decision limit exceeded")
	case len(value.Artifacts) > maxArtifacts:
		return errors.New("finalize report: artifact limit exceeded")
	case len(value.Input.ChangedFiles) > maxChangedFiles:
		return errors.New("finalize report: changed file limit exceeded")
	}
	total := 0
	add := func(name, text string) error {
		if len(text) > maxStringBytes {
			return fmt.Errorf("finalize report: %s string limit exceeded", name)
		}
		total += len(text)
		if total > maxAggregateText {
			return errors.New("finalize report: aggregate text limit exceeded")
		}
		return nil
	}
	stringsToCheck := []struct{ name, value string }{
		{"task id", value.Task.ID}, {"task error", value.Task.TerminalError},
		{"input task id", value.Input.TaskID}, {"input digest", value.Input.Digest},
		{"conclusion", value.Conclusion},
	}
	for _, item := range stringsToCheck {
		if err := add(item.name, item.value); err != nil {
			return err
		}
	}
	for _, changedFile := range value.Input.ChangedFiles {
		if err := add("changed file", changedFile); err != nil {
			return err
		}
	}
	for _, run := range value.SandboxRuns {
		for _, item := range []string{run.TaskID, run.Command, run.Stdout, run.Stderr} {
			if err := add("sandbox run", item); err != nil {
				return err
			}
		}
	}
	for _, decision := range value.GovernanceDecisions {
		for _, item := range []string{decision.TaskID, decision.DecisionID, decision.Tool,
			decision.Reason, decision.Rule} {
			if err := add("governance decision", item); err != nil {
				return err
			}
		}
	}
	for _, finding := range value.Findings {
		for _, item := range []string{finding.TaskID, finding.Category, finding.File,
			finding.SemanticAnchor, finding.Title, finding.Evidence, finding.Recommendation,
			finding.RuleID, finding.Fingerprint} {
			if err := add("finding", item); err != nil {
				return err
			}
		}
	}
	for _, artifact := range value.Artifacts {
		for _, item := range []string{artifact.TaskID, artifact.Name, artifact.Reference,
			artifact.Digest, artifact.MIMEType} {
			if err := add("artifact", item); err != nil {
				return err
			}
		}
	}
	for key := range value.Metrics.ErrorTypeCounts {
		if err := add("error type", key); err != nil {
			return err
		}
	}
	return nil
}

func validateIdentityAndScope(value review.Report) error {
	identities := []string{
		value.SchemaVersion, value.Task.SchemaVersion, value.Task.ID,
		string(value.Task.Status), string(value.Task.Phase), string(value.Task.Mode),
		value.Input.SchemaVersion, value.Input.TaskID, string(value.Input.Source), value.Input.Digest,
	}
	changed := make(map[string]struct{}, len(value.Input.ChangedFiles))
	for _, file := range value.Input.ChangedFiles {
		if !validReportPath(file) {
			return errors.New("finalize report: invalid changed file identity")
		}
		if _, exists := changed[file]; exists {
			return errors.New("finalize report: duplicate changed file identity")
		}
		changed[file] = struct{}{}
		identities = append(identities, file)
	}
	fingerprints := make(map[string]struct{}, len(value.Findings))
	for _, finding := range value.Findings {
		identities = append(identities, finding.SchemaVersion, finding.TaskID,
			string(finding.Severity), string(finding.Layer), finding.File,
			finding.SemanticAnchor, string(finding.Confidence), string(finding.Source),
			finding.RuleID, finding.Fingerprint, string(finding.Disposition))
		if _, exists := changed[finding.File]; !exists {
			return errors.New("finalize report: finding file is absent from changed files")
		}
		if _, exists := fingerprints[finding.Fingerprint]; exists {
			return errors.New("finalize report: duplicate fingerprint")
		}
		fingerprints[finding.Fingerprint] = struct{}{}
	}
	for _, run := range value.SandboxRuns {
		identities = append(identities, run.SchemaVersion, run.TaskID, run.Command,
			string(run.Status))
	}
	for _, decision := range value.GovernanceDecisions {
		identities = append(identities, decision.SchemaVersion, decision.TaskID,
			decision.DecisionID, string(decision.Kind), decision.Tool, string(decision.Action))
	}
	for _, artifact := range value.Artifacts {
		identities = append(identities, artifact.SchemaVersion, artifact.TaskID,
			artifact.Name, artifact.Reference, artifact.Digest, artifact.MIMEType)
	}
	for _, identity := range identities {
		if redact.String(identity) != identity {
			return errors.New("finalize report: secret-bearing identity")
		}
	}
	return nil
}

func validReportPath(value string) bool {
	return value != "" && !path.IsAbs(value) && path.Clean(value) == value &&
		value != "." && !strings.ContainsAny(value, "\\\x00")
}

func cloneSeverityCounts(source map[review.Severity]int) map[review.Severity]int {
	if source == nil {
		return nil
	}
	result := make(map[review.Severity]int, len(source))
	for key, count := range source {
		result[key] = count
	}
	return result
}

func canonicalizeReport(value *review.Report) {
	value.Input.ChangedFiles = append([]string(nil), value.Input.ChangedFiles...)
	sort.Strings(value.Input.ChangedFiles)
	value.Findings = append([]review.Finding(nil), value.Findings...)
	sort.Slice(value.Findings, func(left, right int) bool {
		l, r := value.Findings[left], value.Findings[right]
		if l.File != r.File {
			return l.File < r.File
		}
		if l.Layer != r.Layer {
			return l.Layer < r.Layer
		}
		if l.Line != r.Line {
			return l.Line < r.Line
		}
		if l.Severity != r.Severity {
			return severityRank(l.Severity) > severityRank(r.Severity)
		}
		if l.RuleID != r.RuleID {
			return l.RuleID < r.RuleID
		}
		return l.Fingerprint < r.Fingerprint
	})
}

func encodeCanonicalJSON(value review.Report) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func severityRank(severity review.Severity) int {
	switch severity {
	case review.SeverityCritical:
		return 5
	case review.SeverityHigh:
		return 4
	case review.SeverityMedium:
		return 3
	case review.SeverityLow:
		return 2
	case review.SeverityInfo:
		return 1
	default:
		return 0
	}
}
