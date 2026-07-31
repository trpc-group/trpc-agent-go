//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModuleMetadataChangesAreRepositoryReviewable(t *testing.T) {
	tests := []changedFile{
		{OldPath: "go.mod", NewPath: "go.mod"},
		{OldPath: "nested/go.sum", NewPath: "nested/go.sum"},
		{OldPath: "go.work", NewPath: "go.work"},
		{OldPath: "go.work.sum", NewPath: "go.work.sum"},
	}
	for _, file := range tests {
		parsed := parsedDiff{Files: []changedFile{file}}
		if !hasRepositoryValidationChange(parsed) {
			t.Errorf("metadata change %q was not considered repository-reviewable", file.NewPath)
		}
		if !requiresGoRepositoryValidation(parsed) {
			t.Errorf("metadata change %q did not require repository validation", file.NewPath)
		}
	}
}

func TestRepositoryValidationTargetsCoverMetadataRenameSides(t *testing.T) {
	parsed := parsedDiff{Files: []changedFile{
		{OldPath: "old/go.mod", NewPath: "new/go.mod", IsRename: true},
		{OldPath: "old/go.work", NewPath: "new/go.work", IsRename: true},
	}}
	want := []repositoryValidationTarget{
		{path: "old/go.mod", kind: repositoryValidationTargetModule},
		{path: "new/go.mod", requireRegularFile: true, kind: repositoryValidationTargetModule},
		{path: "old/go.work", kind: repositoryValidationTargetWorkspace},
		{path: "new/go.work", requireRegularFile: true, kind: repositoryValidationTargetWorkspace},
	}
	if got := repositoryValidationTargets(parsed); !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestPrepareAffectedModuleManifestResolvesModuleMetadata(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*testing.T, string)
		file       changedFile
		wantModule []string
	}{
		{
			name: "modified nested go mod",
			prepare: func(t *testing.T, root string) {
				mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
				mustWriteFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.21\n")
			},
			file:       changedFile{OldPath: "nested/go.mod", NewPath: "nested/go.mod"},
			wantModule: []string{"nested"},
		},
		{
			name: "modified nested go sum",
			prepare: func(t *testing.T, root string) {
				mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
				mustWriteFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.21\n")
				mustWriteFile(t, filepath.Join(root, "nested", "go.sum"), "example.com/dependency v1.0.0 h1:test\n")
			},
			file:       changedFile{OldPath: "nested/go.sum", NewPath: "nested/go.sum"},
			wantModule: []string{"nested"},
		},
		{
			name: "deleted nested go mod uses parent",
			prepare: func(t *testing.T, root string) {
				mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
				mustWriteFile(t, filepath.Join(root, "nested", "remaining.go"), "package nested\n")
			},
			file:       changedFile{OldPath: "nested/go.mod", IsDeleted: true},
			wantModule: []string{"."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.prepare(t, root)
			modules, err := prepareAffectedModuleManifest(
				context.Background(),
				root,
				parsedDiff{Files: []changedFile{tt.file}},
			)
			if err != nil {
				t.Fatalf("prepare manifest: %v", err)
			}
			if !reflect.DeepEqual(modules, tt.wantModule) {
				t.Fatalf("modules = %#v, want %#v", modules, tt.wantModule)
			}
		})
	}
}

func TestPrepareAffectedModuleManifestRejectsUnsafeMetadata(t *testing.T) {
	t.Run("parent escape", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
		_, err := prepareAffectedModuleManifest(
			context.Background(),
			root,
			parsedDiff{Files: []changedFile{{NewPath: "../go.mod", IsNew: true}}},
		)
		if err == nil || !strings.Contains(err.Error(), "escapes the repository") {
			t.Fatalf("prepare manifest err = %v, want parent-traversal rejection", err)
		}
	})

	t.Run("non regular go sum", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.21\n")
		if err := os.Mkdir(filepath.Join(root, "nested", "go.sum"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := prepareAffectedModuleManifest(
			context.Background(),
			root,
			parsedDiff{Files: []changedFile{{NewPath: "nested/go.sum", IsNew: true}}},
		)
		if err == nil || !strings.Contains(err.Error(), "not regular") {
			t.Fatalf("prepare manifest err = %v, want regular-file rejection", err)
		}
	})
}

