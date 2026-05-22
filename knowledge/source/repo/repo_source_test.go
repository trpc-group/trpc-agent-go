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
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	docreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

func init() {
	docreader.RegisterReader([]string{".go"}, func(opts ...docreader.Option) docreader.Reader {
		return &testGoReader{}
	})
	docreader.RegisterReader([]string{".proto"}, func(opts ...docreader.Option) docreader.Reader {
		return &testProtoReader{}
	})
	codeast.RegisterDirectoryParser(codeast.FileTypeGo, &stubDirectoryParser{})
}

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

	src := New(WithRepository(Repository{URL: remoteURL, Branch: "feature"}))
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

	src := New(WithRepository(Repository{URL: remoteURL, Tag: "v1.0.0"}))
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

	src := New(WithRepository(Repository{URL: remoteURL, Commit: firstSHA}))
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

	src := New(WithRepository(Repository{
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

	src := New(WithRepository(Repository{URL: remoteURL, Branch: "missing-branch"}))
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

	src := New(WithRepository(Repository{
		Dir:         repoRoot,
		RepoName:    "demo-repo",
		Description: "demo repository for tests",
		RepoURL:     "https://example.com/demo.git",
		Branch:      "main",
	}))
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

	src := New(WithRepository(Repository{Dir: repoRoot}),
		WithSkipSuffixes([]string{".pb.go", ".pb.proto", ".trpc.go", "_mock.go"}),
	)
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

	src := New(WithRepository(Repository{Dir: repoRoot, Subdir: "internal"}))
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

	src := New(WithRepository(Repository{Dir: repoRoot, Subdir: "../outside"}))
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

	src := New(WithRepository(Repository{
		Dir:    repoRoot,
		Subdir: filepath.Join(string(filepath.Separator), "tmp", "demo"),
	}))
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

	src := New(WithRepository(Repository{Dir: repoRoot}))
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

func TestWithRepositoryLastOptionWins(t *testing.T) {
	secondRepo := t.TempDir()
	src := New(WithRepository(Repository{Dir: t.TempDir()}),
		WithRepository(Repository{Dir: secondRepo}),
	)

	assertEqual(t, src.repository.Dir, secondRepo)
}

func TestFirstNonEmpty(t *testing.T) {
	assertEqual(t, firstNonEmpty("commit-sha", "v1.0.0", "main"), "commit-sha")
	assertEqual(t, firstNonEmpty("", "v1.0.0", "main"), "v1.0.0")
	assertEqual(t, firstNonEmpty("", "", "main"), "main")
	assertEqual(t, firstNonEmpty("", "", ""), "")
}

func TestResolvedRepositoryUsesStructuredRepository(t *testing.T) {
	src := New(WithRepository(Repository{URL: "https://example.com/demo.git", Branch: "main"}))
	assertEqual(t, src.repository.URL, "https://example.com/demo.git")
	assertEqual(t, src.repository.Branch, "main")
}

func TestResolvedRepositoryAndDescriptorEdgeCases(t *testing.T) {
	t.Run("missing repository returns false", func(t *testing.T) {
		src := New()
		if _, _, ok := src.RepositoryDescriptor(); ok {
			t.Fatal("expected no repository descriptor")
		}
		if _, err := src.ReadDocuments(context.Background()); err == nil {
			t.Fatal("expected missing repository error")
		}
	})

	t.Run("descriptor uses configured dir and description", func(t *testing.T) {
		repoDir := filepath.Join(t.TempDir(), "demo-repo")
		src := New(WithRepository(Repository{
			Dir:         repoDir,
			Description: "demo repository",
		}))

		name, description, ok := src.RepositoryDescriptor()
		if !ok {
			t.Fatal("expected repository descriptor")
		}
		assertEqual(t, name, "demo-repo")
		assertEqual(t, description, "demo repository")
	})

	t.Run("descriptor rejects empty repository", func(t *testing.T) {
		src := New(WithRepository(Repository{}))
		if _, _, ok := src.RepositoryDescriptor(); ok {
			t.Fatal("expected no repository descriptor")
		}
	})
}

func TestWithFileExtensionsCopiesCallerSlice(t *testing.T) {
	extensions := []string{".go", ".proto"}
	src := New(WithFileExtensions(extensions))

	extensions[0] = ".md"

	if got, want := len(src.fileExtensions), 2; got != want {
		t.Fatalf("fileExtensions length = %d, want %d", got, want)
	}
	assertEqual(t, src.fileExtensions[0], ".go")
	assertEqual(t, src.fileExtensions[1], ".proto")
}

func TestOptionSettersBasicCoverage(t *testing.T) {
	src := New(WithName("repo-src"),
		WithMetadata(map[string]any{"k": "v"}),
		WithMetadataValue("k2", "v2"),
		WithRepository(Repository{Dir: "/tmp/repo", Tag: "v1.0.0", Commit: "commit-sha"}),
		WithSkipDirs([]string{"vendor", "third_party"}),
	)

	assertEqual(t, src.name, "repo-src")
	assertEqual(t, src.metadata["k"], "v")
	assertEqual(t, src.metadata["k2"], "v2")
	assertEqual(t, src.repository.Tag, "v1.0.0")
	assertEqual(t, src.repository.Commit, "commit-sha")
	if len(src.skipDirs) != 2 {
		t.Fatalf("skipDirs len = %d, want 2", len(src.skipDirs))
	}
}

func TestSourceMetadataAndHelpers(t *testing.T) {
	src := New(WithName("repo-src"),
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
	src := New(WithTransformers(noopTransformer{}))
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

	src := New(WithFileExtensions([]string{".go"}),
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

	src := New()
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

func TestProcessFileMetadataAndErrors(t *testing.T) {
	t.Run("success metadata and file node rename", func(t *testing.T) {
		repoRoot := t.TempDir()
		filePath := filepath.Join(repoRoot, "docs", "readme.go")
		writeRepoFile(t, filePath, "package demo\n")

		src := New(WithName("repo-src"), WithMetadata(map[string]any{"k": "v"}))
		src.readers = map[string]docreader.Reader{
			"go": &stubReader{
				fileDocs: []*document.Document{
					{},
					{Metadata: map[string]any{"trpc_ast_type": "file"}},
				},
			},
		}

		docs, err := src.processFile(filePath, repoRoot, &repoInfo{
			name:   "repo",
			url:    "https://example.com/repo.git",
			branch: "main",
		})
		if err != nil {
			t.Fatalf("processFile() error = %v", err)
		}
		if len(docs) != 2 {
			t.Fatalf("len(docs) = %d, want 2", len(docs))
		}

		relPath := "docs/readme.go"
		for _, doc := range docs {
			assertEqual(t, doc.Metadata[source.MetaSource], source.TypeRepo)
			assertEqual(t, doc.Metadata[source.MetaSourceName], "repo-src")
			assertEqual(t, doc.Metadata[source.MetaRepoName], "repo")
			assertEqual(t, doc.Metadata[source.MetaRepoURL], "https://example.com/repo.git")
			assertEqual(t, doc.Metadata[source.MetaBranch], "main")
			assertEqual(t, doc.Metadata[source.MetaFilePath], relPath)
			assertEqual(t, doc.Metadata["trpc_ast_file_path"], relPath)
			assertEqual(t, doc.Metadata["k"], "v")
		}
		if docs[1].Metadata["trpc_ast_name"] != relPath || docs[1].Metadata["trpc_ast_full_name"] != relPath {
			t.Fatalf("expected file node metadata to be rewritten, got name=%v full=%v", docs[1].Metadata["trpc_ast_name"], docs[1].Metadata["trpc_ast_full_name"])
		}
	})

	t.Run("stat file error", func(t *testing.T) {
		src := New()
		_, err := src.processFile(filepath.Join(t.TempDir(), "missing.go"), t.TempDir(), nil)
		if err == nil {
			t.Fatal("expected processFile stat error")
		}
	})

	t.Run("no reader available", func(t *testing.T) {
		repoRoot := t.TempDir()
		filePath := filepath.Join(repoRoot, "demo.go")
		writeRepoFile(t, filePath, "package demo\n")

		src := New()
		src.readers = map[string]docreader.Reader{}
		_, err := src.processFile(filePath, repoRoot, nil)
		if err == nil {
			t.Fatal("expected missing reader error")
		}
	})

	t.Run("reader returns error", func(t *testing.T) {
		repoRoot := t.TempDir()
		filePath := filepath.Join(repoRoot, "demo.go")
		writeRepoFile(t, filePath, "package demo\n")

		src := New()
		src.readers = map[string]docreader.Reader{
			"go": &stubReader{fileErr: fmt.Errorf("boom")},
		}
		_, err := src.processFile(filePath, repoRoot, nil)
		if err == nil {
			t.Fatal("expected reader error")
		}
	})
}

func TestProcessDirectoryMetadataFilteringAndErrors(t *testing.T) {
	t.Run("success with allowed paths and skip suffix", func(t *testing.T) {
		repoRoot := t.TempDir()
		a := filepath.Join(repoRoot, "a.go")
		b := filepath.Join(repoRoot, "b.go")
		skip := filepath.Join(repoRoot, "skip.pb.go")
		missing := filepath.Join(repoRoot, "missing.go")
		writeRepoFile(t, a, "package demo\n")
		writeRepoFile(t, b, "package demo\n")
		writeRepoFile(t, skip, "package demo\n")

		src := New(WithSkipSuffixes([]string{".pb.go"}), WithName("repo-src"), WithMetadata(map[string]any{"x": "y"}))
		src.readers = map[string]docreader.Reader{
			"go": &stubDirectoryReader{stubReader: &stubReader{}, dirDocs: []*document.Document{
				{Metadata: map[string]any{"trpc_ast_file_path": a, "trpc_ast_type": "file"}},
				{Metadata: map[string]any{source.MetaFilePath: b}},
				{Metadata: map[string]any{"trpc_ast_file_path": filepath.Join(repoRoot, "outside.go")}},
				{Metadata: map[string]any{"trpc_ast_file_path": skip}},
				{},
				{Metadata: map[string]any{"trpc_ast_file_path": missing}},
			}},
		}

		allowed := map[string]struct{}{"": {}, "a.go": {}, "b.go": {}, "skip.pb.go": {}, "missing.go": {}}
		docs, err := src.processDirectory(repoRoot, "go", repoRoot, &repoInfo{name: "repo"}, allowed)
		if err != nil {
			t.Fatalf("processDirectory() error = %v", err)
		}
		if len(docs) != 4 {
			t.Fatalf("len(docs) = %d, want 4", len(docs))
		}

		assertEqual(t, docs[0].Metadata[source.MetaFilePath], "a.go")
		assertEqual(t, docs[0].Metadata["trpc_ast_name"], "a.go")
		assertEqual(t, docs[0].Metadata["trpc_ast_full_name"], "a.go")
		assertEqual(t, docs[1].Metadata[source.MetaFilePath], "b.go")
		if _, ok := docs[2].Metadata[source.MetaFilePath]; ok {
			t.Fatalf("expected doc with empty relative path to keep no %s", source.MetaFilePath)
		}
		assertEqual(t, docs[3].Metadata[source.MetaFilePath], "missing.go")
		if _, ok := docs[3].Metadata[source.MetaFileSize]; ok {
			t.Fatal("expected missing file to skip file stat metadata enrichment")
		}
		for _, doc := range docs {
			assertEqual(t, doc.Metadata[source.MetaSource], source.TypeRepo)
			assertEqual(t, doc.Metadata[source.MetaSourceName], "repo-src")
			assertEqual(t, doc.Metadata["x"], "y")
		}
	})

	t.Run("missing reader", func(t *testing.T) {
		src := New()
		src.readers = map[string]docreader.Reader{}
		_, err := src.processDirectory(t.TempDir(), "go", t.TempDir(), nil, nil)
		if err == nil {
			t.Fatal("expected missing reader error")
		}
	})

	t.Run("reader not directory capable", func(t *testing.T) {
		src := New()
		src.readers = map[string]docreader.Reader{"go": &stubReader{}}
		_, err := src.processDirectory(t.TempDir(), "go", t.TempDir(), nil, nil)
		if err == nil {
			t.Fatal("expected not directory-capable error")
		}
	})

	t.Run("directory reader error", func(t *testing.T) {
		src := New()
		src.readers = map[string]docreader.Reader{"go": &stubDirectoryReader{stubReader: &stubReader{}, dirErr: fmt.Errorf("bad dir")}}
		_, err := src.processDirectory(t.TempDir(), "go", t.TempDir(), nil, nil)
		if err == nil {
			t.Fatal("expected directory reader error")
		}
	})
}

func TestClassifyFilesAndRelativePathExtraCoverage(t *testing.T) {
	t.Run("classify files missing reader", func(t *testing.T) {
		repoRoot := t.TempDir()
		txt := filepath.Join(repoRoot, "a.txt")
		writeRepoFile(t, txt, "x")

		src := New()
		src.readers = map[string]docreader.Reader{}
		_, err := src.classifyFiles(repoRoot, []string{txt})
		if err == nil {
			t.Fatal("expected classifyFiles missing reader error")
		}
	})

	t.Run("toRelativeRepoPath extra inputs", func(t *testing.T) {
		repoRoot := t.TempDir()
		if got := toRelativeRepoPath(repoRoot, 123); got != "" {
			t.Fatalf("toRelativeRepoPath(non-string) = %q, want empty", got)
		}
		if got := toRelativeRepoPath(repoRoot, "   "); got != "" {
			t.Fatalf("toRelativeRepoPath(blank) = %q, want empty", got)
		}
		if got := toRelativeRepoPath(repoRoot, filepath.Join(repoRoot, "..", "other", "a.go")); got != "" {
			t.Fatalf("toRelativeRepoPath(outside) = %q, want empty", got)
		}
	})
}

func TestChooseRepoNameAndLooksLikeGitURLExtraCoverage(t *testing.T) {
	if got := chooseRepoName("", "git@github.com:trpc-group/trpc-agent-go.git", "/tmp/fallback"); got != "trpc-agent-go" {
		t.Fatalf("chooseRepoName(git@...) = %q, want trpc-agent-go", got)
	}
	if got := chooseRepoName("", "", "/tmp/fallback-name"); got != "fallback-name" {
		t.Fatalf("chooseRepoName(fallback) = %q, want fallback-name", got)
	}
	if looksLikeGitURL("%") {
		t.Fatal("invalid URL should not be treated as git URL")
	}
}

func TestResolveRepositoryAndBuildBaseMetadataExtraCoverage(t *testing.T) {
	src := New()

	_, _, _, err := src.resolveRepository(context.Background(), Repository{Dir: filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		t.Fatal("expected resolveRepository stat error")
	}

	base := src.buildBaseMetadata("/tmp/repo", nil)
	if base[source.MetaSource] != source.TypeRepo {
		t.Fatalf("unexpected source type: %v", base[source.MetaSource])
	}
	if _, ok := base[source.MetaRepoName]; ok {
		t.Fatalf("did not expect repo name in metadata without repo info")
	}
}

func TestResolveRepositoryRejectsInvalidStructuredInputs(t *testing.T) {
	src := New()

	if _, _, _, err := src.resolveRepository(context.Background(), Repository{}); err == nil {
		t.Fatal("expected error when neither URL nor Dir is configured")
	}

	if _, _, _, err := src.resolveRepository(context.Background(), Repository{
		URL: "https://example.com/demo.git",
		Dir: t.TempDir(),
	}); err == nil {
		t.Fatal("expected error when both URL and Dir are configured")
	}
}

type testGoReader struct{}

func (r *testGoReader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}
	result, err := r.parseFiles(filepath.Dir(name), []testGoFile{{path: name, content: string(content)}})
	if err != nil {
		return nil, err
	}
	return testCodeASTDocs(result), nil
}

func (r *testGoReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	result, err := r.parseFiles(filepath.Dir(filePath), []testGoFile{{path: filePath, content: string(content)}})
	if err != nil {
		return nil, err
	}
	return testCodeASTDocs(result), nil
}

func (r *testGoReader) ReadFromURL(_ string) ([]*document.Document, error) {
	return nil, nil
}

func (r *testGoReader) ReadFromDirectory(dirPath string) ([]*document.Document, error) {
	result, err := r.ReadCodeASTFromDirectory(dirPath)
	if err != nil {
		return nil, err
	}
	return testCodeASTDocs(result), nil
}

func (r *testGoReader) ReadCodeASTFromDirectory(dirPath string) (*codeast.Result, error) {
	var filePaths []string
	if err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
			filePaths = append(filePaths, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(filePaths)

	files := make([]testGoFile, 0, len(filePaths))
	for _, filePath := range filePaths {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		files = append(files, testGoFile{path: filePath, content: string(content)})
	}
	return r.parseFiles(dirPath, files)
}

func (r *testGoReader) Name() string {
	return "test-go"
}

func (r *testGoReader) SupportedExtensions() []string {
	return []string{".go"}
}

func (r *testGoReader) parseFiles(baseDir string, files []testGoFile) (*codeast.Result, error) {
	moduleRoot, modulePath := testGoModule(baseDir)
	fset := token.NewFileSet()
	var nodes []*codeast.Node
	var calls []testGoCall
	var edges []*codeast.Edge

	for _, goFile := range files {
		fileNode, err := goparser.ParseFile(fset, goFile.path, goFile.content, goparser.ParseComments)
		if err != nil {
			return nil, err
		}
		pkgPath := testGoPackagePath(moduleRoot, modulePath, filepath.Dir(goFile.path), fileNode.Name.Name)
		for _, decl := range fileNode.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, ok := typeSpec.Type.(*ast.StructType); !ok {
						continue
					}
					id := pkgPath + "." + typeSpec.Name.Name
					nodes = append(nodes, testGoNode(fset, goFile, d, id, typeSpec.Name.Name, pkgPath, codeast.EntityStruct, d.Doc))
				}
			case *ast.FuncDecl:
				receiver := testGoReceiverName(d)
				name := d.Name.Name
				id := pkgPath + "." + name
				entityType := codeast.EntityFunction
				if receiver != "" {
					receiverType := strings.TrimPrefix(receiver, "*")
					receiverID := pkgPath + "." + receiverType
					id = receiverID + "." + name
					entityType = codeast.EntityMethod
					edges = append(edges, &codeast.Edge{FromID: receiverID, ToID: id, Type: codeast.RelationMethod})
				}
				node := testGoNode(fset, goFile, d, id, name, pkgPath, entityType, d.Doc)
				if receiver != "" {
					node.Metadata = map[string]any{codeast.MetadataKeyReceiverType: receiver}
				}
				nodes = append(nodes, node)
				calls = append(calls, testGoCalls(d, id, pkgPath)...)
			}
		}
	}

	nodeSet := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeSet[node.ID] = struct{}{}
	}
	for _, call := range calls {
		targetID := call.packagePath + "." + call.name
		if _, ok := nodeSet[targetID]; ok {
			edges = append(edges, &codeast.Edge{FromID: call.fromID, ToID: targetID, Type: codeast.RelationCalls})
		}
	}
	return &codeast.Result{Nodes: nodes, Edges: edges}, nil
}

type testGoFile struct {
	path    string
	content string
}

type testGoCall struct {
	fromID      string
	packagePath string
	name        string
}

func testGoModule(baseDir string) (string, string) {
	absDir, err := filepath.Abs(baseDir)
	if err != nil {
		absDir = baseDir
	}
	for dir := absDir; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		content, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "module" {
				return dir, fields[1]
			}
		}
	}
	return absDir, ""
}

func testGoPackagePath(moduleRoot, modulePath, dir, packageName string) string {
	if modulePath == "" {
		return packageName
	}
	rel, err := filepath.Rel(moduleRoot, dir)
	if err != nil || rel == "." {
		return modulePath
	}
	return filepath.ToSlash(filepath.Join(modulePath, rel))
}

func testGoNode(
	fset *token.FileSet,
	goFile testGoFile,
	node ast.Node,
	id string,
	name string,
	pkgPath string,
	entityType codeast.EntityType,
	doc *ast.CommentGroup,
) *codeast.Node {
	start := fset.Position(node.Pos()).Line
	end := fset.Position(node.End()).Line
	comment := ""
	if doc != nil {
		comment = strings.TrimSpace(doc.Text())
	}
	return &codeast.Node{
		ID:         id,
		Type:       entityType,
		Name:       name,
		FullName:   id,
		Scope:      codeast.ScopeCode,
		Language:   codeast.LanguageGo,
		Comment:    comment,
		Code:       testGoSnippet(goFile.content, start, end),
		FilePath:   goFile.path,
		LineStart:  start,
		LineEnd:    end,
		ChunkIndex: len(id),
		Package:    pkgPath,
	}
}

func testGoReceiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch expr := fn.Recv.List[0].Type.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		if ident, ok := expr.X.(*ast.Ident); ok {
			return "*" + ident.Name
		}
	}
	return ""
}

