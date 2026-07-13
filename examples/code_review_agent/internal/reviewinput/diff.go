// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewinput

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	sourcediff "github.com/sourcegraph/go-diff/diff"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
)

// parseReviewDiff creates the single scoped view used by the Agent message,
// Artifact Service, and optional repo patch application. Applying the path
// filter before rendering and masking is important: otherwise files outside
// the requested review scope could leak through the complete diff artifact.
func parseReviewDiff(
	raw []byte,
	requestedPaths []string,
	repoBacked bool,
	sanitizer *redact.Sanitizer,
) (parsed parsedInput, maskedDiff, scopedDiff []byte, err error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return parsedInput{}, nil, nil, errors.New("review diff is empty")
	}
	if sanitizer == nil {
		return parsedInput{}, nil, nil, errors.New("review diff parser requires a sanitizer")
	}

	fileDiffs, err := sourcediff.ParseMultiFileDiff(raw)
	if err != nil {
		return parsedInput{}, nil, nil, fmt.Errorf("parse unified diff: %w", err)
	}
	if len(fileDiffs) == 0 {
		return parsedInput{}, nil, nil, errors.New("input does not contain a unified diff")
	}
	filter := make(map[string]struct{}, len(requestedPaths))
	for _, p := range requestedPaths {
		filter[p] = struct{}{}
	}
	matchedPaths := make(map[string]struct{}, len(requestedPaths))

	selectedDiffs := make([]*sourcediff.FileDiff, 0, len(fileDiffs))
	for _, fd := range fileDiffs {
		oldPath, newPath, filePath, err := diffFilePaths(fd)
		if err != nil {
			return parsedInput{}, nil, nil, err
		}
		if !pathScopeIncludes(filter, matchedPaths, oldPath, filePath) {
			continue
		}
		selectedDiffs = append(selectedDiffs, fd)
		changedFile, hunks, signals := parseFileDiff(
			fd, oldPath, newPath, filePath, repoBacked, sanitizer,
		)
		parsed.ChangedFiles = append(parsed.ChangedFiles, changedFile)
		parsed.ChangedHunks = append(parsed.ChangedHunks, hunks...)
		parsed.SecretSignals = append(parsed.SecretSignals, signals...)
	}
	if len(parsed.ChangedFiles) == 0 {
		return parsedInput{}, nil, nil, errors.New("no changed files remain after applying the requested path scope")
	}
	if len(matchedPaths) != len(filter) {
		missing := make([]string, 0, len(filter)-len(matchedPaths))
		for requestedPath := range filter {
			if _, ok := matchedPaths[requestedPath]; !ok {
				missing = append(missing, requestedPath)
			}
		}
		sort.Strings(missing)
		return parsedInput{}, nil, nil, fmt.Errorf("requested review paths are absent from the diff: %s", strings.Join(missing, ", "))
	}

	parsed.Redactions = summarizeRedactions(parsed.SecretSignals)
	scopedDiff = raw
	if len(requestedPaths) > 0 {
		scopedDiff, err = sourcediff.PrintMultiFileDiff(selectedDiffs)
		if err != nil {
			return parsedInput{}, nil, nil, fmt.Errorf("render path-scoped diff: %w", err)
		}
	}
	maskedDiff = sanitizer.DetectAndMask(scopedDiff).Masked
	sort.Slice(parsed.ChangedFiles, func(i, j int) bool {
		return parsed.ChangedFiles[i].Path < parsed.ChangedFiles[j].Path
	})
	sort.Slice(parsed.SecretSignals, func(i, j int) bool {
		if parsed.SecretSignals[i].File != parsed.SecretSignals[j].File {
			return parsed.SecretSignals[i].File < parsed.SecretSignals[j].File
		}
		return parsed.SecretSignals[i].Line < parsed.SecretSignals[j].Line
	})
	return parsed, maskedDiff, scopedDiff, nil
}

// diffFilePaths resolves both sides of a rename or deletion and chooses the
// post-change path as the primary review identity whenever it exists.
func diffFilePaths(fd *sourcediff.FileDiff) (oldPath, newPath, filePath string, err error) {
	oldPath, err = normalizeDiffPath(fd.OrigName)
	if err != nil {
		return "", "", "", err
	}
	newPath, err = normalizeDiffPath(fd.NewName)
	if err != nil {
		return "", "", "", err
	}
	filePath = newPath
	if filePath == "" {
		filePath = oldPath
	}
	if filePath == "" {
		return "", "", "", errors.New("diff file has neither an old nor a new path")
	}
	return oldPath, newPath, filePath, nil
}

// pathScopeIncludes accepts either side of a rename and records every matched
// requested path. This lets the caller reject partially matched scopes rather
// than silently reviewing fewer files than requested.
func pathScopeIncludes(filter, matched map[string]struct{}, oldPath, filePath string) bool {
	if len(filter) == 0 {
		return true
	}
	included := false
	for _, candidate := range []string{filePath, oldPath} {
		if _, ok := filter[candidate]; ok {
			matched[candidate] = struct{}{}
			included = true
		}
	}
	return included
}

