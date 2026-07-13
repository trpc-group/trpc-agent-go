//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package artifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestNewStoreRejectsEmptyRoot(t *testing.T) {
	if _, err := NewStore("  "); err == nil {
		t.Fatal("empty artifact root was accepted")
	}
}

func TestLocalStoreFileAndDirectoryHelpers(t *testing.T) {
	defaultOperations := (&Store{}).effectiveBundleOperations()
	if defaultOperations.mkdirTemp == nil || defaultOperations.syncDirectory == nil {
		t.Fatal("zero store did not fall back to default operations")
	}
	customOperations := defaultOperations
	store := &Store{bundleOps: customOperations}
	if store.effectiveBundleOperations().mkdirTemp == nil {
		t.Fatal("custom operations were not retained")
	}

	directory := t.TempDir()
	if err := syncDirectory(directory); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := syncDirectory(filepath.Join(directory, "missing")); err == nil {
			t.Fatal("syncDirectory accepted a missing directory")
		}
	}

	path := filepath.Join(directory, "report.json")
	if err := writeSyncedFile(path, []byte("content")); err != nil {
		t.Fatal(err)
	}
	if err := writeSyncedFile(path, []byte("content")); err == nil {
		t.Fatal("writeSyncedFile overwrote an existing report")
	}
}

func TestWriteReportsDoesNotExposePartialRunWhenStorageFails(t *testing.T) {
	tests := []struct {
		name              string
		configure         func(*Store)
		bundleIsPublished bool
	}{
		{
			name: "report file persistence fails",
			configure: func(store *Store) {
				calls := 0
				store.bundleOps.writeSyncedFile = func(path string, content []byte) error {
					calls++
					if calls == 2 {
						return fmt.Errorf("persist report: %w", errors.New("storage unavailable"))
					}
					return writeSyncedFile(path, content)
				}
			},
		},
		{
			name: "temporary bundle cannot be synced",
			configure: func(store *Store) {
				original := store.bundleOps.syncDirectory
				store.bundleOps.syncDirectory = func(path string) error {
					if filepath.Base(path) != filepath.Base(store.root) {
						return errors.New("temporary bundle sync failed")
					}
					return original(path)
				}
			},
		},
		{
			name: "bundle publication fails",
			configure: func(store *Store) {
				store.bundleOps.rename = func(string, string) error {
					return errors.New("publish failed")
				}
			},
		},
		{
			name:              "published directory cannot be made durable",
			bundleIsPublished: true,
			configure: func(store *Store) {
				original := store.bundleOps.syncDirectory
				store.bundleOps.syncDirectory = func(path string) error {
					if path == store.root {
						return errors.New("root sync failed")
					}
					return original(path)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			test.configure(store)
			result := &regression.RunResult{
				RunID: "storage-failure",
				Spec: &regression.RunSpec{
					Runtime: regression.RuntimePolicy{NumRuns: 1},
				},
			}
			files, err := WriteReports(context.Background(), store, result)
			if err == nil || files != nil {
				t.Fatalf("files=%v err=%v, want failed atomic publication", files, err)
			}
			_, statErr := os.Lstat(filepath.Join(store.root, result.RunID))
			if test.bundleIsPublished {
				if statErr != nil {
					t.Fatalf("published report directory was removed: %v", statErr)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("partial report directory is visible: %v", statErr)
			}
			entries, readErr := os.ReadDir(store.root)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".report-bundle-") {
					t.Fatalf("temporary report bundle was not cleaned: %s", entry.Name())
				}
			}
		})
	}
}

func TestStoreIsImmutableAndIdempotentForIdenticalContent(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Write(context.Background(), "run/report.json", []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Write(context.Background(), "run/report.json", []byte("same"))
	if err != nil || first.SHA256 != second.SHA256 {
		t.Fatalf("idempotent write failed: %v", err)
	}
	if _, err := store.Write(context.Background(), "run/report.json", []byte("different")); err == nil {
		t.Fatal("different content overwrote an immutable artifact")
	}
}

func TestStoreRejectsTraversal(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(context.Background(), "../escape", []byte("x")); err == nil {
		t.Fatal("path traversal was accepted")
	}
}

func TestScenarioStoreDoesNotFollowSymlinkOutsideArtifactRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escaped")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := store.Write(
		context.Background(), "escaped/report.json", []byte("sensitive"),
	); err == nil {
		t.Fatal("artifact store followed a symbolic link outside its root")
	}
	if _, err := os.Stat(filepath.Join(outside, "report.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact escaped the configured root: %v", err)
	}
}

func TestStoreRejectsInvalidNames(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", filepath.Join(string(filepath.Separator), "absolute")} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Write(context.Background(), name, []byte("x")); err == nil {
				t.Fatalf("invalid artifact name %q was accepted", name)
			}
		})
	}
}

func TestStoreHonorsCanceledContext(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Write(ctx, "run/report.json", []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("write error = %v, want context canceled", err)
	}
}

func TestStoreSupportsConcurrentIdempotentWrites(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const writers = 16
	var wg sync.WaitGroup
	errorsByWriter := make(chan error, writers)
	digests := make(chan string, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			file, err := store.Write(context.Background(), "run/report.json", []byte("same"))
			if err != nil {
				errorsByWriter <- err
				return
			}
			digests <- file.SHA256
		}()
	}
	wg.Wait()
	close(errorsByWriter)
	close(digests)
	for err := range errorsByWriter {
		t.Fatalf("concurrent write failed: %v", err)
	}
	var expected string
	count := 0
	for digest := range digests {
		if expected == "" {
			expected = digest
		}
		if digest != expected {
			t.Fatalf("digest = %q, want %q", digest, expected)
		}
		count++
	}
	if count != writers {
		t.Fatalf("successful writes = %d, want %d", count, writers)
	}
}