func testGoCalls(fn *ast.FuncDecl, fromID string, packagePath string) []testGoCall {
	if fn.Body == nil {
		return nil
	}
	var calls []testGoCall
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok {
			calls = append(calls, testGoCall{fromID: fromID, packagePath: packagePath, name: ident.Name})
		}
		return true
	})
	return calls
}

func testGoSnippet(content string, start, end int) string {
	if start <= 0 || end < start {
		return ""
	}
	lines := strings.Split(content, "\n")
	if start > len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

func testCodeASTDocs(result *codeast.Result) []*document.Document {
	if result == nil {
		return nil
	}
	docs := make([]*document.Document, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		metadata := map[string]any{
			"trpc_ast_type":       string(node.Type),
			"trpc_ast_name":       node.Name,
			"trpc_ast_full_name":  node.FullName,
			"trpc_ast_language":   string(node.Language),
			"trpc_ast_scope":      string(node.Scope),
			"trpc_ast_package":    node.Package,
			"trpc_ast_file_path":  node.FilePath,
			source.MetaFilePath:   node.FilePath,
			source.MetaChunkIndex: node.ChunkIndex,
		}
		for k, v := range node.Metadata {
			metadata["trpc_ast_"+k] = v
		}
		embeddingText, _ := json.Marshal(map[string]any{
			"id":        node.ID,
			"name":      node.Name,
			"type":      string(node.Type),
			"file_path": node.FilePath,
		})
		docs = append(docs, &document.Document{
			Name:          node.Name,
			Content:       node.Code,
			EmbeddingText: string(embeddingText),
			Metadata:      metadata,
		})
	}
	return docs
}

type testProtoReader struct{}

func (r *testProtoReader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}
	return []*document.Document{r.document(name, string(content))}, nil
}

