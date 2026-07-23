//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"

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
			out[fileIndex].Hunks = make([]review.DiffHunk, len(file.Hunks))
		}
		for hunkIndex, hunk := range file.Hunks {
			out[fileIndex].Hunks[hunkIndex] = hunk
			out[fileIndex].Hunks[hunkIndex].Lines = redactDiffLines(hunk.Lines)
		}
	}
	return out
}

type lineSpan struct {
	start int
	end   int
}

func redactDiffLines(in []review.DiffLine) []review.DiffLine {
	if in == nil {
		return nil
	}
	out := make([]review.DiffLine, len(in))
	for index, line := range in {
		out[index] = line
		out[index].Content = Text(line.Content).Text
	}
	redactMultilinePrivateKeys(out, in)
	return out
}

func redactMultilinePrivateKeys(out []review.DiffLine, original []review.DiffLine) {
	joined, spans := joinDiffLineContents(original)
	if joined == "" {
		return
	}
	for _, match := range privateKeyPattern.FindAllStringIndex(joined, -1) {
		for index, span := range spans {
			if span.start < match[1] && span.end > match[0] {
				out[index].Content = Placeholder
			}
		}
	}
}

func joinDiffLineContents(lines []review.DiffLine) (string, []lineSpan) {
	var joined string
	spans := make([]lineSpan, len(lines))
	for index, line := range lines {
		if index > 0 {
			joined += "\n"
		}
		start := len(joined)
		joined += line.Content
		spans[index] = lineSpan{start: start, end: len(joined)}
	}
	return joined, spans
}
