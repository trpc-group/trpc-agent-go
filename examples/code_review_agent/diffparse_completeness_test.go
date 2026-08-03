//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffParserRejectsIncompleteFileRecords(t *testing.T) {
	tests := []struct {
		name        string
		diff        string
		wantWarning string
	}{
		{
			name: "truncated before first hunk at eof",
			diff: strings.Join([]string{
				"diff --git a/review.go b/review.go",
				"--- a/review.go",
				"+++ b/review.go",
			}, "\n"),
			wantWarning: "text file change is missing a hunk",
		},
		{
			name: "truncated before next file",
			diff: strings.Join([]string{
				"diff --git a/first.go b/first.go",
				"--- a/first.go",
				"+++ b/first.go",
				"diff --git a/second.go b/second.go",
				"--- a/second.go",
				"+++ b/second.go",
				"@@ -1 +1 @@",
				"-package old",
				"+package current",
			}, "\n"),
			wantWarning: "text file change is missing a hunk",
		},
		{
			name: "one path marker",
			diff: strings.Join([]string{
				"diff --git a/review.go b/review.go",
				"--- a/review.go",
			}, "\n"),
			wantWarning: "text file change is missing a hunk",
		},
		{
			name: "malformed first hunk",
			diff: strings.Join([]string{
				"diff --git a/review.go b/review.go",
				"--- a/review.go",
				"+++ b/review.go",
				"@@ malformed @@",
			}, "\n"),
			wantWarning: "malformed hunk header",
		},
		{
			name: "modified rename without hunk",
			diff: strings.Join([]string{
				"diff --git a/old.go b/new.go",
				"similarity index 80%",
				"rename from old.go",
				"rename to new.go",
			}, "\n"),
			wantWarning: "text file change is missing a hunk",
		},
		{
			name: "modified copy without hunk",
			diff: strings.Join([]string{
				"diff --git a/source.go b/copy.go",
				"similarity index 80%",
				"copy from source.go",
				"copy to copy.go",
			}, "\n"),
			wantWarning: "text file change is missing a hunk",
		},
		{
			name: "incomplete copy metadata",
			diff: strings.Join([]string{
				"diff --git a/source.go b/copy.go",
				"similarity index 100%",
				"copy from source.go",
			}, "\n"),
			wantWarning: "copy metadata must include both copy from and copy to",
		},
		{
			name: "unsafe copy path",
			diff: strings.Join([]string{
				"diff --git a/source.go b/copy.go",
				"similarity index 100%",
				"copy from ../source.go",
				"copy to copy.go",
			}, "\n"),
			wantWarning: "malformed copy from path",
		},
		{
			name: "single mode metadata",
			diff: strings.Join([]string{
				"diff --git a/review.go b/review.go",
				"old mode 100644",
			}, "\n"),
			wantWarning: "mode-only change must include both old mode and new mode",
		},
		{
			name: "malformed mode metadata",
			diff: strings.Join([]string{
				"diff --git a/review.go b/review.go",
				"old mode invalid",
				"new mode 100755",
			}, "\n"),
			wantWarning: "malformed old mode metadata",
		},
		{
			name: "conflicting mode metadata",
			diff: strings.Join([]string{
				"diff --git a/review.go b/review.go",
				"old mode 100644",
				"old mode 100755",
				"new mode 100755",
			}, "\n"),
			wantWarning: "conflicting old mode metadata",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := parseUnifiedDiff([]byte(test.diff))
			if !parseWarningsContain(parsed.Warnings, test.wantWarning) {
				t.Fatalf("warnings = %+v, want %q", parsed.Warnings, test.wantWarning)
			}
		})
	}
}

func TestDiffParserAcceptsCompleteZeroHunkForms(t *testing.T) {
	tests := []struct {
		name string
		diff string
	}{
		{
			name: "regular mode only",
			diff: "diff --git a/review.go b/review.go\nold mode 100644\nnew mode 100755\n",
		},
		{
			name: "empty new file",
			diff: "diff --git a/empty.go b/empty.go\nnew file mode 100644\nindex 0000000..e69de29\n",
		},
		{
			name: "empty deleted file",
			diff: "diff --git a/empty.go b/empty.go\ndeleted file mode 100644\nindex e69de29..0000000\n",
		},
		{
			name: "pure rename",
			diff: strings.Join([]string{
				"diff --git a/old.go b/new.go",
				"similarity index 100%",
				"rename from old.go",
				"rename to new.go",
			}, "\n"),
		},
		{
			name: "pure copy",
			diff: strings.Join([]string{
				"diff --git a/source.go b/copy.go",
				"similarity index 100%",
				"copy from source.go",
				"copy to copy.go",
			}, "\n"),
		},
		{
			name: "binary rename",
			diff: strings.Join([]string{
				"diff --git a/image.bin b/renamed.bin",
				"similarity index 80%",
				"rename from image.bin",
				"rename to renamed.bin",
				"Binary files a/image.bin and b/renamed.bin differ",
			}, "\n"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := parseUnifiedDiff([]byte(test.diff))
			if len(parsed.Warnings) != 0 {
				t.Fatalf("warnings = %+v, want none", parsed.Warnings)
			}
		})
	}
}

func TestDiffFileTruncatedBeforeFirstHunkRequiresHumanReview(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/review.go b/review.go",
		"--- a/review.go",
		"+++ b/review.go",
	}, "\n")
	diffPath := filepath.Join(t.TempDir(), "truncated.diff")
	mustWriteFile(t, diffPath, diff)

	code, stdout, stderr := runForTest(t, []string{
		"--diff-file", diffPath,
		"--dry-run",
	}, nil, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var summary reviewSummary
	mustUnmarshalSummary(t, stdout, &summary)
	if summary.ChangedFiles != 1 || summary.Hunks != 0 || summary.ParseWarnings == 0 ||
		summary.Conclusion != reviewConclusionNeedsHumanReview || !summary.NeedsHumanReview {
		t.Fatalf("summary = %+v, want truncated input to require human review", summary)
	}
	report := readReportFromSummary(t, summary)
	found := false
	for _, message := range report.Parse.WarningMessages {
		if strings.Contains(message, "text file change is missing a hunk") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("parse warnings = %+v, want missing-hunk warning", report.Parse.WarningMessages)
	}
}
