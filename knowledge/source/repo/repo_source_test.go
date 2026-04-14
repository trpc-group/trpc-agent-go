//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func TestReadDocumentsFromRemoteBranch(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// MainBranch marks the main branch.\nfunc MainBranch() {}\n",
		},
	}, {
		branch: "feature",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// FeatureBranch marks the feature branch.\nfunc FeatureBranch() {}\n",
		},
	}}, nil)

	src := New(nil, WithRepository(Repository{URL: remoteURL, Branch: "feature"}))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	if findDocByFullName(docs, "example.com/demo.FeatureBranch") == nil {
		t.Fatal("expected feature branch document")
	}
	if findDocByFullName(docs, "example.com/demo.MainBranch") != nil {
		t.Fatal("did not expect main branch document")
	}
	assertBranchMetadata(t, docs, "feature")
}

func TestReadDocumentsFromRemoteTag(t *testing.T) {
	remoteURL, tags := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// TaggedVersion marks the tagged revision.\nfunc TaggedVersion() {}\n",
		},
		tag: "v1.0.0",
	}, {
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// HeadVersion marks the head revision.\nfunc HeadVersion() {}\n",
		},
	}}, nil)

	src := New(nil, WithRepository(Repository{URL: remoteURL, Tag: "v1.0.0"}))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	if findDocByFullName(docs, "example.com/demo.TaggedVersion") == nil {
		t.Fatal("expected tagged version document")
	}
	if findDocByFullName(docs, "example.com/demo.HeadVersion") != nil {
		t.Fatal("did not expect head version document")
	}
	assertBranchMetadata(t, docs, "v1.0.0")
	if tags["v1.0.0"] == "" {
		t.Fatal("expected tag sha to be recorded")
	}
}

func TestReadDocumentsFromRemoteCommit(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// FirstVersion marks the first revision.\nfunc FirstVersion() {}\n",
		},
	}, {
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// SecondVersion marks the second revision.\nfunc SecondVersion() {}\n",
		},
	}}, nil)
	firstSHA := latestCommitSHA(t, remoteURL, "main~1")

	src := New(nil, WithRepository(Repository{URL: remoteURL, Commit: firstSHA}))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	if findDocByFullName(docs, "example.com/demo.FirstVersion") == nil {
		t.Fatal("expected first revision document")
	}
	if findDocByFullName(docs, "example.com/demo.SecondVersion") != nil {
		t.Fatal("did not expect second revision document")
	}
	assertBranchMetadata(t, docs, firstSHA)
}

func TestReadDocumentsRemoteVersionPriorityPrefersCommit(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// FirstVersion marks the first revision.\nfunc FirstVersion() {}\n",
		},
		tag: "v1.0.0",
	}, {
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// SecondVersion marks the second revision.\nfunc SecondVersion() {}\n",
		},
	}, {
		branch: "feature",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\n// FeatureVersion marks the feature revision.\nfunc FeatureVersion() {}\n",
		},
	}}, nil)
	firstSHA := latestCommitSHA(t, remoteURL, "main~1")

	src := New(nil, WithRepository(Repository{
		URL:    remoteURL,
		Branch: "feature",
		Tag:    "v1.0.0",
		Commit: firstSHA,
	}))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	if findDocByFullName(docs, "example.com/demo.FirstVersion") == nil {
		t.Fatal("expected commit-priority document")
	}
	if findDocByFullName(docs, "example.com/demo.SecondVersion") != nil {
		t.Fatal("did not expect head revision document")
	}
	if findDocByFullName(docs, "example.com/demo.FeatureVersion") != nil {
		t.Fatal("did not expect feature branch document")
	}
	assertBranchMetadata(t, docs, firstSHA)
}

func TestReadDocumentsRemoteBranchNotFound(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\nfunc MainBranch() {}\n",
		},
	}}, nil)

	src := New(nil, WithRepository(Repository{URL: remoteURL, Branch: "missing-branch"}))
	_, err := src.ReadDocuments(context.Background())
	if err == nil {
		t.Fatal("expected error for missing remote branch")
	}
}