func TestStoreReportsFilesystemConflict(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(context.Background(), "blocked/report.json", []byte("x")); err == nil {
		t.Fatal("filesystem path conflict was ignored")
	}
}

func TestWriteReportsCreatesJSONAndMarkdownArtifacts(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	result := &regression.RunResult{
		RunID:    "run-1",
		Status:   regression.RunStatusSucceeded,
		Decision: regression.DecisionAccepted,
		Spec: &regression.RunSpec{
			InputFingerprint: "fingerprint",
			Runtime:          regression.RuntimePolicy{Seed: 7, NumRuns: 1},
		},
	}
	files, err := WriteReports(context.Background(), store, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(files))
	}
	for _, file := range files {
		if file.SHA256 == "" || file.Size == 0 {
			t.Fatalf("artifact metadata is incomplete: %+v", file)
		}
		content, err := os.ReadFile(filepath.FromSlash(file.Path))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "run-1") {
			t.Fatalf("artifact %q omitted run id", file.Name)
		}
	}
}

func TestScenarioConcurrentReportPublishersConvergeOnOneCompleteBundle(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	result := &regression.RunResult{
		RunID: "concurrent-run",
		Spec: &regression.RunSpec{
			InputFingerprint: "fingerprint",
			Runtime:          regression.RuntimePolicy{NumRuns: 1},
		},
	}
	const publishers = 12
	var wait sync.WaitGroup
	errorsByPublisher := make(chan error, publishers)
	for index := 0; index < publishers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			files, err := WriteReports(context.Background(), store, result)
			if err != nil {
				errorsByPublisher <- err
				return
			}
			if len(files) != 2 {
				errorsByPublisher <- fmt.Errorf("artifact count = %d", len(files))
			}
		}()
	}
	wait.Wait()
	close(errorsByPublisher)
	for err := range errorsByPublisher {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "concurrent-run"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("published bundle contains %d files, want 2", len(entries))
	}
}

func TestWriteReportsWaitsForPublicationToFinishBeforeReadingBundle(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	originalSync := store.bundleOps.syncDirectory
	published := make(chan struct{})
	release := make(chan struct{})
	store.bundleOps.syncDirectory = func(path string) error {
		if path == store.root {
			select {
			case <-published:
			default:
				close(published)
			}
			<-release
		}
		return originalSync(path)
	}
	result := &regression.RunResult{
		RunID: "serialized-run",
		Spec:  &regression.RunSpec{Runtime: regression.RuntimePolicy{NumRuns: 1}},
	}
	first := make(chan error, 1)
	go func() {
		_, err := WriteReports(context.Background(), store, result)
		first <- err
	}()
	<-published
	second := make(chan error, 1)
	go func() {
		_, err := WriteReports(context.Background(), store, result)
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("second writer returned before publication completed: %v", err)
	default:
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestWriteReportsPreservesBundleConflictWhenPublicationLosesRace(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "run"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run", "optimization_report.json"), []byte("different"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run", "optimization_report.md"), []byte("different"), 0o640); err != nil {
		t.Fatal(err)
	}
	store.bundleOps.rename = func(string, string) error { return errors.New("publish raced") }
	_, err = WriteReports(context.Background(), store, &regression.RunResult{
		RunID: "run",
		Spec:  &regression.RunSpec{Runtime: regression.RuntimePolicy{NumRuns: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists with different content") {
		t.Fatalf("error = %v, want immutable bundle conflict", err)
	}
}

func TestWriteReportsRejectsIncompleteInputs(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		store  *Store
		result *regression.RunResult
	}{
		"nil store":    {result: &regression.RunResult{RunID: "run"}},
		"nil result":   {store: store},
		"empty run id": {store: store, result: &regression.RunResult{}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := WriteReports(context.Background(), test.store, test.result); err == nil {
				t.Fatal("incomplete input was accepted")
			}
		})
	}
}

func TestScenarioRunIDCannotCreateNestedOrEscapingReportDirectory(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{"nested/run", `nested\run`, ".", "..", "CON", "report."} {
		t.Run(runID, func(t *testing.T) {
			_, err := WriteReports(context.Background(), store, &regression.RunResult{
				RunID: runID,
				Spec:  &regression.RunSpec{Runtime: regression.RuntimePolicy{NumRuns: 1}},
			})
			if err == nil {
				t.Fatalf("unsafe run id %q was accepted", runID)
			}
		})
	}
}

func TestWriteReportsRejectsMissingReportSpec(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReports(context.Background(), store, &regression.RunResult{RunID: "run"}); err == nil {
		t.Fatal("result without report spec was accepted")
	}
}

func TestWriteReportsPublishesNoPartialBundleWhenRunDirectoryConflicts(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(context.Background(), "run/optimization_report.md", []byte("conflict")); err != nil {
		t.Fatal(err)
	}
	result := &regression.RunResult{
		RunID: "run",
		Spec: &regression.RunSpec{
			Runtime: regression.RuntimePolicy{NumRuns: 1},
		},
	}
	if _, err := WriteReports(context.Background(), store, result); err == nil {
		t.Fatal("immutable report conflict was ignored")
	}
	if _, err := os.Stat(filepath.Join(root, "run", "optimization_report.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial JSON report was published: %v", err)
	}
}
