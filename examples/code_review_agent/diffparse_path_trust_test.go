//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"
)

func TestDiffPathMetadataCannotReplaceHeaderPaths(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/pkg/leak.go b/pkg/leak.go",
		"--- a/pkg/leak.txt",
		"+++ b/pkg/leak.txt",
		"@@ -1 +1,2 @@",
		" package trusted",
		"+func Added() {}",
	}, "\n")

	parsed := parseUnifiedDiff([]byte(diff))
	if len(parsed.Files) != 1 {
		t.Fatalf("files = %+v, want one", parsed.Files)
	}
	file := parsed.Files[0]
	if file.OldPath != "pkg/leak.go" || file.NewPath != "pkg/leak.go" ||
		file.headerOldPath != "pkg/leak.go" || file.headerNewPath != "pkg/leak.go" {
		t.Fatalf("paths = %q -> %q, headers = %q -> %q", file.OldPath, file.NewPath,
			file.headerOldPath, file.headerNewPath)
	}
	if !file.isGoFile() || file.PackageName != "trusted" {
		t.Fatalf("trusted Go metadata was bypassed: %+v", file)
	}
	assertPathWarning(t, parsed.Warnings, 2, "old path metadata does not match diff header")
	assertPathWarning(t, parsed.Warnings, 3, "new path metadata does not match diff header")
	for _, warning := range parsed.Warnings {
		if strings.Contains(warning.Message, "leak.txt") || strings.Contains(warning.File, "leak.txt") {
			t.Fatalf("warning exposed untrusted path: %+v", warning)
		}
	}
}

func TestMalformedDiffPathMetadataRetainsHeaderPaths(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/pkg/review.go b/pkg/review.go",
		`--- "a/pkg/review.go" trailing`,
		`+++ "b/pkg/review.go`,
		"@@ -1 +1 @@",
		"-package old",
		"+package review",
	}, "\n")

	parsed := parseUnifiedDiff([]byte(diff))
	file := parsed.Files[0]
	if file.OldPath != "pkg/review.go" || file.NewPath != "pkg/review.go" {
		t.Fatalf("metadata replaced trusted header paths: %+v", file)
	}
	assertPathWarning(t, parsed.Warnings, 2, "malformed old path metadata")
	assertPathWarning(t, parsed.Warnings, 3, "malformed new path metadata")
}

func TestMalformedDiffHeaderCannotBeRepairedByMetadata(t *testing.T) {
	diff := strings.Join([]string{
		`diff --git "a/pkg/review.go b/pkg/review.go`,
		"--- a/pkg/review.go",
		"+++ b/pkg/review.go",
	}, "\n")

	parsed := parseUnifiedDiff([]byte(diff))
	if len(parsed.Files) != 1 {
		t.Fatalf("files = %+v, want one", parsed.Files)
	}
	file := parsed.Files[0]
	if file.OldPath != "" || file.NewPath != "" || file.headerOldPath != "" || file.headerNewPath != "" {
		t.Fatalf("metadata repaired malformed header: %+v", file)
	}
	assertPathWarning(t, parsed.Warnings, 1, "malformed diff header")
	assertPathWarning(t, parsed.Warnings, 2, "old path metadata does not match diff header")
	assertPathWarning(t, parsed.Warnings, 3, "new path metadata does not match diff header")
}

func TestUntrustedBinaryMetadataCannotSkipTrustedGoHeader(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/pkg/review.go b/pkg/review.go",
		"Binary files a/pkg/review.go and b/pkg/review.go differ",
		"--- a/pkg/review.go",
		"+++ b/pkg/review.go",
		"@@ -1 +1,2 @@",
		" package review",
		`+const serviceToken = "supersecret123"`,
	}, "\n")

	parsed := parseUnifiedDiff([]byte(diff))
	if len(parsed.Files) != 1 || parsed.Files[0].IsBinary || !parsed.Files[0].isGoFile() {
		t.Fatalf("binary metadata bypassed trusted Go header: %+v", parsed.Files)
	}
	assertPathWarning(t, parsed.Warnings, 5, "binary metadata conflicts with text hunk")
	assertParsedRuleMatch(t, parsed, ruleSecretHardcoded)
}

