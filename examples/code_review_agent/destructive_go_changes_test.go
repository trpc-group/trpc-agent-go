//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRepositoryValidationTargetsCoverBothRenameSides(t *testing.T) {
	tests := []struct {
		name    string
		file    changedFile
		targets []repositoryValidationTarget
	}{
		{
			name: "modified",
			file: changedFile{OldPath: "pkg/review.go", NewPath: "pkg/review.go"},
			targets: []repositoryValidationTarget{{
				path: "pkg/review.go", requireRegularFile: true, kind: repositoryValidationTargetModule,
			}},
		},
		{
			name: "new",
			file: changedFile{NewPath: "pkg/review.go", IsNew: true},
			targets: []repositoryValidationTarget{{
				path: "pkg/review.go", requireRegularFile: true, kind: repositoryValidationTargetModule,
			}},
		},
		{
			name: "deleted",
			file: changedFile{OldPath: "pkg/review.go", IsDeleted: true},
			targets: []repositoryValidationTarget{{
				path: "pkg/review.go", kind: repositoryValidationTargetModule,
			}},
		},
		{
			name: "rename away",
			file: changedFile{
				OldPath:  "old/review.go",
				NewPath:  "old/review.txt",
				IsRename: true,
			},
			targets: []repositoryValidationTarget{{
				path: "old/review.go", kind: repositoryValidationTargetModule,
			}},
		},
		{
			name: "rename into Go",
			file: changedFile{
				OldPath:  "old/review.txt",
				NewPath:  "new/review.go",
				IsRename: true,
			},
			targets: []repositoryValidationTarget{{
				path: "new/review.go", requireRegularFile: true, kind: repositoryValidationTargetModule,
			}},
		},
		{
			name: "cross module Go rename",
			file: changedFile{
				OldPath:  "old/review.go",
				NewPath:  "new/review.go",
				IsRename: true,
			},
			targets: []repositoryValidationTarget{
				{path: "old/review.go", kind: repositoryValidationTargetModule},
				{path: "new/review.go", requireRegularFile: true, kind: repositoryValidationTargetModule},
			},
		},
		{
			name: "binary Go",
			file: changedFile{
				OldPath:  "pkg/review.go",
				NewPath:  "pkg/review.go",
				IsBinary: true,
			},
			targets: []repositoryValidationTarget{{
				path: "pkg/review.go", requireRegularFile: true, kind: repositoryValidationTargetModule,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parsedDiff{Files: []changedFile{tt.file}}
			if got := repositoryValidationTargets(parsed); !reflect.DeepEqual(got, tt.targets) {
				t.Fatalf("targets = %#v, want %#v", got, tt.targets)
			}
			if !hasRepositoryValidationChange(parsed) {
				t.Fatal("Go-impacting change was not considered reviewable")
			}
		})
	}
}

func TestPrepareAffectedModuleManifestAllowsDeletedLeafAndCrossModuleRename(t *testing.T) {
	snapshotRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(snapshotRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(snapshotRoot, "old", "go.mod"), "module example.com/old\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(snapshotRoot, "new", "go.mod"), "module example.com/new\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(snapshotRoot, "new", "review.go"), "package new\n")

	parsed := parsedDiff{Files: []changedFile{
		{OldPath: "old/deleted.go", IsDeleted: true},
		{OldPath: "old/renamed.go", NewPath: "new/review.go", IsRename: true},
	}}
	modules, err := prepareAffectedModuleManifest(context.Background(), snapshotRoot, parsed)
	if err != nil {
		t.Fatalf("prepare manifest: %v", err)
	}
	if want := []string{"new", "old"}; !reflect.DeepEqual(sandboxModulePaths(modules), want) {
		t.Fatalf("modules = %#v, want %#v", sandboxModulePaths(modules), want)
	}
}

func TestPrepareAffectedModuleManifestIncludesBinaryGo(t *testing.T) {
	snapshotRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(snapshotRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(snapshotRoot, "review.go"), "package root\n\nconst Value = 1\n")
	parsed := parsedDiff{Files: []changedFile{
		{OldPath: "review.go", NewPath: "review.go", IsBinary: true},
	}}

	modules, err := prepareAffectedModuleManifest(context.Background(), snapshotRoot, parsed)
	if err != nil {
		t.Fatalf("prepare manifest: %v", err)
	}
	if want := []string{"."}; !reflect.DeepEqual(sandboxModulePaths(modules), want) {
		t.Fatalf("modules = %#v, want %#v", sandboxModulePaths(modules), want)
	}
}

func TestBinaryGoRenameAwayRetainsParseWarning(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(strings.Join([]string{
		"diff --git a/review.go b/review.bin",
		"similarity index 80%",
		"rename from review.go",
		"rename to review.bin",
		"Binary files a/review.go and b/review.bin differ",
	}, "\n")))
	if len(parsed.Files) != 1 || !parsed.Files[0].IsRename || !parsed.Files[0].IsBinary {
		t.Fatalf("parsed binary rename = %+v", parsed)
	}
	if !parseWarningsContain(parsed.Warnings, "Go source path is represented as binary") {
		t.Fatalf("warnings = %+v, want binary Go parse warning", parsed.Warnings)
	}
	if parsed.Warnings[0].File != "review.go" {
		t.Fatalf("warning file = %q, want old Go path", parsed.Warnings[0].File)
	}
	if conclusion := determineConclusion(reviewReport{
		Parse: reportParse{Warnings: len(parsed.Warnings)},
	}); conclusion != reviewConclusionNeedsHumanReview {
		t.Fatalf("conclusion = %q, want human review", conclusion)
	}
}

