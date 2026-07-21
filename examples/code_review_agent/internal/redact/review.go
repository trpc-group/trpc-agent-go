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
	out := make([]review.DiffFile, len(in))
	for fileIndex, file := range in {
		out[fileIndex] = file
		out[fileIndex].OldPath = Text(file.OldPath).Text
		out[fileIndex].NewPath = Text(file.NewPath).Text
		out[fileIndex].PackageDir = Text(file.PackageDir).Text
		out[fileIndex].Hunks = make([]review.DiffHunk, len(file.Hunks))
		for hunkIndex, hunk := range file.Hunks {
			out[fileIndex].Hunks[hunkIndex] = hunk
			out[fileIndex].Hunks[hunkIndex].Lines = make([]review.DiffLine, len(hunk.Lines))
			for lineIndex, line := range hunk.Lines {
				out[fileIndex].Hunks[hunkIndex].Lines[lineIndex] = line
				out[fileIndex].Hunks[hunkIndex].Lines[lineIndex].Content = Text(line.Content).Text
			}
		}
	}
	return out
}