func (r *testProtoReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return []*document.Document{r.document(filePath, string(content))}, nil
}

func (r *testProtoReader) ReadFromURL(_ string) ([]*document.Document, error) {
	return nil, nil
}

func (r *testProtoReader) Name() string {
	return "test-proto"
}

func (r *testProtoReader) SupportedExtensions() []string {
	return []string{".proto"}
}

func (r *testProtoReader) document(name string, content string) *document.Document {
	metadata := map[string]any{
		"trpc_ast_type":      "file",
		"trpc_ast_name":      name,
		"trpc_ast_full_name": name,
		"trpc_ast_language":  string(codeast.LanguageProto),
		"trpc_ast_scope":     string(codeast.ScopeCode),
		"trpc_ast_file_path": name,
		source.MetaFilePath:  name,
	}
	return &document.Document{Name: filepath.Base(name), Content: content, Metadata: metadata}
}

type stubReader struct {
	fileDocs []*document.Document
	fileErr  error
}

func (s *stubReader) ReadFromReader(_ string, _ io.Reader) ([]*document.Document, error) {
	return nil, nil
}

func (s *stubReader) ReadFromFile(_ string) ([]*document.Document, error) {
	if s.fileErr != nil {
		return nil, s.fileErr
	}
	return s.fileDocs, nil
}