func TestReadDocumentsFromLocalRepoAlignsMetadata(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "service.go"), `package demo

import "context"

// Service serves requests.
type Service struct{}

// Do runs the service logic.
func (s *Service) Do(ctx context.Context) error { return nil }
`)

	src := New([]string{repoRoot}, WithRepoName("demo-repo"), WithRepoURL("https://example.com/demo.git"), WithBranch("main"))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected repository documents")
	}

	var methodDocFound bool
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_full_name"] == "example.com/demo.Service.Do" {
			methodDocFound = true
			assertEqual(t, doc.Metadata[source.MetaSource], source.TypeRepo)
			assertEqual(t, doc.Metadata[source.MetaRepoName], "demo-repo")
			assertEqual(t, doc.Metadata[source.MetaRepoURL], "https://example.com/demo.git")
			assertEqual(t, doc.Metadata[source.MetaBranch], "main")
			assertEqual(t, doc.Metadata["trpc_ast_file_path"], "service.go")

			var payload map[string]any
			if err := json.Unmarshal([]byte(doc.EmbeddingText), &payload); err != nil {
				t.Fatalf("failed to unmarshal embedding text: %v", err)
			}
			assertEqual(t, payload["id"], "example.com/demo.Service.Do")
		}
	}
	if !methodDocFound {
		t.Fatal("expected method document not found")
	}
}

func TestReadDocumentsSkipsGeneratedFiles(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "keep.go"), "package demo\n\nfunc Keep() {}\n")
	writeRepoFile(t, filepath.Join(repoRoot, "skip.pb.go"), "package demo\n\nfunc Skip() {}\n")
	writeRepoFile(t, filepath.Join(repoRoot, "api.proto"), `syntax = "proto3";
package demo;

message KeepProto { string name = 1; }
`)
	writeRepoFile(t, filepath.Join(repoRoot, "api.pb.proto"), `syntax = "proto3";
package demo;

message SkipProto { string name = 1; }
`)

	src := New([]string{repoRoot}, WithSkipSuffixes([]string{".pb.go", ".pb.proto", ".trpc.go", "_mock.go"}))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_file_path"] == "skip.pb.go" {
			t.Fatal("generated file should have been skipped")
		}
		if doc.Metadata["trpc_ast_file_path"] == "api.pb.proto" {
			t.Fatal("generated proto file should have been skipped")
		}
	}
}

func TestReadDocumentsParserTaskRespectsSubdirFilter(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "service.go"), "package demo\n\ntype Root struct{}\n")
	writeRepoFile(t, filepath.Join(repoRoot, "internal", "api.go"), "package internal\n\ntype Internal struct{}\n")

	src := New([]string{repoRoot}, WithSubdir("internal"))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	for _, doc := range docs {
		if doc.Metadata["trpc_ast_file_path"] == "service.go" {
			t.Fatal("root-level Go entity should not be included when subdir=internal")
		}
	}
}

func TestReadDocumentsRejectsEscapingSubdir(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "service.go"), "package demo\n\nfunc Root() {}\n")

	src := New([]string{repoRoot}, WithSubdir("../outside"))
	_, err := src.ReadDocuments(context.Background())
	if err == nil {
		t.Fatal("expected error for escaping subdir")
	}
	if !strings.Contains(err.Error(), "escapes repository root") {
		t.Fatalf("expected escaping subdir error, got %v", err)
	}
}

func TestReadDocumentsRejectsAbsoluteSubdir(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")

	src := New([]string{repoRoot}, WithSubdir(filepath.Join(string(filepath.Separator), "tmp", "demo")))
	_, err := src.ReadDocuments(context.Background())
	if err == nil {
		t.Fatal("expected error for absolute subdir")
	}
	if !strings.Contains(err.Error(), "must be relative to repository root") {
		t.Fatalf("expected absolute subdir error, got %v", err)
	}
}