// parseFileDiff extracts one file's metadata and masked hunks after the file
// has passed path-scope validation.
func parseFileDiff(
	fd *sourcediff.FileDiff,
	oldPath, newPath, filePath string,
	repoBacked bool,
	sanitizer *redact.Sanitizer,
) (changedFile ChangedFile, hunks []ChangedHunk, signals []SecretSignal) {
	stat := fd.Stat()
	changedFile = ChangedFile{
		Path:               filePath,
		OldPath:            oldPath,
		Status:             diffStatus(oldPath, newPath),
		Language:           languageForPath(filePath),
		IsGo:               strings.HasSuffix(filePath, ".go"),
		IsTest:             strings.HasSuffix(filePath, "_test.go"),
		HasCompleteContext: repoBacked,
		HunkCount:          len(fd.Hunks),
		AddedLines:         int(stat.Added),
		ChangedLines:       int(stat.Changed),
		DeletedLines:       int(stat.Deleted),
		Binary:             len(fd.Hunks) == 0 && hasBinaryHeader(fd.Extended),
	}
	if changedFile.OldPath == changedFile.Path {
		changedFile.OldPath = ""
	}

	hunks = make([]ChangedHunk, 0, len(fd.Hunks))
	for hunkIndex, h := range fd.Hunks {
		maskedBody := sanitizer.DetectAndMask(h.Body)
		candidateLines, hunkSignals := inspectAddedLines(
			filePath, int(h.NewStartLine), h.Body, maskedBody.Signals,
		)
		hunks = append(hunks, ChangedHunk{
			ID:             fmt.Sprintf("%s:%d:%d", filePath, h.NewStartLine, hunkIndex+1),
			File:           filePath,
			OldStart:       int(h.OrigStartLine),
			OldLines:       int(h.OrigLines),
			NewStart:       int(h.NewStartLine),
			NewLines:       int(h.NewLines),
			Section:        h.Section,
			Body:           string(maskedBody.Masked),
			CandidateLines: candidateLines,
		})
		signals = append(signals, hunkSignals...)
	}
	return changedFile, hunks, signals
}

// normalizeDiffPath converts Git's a/ and b/ names to review-root-relative
// paths. Rejecting absolute and parent paths here protects both later snapshot
// access and every file name exposed to the Agent.
func normalizeDiffPath(name string) (normalized string, err error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "/dev/null" {
		return "", nil
	}
	name = strings.Trim(name, `"`)
	name = strings.TrimPrefix(name, "a/")
	name = strings.TrimPrefix(name, "b/")
	name = strings.ReplaceAll(name, "\\", "/")
	cleaned := path.Clean(name)
	if cleaned == "." || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("diff path %q escapes the review root", name)
	}
	return cleaned, nil
}

func diffStatus(oldPath, newPath string) string {
	switch {
	case oldPath == "":
		return "added"
	case newPath == "":
		return "deleted"
	case oldPath != newPath:
		return "renamed"
	default:
		return "modified"
	}
}

func languageForPath(filePath string) string {
	switch {
	case strings.HasSuffix(filePath, ".go"):
		return "go"
	case strings.HasSuffix(filePath, ".sql"):
		return "sql"
	case strings.HasSuffix(filePath, ".md"):
		return "markdown"
	case strings.HasSuffix(filePath, ".yaml"), strings.HasSuffix(filePath, ".yml"):
		return "yaml"
	case strings.HasSuffix(filePath, ".json"):
		return "json"
	default:
		return ""
	}
}

func hasBinaryHeader(headers []string) bool {
	for _, header := range headers {
		if strings.Contains(header, "Binary files ") || strings.HasPrefix(header, "GIT binary patch") {
			return true
		}
	}
	return false
}

// inspectAddedLines maps hunk-body positions back to new-file line numbers.
// Secret detection is performed over the whole hunk before this function so a
// multiline credential can be recognized; only signals that begin on added
// lines are promoted as newly introduced review risks.
func inspectAddedLines(
	filePath string,
	newStart int,
	body []byte,
	detected []redact.Signal,
) (candidateLines []int, signals []SecretSignal) {
	newLine := newStart
	bodyLine := 0
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		bodyLine++
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case '+':
			candidateLines = append(candidateLines, newLine)
			// Signals are detected over the complete hunk rather than one line
			// at a time. This is required for multiline credentials such as PEM
			// private keys. Only signals beginning on an added line become
			// review findings; deleted and context secrets are not new leaks.
			for _, signal := range detected {
				if signal.Line != bodyLine {
					continue
				}
				signals = append(signals, SecretSignal{
					Kind:        signal.Kind,
					RuleID:      signal.RuleID,
					File:        filePath,
					Line:        newLine,
					Evidence:    signal.Evidence,
					Confidence:  signal.Confidence,
					Fingerprint: signal.Fingerprint,
				})
			}
			newLine++
		case ' ':
			newLine++
		case '-':
			// Deleted lines do not consume a line number in the new file.
		case '\\':
			// "No newline at end of file" is metadata, not source content.
		default:
			// The parser already validates hunk bodies. Keeping this branch
			// explicit makes line-number behavior easy to audit.
		}
	}
	return candidateLines, signals
}

func summarizeRedactions(signals []SecretSignal) RedactionSummary {
	summary := RedactionSummary{Count: len(signals)}
	if len(signals) == 0 {
		return summary
	}
	summary.ByKind = make(map[string]int)
	for _, signal := range signals {
		summary.ByKind[signal.Kind]++
	}
	return summary
}