func (s *stubReader) ReadFromURL(_ string) ([]*document.Document, error) {
	return nil, nil
}

func (s *stubReader) Name() string {
	return "stub"
}

func (s *stubReader) SupportedExtensions() []string {
	return []string{".txt", ".go"}
}

type stubDirectoryReader struct {
	*stubReader
	dirDocs []*document.Document
	dirErr  error
}

func (s *stubDirectoryReader) ReadFromDirectory(_ string) ([]*document.Document, error) {
	if s.dirErr != nil {
		return nil, s.dirErr
	}
	return s.dirDocs, nil
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

func hasGraphNode(data *graph.Data, id string) bool {
	for _, node := range data.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func hasGraphNodeMetadataKey(data *graph.Data, id, key string) bool {
	for _, node := range data.Nodes {
		if node.ID != id {
			continue
		}
		_, ok := node.Metadata[key]
		return ok
	}
	return false
}

func hasGraphNodeMetadataValue(data *graph.Data, id, key string, value any) bool {
	for _, node := range data.Nodes {
		if node.ID == id && node.Metadata[key] == value {
			return true
		}
	}
	return false
}

func hasGraphEdge(data *graph.Data, fromID, toID, edgeType string) bool {
	for _, edge := range data.Edges {
		if edge.FromID == fromID && edge.ToID == toID && edge.Type == edgeType {
			return true
		}
	}
	return false
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

func isGraphDocumentNode(node *graph.Node) bool {
	if node == nil || node.Metadata == nil {
		return false
	}
	filePath, _ := node.Metadata[source.MetaFilePath].(string)
	if filePath == "" {
		return false
	}
	_, hasASTScope := node.Metadata["trpc_ast_scope"]
	return !hasASTScope
}

func countGraphDocumentNodes(data *graph.Data) int {
	n := 0
	for _, node := range data.Nodes {
		if isGraphDocumentNode(node) {
			n++
		}
	}
	return n
}

func findGraphDocumentNodeByContent(data *graph.Data, substr string) *graph.Node {
	for _, node := range data.Nodes {
		if isGraphDocumentNode(node) && strings.Contains(node.Content, substr) {
			return node
		}
	}
	return nil
}

func TestReadGraphIncludesDocumentNodes(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "service.go"), "package demo\n\nfunc Run() {}\n")
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# Demo\n\nThis is the readme.\n")
	writeRepoFile(t, filepath.Join(dir, "CHANGELOG.md"), "# Changelog\n\nv1.0.0 initial release\n")
	writeRepoFile(t, filepath.Join(dir, "internal", "notes.txt"), "internal notes")

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	src := New(
		WithRepository(Repository{Dir: dir}),
		WithDocExtensions([]string{".md"}),
	)

	data, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("ReadGraph() error = %v", err)
	}

	docCount := countGraphDocumentNodes(data)
	if docCount != 2 {
		t.Fatalf("expected 2 document nodes (README + CHANGELOG), got %d", docCount)
	}

	readmeNode := findGraphDocumentNodeByContent(data, "readme")
	if readmeNode == nil {
		t.Fatal("expected README document node")
	}
	if readmeNode.Metadata[source.MetaFilePath] == "" {
		t.Error("expected document node to have MetaFilePath")
	}
	if _, ok := readmeNode.Metadata["kind"]; ok {
		t.Error("document node should not have legacy kind metadata")
	}
	if _, ok := readmeNode.Metadata["trpc_ast_scope"]; ok {
		t.Error("document node should not use trpc_ast_scope")
	}
	if readmeNode.ID == "" {
		t.Error("expected document node to have non-empty ID")
	}

	txtNode := findGraphDocumentNodeByContent(data, "internal notes")
	if txtNode != nil {
		t.Fatal("txt file should not be included when only .md is configured")
	}
}