func TestReadDocumentsFromModuleParsesCrossFilePackage(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "service.go"), `package demo

type Service struct{}
`)
	writeRepoFile(t, filepath.Join(repoRoot, "method.go"), `package demo

func (s *Service) Do() error { return nil }
`)

	src := New([]string{repoRoot})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	var foundService, foundMethod bool
	var serviceCount, methodCount int
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_full_name"] == "example.com/demo.Service" {
			foundService = true
			serviceCount++
		}
		if doc.Metadata["trpc_ast_full_name"] == "example.com/demo.Service.Do" {
			foundMethod = true
			methodCount++
			assertEqual(t, doc.Metadata["trpc_ast_file_path"], "method.go")
		}
	}
	if !foundService || !foundMethod {
		t.Fatalf("expected both service and method docs, got service=%v method=%v", foundService, foundMethod)
	}
	assertEqual(t, serviceCount, 1)
	assertEqual(t, methodCount, 1)
}

func TestReadDocumentsRejectsMultipleRepositoriesPerSource(t *testing.T) {
	repoRoot := t.TempDir()
	src := New(nil, WithRepository(
		Repository{Dir: repoRoot},
		Repository{URL: "https://example.com/demo.git"},
	))

	_, err := src.ReadDocuments(context.Background())
	if err == nil {
		t.Fatal("expected error for multiple repositories per source")
	}
}

func TestResolvedInputsUsesStructuredOptions(t *testing.T) {
	src := New(nil, WithRepoURLs("https://example.com/demo.git"), WithDirs("/tmp/demo"))
	inputs := src.resolvedInputs()
	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}
	assertEqual(t, inputs[0], "https://example.com/demo.git")
	assertEqual(t, inputs[1], "/tmp/demo")
}

func TestFirstNonEmpty(t *testing.T) {
	assertEqual(t, firstNonEmpty("commit-sha", "v1.0.0", "main"), "commit-sha")
	assertEqual(t, firstNonEmpty("", "v1.0.0", "main"), "v1.0.0")
	assertEqual(t, firstNonEmpty("", "", "main"), "main")
	assertEqual(t, firstNonEmpty("", "", ""), "")
}

func TestResolvedRepositoriesUsesStructuredRepositories(t *testing.T) {
	src := New(nil, WithRepository(
		Repository{URL: "https://example.com/demo.git", Branch: "main"},
		Repository{Dir: "/tmp/demo", Tag: "v1.0.0"},
	))
	repositories := src.resolvedRepositories()
	if len(repositories) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(repositories))
	}
	assertEqual(t, repositories[0].URL, "https://example.com/demo.git")
	assertEqual(t, repositories[0].Branch, "main")
	assertEqual(t, repositories[1].Dir, "/tmp/demo")
	assertEqual(t, repositories[1].Tag, "v1.0.0")
}

func TestWithFileExtensionsCopiesCallerSlice(t *testing.T) {
	extensions := []string{".go", ".proto"}
	src := New(nil, WithFileExtensions(extensions))

	extensions[0] = ".md"

	if got, want := len(src.fileExtensions), 2; got != want {
		t.Fatalf("fileExtensions length = %d, want %d", got, want)
	}
	assertEqual(t, src.fileExtensions[0], ".go")
	assertEqual(t, src.fileExtensions[1], ".proto")
}

func TestOptionSettersBasicCoverage(t *testing.T) {
	src := New(
		nil,
		WithName("repo-src"),
		WithMetadata(map[string]any{"k": "v"}),
		WithMetadataValue("k2", "v2"),
		WithTag("v1.0.0"),
		WithCommit("commit-sha"),
		WithSkipDirs([]string{"vendor", "third_party"}),
	)

	assertEqual(t, src.name, "repo-src")
	assertEqual(t, src.metadata["k"], "v")
	assertEqual(t, src.metadata["k2"], "v2")
	assertEqual(t, src.tag, "v1.0.0")
	assertEqual(t, src.commit, "commit-sha")
	if len(src.skipDirs) != 2 {
		t.Fatalf("skipDirs len = %d, want 2", len(src.skipDirs))
	}
}

