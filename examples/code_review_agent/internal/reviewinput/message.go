// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewinput

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	hunkTruncationNotice    = "\n...[hunk truncated; inspect work/inputs/change.diff]"
	messageTruncationNotice = "\n...[message budget reached; inspect work/inputs/change.diff]\n"
)

// buildReviewMessage supplies bounded navigation rather than duplicating the
// complete diff in model context. Omitted detail remains available at the
// stable workspace paths declared in the same message.
func buildReviewMessage(kind, mode string, paths []string, parsed parsedInput, limits Limits) string {
	limits = limits.withDefaults()
	var b strings.Builder
	fmt.Fprintf(&b, "Review this code change using the code-review Skill.\n\n")
	fmt.Fprintf(&b, "Input:\n- source: %s\n- mode: %s\n- changed files: %d\n- changed hunks: %d\n- Go packages: %d\n",
		kind, mode, len(parsed.ChangedFiles), len(parsed.ChangedHunks), len(parsed.GoPackages))
	if len(paths) > 0 {
		fmt.Fprintf(&b, "\nReview scope (requested paths):\n")
		pathLimit := min(len(paths), limits.MaxFiles)
		for _, requestedPath := range paths[:pathLimit] {
			fmt.Fprintf(&b, "- %s\n", requestedPath)
		}
		if omitted := len(paths) - pathLimit; omitted > 0 {
			fmt.Fprintf(&b, "- ... %d additional requested paths omitted from this message\n", omitted)
		}
	}
	fmt.Fprintf(&b, "\nWorkspace:\n- complete masked diff: work/inputs/change.diff\n")
	if mode == ReviewModeRepoBacked {
		fmt.Fprintf(&b, "- repository snapshot: work/inputs/repo\n")
	} else {
		fmt.Fprintf(&b, "- repository snapshot: unavailable; do not claim full-file or executable-repo evidence\n")
	}

	fmt.Fprintf(&b, "\nChanged files:\n")
	fileLimit := min(len(parsed.ChangedFiles), limits.MaxFiles)
	for _, file := range parsed.ChangedFiles[:fileLimit] {
		fmt.Fprintf(&b, "- %s: %s, hunks=%d, +%d ~%d -%d, complete_file_available=%t\n",
			file.Path, file.Status, file.HunkCount, file.AddedLines, file.ChangedLines,
			file.DeletedLines, file.HasCompleteContext)
	}
	if omitted := len(parsed.ChangedFiles) - fileLimit; omitted > 0 {
		fmt.Fprintf(&b, "- ... %d additional files omitted from this message\n", omitted)
	}

	if len(parsed.GoPackages) > 0 {
		fmt.Fprintf(&b, "\nGo packages:\n")
		packageLimit := min(len(parsed.GoPackages), limits.MaxFiles)
		for _, pkg := range parsed.GoPackages[:packageLimit] {
			fmt.Fprintf(&b, "- dir=%s package=%s module=%s test_arg=%s package_context_complete=%t\n",
				pkg.Directory, pkg.PackageName, pkg.ModulePath, pkg.SuggestedTestArg, pkg.Complete)
		}
		if omitted := len(parsed.GoPackages) - packageLimit; omitted > 0 {
			fmt.Fprintf(&b, "- ... %d additional packages omitted from this message\n", omitted)
		}
	}

	if len(parsed.SecretSignals) > 0 {
		fmt.Fprintf(&b, "\nMasked secret signals:\n")
		for _, signal := range parsed.SecretSignals {
			fmt.Fprintf(&b, "- %s:%d kind=%s rule=%s confidence=%.2f evidence=%s\n",
				signal.File, signal.Line, signal.Kind, signal.RuleID, signal.Confidence, signal.Evidence)
		}
	}

	fmt.Fprintf(&b, "\nHunk previews:\n")
	fmt.Fprintf(&b, "candidate_lines identify added or modified new-file lines; they are not confirmed findings.\n")
	hunkLimit := min(len(parsed.ChangedHunks), limits.MaxHunks)
	for _, hunk := range parsed.ChangedHunks[:hunkLimit] {
		body := hunk.Body
		if len(body) > limits.MaxHunkBytes {
			body = truncateWithNotice(body, limits.MaxHunkBytes, hunkTruncationNotice)
		}
		fmt.Fprintf(&b, "\n[%s] %s old=%d,%d new=%d,%d candidate_lines=%v\n%s\n",
			hunk.ID, hunk.File, hunk.OldStart, hunk.OldLines, hunk.NewStart, hunk.NewLines,
			hunk.CandidateLines, body)
		if b.Len() >= limits.MaxMessageBytes {
			break
		}
	}
	if omitted := len(parsed.ChangedHunks) - hunkLimit; omitted > 0 {
		fmt.Fprintf(&b, "\n%d additional hunks were omitted from this message.\n", omitted)
	}
	if b.Len() > limits.MaxMessageBytes {
		return truncateWithNotice(b.String(), limits.MaxMessageBytes, messageTruncationNotice)
	}
	if mode == ReviewModeRepoBacked {
		fmt.Fprintf(&b, "\nInspect the complete diff and relevant files in the repository snapshot through workspace_exec before forming conclusions.")
	} else {
		fmt.Fprintf(&b, "\nInspect the complete diff through workspace_exec before forming conclusions.")
	}
	fmt.Fprintf(&b, " Base every finding on changed hunks or observed tool output, then submit the result through submit_review_results.\n")
	return truncateWithNotice(b.String(), limits.MaxMessageBytes, messageTruncationNotice)
}

// buildInputSummary produces the bounded Review Store projection. It includes
// enough metadata for task queries while keeping hunk bodies in Artifact
// Service, where large diffs do not inflate the task row.
func buildInputSummary(kind, mode string, paths []string, parsed parsedInput, limits Limits) inputSummary {
	limits = limits.withDefaults()
	files := parsed.ChangedFiles
	if len(files) > limits.MaxFiles {
		files = files[:limits.MaxFiles]
	}
	signals := parsed.SecretSignals
	if len(signals) > limits.MaxFiles {
		signals = signals[:limits.MaxFiles]
	}
	candidateCount := 0
	for _, hunk := range parsed.ChangedHunks {
		candidateCount += len(hunk.CandidateLines)
	}
	return inputSummary{
		InputKind:      kind,
		ReviewMode:     mode,
		RequestedPaths: append([]string(nil), paths...),
		ChangedFiles:   append([]ChangedFile(nil), files...),
		GoPackages:     append([]GoPackage(nil), parsed.GoPackages[:min(len(parsed.GoPackages), limits.MaxFiles)]...),
		HunkCount:      len(parsed.ChangedHunks),
		CandidateLines: candidateCount,
		SecretSignals:  append([]SecretSignal(nil), signals...),
		Redactions:     parsed.Redactions,
	}
}

// truncateWithNotice enforces a byte budget without splitting a UTF-8 code
// point. The notice is included in the same budget so callers can rely on the
// configured limit even when the input contains multibyte source text.
func truncateWithNotice(value string, maxBytes int, notice string) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	if len(notice) >= maxBytes {
		return truncateUTF8(notice, maxBytes)
	}
	return truncateUTF8(value, maxBytes-len(notice)) + notice
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