func TestReadGraphDocumentNodesWithoutCodeNodes(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# Hello\n\nworld\n")

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	src := New(
		WithRepository(Repository{Dir: dir}),
		WithDocExtensions([]string{".md"}),
	)

	data, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("ReadGraph() error = %v", err)
	}
	if len(data.Nodes) == 0 {
		t.Fatal("expected at least one document node")
	}
	if countGraphDocumentNodes(data) == 0 {
		t.Fatal("expected document node")
	}
}

func TestReadGraphDocumentIDsAreDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "service.go"), "package demo\n\nfunc Run() {}\n")
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# Hello\n")

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	src := New(
		WithRepository(Repository{Dir: dir}),
		WithDocExtensions([]string{".md"}),
	)

	data1, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("first ReadGraph() error = %v", err)
	}
	data2, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("second ReadGraph() error = %v", err)
	}

	ids1 := make(map[string]struct{})
	for _, node := range data1.Nodes {
		if isGraphDocumentNode(node) {
			ids1[node.ID] = struct{}{}
		}
	}
	for _, node := range data2.Nodes {
		if isGraphDocumentNode(node) {
			if _, ok := ids1[node.ID]; !ok {
				t.Fatalf("document node ID %q not stable across runs", node.ID)
			}
		}
	}
}

func TestReadGraphDocumentNodesNotIncludedByDefault(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "service.go"), "package demo\n\nfunc Run() {}\n")
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# Hello\n")

	src := New(WithRepository(Repository{Dir: dir}))

	data, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("ReadGraph() error = %v", err)
	}
	if countGraphDocumentNodes(data) != 0 {
		t.Fatal("document nodes should not appear without WithDocExtensions")
	}
}

type testMarkdownReader struct{}

func (r *testMarkdownReader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}
	return []*document.Document{{
		Name:     filepath.Base(name),
		Content:  strings.ToLower(string(content)),
		Metadata: map[string]any{source.MetaChunkIndex: 0},
	}}, nil
}

func (r *testMarkdownReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return []*document.Document{{
		Name:     filepath.Base(filePath),
		Content:  strings.ToLower(string(content)),
		Metadata: map[string]any{source.MetaChunkIndex: 0},
	}}, nil
}

func (r *testMarkdownReader) ReadFromURL(_ string) ([]*document.Document, error) { return nil, nil }
func (r *testMarkdownReader) Name() string                                       { return "test-md" }
func (r *testMarkdownReader) SupportedExtensions() []string                      { return []string{".md"} }

// stubDirectoryParser is a minimal codeast.DirectoryParser used in tests to
// avoid importing the real golang reader package (which requires CGO / full
// type checking). It returns an empty result so that code-path logic in
// ReadGraph can be exercised without real Go AST parsing.
type stubDirectoryParser struct{}

func (stubDirectoryParser) ParseDirectory(_ string, _ ...codeast.ParseOption) (*codeast.Result, error) {
	return &codeast.Result{}, nil
}

// errorOnBadMarkdownReader returns an error for files named "bad.md",
// used to test that readDocumentNodes continues past reader errors.
type errorOnBadMarkdownReader struct{}

func (r *errorOnBadMarkdownReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	if strings.Contains(filepath.Base(filePath), "bad") {
		return nil, fmt.Errorf("simulated read error")
	}
	return []*document.Document{{
		Name:     filepath.Base(filePath),
		Content:  "good content",
		Metadata: map[string]any{source.MetaChunkIndex: 0},
	}}, nil
}

func (r *errorOnBadMarkdownReader) ReadFromReader(_ string, _ io.Reader) ([]*document.Document, error) {
	return nil, nil
}

func (r *errorOnBadMarkdownReader) ReadFromURL(_ string) ([]*document.Document, error) {
	return nil, nil
}

func (r *errorOnBadMarkdownReader) Name() string                  { return "error-on-bad-md" }
func (r *errorOnBadMarkdownReader) SupportedExtensions() []string { return []string{".md"} }

