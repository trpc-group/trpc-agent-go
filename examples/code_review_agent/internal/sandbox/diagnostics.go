//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"bufio"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	maxDiagnosticBytes = 1 << 20
	maxDiagnostics     = 1000
)

var diagnosticPattern = regexp.MustCompile(`^([^:\r\n]+\.go):([0-9]+)(?::[0-9]+)?:\s*(.+)$`)

// ParseDiagnostics converts bounded checker output on added lines into
// untrusted candidates for the shared findings normalizer.
func ParseDiagnostics(
	taskID string,
	check Check,
	diff input.Diff,
	output string,
) []findings.Candidate {
	if len(output) > maxDiagnosticBytes {
		output = output[:maxDiagnosticBytes]
		for !utf8.ValidString(output) {
			output = output[:len(output)-1]
		}
	}
	locations := diagnosticLocations(diff)
	var candidates []findings.Candidate
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 1024), 64<<10)
	for scanner.Scan() && len(candidates) < maxDiagnostics {
		match := diagnosticPattern.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		file := strings.TrimPrefix(match[1], "./")
		if file == "" || path.IsAbs(file) || path.Clean(file) != file ||
			strings.ContainsAny(file, "\\\x00") {
			continue
		}
		line, err := strconv.Atoi(match[2])
		if err != nil || line < 1 {
			continue
		}
		layers := locations[diagnosticLocation{file: file, line: line}]
		for _, layer := range layers {
			if len(candidates) >= maxDiagnostics {
				break
			}
			candidates = append(candidates, findings.Candidate{
				SchemaVersion:  review.SchemaVersion,
				TaskID:         taskID,
				Severity:       review.SeverityMedium,
				Category:       "tool-diagnostic",
				Layer:          layer,
				File:           file,
				Line:           line,
				SemanticAnchor: "diagnostic-" + string(check),
				Title:          checkTitle(check),
				Evidence:       redact.String(match[3]),
				Recommendation: "address the reported diagnostic",
				Confidence:     review.ConfidenceHigh,
				Source:         review.SourceTool,
				RuleID:         "tool/" + string(check) + "/v1",
				Disposition:    review.DispositionFinding,
			})
		}
	}
	return candidates
}

type diagnosticLocation struct {
	file string
	line int
}

func diagnosticLocations(diff input.Diff) map[diagnosticLocation][]review.ChangeLayer {
	sets := make(map[diagnosticLocation]map[review.ChangeLayer]struct{})
	for _, file := range diff.Files {
		if file.NewPath == "" || file.Binary {
			continue
		}
		layer := file.Layer
		if layer == "" {
			layer = review.ChangeLayerUnified
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind != input.LineAdded || line.NewNumber == nil {
					continue
				}
				key := diagnosticLocation{file: file.NewPath, line: *line.NewNumber}
				if sets[key] == nil {
					sets[key] = make(map[review.ChangeLayer]struct{})
				}
				sets[key][layer] = struct{}{}
			}
		}
	}
	result := make(map[diagnosticLocation][]review.ChangeLayer, len(sets))
	for key, layers := range sets {
		for layer := range layers {
			result[key] = append(result[key], layer)
		}
		sort.Slice(result[key], func(left, right int) bool {
			return result[key][left] < result[key][right]
		})
	}
	return result
}

func checkTitle(check Check) string {
	switch check {
	case CheckGoTest:
		return "go test reported an issue"
	case CheckGoVet:
		return "go vet reported an issue"
	case CheckStaticcheck:
		return "staticcheck reported an issue"
	default:
		return "review checker reported an issue"
	}
}
