//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDiffTracksFilesHunksAndLines(t *testing.T) {
	diff, err := parseFixture(t, "ordinary.patch")
	require.NoError(t, err)
	require.Len(t, diff.Files, 2)

	alpha := diff.Files[0]
	require.Equal(t, "alpha.go", alpha.OldPath)
	require.Equal(t, "alpha.go", alpha.NewPath)
	require.Equal(t, ChangeModified, alpha.Change)
	require.False(t, alpha.Binary)
	require.Len(t, alpha.Hunks, 2)
	require.Equal(t, Hunk{OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 5, Lines: []Line{
		{Kind: LineContext, Text: "package alpha", OldNumber: intPtr(1), NewNumber: intPtr(1)},
		{Kind: LineDeleted, Text: "old", OldNumber: intPtr(2)},
		{Kind: LineAdded, Text: "new", NewNumber: intPtr(2)},
		{Kind: LineContext, Text: "context", OldNumber: intPtr(3), NewNumber: intPtr(3)},
		{Kind: LineContext, Text: "tail", OldNumber: intPtr(4), NewNumber: intPtr(4)},
		{Kind: LineAdded, Text: "added", NewNumber: intPtr(5)},
	}}, alpha.Hunks[0])
	require.Equal(t, Hunk{OldStart: 10, OldLines: 2, NewStart: 11, NewLines: 2, Lines: []Line{
		{Kind: LineDeleted, Text: "removed", OldNumber: intPtr(10)},
		{Kind: LineAdded, Text: "replacement", NewNumber: intPtr(11)},
		{Kind: LineContext, Text: "unchanged", OldNumber: intPtr(11), NewNumber: intPtr(12)},
	}}, alpha.Hunks[1])

	beta := diff.Files[1]
	require.Equal(t, "beta.go", beta.NewPath)
	require.Equal(t, Hunk{OldStart: 3, OldLines: 1, NewStart: 3, NewLines: 1, Lines: []Line{
		{Kind: LineDeleted, Text: "before", OldNumber: intPtr(3)},
		{Kind: LineAdded, Text: "after", NewNumber: intPtr(3)},
	}}, beta.Hunks[0])
}

func TestParseDiffClassifiesChangesAndBinaryFiles(t *testing.T) {
	diff, err := parseFixture(t, "changes.patch")
	require.NoError(t, err)
	require.Len(t, diff.Files, 5)

	require.Equal(t, []File{
		{OldPath: "", NewPath: "new.txt", Change: ChangeAdded, Hunks: diff.Files[0].Hunks},
		{OldPath: "gone.txt", NewPath: "", Change: ChangeDeleted, Hunks: diff.Files[1].Hunks},
		{OldPath: "old name.txt", NewPath: "new name.txt", Change: ChangeRenamed},
		{OldPath: "source.txt", NewPath: "copy.txt", Change: ChangeCopied},
		{OldPath: "", NewPath: "image.png", Change: ChangeAdded, Binary: true},
	}, diff.Files)
	require.Equal(t, 1, *diff.Files[0].Hunks[0].Lines[0].NewNumber)
	require.Nil(t, diff.Files[0].Hunks[0].Lines[0].OldNumber)
	require.Equal(t, 1, *diff.Files[1].Hunks[0].Lines[0].OldNumber)
	require.Nil(t, diff.Files[1].Hunks[0].Lines[0].NewNumber)
}

func TestParseDiffPreservesUnprefixedRenameAndCopyPaths(t *testing.T) {
	tests := []struct {
		name       string
		metadata   string
		wantChange ChangeKind
	}{
		{
			name:       "rename",
			metadata:   "similarity index 100%\nrename from a/old.txt\nrename to b/new.txt\n",
			wantChange: ChangeRenamed,
		},
		{
			name:       "copy",
			metadata:   "similarity index 100%\ncopy from a/old.txt\ncopy to b/new.txt\n",
			wantChange: ChangeCopied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git a/a/old.txt b/b/new.txt\n" + tt.metadata
			diff, err := Parse(strings.NewReader(patch))
			require.NoError(t, err)
			require.Equal(t, "a/old.txt", diff.Files[0].OldPath)
			require.Equal(t, "b/new.txt", diff.Files[0].NewPath)
			require.Equal(t, tt.wantChange, diff.Files[0].Change)
		})
	}
}