func TestUntrustedDeletedMetadataCannotSkipTrustedGoHeader(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/pkg/review.go b/pkg/review.go",
		"deleted file mode 100644",
		"--- a/pkg/review.go",
		"+++ /dev/null",
		"@@ -1 +1,2 @@",
		" package review",
		`+const serviceToken = "supersecret123"`,
	}, "\n")

	parsed := parseUnifiedDiff([]byte(diff))
	if len(parsed.Files) != 1 {
		t.Fatalf("files = %+v, want one", parsed.Files)
	}
	file := parsed.Files[0]
	if file.IsDeleted || file.NewPath != "pkg/review.go" || !file.isGoFile() {
		t.Fatalf("deleted metadata bypassed trusted Go header: %+v", file)
	}
	assertPathWarning(t, parsed.Warnings, 5, "deleted file new hunk range must be 0,0")
	assertParsedRuleMatch(t, parsed, ruleSecretHardcoded)
}

func TestUntrustedNewMetadataRestoresTrustedOldHeaderPath(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(strings.Join([]string{
		"diff --git a/pkg/review.go b/pkg/review.go",
		"new file mode 100644",
		"--- a/pkg/review.go",
		"+++ b/pkg/review.go",
		"@@ -1 +1,2 @@",
		" package review",
		"+func Added() {}",
	}, "\n")))

	file := parsed.Files[0]
	if file.IsNew || file.OldPath != "pkg/review.go" || file.NewPath != "pkg/review.go" {
		t.Fatalf("untrusted new metadata changed trusted header state: %+v", file)
	}
	assertPathWarning(t, parsed.Warnings, 1, "new file old path must be /dev/null")
}

func TestRenameMetadataUsesRepositoryRelativePaths(t *testing.T) {
	t.Run("real a and b directories", func(t *testing.T) {
		parsed := parseUnifiedDiff([]byte(strings.Join([]string{
			"diff --git a/a/old.go b/b/new.go",
			"similarity index 100%",
			"rename from a/old.go",
			"rename to b/new.go",
		}, "\n")))
		if len(parsed.Warnings) != 0 {
			t.Fatalf("warnings = %+v, want none", parsed.Warnings)
		}
		file := parsed.Files[0]
		if !file.IsRename || file.OldPath != "a/old.go" || file.NewPath != "b/new.go" {
			t.Fatalf("rename = %+v", file)
		}
	})

	t.Run("mismatch retains header", func(t *testing.T) {
		parsed := parseUnifiedDiff([]byte(strings.Join([]string{
			"diff --git a/a/old.go b/b/new.go",
			"rename from old.go",
			"rename to b/new.go",
		}, "\n")))
		file := parsed.Files[0]
		if file.OldPath != "a/old.go" || file.NewPath != "b/new.go" {
			t.Fatalf("rename metadata replaced header paths: %+v", file)
		}
		assertPathWarning(t, parsed.Warnings, 2, "rename from path does not match diff header")
	})

	for _, renamePath := range []string{"../old.go", `..\old.go`, "C:/old.go"} {
		t.Run("malformed repository relative path "+renamePath, func(t *testing.T) {
			parsed := parseUnifiedDiff([]byte(strings.Join([]string{
				"diff --git a/a/old.go b/b/new.go",
				"rename from " + renamePath,
				"rename to b/new.go",
			}, "\n")))
			assertPathWarning(t, parsed.Warnings, 2, "malformed rename from path")
		})
	}

	t.Run("identical endpoints", func(t *testing.T) {
		parsed := parseUnifiedDiff([]byte(strings.Join([]string{
			"diff --git a/same.go b/same.go",
			"rename from same.go",
			"rename to same.go",
		}, "\n")))
		assertPathWarning(t, parsed.Warnings, 3, "rename paths must be different")
	})
}