func TestWorkspaceMetadataPlansAllSnapshotModules(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(root, "go.work"), "go 1.21\n\nuse ./nested\n")
	mustWriteFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(root, "other", "go.mod"), "module example.com/other\n\ngo 1.21\n")

	modules, err := prepareAffectedModuleManifest(
		context.Background(),
		root,
		parsedDiff{Files: []changedFile{{OldPath: "go.work", NewPath: "go.work"}}},
	)
	if err != nil {
		t.Fatalf("prepare manifest: %v", err)
	}
	if want := []string{".", "nested", "other"}; !reflect.DeepEqual(modules, want) {
		t.Fatalf("modules = %#v, want %#v", modules, want)
	}
	workspaceManifest, err := os.ReadFile(filepath.Join(root, reviewWorkspaceManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte(".\x00"); !bytes.Equal(workspaceManifest, want) {
		t.Fatalf("workspace manifest = %q, want %q", workspaceManifest, want)
	}
}

func TestDeletedWorkspaceMetadataStillPlansAllSnapshotModules(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.21\n")

	modules, err := prepareAffectedModuleManifest(
		context.Background(),
		root,
		parsedDiff{Files: []changedFile{{OldPath: "go.work", IsDeleted: true}}},
	)
	if err != nil {
		t.Fatalf("prepare manifest: %v", err)
	}
	if want := []string{".", "nested"}; !reflect.DeepEqual(modules, want) {
		t.Fatalf("modules = %#v, want %#v", modules, want)
	}
}

func TestRepositoryModuleMetadataPlansChecks(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	parsed := parsedDiff{Files: []changedFile{{OldPath: "go.mod", NewPath: "go.mod"}}}
	if _, err := prepareAffectedModuleManifest(context.Background(), root, parsed); err != nil {
		t.Fatalf("prepare manifest: %v", err)
	}
	runner := &recordingSandboxRunner{}
	governance, err := runGovernance(
		context.Background(),
		config{},
		reviewInput{
			kind:            inputKindRepoPath,
			repoRoot:        root,
			sandboxRepoRoot: root,
		},
		parsed,
		runtimeHooks{sandboxRunner: runner},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if len(governance.Matches) != 0 {
		t.Fatalf("governance warnings = %+v, want none", governance.Matches)
	}
	wantKinds := []commandKind{commandCheckGoVersion, commandCheckGoTest, commandCheckGoVet}
	gotKinds := make([]commandKind, 0, len(runner.calls))
	for _, call := range runner.calls {
		gotKinds = append(gotKinds, call.Kind)
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("commands = %v, want %v", gotKinds, wantKinds)
	}
}

func TestModuleMetadataWithoutRepositoryRequiresHumanReview(t *testing.T) {
	parsed := parsedDiff{Files: []changedFile{{OldPath: "go.mod", NewPath: "go.mod"}}}
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
			if len(governance.Matches) != 1 ||
				governance.Matches[0].RuleID != ruleSandboxSnapshotUnavailable {
				t.Fatalf("governance warnings = %+v, want snapshot warning", governance.Matches)
			}
		})
	}
}

func TestRunChecksValidatesNestedWorkspaceMetadata(t *testing.T) {
	for _, toolName := range []string{"bash", "go"} {
		if _, err := exec.LookPath(toolName); err != nil {
			t.Skipf("%s unavailable: %v", toolName, err)
		}
	}
	repoRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, "root.go"), "package root\n")
	mustWriteFile(t, filepath.Join(repoRoot, reviewModuleManifestName), ".\x00")
	mustWriteFile(t, filepath.Join(repoRoot, reviewWorkspaceManifestName), "workspace\x00")
	mustWriteFile(t, filepath.Join(repoRoot, "workspace", "go.work"), "go 1.21\n\nuse (\n")

	scriptPath, err := filepath.Abs(filepath.Join("skills", "code-review", "scripts", "run_checks.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.ToSlash(scriptPath), "test")
	cmd.Env = append(os.Environ(), "REVIEW_REPO_DIR="+filepath.ToSlash(repoRoot))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("run_checks succeeded with invalid nested go.work: %s", output)
	}
	if !strings.Contains(string(output), "workspace") {
		t.Fatalf("run_checks output = %s, want workspace validation evidence", output)
	}
}

func TestRunChecksRejectsInvalidGoModOnlyChange(t *testing.T) {
	for _, toolName := range []string{"bash", "go"} {
		if _, err := exec.LookPath(toolName); err != nil {
			t.Skipf("%s unavailable: %v", toolName, err)
		}
	}
	repoRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/root\n\nrequire (\n")
	mustWriteFile(t, filepath.Join(repoRoot, "root.go"), "package root\n")
	mustWriteFile(t, filepath.Join(repoRoot, reviewModuleManifestName), ".\x00")

	scriptPath, err := filepath.Abs(filepath.Join("skills", "code-review", "scripts", "run_checks.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.ToSlash(scriptPath), "test")
	cmd.Env = append(os.Environ(), "REVIEW_REPO_DIR="+filepath.ToSlash(repoRoot))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("run_checks succeeded with invalid go.mod: %s", output)
	}
}