// multiChunkMarkdownReader returns multiple chunks including nil to test
// chunk index extraction and nil doc skipping in readDocumentNodes.
type multiChunkMarkdownReader struct{}

func (r *multiChunkMarkdownReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	return []*document.Document{
		nil,
		{Name: "part1", Content: "part 1 content", Metadata: map[string]any{source.MetaChunkIndex: 5}},
		{Name: "part2", Content: "part 2 content"},
	}, nil
}

func (r *multiChunkMarkdownReader) ReadFromReader(_ string, _ io.Reader) ([]*document.Document, error) {
	return nil, nil
}

func (r *multiChunkMarkdownReader) ReadFromURL(_ string) ([]*document.Document, error) {
	return nil, nil
}

func (r *multiChunkMarkdownReader) Name() string                  { return "multi-chunk-md" }
func (r *multiChunkMarkdownReader) SupportedExtensions() []string { return []string{".md"} }

// configurableDirectoryParser allows tests to inject specific parse results.
type configurableDirectoryParser struct {
	result *codeast.Result
	err    error
	opts   []codeast.ParseOption
}

func (p *configurableDirectoryParser) ParseDirectory(_ string, opts ...codeast.ParseOption) (*codeast.Result, error) {
	p.opts = opts
	if p.err != nil {
		return nil, p.err
	}
	return p.result, nil
}

func TestReadGraphNoAllowedGoPathsReturnsEmptyData(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# Hello\n")

	src := New(WithRepository(Repository{Dir: dir}))
	data, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("ReadGraph() error = %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
	if len(data.Nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(data.Nodes))
	}
	if len(data.Edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(data.Edges))
	}
}

func TestReadGraphParseConcurrencyOverride(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "main.go"), "package demo\n\nfunc Main() {}\n")

	parser := &configurableDirectoryParser{
		result: &codeast.Result{},
	}
	codeast.RegisterDirectoryParser(codeast.FileTypeGo, parser)
	defer codeast.RegisterDirectoryParser(codeast.FileTypeGo, stubDirectoryParser{})

	t.Run("option overrides source field", func(t *testing.T) {
		src := New(
			WithRepository(Repository{Dir: dir}),
			WithParseConcurrency(3),
		)
		_, err := src.ReadGraph(context.Background(), source.WithReadGraphParseConcurrency(7))
		if err != nil {
			t.Fatalf("ReadGraph() error = %v", err)
		}
		resolved := codeast.ParseConcurrency(parser.opts)
		if resolved != 7 {
			t.Fatalf("expected parseConcurrency=7 from option, got %d", resolved)
		}
	})

	t.Run("falls back to source field", func(t *testing.T) {
		src := New(
			WithRepository(Repository{Dir: dir}),
			WithParseConcurrency(5),
		)
		_, err := src.ReadGraph(context.Background())
		if err != nil {
			t.Fatalf("ReadGraph() error = %v", err)
		}
		resolved := codeast.ParseConcurrency(parser.opts)
		if resolved != 5 {
			t.Fatalf("expected parseConcurrency=5 from source, got %d", resolved)
		}
	})

	t.Run("zero option uses source field", func(t *testing.T) {
		src := New(
			WithRepository(Repository{Dir: dir}),
			WithParseConcurrency(4),
		)
		_, err := src.ReadGraph(context.Background(), source.WithReadGraphParseConcurrency(0))
		if err != nil {
			t.Fatalf("ReadGraph() error = %v", err)
		}
		resolved := codeast.ParseConcurrency(parser.opts)
		if resolved != 4 {
			t.Fatalf("expected parseConcurrency=4 from source fallback, got %d", resolved)
		}
	})
}

