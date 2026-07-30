//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewinput

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

type memoryArtifactStore struct {
	info     artifact.SessionInfo
	filename string
	item     *artifact.Artifact
}

func (m *memoryArtifactStore) SaveArtifact(_ context.Context, info artifact.SessionInfo, filename string, item *artifact.Artifact) (version int, err error) {
	m.info = info
	m.filename = filename
	m.item = &artifact.Artifact{Data: append([]byte(nil), item.Data...), MimeType: item.MimeType, Name: item.Name}
	return 3, nil
}

func TestPrepareDiffOnlyBuildsArtifactMessageAndBootstrap(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "change.diff")
	raw := "diff --git a/foo.go b/foo.go\n--- a/foo.go\n+++ b/foo.go\n@@ -1 +1,2 @@\n package foo\n+var token = \"super-secret-value\"\n"
	writeTestFile(t, diffPath, raw)
	store := &memoryArtifactStore{}
	preparer, err := NewPreparer(store, redact.New(), Config{})
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-1", AppName: "app", UserID: "user",
	}, Spec{DiffFile: diffPath, Paths: []string{"foo.go"}})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	if prepared.ReviewMode != ReviewModePatchOnly || prepared.InputKind != InputKindDiffFile {
		t.Fatalf("prepared mode = %s/%s", prepared.InputKind, prepared.ReviewMode)
	}
	if strings.Contains(prepared.Message, "super-secret-value") || strings.Contains(string(store.item.Data), "super-secret-value") {
		t.Fatal("Agent message or artifact contains plaintext secret")
	}
	if strings.Contains(prepared.Message, "review_context.json") {
		t.Fatal("Agent message references review_context.json")
	}
	if !strings.Contains(prepared.Message, "Review scope (requested paths):\n- foo.go") {
		t.Fatalf("Agent message does not contain the requested path scope:\n%s", prepared.Message)
	}
	if len(prepared.Bootstrap.Files) != 1 || prepared.Bootstrap.Files[0].Target != "work/inputs/change.diff" {
		t.Fatalf("bootstrap files = %#v", prepared.Bootstrap.Files)
	}
	if got := prepared.Bootstrap.Files[0].Input.From; got != "artifact://review_input.diff@3" {
		t.Fatalf("artifact bootstrap source = %q", got)
	}
	if store.info.SessionID != "task-1" {
		t.Fatalf("artifact session = %#v", store.info)
	}
}

func TestPrepareRejectsOversizedDiffFile(t *testing.T) {
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	writeTestFile(t, diffPath, strings.Repeat("x", 65))
	preparer, err := NewPreparer(
		&memoryArtifactStore{},
		redact.New(),
		Config{Limits: Limits{MaxDiffBytes: 64}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-large-diff", AppName: "app", UserID: "user",
	}, Spec{DiffFile: diffPath})
	if err == nil || !strings.Contains(err.Error(), "64-byte limit") {
		t.Fatalf("Prepare error = %v, want diff byte limit", err)
	}
}

func TestPrepareRejectsOversizedGitOutput(t *testing.T) {
	repo := newGitRepo(t)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = 1\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-m", "base")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = \""+strings.Repeat("x", 256)+"\"\n")
	if diff := runGit(t, repo, "diff", "--binary", "HEAD", "--"); len(diff) <= 64 {
		t.Fatalf("test Git diff length = %d, want more than 64", len(diff))
	}
	_, err := (gitClient{
		timeout:        time.Second,
		maxOutputBytes: 64,
		maxDiffBytes:   1024,
	}).run(context.Background(), repo, nil, "diff", "--binary", "HEAD", "--")
	if err == nil {
		t.Fatal("bounded Git client accepted oversized output")
	}

	preparer, err := NewPreparer(
		&memoryArtifactStore{},
		redact.New(),
		Config{
			TempRoot: t.TempDir(),
			Limits: Limits{
				MaxDiffBytes:      1024,
				MaxGitOutputBytes: 64,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-large-git-output", AppName: "app", UserID: "user",
	}, Spec{RepoPath: repo})
	if err == nil || !strings.Contains(err.Error(), "git output exceeds the 64-byte limit") {
		t.Fatalf("Prepare error = %v, want Git output byte limit", err)
	}
}