func TestSourceMetadataAndHelpers(t *testing.T) {
	src := New(nil,
		WithName("repo-src"),
		WithMetadata(map[string]any{"k": "v"}),
	)

	assertEqual(t, src.Name(), "repo-src")
	assertEqual(t, src.Type(), source.TypeRepo)

	meta := src.GetMetadata()
	assertEqual(t, meta["k"], "v")
	meta["k"] = "changed"
	assertEqual(t, src.metadata["k"], "v")

	if !src.shouldSkipDir(".git") {
		t.Fatal(".git should be skipped by default")
	}
	src.skipSuffixes = []string{".pb.go"}
	if !src.shouldSkipFile("xx.pb.go") {
		t.Fatal("xx.pb.go should be skipped by suffix")
	}
}

func TestResolveScanRootAndRelativePathHelpers(t *testing.T) {
	repoRoot := t.TempDir()

	root, err := resolveScanRoot(repoRoot, "")
	if err != nil {
		t.Fatalf("resolveScanRoot empty subdir error = %v", err)
	}
	assertEqual(t, root, repoRoot)

	root, err = resolveScanRoot(repoRoot, "a/b")
	if err != nil {
		t.Fatalf("resolveScanRoot relative subdir error = %v", err)
	}
	assertEqual(t, root, filepath.Join(repoRoot, filepath.Clean("a/b")))

	if _, err := resolveScanRoot(repoRoot, "../escape"); err == nil {
		t.Fatal("expected error when subdir escapes repo root")
	}

	abs := filepath.Join(repoRoot, "a", "b.go")
	rel := toRelativeRepoPath(repoRoot, abs)
	assertEqual(t, rel, "a/b.go")

	rel = toRelativeRepoPath(repoRoot, "x/y.go")
	assertEqual(t, rel, "x/y.go")

	rel = toRelativeRepoPath(repoRoot, nil)
	assertEqual(t, rel, "")
}

func TestLooksLikeGitURLAndChooseRepoHelpers(t *testing.T) {
	if !looksLikeGitURL("https://github.com/trpc-group/trpc-agent-go") {
		t.Fatal("https URL should be treated as git URL")
	}
	if looksLikeGitURL("./local/path") {
		t.Fatal("local path should not be treated as git URL")
	}

	assertEqual(t, chooseRepoName("explicit", "https://github.com/a/b.git", "/tmp/fallback"), "explicit")
	assertEqual(t, chooseRepoName("", "https://github.com/a/b.git", "/tmp/fallback"), "b")
	assertEqual(t, chooseRepoURL("explicit-url", "https://github.com/a/b.git"), "explicit-url")
	assertEqual(t, chooseRepoURL("", "https://github.com/a/b.git"), "https://github.com/a/b.git")
	assertEqual(t, firstNonEmpty("", "x", "y"), "x")
}

func TestCloneRemoteRepositoryWithBranchTagAndDefault(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod": "module example.com/demo\n\ngo 1.21\n",
		},
		tag: "v1.0.0",
	}}, nil)

	for _, tc := range []struct {
		name string
		repo Repository
	}{
		{name: "default", repo: Repository{URL: remoteURL}},
		{name: "branch", repo: Repository{URL: remoteURL, Branch: "main"}},
		{name: "tag", repo: Repository{URL: remoteURL, Tag: "v1.0.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			if _, err := cloneRemoteRepository(context.Background(), tc.repo, tmp); err != nil {
				t.Fatalf("cloneRemoteRepository(%s) error = %v", tc.name, err)
			}
		})
	}
}

func TestCloneRemoteRepositoryUnknownTargetKind(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files:  map[string]string{"go.mod": "module example.com/demo\n\ngo 1.21\n"},
	}}, nil)
	tmp := t.TempDir()

	if _, err := cloneRemoteRepository(context.Background(), Repository{URL: remoteURL, Branch: "main"}, tmp); err != nil {
		t.Fatalf("cloneRemoteRepository baseline error = %v", err)
	}
	if _, err := resolveScanRoot(tmp, "../../escape"); err == nil {
		t.Fatal("expected resolveScanRoot to reject escaping path")
	}
}