func TestGraphDataFromCodeAST(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "service.go"), "package demo\n")

	src := New(WithRepository(Repository{Dir: dir, RepoName: "demo", RepoURL: "https://example.com/demo.git", Branch: "main"}))

	t.Run("nil result returns empty data", func(t *testing.T) {
		data := src.graphDataFromCodeAST(nil, dir, &repoInfo{name: "demo"}, nil)
		if data == nil {
			t.Fatal("expected non-nil data")
		}
		if len(data.Nodes) != 0 || len(data.Edges) != 0 {
			t.Fatal("expected empty data for nil result")
		}
	})

	t.Run("nodes filtered by allowedGoPaths", func(t *testing.T) {
		result := &codeast.Result{
			Nodes: []*codeast.Node{
				{
					ID: "example.com/demo.Allowed", Type: codeast.EntityStruct,
					Name: "Allowed", FullName: "example.com/demo.Allowed",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "type Allowed struct{}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 1, LineEnd: 1, Package: "example.com/demo",
				},
				{
					ID: "example.com/demo.Excluded", Type: codeast.EntityStruct,
					Name: "Excluded", FullName: "example.com/demo.Excluded",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "type Excluded struct{}", FilePath: filepath.Join(dir, "other.go"),
					LineStart: 1, LineEnd: 1, Package: "example.com/demo",
				},
			},
		}
		allowed := map[string]struct{}{"service.go": {}}
		data := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo", url: "https://example.com/demo.git", branch: "main"}, allowed)
		if len(data.Nodes) != 1 {
			t.Fatalf("expected 1 node (Allowed), got %d", len(data.Nodes))
		}
		if data.Nodes[0].Name != "Allowed" {
			t.Fatalf("expected Allowed node, got %s", data.Nodes[0].Name)
		}
	})

	t.Run("nil and empty-ID nodes are skipped", func(t *testing.T) {
		result := &codeast.Result{
			Nodes: []*codeast.Node{
				nil,
				{ID: "", Name: "Empty"},
				{
					ID: "example.com/demo.Valid", Type: codeast.EntityFunction,
					Name: "Valid", FullName: "example.com/demo.Valid",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "func Valid() {}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 1, LineEnd: 1, Package: "example.com/demo",
				},
			},
		}
		allowed := map[string]struct{}{"service.go": {}}
		data := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo"}, allowed)
		if len(data.Nodes) != 1 {
			t.Fatalf("expected 1 node, got %d", len(data.Nodes))
		}
	})

	t.Run("edges generated correctly", func(t *testing.T) {
		result := &codeast.Result{
			Nodes: []*codeast.Node{
				{
					ID: "example.com/demo.Service", Type: codeast.EntityStruct,
					Name: "Service", FullName: "example.com/demo.Service",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "type Service struct{}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 1, LineEnd: 1, Package: "example.com/demo",
				},
				{
					ID: "example.com/demo.Service.Do", Type: codeast.EntityMethod,
					Name: "Do", FullName: "example.com/demo.Service.Do",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "func (s *Service) Do() {}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 2, LineEnd: 2, Package: "example.com/demo",
					Metadata: map[string]any{"receiver_type": "*Service"},
				},
			},
			Edges: []*codeast.Edge{
				{FromID: "example.com/demo.Service", ToID: "example.com/demo.Service.Do", Type: codeast.RelationMethod},
				nil,
				{FromID: "", ToID: "example.com/demo.Service.Do", Type: codeast.RelationCalls},
				{FromID: "example.com/demo.Service", ToID: "", Type: codeast.RelationCalls},
				{FromID: "example.com/demo.Service", ToID: "example.com/demo.Service.Do", Type: ""},
				{FromID: "example.com/demo.Service", ToID: "example.com/demo.Missing", Type: codeast.RelationCalls},
				{FromID: "example.com/demo.Missing", ToID: "example.com/demo.Service", Type: codeast.RelationCalls},
			},
		}
		allowed := map[string]struct{}{"service.go": {}}
		data := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo", url: "https://example.com/demo.git", branch: "main"}, allowed)
		if len(data.Nodes) != 2 {
			t.Fatalf("expected 2 nodes, got %d", len(data.Nodes))
		}
		if len(data.Edges) != 1 {
			t.Fatalf("expected 1 valid edge, got %d", len(data.Edges))
		}
		edge := data.Edges[0]
		if edge.Type != string(codeast.RelationMethod) {
			t.Fatalf("expected edge type METHOD, got %s", edge.Type)
		}
		if edge.Metadata == nil || edge.Metadata["builder"] != "repo_graph_source" {
			t.Fatal("expected builder metadata on edge")
		}
	})

	t.Run("edge with existing metadata preserves it", func(t *testing.T) {
		result := &codeast.Result{
			Nodes: []*codeast.Node{
				{
					ID: "example.com/demo.A", Type: codeast.EntityFunction,
					Name: "A", FullName: "example.com/demo.A",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "func A() {}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 1, LineEnd: 1,
				},
				{
					ID: "example.com/demo.B", Type: codeast.EntityFunction,
					Name: "B", FullName: "example.com/demo.B",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "func B() {}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 2, LineEnd: 2,
				},
			},
			Edges: []*codeast.Edge{
				{
					FromID: "example.com/demo.A", ToID: "example.com/demo.B",
					Type:     codeast.RelationCalls,
					Metadata: map[string]any{"weight": 1},
				},
			},
		}
		allowed := map[string]struct{}{"service.go": {}}
		data := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo"}, allowed)
		if len(data.Edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(data.Edges))
		}
		if data.Edges[0].Metadata["weight"] != 1 {
			t.Fatal("expected edge metadata to be preserved")
		}
		if data.Edges[0].Metadata["builder"] != "repo_graph_source" {
			t.Fatal("expected builder metadata on edge")
		}
	})

	t.Run("node IDs are deterministic SHA1", func(t *testing.T) {
		result := &codeast.Result{
			Nodes: []*codeast.Node{
				{
					ID: "example.com/demo.Func", Type: codeast.EntityFunction,
					Name: "Func", FullName: "example.com/demo.Func",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "func Func() {}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 1, LineEnd: 1,
				},
			},
		}
		allowed := map[string]struct{}{"service.go": {}}
		data1 := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo", branch: "main"}, allowed)
		data2 := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo", branch: "main"}, allowed)
		if data1.Nodes[0].ID != data2.Nodes[0].ID {
			t.Fatalf("node IDs not deterministic: %s vs %s", data1.Nodes[0].ID, data2.Nodes[0].ID)
		}
		if !strings.HasPrefix(data1.Nodes[0].ID, "node:") {
			t.Fatalf("expected node ID prefix 'node:', got %s", data1.Nodes[0].ID)
		}
	})
}

func TestReadDocumentNodesInternal(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# Hello\n\nworld\n")
	writeRepoFile(t, filepath.Join(dir, "CHANGELOG.md"), "# Changelog\n")
	writeRepoFile(t, filepath.Join(dir, "notes.txt"), "notes content")
	writeRepoFile(t, filepath.Join(dir, "main.go"), "package main\n")

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	t.Run("reads only matching extensions", func(t *testing.T) {
		src := New(
			WithRepository(Repository{Dir: dir}),
			WithDocExtensions([]string{".md"}),
		)
		nodes, err := src.readDocumentNodes(context.Background(), dir, dir, &repoInfo{name: "demo", branch: "main"})
		if err != nil {
			t.Fatalf("readDocumentNodes() error = %v", err)
		}
		if len(nodes) != 2 {
			t.Fatalf("expected 2 document nodes (.md files), got %d", len(nodes))
		}
		for _, node := range nodes {
			if node.ID == "" {
				t.Fatal("expected non-empty node ID")
			}
			if node.Metadata[source.MetaFilePath] == "" {
				t.Fatal("expected non-empty file path in metadata")
			}
		}
	})

	t.Run("no reader available skips file gracefully", func(t *testing.T) {
		noReaderDir := t.TempDir()
		writeRepoFile(t, filepath.Join(noReaderDir, "data.xyz"), "xyz content")
		src := New(
			WithRepository(Repository{Dir: noReaderDir}),
			WithDocExtensions([]string{".xyz"}),
		)
		nodes, err := src.readDocumentNodes(context.Background(), noReaderDir, noReaderDir, &repoInfo{name: "demo"})
		if err != nil {
			t.Fatalf("readDocumentNodes() error = %v", err)
		}
		if len(nodes) != 0 {
			t.Fatalf("expected 0 nodes for unsupported extension, got %d", len(nodes))
		}
	})

	t.Run("nil info produces valid namespace", func(t *testing.T) {
		src := New(
			WithRepository(Repository{Dir: dir}),
			WithDocExtensions([]string{".md"}),
		)
		nodes, err := src.readDocumentNodes(context.Background(), dir, dir, nil)
		if err != nil {
			t.Fatalf("readDocumentNodes() error = %v", err)
		}
		if len(nodes) == 0 {
			t.Fatal("expected at least one node")
		}
		for _, node := range nodes {
			if node.ID == "" {
				t.Fatal("expected non-empty node ID with nil info")
			}
		}
	})

	t.Run("chunk index extraction and nil doc handling", func(t *testing.T) {
		chunkDir := t.TempDir()
		writeRepoFile(t, filepath.Join(chunkDir, "doc.md"), "# Part 1\n\n---\n\n# Part 2\n")

		docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
			return &multiChunkMarkdownReader{}
		})
		defer docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
			return &testMarkdownReader{}
		})

		src := New(
			WithRepository(Repository{Dir: chunkDir}),
			WithDocExtensions([]string{".md"}),
		)
		src.readers["markdown"] = &multiChunkMarkdownReader{}
		nodes, err := src.readDocumentNodes(context.Background(), chunkDir, chunkDir, &repoInfo{name: "demo"})
		if err != nil {
			t.Fatalf("readDocumentNodes() error = %v", err)
		}
		if len(nodes) != 2 {
			t.Fatalf("expected 2 nodes (nil doc skipped), got %d", len(nodes))
		}
		found5 := false
		for _, node := range nodes {
			if node.Metadata[source.MetaChunkIndex] == 5 {
				found5 = true
			}
		}
		if !found5 {
			t.Fatal("expected chunk index 5 from metadata")
		}
	})
}