func TestPrepareRejectsOversizedSnapshotFile(t *testing.T) {
	repo := newGitRepo(t)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = 1\n")
	writeTestFile(t, filepath.Join(repo, "large.bin"), strings.Repeat("x", 65))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-m", "base")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = 2\n")

	preparer, err := NewPreparer(
		&memoryArtifactStore{},
		redact.New(),
		Config{
			TempRoot: t.TempDir(),
			Limits: Limits{
				MaxSnapshotFileBytes: 64,
				MaxSnapshotBytes:     1024,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-large-snapshot-file", AppName: "app", UserID: "user",
	}, Spec{RepoPath: repo})
	if err == nil || !strings.Contains(err.Error(), "large.bin exceeds the 64-byte snapshot file limit") {
		t.Fatalf("Prepare error = %v, want snapshot file byte limit", err)
	}
}

func TestPrepareRejectsOversizedSnapshotTotal(t *testing.T) {
	repo := newGitRepo(t)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = 1\n")
	writeTestFile(t, filepath.Join(repo, "first.txt"), strings.Repeat("a", 40))
	writeTestFile(t, filepath.Join(repo, "second.txt"), strings.Repeat("b", 40))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-m", "base")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = 2\n")

	preparer, err := NewPreparer(
		&memoryArtifactStore{},
		redact.New(),
		Config{
			TempRoot: t.TempDir(),
			Limits: Limits{
				MaxSnapshotFileBytes: 1024,
				MaxSnapshotBytes:     100,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-large-snapshot", AppName: "app", UserID: "user",
	}, Spec{RepoPath: repo})
	if err == nil || !strings.Contains(err.Error(), "100-byte snapshot limit") {
		t.Fatalf("Prepare error = %v, want snapshot total byte limit", err)
	}
}

func TestPrepareRepoIncludesTrackedAndUntrackedChanges(t *testing.T) {
	repo := newGitRepo(t)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(repo, "foo.go"), "package review\n\nfunc Value() int { return 1 }\n")
	runGit(t, repo, "add", "go.mod", "foo.go")
	runGit(t, repo, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-m", "base")
	writeTestFile(t, filepath.Join(repo, "foo.go"), "package review\n\nfunc Value() int { return 2 }\n")
	writeTestFile(t, filepath.Join(repo, "foo_test.go"), "package review\n\nfunc TestValue(t *testing.T) {}\n")

	store := &memoryArtifactStore{}
	preparer, err := NewPreparer(store, redact.New(), Config{TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-repo", AppName: "app", UserID: "user",
	}, Spec{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}

	if prepared.ReviewMode != ReviewModeRepoBacked || len(prepared.Bootstrap.Files) != 2 {
		t.Fatalf("repo bootstrap = %#v", prepared.Bootstrap.Files)
	}
	paths := map[string]bool{}
	for _, file := range prepared.parsed.ChangedFiles {
		paths[file.Path] = true
	}
	if !paths["foo.go"] || !paths["foo_test.go"] {
		t.Fatalf("changed paths = %#v", paths)
	}
	if len(prepared.parsed.GoPackages) != 1 || prepared.parsed.GoPackages[0].ImportPath != "example.com/review" {
		t.Fatalf("Go packages = %#v", prepared.parsed.GoPackages)
	}
	snapshot := strings.TrimPrefix(prepared.Bootstrap.Files[1].Input.From, "host://")
	if _, err := os.Stat(filepath.Join(snapshot, "foo_test.go")); err != nil {
		t.Fatalf("snapshot missing untracked file: %v", err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot still exists after Close: %v", err)
	}
}

func TestPrepareRepoDirectoryPathScope(t *testing.T) {
	repo := newGitRepo(t)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(repo, "internal", "a.go"), "package internal\n\nconst A = 1\n")
	writeTestFile(t, filepath.Join(repo, "root.go"), "package review\n\nconst Root = 1\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-m", "base")
	writeTestFile(t, filepath.Join(repo, "internal", "a.go"), "package internal\n\nconst A = 2\n")
	writeTestFile(t, filepath.Join(repo, "root.go"), "package review\n\nconst Root = 2\n")

	preparer, err := NewPreparer(&memoryArtifactStore{}, redact.New(), Config{TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-directory-scope", AppName: "app", UserID: "user",
	}, Spec{RepoPath: repo, Paths: []string{"internal"}})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.parsed.ChangedFiles) != 1 ||
		prepared.parsed.ChangedFiles[0].Path != "internal/a.go" {
		t.Fatalf("directory-scoped changed files = %#v", prepared.parsed.ChangedFiles)
	}
}