func TestInitializeReadersWithTransformerOptionCoverage(t *testing.T) {
	src := New(nil, WithTransformers(noopTransformer{}))
	if src.readers == nil || len(src.readers) == 0 {
		t.Fatal("expected readers to be initialized")
	}
}

func TestGetFilePathsHonorsSkipAndExtensions(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "keep.go"), "package demo\n")
	writeRepoFile(t, filepath.Join(repoRoot, "keep.md"), "# keep\n")
	writeRepoFile(t, filepath.Join(repoRoot, "skip.pb.go"), "package demo\n")
	writeRepoFile(t, filepath.Join(repoRoot, "vendor", "x.go"), "package vendor\n")
	writeRepoFile(t, filepath.Join(repoRoot, ".git", "HEAD"), "ref: refs/heads/main\n")

	src := New(nil,
		WithFileExtensions([]string{".go"}),
		WithSkipDirs([]string{".git", "vendor"}),
		WithSkipSuffixes([]string{".pb.go"}),
	)
	filePaths, err := src.getFilePaths(repoRoot)
	if err != nil {
		t.Fatalf("getFilePaths() error = %v", err)
	}
	if len(filePaths) != 1 {
		t.Fatalf("len(filePaths) = %d, want 1", len(filePaths))
	}
	if filepath.Base(filePaths[0]) != "keep.go" {
		t.Fatalf("filePaths[0] = %s, want keep.go", filePaths[0])
	}
}

func TestIsUnpopulatedGitLink(t *testing.T) {
	root := t.TempDir()

	unpopulated := filepath.Join(root, "sub1")
	if err := os.MkdirAll(unpopulated, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", unpopulated, err)
	}
	if err := os.WriteFile(filepath.Join(unpopulated, ".git"), []byte("gitdir: ../.git/modules/sub1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	if !isUnpopulatedGitLink(unpopulated) {
		t.Fatal("expected unpopulated submodule link")
	}

	populated := filepath.Join(root, "sub2")
	if err := os.MkdirAll(populated, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", populated, err)
	}
	if err := os.WriteFile(filepath.Join(populated, ".git"), []byte("gitdir: ../.git/modules/sub2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(populated, "main.go"), []byte("package demo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(main.go) error = %v", err)
	}
	if isUnpopulatedGitLink(populated) {
		t.Fatal("expected populated submodule directory to be allowed")
	}

	normal := filepath.Join(root, "normal")
	if err := os.MkdirAll(filepath.Join(normal, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(normal/.git) error = %v", err)
	}
	if isUnpopulatedGitLink(normal) {
		t.Fatal("directory with .git dir should not be treated as unpopulated submodule")
	}
}

func TestResolveRepositoryLocalPathMustBeDirectory(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "not-dir.txt")
	writeRepoFile(t, filePath, "x")

	src := New(nil)
	_, _, _, err := src.resolveRepository(context.Background(), Repository{Dir: filePath})
	if err == nil {
		t.Fatal("expected error for non-directory local repository path")
	}
}

func TestCloneRemoteRepositoryCommitFetchFailure(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod": "module example.com/demo\n\ngo 1.21\n",
		},
	}}, nil)
	tmp := t.TempDir()

	_, err := cloneRemoteRepository(context.Background(), Repository{URL: remoteURL, Commit: "deadbeef"}, tmp)
	if err == nil {
		t.Fatal("expected cloneRemoteRepository commit fetch failure")
	}
}

func TestLooksLikeGitURLAdditionalCases(t *testing.T) {
	if !looksLikeGitURL("git@github.com:trpc-group/trpc-agent-go.git") {
		t.Fatal("git@ URL should be treated as git URL")
	}
	if !looksLikeGitURL("ssh://git@github.com/trpc-group/trpc-agent-go.git") {
		t.Fatal("ssh:// URL should be treated as git URL")
	}
	if looksLikeGitURL("C:/work/repo") {
		t.Fatal("local windows-like path should not be treated as git URL")
	}
}

