//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package dir

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// TestReadDocuments verifies Directory Source with and without
// custom chunk configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	// Create two small files to ensure multiple documents are produced.
	for i := 0; i < 2; i++ {
		filePath := filepath.Join(tmpDir, "file"+strconv.Itoa(i)+".txt")
		content := strings.Repeat("0123456789", 5) // 50 chars
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}
	}

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{tmpDir}, WithRecursive(false))
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) == 0 {
			t.Fatalf("expected documents, got 0")
		}
	})

	t.Run("custom-chunk-config", func(t *testing.T) {
		const chunkSize = 10
		const overlap = 2
		src := New(
			[]string{tmpDir},
			WithRecursive(false),
			WithChunkSize(chunkSize),
			WithChunkOverlap(overlap),
		)
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) == 0 {
			t.Fatalf("expected documents, got 0")
		}
		_ = docs // ensure docs produced with custom chunk config.
	})
}

// TestGetFilePaths verifies recursive and non-recursive traversal as well as
// extension filtering.
func TestGetFilePaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Directory structure:
	// tmpDir/
	//   file1.txt
	//   file2.md
	//   sub/
	//     nested.txt

	mustWrite := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", path, err)
		}
	}

	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.md")
	subDir := filepath.Join(tmpDir, "sub")
	_ = os.Mkdir(subDir, 0755)
	nested := filepath.Join(subDir, "nested.txt")

	mustWrite(file1, "hello")
	mustWrite(file2, "world")
	mustWrite(nested, strings.Repeat("x", 10))

	// Non-recursive: should only see root files.
	srcNonRec := New([]string{tmpDir}, WithRecursive(false))
	paths, err := srcNonRec.getFilePaths(tmpDir)
	if err != nil {
		t.Fatalf("getFilePaths returned error: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}

	// Recursive: should include nested file.
	srcRec := New([]string{tmpDir}, WithRecursive(true))
	paths, err = srcRec.getFilePaths(tmpDir)
	if err != nil {
		t.Fatalf("getFilePaths returned error: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths with recursion, got %d", len(paths))
	}

	// Extension filter: only *.md.
	srcFilter := New([]string{tmpDir}, WithFileExtensions([]string{".md"}))
	paths, err = srcFilter.getFilePaths(tmpDir)
	if err != nil {
		t.Fatalf("getFilePaths returned error: %v", err)
	}
	if len(paths) != 1 || filepath.Ext(paths[0]) != ".md" {
		t.Fatalf("extension filter failed, paths: %v", paths)
	}
}

// TestReadDocuments_Basic ensures documents are returned without error.
func TestReadDocuments_Basic(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(filePath, []byte("sample content"), 0644); err != nil {
		t.Fatalf("failed to write sample file: %v", err)
	}

	src := New([]string{tmpDir})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments returned error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document")
	}

	if docs[0].Metadata == nil {
		t.Fatalf("expected metadata to be set")
	}
}

// TestNameAndMetadata verifies functional options related to name and metadata.
func TestNameAndMetadata(t *testing.T) {
	const customName = "my-dir-src"
	meta := map[string]interface{}{"k": "v"}
	src := New([]string{"dummy"}, WithName(customName), WithMetadata(meta))

	if src.Name() != customName {
		t.Fatalf("expected name %s, got %s", customName, src.Name())
	}
	if src.Type() != source.TypeDir {
		t.Fatalf("unexpected Type value %s", src.Type())
	}

	if v, ok := src.metadata["k"]; !ok || v != "v" {
		t.Fatalf("metadata not applied correctly")
	}
}

func TestSource_FileExtensionFilter(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// create files .txt and .json
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "b.json"), []byte("{}"), 0o644)

	src := New([]string{root}, WithFileExtensions([]string{".txt"}))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 txt doc, got %d", len(docs))
	}
}

func TestSource_Recursive(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "c.txt"), []byte("y"), 0o644)

	src := New([]string{root}, WithRecursive(true))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("recursive read failed")
	}
}