func TestPrepareAffectedModuleManifestCanonicalizesCaseOnlyRename(t *testing.T) {
	snapshotRoot := t.TempDir()
	const actualModule = "nestedmodule"
	const oldModule = "NESTEDMODULE"
	mustWriteFile(t, filepath.Join(snapshotRoot, actualModule, "go.mod"), "module example.com/nested\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(snapshotRoot, actualModule, "review.go"), "package nested\n")
	if _, err := os.Lstat(filepath.Join(snapshotRoot, oldModule, "go.mod")); err != nil {
		t.Skip("filesystem does not resolve case-only aliases")
	}

	parsed := parsedDiff{Files: []changedFile{{
		OldPath:  oldModule + "/review.go",
		NewPath:  actualModule + "/review.go",
		IsRename: true,
	}}}
	modules, err := prepareAffectedModuleManifest(context.Background(), snapshotRoot, parsed)
	if err != nil {
		t.Fatalf("prepare manifest: %v", err)
	}
	if want := []string{actualModule}; !reflect.DeepEqual(sandboxModulePaths(modules), want) {
		t.Fatalf("modules = %#v, want %#v", sandboxModulePaths(modules), want)
	}
}

func TestRepositoryDeletionAndRenameAwayRunModuleChecks(t *testing.T) {
	tests := []struct {
		name       string
		changeRepo func(*testing.T, string)
	}{
		{
			name: "deletion",
			changeRepo: func(t *testing.T, repoRoot string) {
				if err := os.Remove(filepath.Join(repoRoot, "review.go")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "rename away",
			changeRepo: func(t *testing.T, repoRoot string) {
				mustRunGit(t, repoRoot, "mv", "review.go", "review.txt")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			mustRunGit(t, repoRoot, "init")
			mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/review\n\ngo 1.21\n")
			mustWriteFile(t, filepath.Join(repoRoot, "review.go"), "package review\n\nfunc Removed() {}\n")
			mustRunGit(t, repoRoot, "add", "go.mod", "review.go")
			repoRootP1Commit(t, repoRoot)
			tt.changeRepo(t, repoRoot)

			input, err := loadReviewInput(context.Background(), config{repoPath: repoRoot}, runGitCommand)
			if err != nil {
				t.Fatal(err)
			}
			parsed := parseUnifiedDiff(input.diff)
			if len(parsed.Files) != 1 || parsed.Files[0].OldPath != "review.go" {
				t.Fatalf("parsed files = %+v", parsed.Files)
			}
			if !hasRepositoryValidationChange(parsed) {
				t.Fatal("destructive Go change was not reviewable")
			}

			runner := &recordingSandboxRunner{}
			governance, err := runGovernance(
				context.Background(),
				config{},
				input,
				parsed,
				runtimeHooks{sandboxRunner: runner},
			)
			if err != nil {
				t.Fatalf("run governance: %v", err)
			}
			if len(runner.calls) != 3 {
				t.Fatalf("sandbox calls = %+v, want version/test/vet", runner.calls)
			}
			if got := []commandKind{runner.calls[0].Kind, runner.calls[1].Kind, runner.calls[2].Kind}; !reflect.DeepEqual(got, []commandKind{commandCheckGoVersion, commandCheckGoTest, commandCheckGoVet}) {
				t.Fatalf("sandbox commands = %v, want version/test/vet", got)
			}
			if len(governance.Matches) != 0 {
				t.Fatalf("governance warnings = %+v, want checks to be available", governance.Matches)
			}
		})
	}
}

func TestGoDestructiveChangesWithoutRepositoryValidationRequireHumanReview(t *testing.T) {
	parsed := parsedDiff{Files: []changedFile{
		{OldPath: "review.go", IsDeleted: true},
	}}
	for _, kind := range []string{inputKindDiffFile, inputKindFixture} {
		t.Run(kind, func(t *testing.T) {
			runner := &recordingSandboxRunner{}
			governance, err := runGovernance(
				context.Background(),
				config{},
				reviewInput{kind: kind},
				parsed,
				runtimeHooks{sandboxRunner: runner},
			)
			if err != nil {
				t.Fatalf("run governance: %v", err)
			}
			if len(runner.calls) != 1 || runner.calls[0].Kind != commandCheckGoVersion {
				t.Fatalf("sandbox calls = %+v, want only go version", runner.calls)
			}
			if len(governance.Matches) != 1 ||
				governance.Matches[0].RuleID != ruleSandboxSnapshotUnavailable ||
				!strings.Contains(governance.Matches[0].Evidence, "complete repository snapshot") {
				t.Fatalf("governance warnings = %+v, want unavailable snapshot warning", governance.Matches)
			}
			finalized := finalizeRuleMatches(governance.Matches)
			if !finalized.NeedsHumanReview {
				t.Fatalf("finalized = %+v, want human review", finalized)
			}
		})
	}
}