func TestPrepareRepoSkipsCheckedOutSubmoduleGitlink(t *testing.T) {
	submodule := newGitRepo(t)
	writeTestFile(t, filepath.Join(submodule, "nested.txt"), "submodule content\n")
	runGit(t, submodule, "add", "nested.txt")
	runGit(t, submodule, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-m", "base")

	repo := newGitRepo(t)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = 1\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", submodule, "third_party/nested")
	runGit(t, repo, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-am", "base")
	writeTestFile(t, filepath.Join(repo, "value.go"), "package review\n\nconst Value = 2\n")

	preparer, err := NewPreparer(&memoryArtifactStore{}, redact.New(), Config{TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-submodule", AppName: "app", UserID: "user",
	}, Spec{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	snapshot := strings.TrimPrefix(prepared.Bootstrap.Files[1].Input.From, "host://")
	if _, err := os.Lstat(filepath.Join(snapshot, "third_party", "nested")); !os.IsNotExist(err) {
		t.Fatalf("submodule gitlink copied into snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "value.go")); err != nil {
		t.Fatalf("superproject file missing from snapshot: %v", err)
	}
}

func TestPrepareDiffAndRepoAppliesPatchToSnapshot(t *testing.T) {
	repo := newGitRepo(t)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(repo, "foo.go"), "package review\n\nfunc Value() int { return 1 }\n")
	runGit(t, repo, "add", "go.mod", "foo.go")
	runGit(t, repo, "-c", "user.name=Review Test", "-c", "user.email=review@example.com", "commit", "-m", "base")
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	writeTestFile(t, diffPath, "diff --git a/foo.go b/foo.go\n--- a/foo.go\n+++ b/foo.go\n@@ -1,3 +1,3 @@\n package review\n \n-func Value() int { return 1 }\n+func Value() int { return 2 }\n")

	preparer, err := NewPreparer(&memoryArtifactStore{}, redact.New(), Config{TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(context.Background(), TaskScope{
		TaskID: "task-patch", AppName: "app", UserID: "user",
	}, Spec{DiffFile: diffPath, RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	snapshot := strings.TrimPrefix(prepared.Bootstrap.Files[1].Input.From, "host://")
	data, err := os.ReadFile(filepath.Join(snapshot, "foo.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "return 2") {
		t.Fatalf("diff was not applied to snapshot: %s", data)
	}
}

func TestResolveSpecRejectsEscapingPath(t *testing.T) {
	_, err := resolveSpec(Spec{DiffFile: "change.diff", Paths: []string{"../secret"}}, "")
	if err == nil {
		t.Fatal("escaping path unexpectedly accepted")
	}
}

func TestPrepareRepoBootstrapStagesDeclaredWorkspaceLayout(t *testing.T) {
	ctx := context.Background()
	service := inmemory.NewService()
	preparer, err := NewPreparer(service, redact.New(), Config{
		FixtureRoot: filepath.Join("..", "..", "testdata", "fixtures"),
		TempRoot:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := TaskScope{TaskID: "task-layout", AppName: "app", UserID: "user"}
	prepared, err := preparer.Prepare(ctx, scope, Spec{Fixture: "input-repo"})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	ctx = codeexecutor.WithArtifactService(ctx, service)
	ctx = codeexecutor.WithArtifactSession(ctx, artifact.SessionInfo{
		AppName: scope.AppName, UserID: scope.UserID, SessionID: scope.TaskID,
	})
	executor := localexec.New(localexec.WithWorkDir(t.TempDir()))
	execTool := workspaceexec.NewExecTool(
		executor,
		workspaceexec.WithWorkspaceBootstrap(prepared.Bootstrap),
	)
	result, err := execTool.Call(ctx, []byte(
		`{"command":"cat work/inputs/change.diff && cat work/inputs/repo/value.go"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "func Value() int") {
		t.Fatalf("workspace layout did not expose repo/value.go: %s", encoded)
	}
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
