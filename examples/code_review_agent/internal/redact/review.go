//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// DiffFiles returns a deep redacted copy of parsed diff files.
func DiffFiles(in []review.DiffFile) []review.DiffFile {
	if in == nil {
		return nil
	}
	out := make([]review.DiffFile, len(in))
	for fileIndex, file := range in {
		out[fileIndex] = file
		out[fileIndex].OldPath = Text(file.OldPath).Text
		out[fileIndex].NewPath = Text(file.NewPath).Text
		out[fileIndex].PackageDir = Text(file.PackageDir).Text
		if file.Hunks != nil {
			out[fileIndex].Hunks = redactDiffHunks(file.Hunks)
		}
	}
	return out
}

type lineSpan struct {
	hunk  int
	line  int
	start int
	end   int
}

func redactDiffLines(in []review.DiffLine) []review.DiffLine {
	if in == nil {
		return nil
	}
	return redactDiffHunks([]review.DiffHunk{{Lines: in}})[0].Lines
}

func redactDiffHunks(in []review.DiffHunk) []review.DiffHunk {
	if in == nil {
		return nil
	}
	out := make([]review.DiffHunk, len(in))
	for hunkIndex, hunk := range in {
		out[hunkIndex] = hunk
		if hunk.Lines == nil {
			continue
		}
		out[hunkIndex].Lines = make([]review.DiffLine, len(hunk.Lines))
		for lineIndex, line := range hunk.Lines {
			out[hunkIndex].Lines[lineIndex] = line
			out[hunkIndex].Lines[lineIndex].Content = Text(line.Content).Text
		}
	}
	joined, spans := joinDiffHunkContents(in)
	if joined == "" {
		return out
	}
	for _, match := range privateKeyPattern.FindAllStringIndex(joined, -1) {
		for _, span := range spans {
			if span.start < match[1] && span.end > match[0] {
				out[span.hunk].Lines[span.line].Content = Placeholder
			}
		}
	}
	return out
}

func redactMultilinePrivateKeys(out []review.DiffLine, original []review.DiffLine) {
	redacted := redactDiffLines(original)
	copy(out, redacted)
}

func joinDiffLineContents(lines []review.DiffLine) (string, []lineSpan) {
	return joinDiffHunkContents([]review.DiffHunk{{Lines: lines}})
}

func joinDiffHunkContents(hunks []review.DiffHunk) (string, []lineSpan) {
	var joined strings.Builder
	lineCount := 0
	for _, hunk := range hunks {
		lineCount += len(hunk.Lines)
	}
	spans := make([]lineSpan, 0, lineCount)
	for hunkIndex, hunk := range hunks {
		for lineIndex, line := range hunk.Lines {
			if joined.Len() > 0 {
				joined.WriteByte('\n')
			}
			start := joined.Len()
			joined.WriteString(line.Content)
			spans = append(spans, lineSpan{hunk: hunkIndex, line: lineIndex, start: start, end: joined.Len()})
		}
	}
	return joined.String(), spans
}
