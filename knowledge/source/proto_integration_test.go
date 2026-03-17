//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package source_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"

	// Import proto reader to ensure registration
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/proto"
)

func TestProtoFileWithFileSource(t *testing.T) {
	// Create a temporary proto file
	tmpDir := t.TempDir()
	protoFile := filepath.Join(tmpDir, "service.proto")

	protoContent := `syntax = "proto3";

package test.v1;

service TestService {
  rpc GetData(GetDataRequest) returns (GetDataResponse);
}

message GetDataRequest {
  string id = 1;
}

message GetDataResponse {
  string data = 1;
}
`
	if err := os.WriteFile(protoFile, []byte(protoContent), 0644); err != nil {
		t.Fatalf("failed to write proto file: %v", err)
	}

	// Test with file source
	src := file.New([]string{protoFile})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("failed to read proto file: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Verify metadata was extracted
	firstDoc := docs[0]
	if firstDoc.Metadata == nil {
		t.Fatal("expected metadata to be present")
	}

	if syntax, ok := firstDoc.Metadata["trpc_ast_syntax"]; !ok || syntax != "proto3" {
		t.Errorf("expected proto_syntax='proto3', got %v", syntax)
	}

	if pkg, ok := firstDoc.Metadata["trpc_ast_package"]; !ok || pkg != "test.v1" {
		t.Errorf("expected proto_package='test.v1', got %v", pkg)
	}

	t.Logf("Successfully loaded proto file with %d documents", len(docs))
}

func TestProtoFileWithDirSource(t *testing.T) {
	// Create a temporary directory with proto files
	tmpDir := t.TempDir()

	// Create multiple proto files
	protoFiles := []struct {
		name    string
		content string
	}{
		{
			name: "user.proto",
			content: `syntax = "proto3";
package user.v1;
message User {
  string id = 1;
  string name = 2;
}
`,
		},
		{
			name: "order.proto",
			content: `syntax = "proto3";
package order.v1;
message Order {
  string id = 1;
  float amount = 2;
}
`,
		},
	}

	for _, pf := range protoFiles {
		path := filepath.Join(tmpDir, pf.name)
		if err := os.WriteFile(path, []byte(pf.content), 0644); err != nil {
			t.Fatalf("failed to write proto file %s: %v", pf.name, err)
		}
	}

	// Test with dir source
	src := dir.New([]string{tmpDir})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("failed to read proto files from directory: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	t.Logf("Successfully loaded %d proto files from directory", len(docs))
}

func TestProtoFileWithAutoSource(t *testing.T) {
	// Create a temporary proto file
	tmpDir := t.TempDir()
	protoFile := filepath.Join(tmpDir, "api.proto")

	protoContent := `syntax = "proto3";
package api.v1;
service APIService {
  rpc Call(Request) returns (Response);
}
message Request {}
message Response {}
`
	if err := os.WriteFile(protoFile, []byte(protoContent), 0644); err != nil {
		t.Fatalf("failed to write proto file: %v", err)
	}

	// Test with auto source - should auto-detect file type
	src := auto.New([]string{protoFile})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("failed to read proto file with auto source: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Verify metadata extraction worked
	firstDoc := docs[0]
	if firstDoc.Metadata == nil {
		t.Fatal("expected metadata to be present")
	}

	if _, ok := firstDoc.Metadata["trpc_ast_syntax"]; !ok {
		t.Error("expected proto_syntax metadata")
	}

	t.Logf("Auto source successfully detected and loaded proto file")
}

func TestFileReaderTypeProto(t *testing.T) {
	// Verify the constant exists
	if source.FileReaderTypeProto != "proto" {
		t.Errorf("expected FileReaderTypeProto='proto', got %s", source.FileReaderTypeProto)
	}
}
