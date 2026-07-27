//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sanitize removes secrets from the complete review data model.
package sanitize

import (
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// Report returns a deep-copy of report with every caller-controlled string
// redacted. The copy prevents reporting and persistence from mutating the
// in-memory review result.
func Report(in review.ReviewReport) review.ReviewReport {
	out := in
	out.Task = Task(in.Task)
	out.Files = Files(in.Files)
	out.Findings = Findings(in.Findings)
	out.Warnings = Findings(in.Warnings)
	out.NeedsHumanReview = Findings(in.NeedsHumanReview)
	out.SandboxRuns = Runs(in.SandboxRuns)
	out.PermissionDecisions = PermissionDecisions(in.PermissionDecisions)
	out.FilterDecisions = FilterDecisions(in.FilterDecisions)
	out.Artifacts = Artifacts(in.Artifacts)
	out.Summary = text(in.Summary)
	return out
}

// Task returns a redacted task copy.
func Task(in review.ReviewTask) review.ReviewTask {
	in.ID = text(in.ID)
	in.Status = text(in.Status)
	in.InputType = text(in.InputType)
	in.InputSummary = text(in.InputSummary)
	in.RepoPath = text(in.RepoPath)
	in.Error = text(in.Error)
	return in
}

// Files returns a deep-copy of changed files with all strings redacted.
func Files(in []review.ChangedFile) []review.ChangedFile {
	out := make([]review.ChangedFile, len(in))
	for i, file := range in {
		out[i] = file
		out[i].OldPath = text(file.OldPath)
		out[i].NewPath = text(file.NewPath)
		out[i].Language = text(file.Language)
		out[i].PackageName = text(file.PackageName)
		out[i].Hunks = make([]review.Hunk, len(file.Hunks))
		for j, hunk := range file.Hunks {
			out[i].Hunks[j] = hunk
			out[i].Hunks[j].Header = text(hunk.Header)
			out[i].Hunks[j].Lines = make([]review.DiffLine, len(hunk.Lines))
			for k, line := range hunk.Lines {
				out[i].Hunks[j].Lines[k] = line
				out[i].Hunks[j].Lines[k].Kind = text(line.Kind)
				out[i].Hunks[j].Lines[k].Content = text(line.Content)
			}
		}
	}
	return out
}

// Findings returns redacted finding copies.
func Findings(in []review.Finding) []review.Finding {
	out := make([]review.Finding, len(in))
	copy(out, in)
	for i := range out {
		out[i].Severity = text(out[i].Severity)
		out[i].Category = text(out[i].Category)
		out[i].File = text(out[i].File)
		out[i].Title = text(out[i].Title)
		out[i].Evidence = text(out[i].Evidence)
		out[i].Recommendation = text(out[i].Recommendation)
		out[i].Source = text(out[i].Source)
		out[i].RuleID = text(out[i].RuleID)
	}
	return out
}

// Runs returns redacted sandbox run copies.
func Runs(in []review.SandboxRun) []review.SandboxRun {
	out := make([]review.SandboxRun, len(in))
	copy(out, in)
	for i := range out {
		out[i].Command = text(out[i].Command)
		out[i].Status = text(out[i].Status)
		out[i].StdoutExcerpt = text(out[i].StdoutExcerpt)
		out[i].StderrExcerpt = text(out[i].StderrExcerpt)
		out[i].Error = text(out[i].Error)
		out[i].FailureKind = text(out[i].FailureKind)
	}
	return out
}

// PermissionDecisions returns redacted permission decision copies.
func PermissionDecisions(in []review.PermissionDecision) []review.PermissionDecision {
	out := make([]review.PermissionDecision, len(in))
	copy(out, in)
	for i := range out {
		out[i].Command = text(out[i].Command)
		out[i].Decision = text(out[i].Decision)
		out[i].Reason = text(out[i].Reason)
	}
	return out
}

// FilterDecisions returns redacted filter decision copies.
func FilterDecisions(in []review.FilterDecision) []review.FilterDecision {
	out := make([]review.FilterDecision, len(in))
	copy(out, in)
	for i := range out {
		out[i].RuleID = text(out[i].RuleID)
		out[i].File = text(out[i].File)
		out[i].Source = text(out[i].Source)
		out[i].Stage = text(out[i].Stage)
		out[i].Decision = text(out[i].Decision)
		out[i].Reason = text(out[i].Reason)
	}
	return out
}

// Artifacts returns redacted artifact copies.
func Artifacts(in []review.Artifact) []review.Artifact {
	out := make([]review.Artifact, len(in))
	copy(out, in)
	for i := range out {
		out[i].Kind = text(out[i].Kind)
		out[i].Path = text(out[i].Path)
		out[i].SHA256 = text(out[i].SHA256)
	}
	return out
}

func text(value string) string { return redaction.RedactText(value) }