func TestReadDocumentNodesReaderErrorContinues(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "good.md"), "# Good\n")
	writeRepoFile(t, filepath.Join(dir, "bad.md"), "# Bad\n")

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &errorOnBadMarkdownReader{}
	})
	defer docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	src := New(
		WithRepository(Repository{Dir: dir}),
		WithDocExtensions([]string{".md"}),
	)
	src.readers["markdown"] = &errorOnBadMarkdownReader{}
	nodes, err := src.readDocumentNodes(context.Background(), dir, dir, &repoInfo{name: "demo"})
	if err == nil {
		t.Fatal("readDocumentNodes() expected error for failed file read, got nil")
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node (successful file still collected), got %d", len(nodes))
	}
}

func TestReadGraphWithDocExtensionsOnly(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# Test\n\ncontent\n")

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	src := New(
		WithRepository(Repository{Dir: dir}),
		WithDocExtensions([]string{".md"}),
	)
	data, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("ReadGraph() error = %v", err)
	}
	if len(data.Nodes) == 0 {
		t.Fatal("expected document nodes")
	}
	for _, node := range data.Nodes {
		if node.Metadata[source.MetaFilePath] == "" {
			t.Fatal("expected file_path metadata on document node")
		}
	}
}

func TestWithDocExtensionsOption(t *testing.T) {
	extensions := []string{".md", ".txt"}
	src := New(WithDocExtensions(extensions))

	extensions[0] = ".rst"

	if src.docExtensions[0] != ".md" {
		t.Fatal("WithDocExtensions should copy the slice")
	}
	if len(src.docExtensions) != 2 {
		t.Fatalf("expected 2 doc extensions, got %d", len(src.docExtensions))
	}
}

func TestWithParseConcurrencyOption(t *testing.T) {
	src := New(WithParseConcurrency(8))
	assertEqual(t, src.parseConcurrency, 8)

	src2 := New(WithParseConcurrency(0))
	assertEqual(t, src2.parseConcurrency, 0)

	src3 := New(WithParseConcurrency(-1))
	assertEqual(t, src3.parseConcurrency, -1)
}

func TestWithMetadataValueNilMetadata(t *testing.T) {
	src := &Source{}
	opt := WithMetadataValue("key", "val")
	opt(src)
	if src.metadata == nil {
		t.Fatal("expected metadata to be initialized")
	}
	assertEqual(t, src.metadata["key"], "val")
}

func TestReadGraphParseDirectoryError(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Main() {}\n")

	parser := &configurableDirectoryParser{err: fmt.Errorf("parse failure")}
	codeast.RegisterDirectoryParser(codeast.FileTypeGo, parser)
	defer codeast.RegisterDirectoryParser(codeast.FileTypeGo, stubDirectoryParser{})

	src := New(WithRepository(Repository{Dir: dir}))
	_, err := src.ReadGraph(context.Background())
	if err == nil {
		t.Fatal("expected parse error to propagate")
	}
	if !strings.Contains(err.Error(), "parse failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadGraphWithCustomParserResult(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "service.go"), "package demo\n\ntype Service struct{}\n\nfunc (s *Service) Do() error { return nil }\n")

	parser := &configurableDirectoryParser{
		result: &codeast.Result{
			Nodes: []*codeast.Node{
				{
					ID: "example.com/demo.Service", Type: codeast.EntityStruct,
					Name: "Service", FullName: "example.com/demo.Service",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "type Service struct{}", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 3, LineEnd: 3, Package: "example.com/demo",
				},
				{
					ID: "example.com/demo.Service.Do", Type: codeast.EntityMethod,
					Name: "Do", FullName: "example.com/demo.Service.Do",
					Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
					Code: "func (s *Service) Do() error { return nil }", FilePath: filepath.Join(dir, "service.go"),
					LineStart: 5, LineEnd: 5, Package: "example.com/demo",
					Metadata: map[string]any{"receiver_type": "*Service"},
				},
			},
			Edges: []*codeast.Edge{
				{FromID: "example.com/demo.Service", ToID: "example.com/demo.Service.Do", Type: codeast.RelationMethod},
			},
		},
	}
	codeast.RegisterDirectoryParser(codeast.FileTypeGo, parser)
	defer codeast.RegisterDirectoryParser(codeast.FileTypeGo, stubDirectoryParser{})

	src := New(WithRepository(Repository{
		Dir:      dir,
		RepoName: "demo",
		RepoURL:  "https://example.com/demo.git",
		Branch:   "main",
	}))
	data, err := src.ReadGraph(context.Background())
	if err != nil {
		t.Fatalf("ReadGraph() error = %v", err)
	}
	if len(data.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(data.Nodes))
	}
	if len(data.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(data.Edges))
	}

	var serviceNode, methodNode *graph.Node
	for _, n := range data.Nodes {
		if n.Name == "Service" {
			serviceNode = n
		}
		if n.Name == "Do" {
			methodNode = n
		}
	}
	if serviceNode == nil || methodNode == nil {
		t.Fatal("expected both Service and Do nodes")
	}

	edge := data.Edges[0]
	if edge.FromID != serviceNode.ID || edge.ToID != methodNode.ID {
		t.Fatalf("edge does not connect expected nodes: from=%s to=%s", edge.FromID, edge.ToID)
	}
	assertEqual(t, edge.Type, string(codeast.RelationMethod))
}