func TestRunGitErrorPath(t *testing.T) {
	err := runGit(context.Background(), t.TempDir(), "not-a-real-git-arg-xxx")
	if err == nil {
		t.Fatal("expected runGit to fail for invalid args")
	}
}

type noopTransformer struct{}

func (noopTransformer) Name() string { return "noop" }

func (noopTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

func (noopTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

var _ transform.Transformer = noopTransformer{}
var _ io.Reader = strings.NewReader("")

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertBranchMetadata(t *testing.T, docs []*document.Document, want string) {
	t.Helper()
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
	for _, doc := range docs {
		if got := doc.Metadata[source.MetaBranch]; got != want {
			t.Fatalf("branch metadata = %v, want %v", got, want)
		}
	}
}

func findDocByFullName(docs []*document.Document, fullName string) *document.Document {
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_full_name"] == fullName {
			return doc
		}
	}
	return nil
}

type repoCommit struct {
	branch string
	files  map[string]string
	tag    string
}

func createRemoteRepo(t *testing.T, commits []repoCommit, extraBranches map[string]string) (string, map[string]string) {
	t.Helper()
	remoteRoot := t.TempDir()
	remotePath := filepath.Join(remoteRoot, "remote.git")
	runGitCommand(t, remoteRoot, "git", "init", "--bare", remotePath)

	workDir := filepath.Join(remoteRoot, "work")
	runGitCommand(t, remoteRoot, "git", "clone", remotePath, workDir)
	runGitCommand(t, workDir, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, workDir, "git", "config", "user.name", "test")

	branchSHAs := make(map[string]string)
	tagSHAs := make(map[string]string)
	for i, commit := range commits {
		branch := commit.branch
		if branch == "" {
			branch = "main"
		}
		if i == 0 {
			runGitCommand(t, workDir, "git", "checkout", "-B", branch)
		} else if _, ok := branchSHAs[branch]; ok {
			runGitCommand(t, workDir, "git", "checkout", branch)
		} else {
			runGitCommand(t, workDir, "git", "checkout", "-B", branch)
		}
		for path, content := range commit.files {
			writeRepoFile(t, filepath.Join(workDir, path), content)
		}
		runGitCommand(t, workDir, "git", "add", ".")
		runGitCommand(t, workDir, "git", "commit", "-m", fmt.Sprintf("commit-%d-%s", i, branch))
		sha := strings.TrimSpace(runGitCommand(t, workDir, "git", "rev-parse", "HEAD"))
		branchSHAs[branch] = sha
		runGitCommand(t, workDir, "git", "push", "origin", fmt.Sprintf("HEAD:%s", branch))
		if commit.tag != "" {
			runGitCommand(t, workDir, "git", "tag", "-f", commit.tag)
			runGitCommand(t, workDir, "git", "push", "origin", "--force", commit.tag)
			tagSHAs[commit.tag] = sha
		}
	}

	for branch, revision := range extraBranches {
		runGitCommand(t, workDir, "git", "branch", "-f", branch, revision)
		runGitCommand(t, workDir, "git", "push", "origin", fmt.Sprintf("%s:%s", revision, branch), "--force")
		branchSHAs[branch] = strings.TrimSpace(runGitCommand(t, workDir, "git", "rev-parse", revision))
	}

	if _, ok := branchSHAs["main"]; ok {
		runGitCommand(t, remotePath, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	}

	return "file://" + filepath.ToSlash(remotePath), tagSHAs
}

func latestCommitSHA(t *testing.T, remoteURL string, revision string) string {
	t.Helper()
	cloneDir := t.TempDir()
	runGitCommand(t, cloneDir, "git", "clone", remoteURL, filepath.Join(cloneDir, "repo"))
	return strings.TrimSpace(runGitCommand(t, filepath.Join(cloneDir, "repo"), "git", "rev-parse", revision))
}

func runGitCommand(t *testing.T, cwd string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\n%s", name, args, err, string(output))
	}
	return string(output)
}

func writeRepoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