func TestTrustedDiffPathLegalForms(t *testing.T) {
	tests := []struct {
		name     string
		diff     string
		oldPath  string
		newPath  string
		isNew    bool
		isDelete bool
		isBinary bool
		isRename bool
	}{
		{
			name: "regular with quoted Unicode and tab timestamp",
			diff: strings.Join([]string{
				`diff --git "a/pkg/中文 file.go" "b/pkg/中文 file.go"`,
				"--- \"a/pkg/中文 file.go\"\t2026-07-31 01:02:03 +0800",
				"+++ \"b/pkg/中文 file.go\"\t2026-07-31 01:02:04 +0800",
				"@@ -1 +1 @@",
				"-package old",
				"+package current",
			}, "\n"),
			oldPath: "pkg/中文 file.go",
			newPath: "pkg/中文 file.go",
		},
		{
			name:    "new file",
			diff:    "diff --git a/new.go b/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/new.go\n@@ -0,0 +1,1 @@\n+package current\n",
			newPath: "new.go",
			isNew:   true,
		},
		{
			name:     "deleted file",
			diff:     "diff --git a/old.go b/old.go\ndeleted file mode 100644\n--- a/old.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-package old\n",
			oldPath:  "old.go",
			isDelete: true,
		},
		{
			name:     "binary file",
			diff:     "diff --git a/logo.png b/logo.png\nnew file mode 100644\nGIT binary patch\nliteral 0\n",
			oldPath:  "logo.png",
			newPath:  "logo.png",
			isNew:    true,
			isBinary: true,
		},
		{
			name: "quoted Unicode rename with hunk",
			diff: strings.Join([]string{
				`diff --git "a/pkg/旧.go" "b/pkg/新.go"`,
				"similarity index 80%",
				`rename from "pkg/旧.go"`,
				`rename to "pkg/新.go"`,
				`--- "a/pkg/旧.go"`,
				`+++ "b/pkg/新.go"`,
				"@@ -1 +1 @@",
				"-package old",
				"+package current",
			}, "\n"),
			oldPath:  "pkg/旧.go",
			newPath:  "pkg/新.go",
			isRename: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseUnifiedDiff([]byte(tt.diff))
			if len(parsed.Warnings) != 0 {
				t.Fatalf("warnings = %+v, want none", parsed.Warnings)
			}
			if len(parsed.Files) != 1 {
				t.Fatalf("files = %+v, want one", parsed.Files)
			}
			file := parsed.Files[0]
			if file.OldPath != tt.oldPath || file.NewPath != tt.newPath || file.IsNew != tt.isNew ||
				file.IsDeleted != tt.isDelete || file.IsBinary != tt.isBinary || file.IsRename != tt.isRename {
				t.Fatalf("file = %+v", file)
			}
		})
	}
}

func TestModeOnlyEmptyFileStatusIsPreserved(t *testing.T) {
	newFile := parseUnifiedDiff([]byte(strings.Join([]string{
		"diff --git a/empty.go b/empty.go",
		"new file mode 100644",
		"index 0000000..e69de29",
	}, "\n")))
	if len(newFile.Files) != 1 || !newFile.Files[0].IsNew || newFile.Files[0].NewPath != "empty.go" {
		t.Fatalf("mode-only new file = %+v", newFile)
	}

	deletedFile := parseUnifiedDiff([]byte(strings.Join([]string{
		"diff --git a/empty.go b/empty.go",
		"deleted file mode 100644",
		"index e69de29..0000000",
	}, "\n")))
	if len(deletedFile.Files) != 1 || !deletedFile.Files[0].IsDeleted || deletedFile.Files[0].OldPath != "empty.go" {
		t.Fatalf("mode-only deleted file = %+v", deletedFile)
	}
}

func assertPathWarning(t *testing.T, warnings []parseWarning, line int, message string) {
	t.Helper()
	for _, warning := range warnings {
		if warning.Line == line && strings.Contains(warning.Message, message) {
			return
		}
	}
	t.Fatalf("warnings = %+v, want line %d containing %q", warnings, line, message)
}

func assertParsedRuleMatch(t *testing.T, parsed parsedDiff, ruleID string) {
	t.Helper()
	for _, match := range runRules(parsed, "") {
		if match.RuleID == ruleID {
			return
		}
	}
	t.Fatalf("rules did not include %q for parsed diff %+v", ruleID, parsed)
}