func TestParseDiffDecodesGitCQuotedPaths(t *testing.T) {
	const (
		quotedOld = `"a/\344\270\255\346\226\207 old\t\"name\".txt"`
		quotedNew = `"b/\344\270\255\346\226\207 new\t\"name\".txt"`
		oldPath   = "中文 old\t\"name\".txt"
		newPath   = "中文 new\t\"name\".txt"
	)

	tests := []struct {
		name  string
		patch string
	}{
		{
			name: "diff header and file markers",
			patch: "diff --git " + quotedOld + " " + quotedNew + "\n" +
				"--- " + quotedOld + "\n" +
				"+++ " + quotedNew + "\n" +
				"@@ -1 +1 @@\n-old\n+new\n",
		},
		{
			name: "rename metadata",
			patch: "diff --git " + quotedOld + " " + quotedNew + "\n" +
				"similarity index 100%\n" +
				`rename from "\344\270\255\346\226\207 old\t\"name\".txt"` + "\n" +
				`rename to "\344\270\255\346\226\207 new\t\"name\".txt"` + "\n",
		},
		{
			name: "copy metadata",
			patch: "diff --git " + quotedOld + " " + quotedNew + "\n" +
				"similarity index 100%\n" +
				`copy from "\344\270\255\346\226\207 old\t\"name\".txt"` + "\n" +
				`copy to "\344\270\255\346\226\207 new\t\"name\".txt"` + "\n",
		},
		{
			name: "binary files summary",
			patch: "diff --git " + quotedOld + " " + quotedNew + "\n" +
				"index 1111111..2222222 100644\n" +
				"Binary files " + quotedOld + " and " + quotedNew + " differ\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, err := Parse(strings.NewReader(tt.patch))
			require.NoError(t, err)
			require.Equal(t, oldPath, diff.Files[0].OldPath)
			require.Equal(t, newPath, diff.Files[0].NewPath)
		})
	}
}

func TestParseDiffAcceptsMixedQuotedAndUnquotedHeaderPaths(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		oldPath string
		newPath string
	}{
		{
			name:    "quoted new path",
			header:  `a/old name.txt "b/new\tname.txt"`,
			oldPath: "old name.txt",
			newPath: "new\tname.txt",
		},
		{
			name:    "quoted old path",
			header:  `"a/old\tname.txt" b/new name.txt`,
			oldPath: "old\tname.txt",
			newPath: "new name.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git " + tt.header + "\n" +
				"--- a/old name.txt\n+++ b/new name.txt\n" +
				"@@ -1 +1 @@\n-old\n+new\n"
			if strings.Contains(tt.oldPath, "\t") {
				patch = "diff --git " + tt.header + "\n" +
					`--- "a/old\tname.txt"` + "\n+++ b/new name.txt\n" +
					"@@ -1 +1 @@\n-old\n+new\n"
			}
			if strings.Contains(tt.newPath, "\t") {
				patch = "diff --git " + tt.header + "\n--- a/old name.txt\n" +
					`+++ "b/new\tname.txt"` + "\n" +
					"@@ -1 +1 @@\n-old\n+new\n"
			}
			diff, err := Parse(strings.NewReader(patch))
			require.NoError(t, err)
			require.Equal(t, tt.oldPath, diff.Files[0].OldPath)
			require.Equal(t, tt.newPath, diff.Files[0].NewPath)
		})
	}
}

func TestParseDiffAcceptsMixedQuotedAndUnquotedBinaryPaths(t *testing.T) {
	patch := "diff --git a/old name.bin \"b/new\\tname.bin\"\n" +
		"Binary files a/old name.bin and \"b/new\\tname.bin\" differ\n"
	diff, err := Parse(strings.NewReader(patch))
	require.NoError(t, err)
	require.Len(t, diff.Files, 1)
	require.True(t, diff.Files[0].Binary)
	require.Equal(t, "old name.bin", diff.Files[0].OldPath)
	require.Equal(t, "new\tname.bin", diff.Files[0].NewPath)
}

func TestParseDiffRejectsInvalidUTF8QuotedPath(t *testing.T) {
	patch := "diff --git a/file.go \"b/\\376.go\"\n" +
		"--- a/file.go\n+++ \"b/\\376.go\"\n@@ -1 +1 @@\n-old\n+new\n"
	_, err := Parse(strings.NewReader(patch))
	require.ErrorContains(t, err, "path encoding")
}

func TestParseDiffBoundsAmbiguousPathCandidates(t *testing.T) {
	header := "a/old" + strings.Repeat(" b/ambiguous", 17) + " b/new"
	_, err := Parse(strings.NewReader("diff --git " + header + "\n"))
	require.ErrorContains(t, err, "too many ambiguous")
}

func TestParseDiffErrorsDoNotEchoUntrustedContent(t *testing.T) {
	const secret = "password=correct-horse-battery-staple"
	_, err := Parse(strings.NewReader(secret + "\n"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
}

func TestParseDiffRejectsDangerousGitCQuotedPathsAfterDecoding(t *testing.T) {
	tests := []struct {
		name   string
		quoted string
	}{
		{name: "nul", quoted: `"b/file\000.txt"`},
		{name: "backslash", quoted: `"b/dir\134file.txt"`},
		{name: "absolute", quoted: `"\057etc/passwd"`},
		{name: "traversal", quoted: `"b/dir/..\057file.txt"`},
		{name: "dot component", quoted: `"b/dir/\056/file.txt"`},
		{name: "repeated separator", quoted: `"b/dir\057\057file.txt"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ " + tt.quoted +
				"\n@@ -1 +1 @@\n-old\n+new\n"
			_, err := Parse(strings.NewReader(patch))
			require.ErrorContains(t, err, "unsafe path")
		})
	}
}

func TestParseDiffAcceptsNoNewlineMarkers(t *testing.T) {
	diff, err := parseFixture(t, "no-newline.patch")
	require.NoError(t, err)
	require.Len(t, diff.Files[0].Hunks[0].Lines, 2)
	require.Equal(t, "old", diff.Files[0].Hunks[0].Lines[0].Text)
	require.Equal(t, "new", diff.Files[0].Hunks[0].Lines[1].Text)
}

func TestParseDiffPermitsOtherSideAfterNoNewlineMarker(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "old side reaches eof first",
			body: "@@ -1 +1,2 @@\n-old\n\\ No newline at end of file\n+new1\n+new2\n",
		},
		{
			name: "new side reaches eof first",
			body: "@@ -1,2 +1 @@\n+new\n\\ No newline at end of file\n-old1\n-old2\n",
		},
		{
			name: "both sides reach eof separately",
			body: "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(oneFile(tt.body)))
			require.NoError(t, err)
		})
	}
}

func TestParseDiffRejectsContentAfterSideEOF(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "old line later in same hunk",
			body: "@@ -1 +1,2 @@\n-old\n\\ No newline at end of file\n-old2\n+new1\n+new2\n",
			want: "old side after end of file",
		},
		{
			name: "new line later in same hunk",
			body: "@@ -1,2 +1 @@\n+new\n\\ No newline at end of file\n+new2\n-old1\n-old2\n",
			want: "new side after end of file",
		},
		{
			name: "old side in later hunk",
			body: "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n@@ -2 +2 @@\n-old2\n+new2\n",
			want: "old side after end of file",
		},
		{
			name: "new side in later hunk",
			body: "@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file\n@@ -2 +2 @@\n-old2\n+new2\n",
			want: "new side after end of file",
		},
		{
			name: "both sides in later hunk",
			body: "@@ -1 +1 @@\n old\n\\ No newline at end of file\n@@ -2 +2 @@\n old2\n",
			want: "old side after end of file",
		},
		{
			name: "new-only later hunk after old eof",
			body: "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n" +
				"@@ -2,0 +3 @@\n+later\n",
			want: "old side after end of file",
		},
		{
			name: "old-only later hunk after new eof",
			body: "@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file\n" +
				"@@ -3 +2,0 @@\n-later\n",
			want: "new side after end of file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(oneFile(tt.body)))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestParseDiffAcceptsZeroCountAtNonzeroStart(t *testing.T) {
	diff, err := Parse(strings.NewReader(oneFile("@@ -1,0 +2 @@\n+inserted\n")))
	require.NoError(t, err)
	require.Equal(t, Hunk{
		OldStart: 1,
		OldLines: 0,
		NewStart: 2,
		NewLines: 1,
		Lines: []Line{
			{Kind: LineAdded, Text: "inserted", NewNumber: intPtr(2)},
		},
	}, diff.Files[0].Hunks[0])
}

func TestParseDiffRejectsMisplacedNoNewlineMarker(t *testing.T) {
	patch := oneFile("@@ -1 +1 @@\n\\ No newline at end of file\n-old\n+new\n")
	_, err := Parse(strings.NewReader(patch))
	require.ErrorContains(t, err, "no-newline marker")
}

func TestParseDiffRejectsEarlyNoNewlineMarkers(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "deleted line before old range ends",
			body: "@@ -1,2 +1 @@\n-old1\n\\ No newline at end of file\n-old2\n+new\n",
		},
		{
			name: "added line before new range ends",
			body: "@@ -1 +1,2 @@\n-old\n+new1\n\\ No newline at end of file\n+new2\n",
		},
		{
			name: "context line before either range ends",
			body: "@@ -1,2 +1,2 @@\n same\n\\ No newline at end of file\n-old\n+new\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(oneFile(tt.body)))
			require.ErrorContains(t, err, "no-newline marker")
		})
	}
}

func TestParseDiffRejectsInvalidFileMarkers(t *testing.T) {
	tests := []struct {
		name  string
		patch string
	}{
		{
			name:  "duplicate old marker",
			patch: "diff --git a/file.txt b/file.txt\n--- a/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
		},
		{
			name:  "duplicate new marker",
			patch: "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
		},
		{
			name:  "new marker before old marker",
			patch: "diff --git a/file.txt b/file.txt\n+++ b/file.txt\n--- a/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
		},
		{
			name:  "old marker conflicts with header",
			patch: "diff --git a/file.txt b/file.txt\n--- a/other.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
		},
		{
			name:  "new marker conflicts with header",
			patch: "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/other.txt\n@@ -1 +1 @@\n-old\n+new\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.patch))
			require.ErrorContains(t, err, "file marker")
		})
	}
}

func TestParseDiffRejectsMetadataHeaderMismatches(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
	}{
		{name: "rename from", metadata: "rename from other.txt\nrename to new.txt\n"},
		{name: "rename to", metadata: "rename from old.txt\nrename to other.txt\n"},
		{name: "copy from", metadata: "copy from other.txt\ncopy to new.txt\n"},
		{name: "copy to", metadata: "copy from old.txt\ncopy to other.txt\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git a/old.txt b/new.txt\nsimilarity index 100%\n" + tt.metadata
			_, err := Parse(strings.NewReader(patch))
			require.ErrorContains(t, err, "header path mismatch")
		})
	}
}

func TestParseDiffRejectsInvalidRenameCopyMetadataState(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
	}{
		{name: "rename missing from", metadata: "rename to new.txt\n"},
		{name: "rename missing to", metadata: "rename from old.txt\n"},
		{name: "copy missing from", metadata: "copy to new.txt\n"},
		{name: "copy missing to", metadata: "copy from old.txt\n"},
		{name: "duplicate rename from", metadata: "rename from old.txt\nrename from old.txt\nrename to new.txt\n"},
		{name: "duplicate rename to", metadata: "rename from old.txt\nrename to new.txt\nrename to new.txt\n"},
		{name: "duplicate copy from", metadata: "copy from old.txt\ncopy from old.txt\ncopy to new.txt\n"},
		{name: "duplicate copy to", metadata: "copy from old.txt\ncopy to new.txt\ncopy to new.txt\n"},
		{name: "mixed rename and copy", metadata: "rename from old.txt\ncopy to new.txt\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git a/old.txt b/new.txt\nsimilarity index 100%\n" + tt.metadata
			_, err := Parse(strings.NewReader(patch))
			require.ErrorContains(t, err, "operation metadata")
		})
	}
}

func TestParseDiffRejectsDuplicateOrConflictingFileOperations(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		metadata string
	}{
		{name: "duplicate added", header: "diff --git a/file.txt b/file.txt\n", metadata: "new file mode 100644\nnew file mode 100644\n"},
		{name: "duplicate deleted", header: "diff --git a/file.txt b/file.txt\n", metadata: "deleted file mode 100644\ndeleted file mode 100644\n"},
		{name: "added then deleted", header: "diff --git a/file.txt b/file.txt\n", metadata: "new file mode 100644\ndeleted file mode 100644\n"},
		{name: "deleted then added", header: "diff --git a/file.txt b/file.txt\n", metadata: "deleted file mode 100644\nnew file mode 100644\n"},
		{name: "added then rename", header: "diff --git a/old.txt b/new.txt\n", metadata: "new file mode 100644\nrename from old.txt\nrename to new.txt\n"},
		{name: "rename then added", header: "diff --git a/old.txt b/new.txt\n", metadata: "rename from old.txt\nrename to new.txt\nnew file mode 100644\n"},
		{name: "deleted then copy", header: "diff --git a/old.txt b/new.txt\n", metadata: "deleted file mode 100644\ncopy from old.txt\ncopy to new.txt\n"},
		{name: "copy then deleted", header: "diff --git a/old.txt b/new.txt\n", metadata: "copy from old.txt\ncopy to new.txt\ndeleted file mode 100644\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.header + tt.metadata))
			require.ErrorContains(t, err, "operation metadata")
		})
	}
}

func TestParseDiffRejectsFileOperationsAfterMarkers(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		markers   string
		operation string
	}{
		{
			name:      "added mode",
			header:    "diff --git a/file.txt b/file.txt\n",
			markers:   "--- a/file.txt\n+++ b/file.txt\n",
			operation: "new file mode 100644\n",
		},
		{
			name:      "deleted mode",
			header:    "diff --git a/file.txt b/file.txt\n",
			markers:   "--- a/file.txt\n+++ b/file.txt\n",
			operation: "deleted file mode 100644\n",
		},
		{
			name:      "rename pair",
			header:    "diff --git a/old.txt b/new.txt\n",
			markers:   "--- a/old.txt\n+++ b/new.txt\n",
			operation: "rename from old.txt\nrename to new.txt\n",
		},
		{
			name:      "copy pair",
			header:    "diff --git a/old.txt b/new.txt\n",
			markers:   "--- a/old.txt\n+++ b/new.txt\n",
			operation: "copy from old.txt\ncopy to new.txt\n",
		},
		{
			name:      "rename to after markers",
			header:    "diff --git a/old.txt b/new.txt\nrename from old.txt\n",
			markers:   "--- a/old.txt\n+++ b/new.txt\n",
			operation: "rename to new.txt\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.header + tt.markers + tt.operation))
			require.ErrorContains(t, err, "operation metadata after content started")
		})
	}
}

func TestParseDiffRejectsFileOperationsAfterContentStarts(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		operation string
	}{
		{
			name:      "binary files summary",
			content:   "index 1111111..2222222 100644\nBinary files a/file.txt and b/file.txt differ\n",
			operation: "new file mode 100644\n",
		},
		{
			name:      "git binary patch",
			content:   "index 1111111..2222222 100644\nGIT binary patch\n",
			operation: "deleted file mode 100644\n",
		},
		{
			name:      "binary files summary before copy",
			content:   "index 1111111..2222222 100644\nBinary files a/file.txt and b/file.txt differ\n",
			operation: "copy from file.txt\ncopy to file.txt\n",
		},
		{
			name:      "hunk",
			content:   "--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
			operation: "rename from file.txt\nrename to file.txt\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git a/file.txt b/file.txt\n" + tt.content + tt.operation
			_, err := Parse(strings.NewReader(patch))
			require.ErrorContains(t, err, "operation metadata after content started")
		})
	}
}

func TestParseDiffRejectsMixedFileContentKinds(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "binary summary after text hunk",
			content: "--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n" +
				"Binary files a/file.txt and b/file.txt differ\n",
		},
		{
			name: "binary patch after text hunk",
			content: "--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n" +
				"GIT binary patch\n",
		},
		{
			name: "text hunk after binary summary",
			content: "Binary files a/file.txt and b/file.txt differ\n" +
				"--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
		},
		{
			name: "text hunk after binary patch",
			content: "GIT binary patch\nliteral 0\nHcmV?d00001\n" +
				"--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
		},
		{
			name:    "binary patch after binary summary",
			content: "Binary files a/file.txt and b/file.txt differ\nGIT binary patch\n",
		},
		{
			name:    "binary summary after binary patch",
			content: "GIT binary patch\nliteral 0\nHcmV?d00001\nBinary files a/file.txt and b/file.txt differ\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader("diff --git a/file.txt b/file.txt\n" + tt.content))
			require.ErrorContains(t, err, "content")
		})
	}
}

func TestParseDiffRejectsStructuralMetadataAfterContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "index after text hunk",
			content: "--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n" +
				"index 1111111..2222222 100644\n",
		},
		{
			name:    "mode after binary summary",
			content: "Binary files a/file.txt and b/file.txt differ\nold mode 100644\n",
		},
		{
			name:    "file marker after binary patch",
			content: "GIT binary patch\nliteral 0\nHcmV?d00001\n--- a/file.txt\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader("diff --git a/file.txt b/file.txt\n" + tt.content))
			require.ErrorContains(t, err, "structural metadata after content")
		})
	}
}

func TestParseDiffRejectsStructuralMetadataAfterFileMarkersStart(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "between markers",
			content: "--- a/file.txt\nindex 1111111..2222222 100644\n+++ b/file.txt\n",
		},
		{
			name:    "after markers",
			content: "--- a/file.txt\n+++ b/file.txt\nold mode 100644\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader("diff --git a/file.txt b/file.txt\n" + tt.content))
			require.ErrorContains(t, err, "structural metadata after content")
		})
	}
}

func TestParseDiffRejectsTrailingTextAfterBinarySummary(t *testing.T) {
	patch := "diff --git a/file.txt b/file.txt\n" +
		"Binary files a/file.txt and b/file.txt differ\n" +
		"unexpected trailing text\n"
	_, err := Parse(strings.NewReader(patch))
	require.ErrorContains(t, err, "content after binary summary")
}

func TestParseDiffReconcilesHeaderPathsContainingBPrefix(t *testing.T) {
	patch := "diff --git a/old b/part.txt b/new b/part.txt\n" +
		"similarity index 100%\n" +
		"rename from old b/part.txt\n" +
		"rename to new b/part.txt\n"
	diff, err := Parse(strings.NewReader(patch))
	require.NoError(t, err)
	require.Equal(t, "old b/part.txt", diff.Files[0].OldPath)
	require.Equal(t, "new b/part.txt", diff.Files[0].NewPath)
}

func TestParseDiffRejectsDuplicateOrOverlappingHunks(t *testing.T) {
	overlap, err := os.Open("testdata/overlap.patch")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, overlap.Close()) })
	_, err = Parse(overlap)
	require.ErrorContains(t, err, "overlapping hunk")

	duplicate := strings.ReplaceAll(readFixture(t, "overlap.patch"), "@@ -2,2 +2,2 @@", "@@ -1,2 +1,2 @@")
	_, err = Parse(strings.NewReader(duplicate))
	require.ErrorContains(t, err, "overlapping hunk")
}

func TestParseDiffRejectsRepeatedEmptyRangeAnchors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "old side",
			body: "@@ -1,0 +1 @@\n+one\n@@ -1,0 +3 @@\n+two\n",
			want: "repeated old empty-range anchor",
		},
		{
			name: "new side",
			body: "@@ -1 +1,0 @@\n-one\n@@ -3 +1,0 @@\n-two\n",
			want: "repeated new empty-range anchor",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(oneFile(tt.body)))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestParseDiffRejectsEmptyHunks(t *testing.T) {
	_, err := Parse(strings.NewReader(oneFile("@@ -1,0 +1,0 @@\n")))
	require.ErrorContains(t, err, "empty hunk")
}

func TestParseDiffParsesAndValidatesBinaryPaths(t *testing.T) {
	valid := "diff --git a/old image.png b/new image.png\n" +
		"index 1111111..2222222 100644\n" +
		"Binary files a/old image.png and b/new image.png differ\n"
	diff, err := Parse(strings.NewReader(valid))
	require.NoError(t, err)
	require.Equal(t, "old image.png", diff.Files[0].OldPath)
	require.Equal(t, "new image.png", diff.Files[0].NewPath)
	require.True(t, diff.Files[0].Binary)

	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "unsafe old absolute", old: "/etc/passwd", new: "b/image.png", want: "unsafe path"},
		{name: "unsafe old traversal", old: "a/../image.png", new: "b/image.png", want: "unsafe path"},
		{name: "unsafe new backslash", old: "a/image.png", new: `b/dir\image.png`, want: "unsafe path"},
		{name: "unsafe new traversal", old: "a/image.png", new: "b/../image.png", want: "unsafe path"},
		{name: "old mismatch", old: "a/other.png", new: "b/image.png", want: "binary paths mismatch"},
		{name: "new mismatch", old: "a/image.png", new: "b/other.png", want: "binary paths mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git a/image.png b/image.png\nindex 1111111..2222222 100644\nBinary files " +
				tt.old + " and " + tt.new + " differ\n"
			_, err := Parse(strings.NewReader(patch))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestParseDiffRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "absolute", path: "/etc/passwd"},
		{name: "backslash", path: `dir\file.go`},
		{name: "parent traversal", path: "dir/../file.go"},
		{name: "dot component", path: "dir/./file.go"},
		{name: "repeated separator", path: "dir//file.go"},
		{name: "trailing separator", path: "dir/"},
		{name: "nul", path: "file\x00.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := "diff --git a/safe.go b/safe.go\n--- a/safe.go\n+++ b/" + tt.path + "\n@@ -1 +1 @@\n-old\n+new\n"
			_, err := Parse(strings.NewReader(patch))
			require.ErrorContains(t, err, "unsafe path")
		})
	}
}

func TestParseDiffRejectsNULInFinalLineWithoutNewline(t *testing.T) {
	patch := strings.TrimSuffix(oneFile("@@ -1 +1 @@\n-old\n+new\n"), "\n") + "\x00"
	_, err := Parse(strings.NewReader(patch))
	require.ErrorContains(t, err, "nul byte")
}

func TestParseDiffRejectsMalformedCountsAndMismatches(t *testing.T) {
	tests := []struct {
		name  string
		patch string
		want  string
	}{
		{name: "invalid count", patch: oneFile("@@ -1,x +1 @@\n-old\n+new\n"), want: "invalid hunk header"},
		{name: "negative count", patch: oneFile("@@ -1,-1 +1 @@\n-old\n+new\n"), want: "invalid hunk header"},
		{name: "range overflow", patch: oneFile("@@ -" + strconv.Itoa(int(^uint(0)>>1)) + " +1 @@\n-old\n+new\n"), want: "invalid hunk header"},
		{name: "old mismatch", patch: oneFile("@@ -1,2 +1 @@\n-old\n+new\n"), want: "hunk line count mismatch"},
		{name: "new mismatch", patch: oneFile("@@ -1 +1,2 @@\n-old\n+new\n"), want: "hunk line count mismatch"},
		{name: "extra old line", patch: oneFile("@@ -1 +1 @@\n-old\n-extra\n+new\n"), want: "hunk line count mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.patch))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestParseDiffEnforcesLimits(t *testing.T) {
	tests := []struct {
		name   string
		patch  string
		limits Limits
		want   string
	}{
		{name: "input bytes", patch: oneFile("@@ -1 +1 @@\n-old\n+new\n"), limits: Limits{MaxInputBytes: 32}, want: "input byte limit"},
		{name: "line length", patch: oneFile("@@ -1 +1 @@\n-old\n+" + strings.Repeat("x", 81) + "\n"), limits: Limits{MaxLineBytes: 80}, want: "line length limit"},
		{name: "physical lines", patch: strings.TrimSuffix(oneFile("@@ -1 +1 @@\n-old\n+new\n"), "\n"), limits: Limits{MaxLines: 5}, want: "line limit"},
		{name: "files", patch: twoFiles(), limits: Limits{MaxFiles: 1}, want: "file limit"},
		{name: "hunks", patch: oneFile("@@ -1 +1 @@\n-old\n+new\n@@ -3 +3 @@\n-old\n+new\n"), limits: Limits{MaxHunks: 1}, want: "hunk limit"},
		{name: "changed lines", patch: oneFile("@@ -1,2 +1,2 @@\n-old1\n-old2\n+new1\n+new2\n"), limits: Limits{MaxChangedLines: 3}, want: "changed line limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.patch), WithLimits(tt.limits))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestParseDiffRejectsMaxInt64InputByteLimit(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("MaxInt64 is not representable as int")
	}
	_, err := Parse(
		strings.NewReader(oneFile("@@ -1 +1 @@\n-old\n+new\n")),
		WithLimits(Limits{MaxInputBytes: int(^uint(0) >> 1)}),
	)
	require.ErrorContains(t, err, "input byte limit too large")
}

func TestParseDiffRejectsNonPositiveMaxLines(t *testing.T) {
	_, err := Parse(
		strings.NewReader(oneFile("@@ -1 +1 @@\n-old\n+new\n")),
		WithLimits(Limits{MaxLines: -1}),
	)
	require.ErrorContains(t, err, "limits must be positive")
}

func TestLexGitTokensRejectsMalformedQuoting(t *testing.T) {
	tests := []struct {
		name  string
		value string
		limit int
	}{
		{name: "unterminated", value: `"a/file.txt`, limit: 1},
		{name: "unknown escape", value: `"a/file\x2e"`, limit: 1},
		{name: "octal overflow", value: `"a/file\400"`, limit: 1},
		{name: "suffix after quote", value: `"a/file.txt"suffix`, limit: 1},
		{name: "too many tokens", value: `a/one b/two extra`, limit: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lexGitTokens(tt.value, tt.limit)
			require.Error(t, err)
		})
	}
}

func FuzzParseDiff(f *testing.F) {
	f.Add(oneFile("@@ -1 +1 @@\n-old\n+new\n"))
	f.Add(`diff --git "a/\344\270\255\told.txt" "b/\344\270\255\tnew.txt"` + "\n")
	f.Add("diff --git a/file.txt b/file.txt\nBinary files a/file.txt and b/file.txt differ\n")
	f.Fuzz(func(t *testing.T, patch string) {
		_, _ = Parse(strings.NewReader(patch), WithLimits(Limits{
			MaxInputBytes:   4 << 10,
			MaxLineBytes:    512,
			MaxLines:        128,
			MaxFiles:        16,
			MaxHunks:        32,
			MaxChangedLines: 128,
		}))
	})
}

func parseFixture(t *testing.T, name string) (Diff, error) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })
	return Parse(f)
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return string(data)
}

func oneFile(body string) string {
	return "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n" + body
}

func twoFiles() string {
	return oneFile("@@ -1 +1 @@\n-old\n+new\n") + strings.ReplaceAll(oneFile("@@ -1 +1 @@\n-old\n+new\n"), "file.txt", "other.txt")
}

func intPtr(value int) *int {
	return &value
}
