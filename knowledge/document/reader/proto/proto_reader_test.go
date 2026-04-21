//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package proto

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	codeproto "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast/proto"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

type failingReader struct{}

func (f failingReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read failed")
}

type mockProtoTransformer struct {
	failPre  bool
	failPost bool
}

func (m *mockProtoTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	if m.failPre {
		return nil, errors.New("preprocess failed")
	}
	return docs, nil
}

func (m *mockProtoTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	if m.failPost {
		return nil, errors.New("postprocess failed")
	}
	return docs, nil
}

func (m *mockProtoTransformer) Name() string {
	return "mockProtoTransformer"
}

func TestNew(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("expected reader to be created, got nil")
	}

	protoReader, ok := r.(*Reader)
	if !ok {
		t.Fatal("expected *Reader type")
	}

	if protoReader.chunk != true {
		t.Errorf("expected chunk to be true by default, got %v", protoReader.chunk)
	}

	if protoReader.Name() != "Proto Reader" {
		t.Errorf("expected name 'Proto Reader', got %s", protoReader.Name())
	}
}

func TestNewWithOptions(t *testing.T) {
	r := New(
		reader.WithChunkSize(500),
		reader.WithChunkOverlap(50),
	)
	if r == nil {
		t.Fatal("expected reader to be created, got nil")
	}

	protoReader, ok := r.(*Reader)
	if !ok {
		t.Fatal("expected *Reader type")
	}

	// AST-based proto reader doesn't use chunking strategy - it extracts entities
	if protoReader.chunk != true {
		t.Error("expected chunk to be true by default")
	}
}

func TestReadFromFile(t *testing.T) {
	// Create a temporary proto file
	tmpDir := t.TempDir()
	protoFile := filepath.Join(tmpDir, "test.proto")

	protoContent := `syntax = "proto3";

package example.v1;

import "google/protobuf/timestamp.proto";
import "google/protobuf/empty.proto";

option go_package = "github.com/example/api";

// User represents a user in the system
message User {
  string id = 1;
  string name = 2;
  string email = 3;
  google.protobuf.Timestamp created_at = 4;
}

// UserService provides user management operations
service UserService {
  rpc GetUser(GetUserRequest) returns (User);
  rpc CreateUser(CreateUserRequest) returns (User);
  rpc DeleteUser(DeleteUserRequest) returns (google.protobuf.Empty);
}

message GetUserRequest {
  string id = 1;
}

message CreateUserRequest {
  string name = 1;
  string email = 2;
}

message DeleteUserRequest {
  string id = 1;
}
`

	if err := os.WriteFile(protoFile, []byte(protoContent), 0644); err != nil {
		t.Fatalf("failed to write proto file: %v", err)
	}

	r := New()
	docs, err := r.ReadFromFile(protoFile)
	if err != nil {
		t.Fatalf("failed to read proto file: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Check metadata extraction
	firstDoc := docs[0]
	if firstDoc.Metadata == nil {
		t.Fatal("expected metadata to be present")
	}

	// Check syntax
	if syntax, ok := firstDoc.Metadata["trpc_ast_syntax"]; !ok || syntax != "proto3" {
		t.Errorf("expected proto_syntax='proto3', got %v", syntax)
	}

	// Check package
	if pkg, ok := firstDoc.Metadata["trpc_ast_package"]; !ok || pkg != "example.v1" {
		t.Errorf("expected proto_package='example.v1', got %v", pkg)
	}

	// Check imports
	if imports, ok := firstDoc.Metadata["trpc_ast_imports"].([]string); !ok || len(imports) != 2 {
		t.Errorf("expected 2 imports, got %v", firstDoc.Metadata["trpc_ast_imports"])
	}

	// Check services
	if services, ok := firstDoc.Metadata["trpc_ast_services"].([]string); !ok || len(services) != 1 {
		t.Errorf("expected 1 service, got %v", firstDoc.Metadata["trpc_ast_services"])
	} else if services[0] != "UserService" {
		t.Errorf("expected UserService, got %s", services[0])
	}

	// Check go_package metadata extracted from option
	if goPkg, ok := firstDoc.Metadata["trpc_ast_go_package"].(string); !ok || goPkg != "github.com/example/api" {
		t.Errorf("expected trpc_ast_go_package='github.com/example/api', got %v", firstDoc.Metadata["trpc_ast_go_package"])
	}

	// Check file-source style metadata is attached when reading from file
	if srcType, ok := firstDoc.Metadata[source.MetaSource].(string); !ok || srcType != source.TypeFile {
		t.Errorf("expected %s='%s', got %v", source.MetaSource, source.TypeFile, firstDoc.Metadata[source.MetaSource])
	}
	if fileName, ok := firstDoc.Metadata[source.MetaFileName].(string); !ok || fileName != filepath.Base(protoFile) {
		t.Errorf("expected %s='%s', got %v", source.MetaFileName, filepath.Base(protoFile), firstDoc.Metadata[source.MetaFileName])
	}
	if ext, ok := firstDoc.Metadata[source.MetaFileExt].(string); !ok || ext != ".proto" {
		t.Errorf("expected %s='.proto', got %v", source.MetaFileExt, firstDoc.Metadata[source.MetaFileExt])
	}
	if sourceName, ok := firstDoc.Metadata[source.MetaSourceName].(string); !ok || sourceName != "Proto Reader" {
		t.Errorf("expected %s='Proto Reader', got %v", source.MetaSourceName, firstDoc.Metadata[source.MetaSourceName])
	}
	if uriVal, ok := firstDoc.Metadata[source.MetaURI].(string); !ok {
		t.Fatalf("expected %s to be present", source.MetaURI)
	} else {
		parsed, err := url.Parse(uriVal)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", source.MetaURI, err)
		}
		if parsed.Scheme != "file" {
			t.Errorf("expected %s scheme=file, got %s", source.MetaURI, parsed.Scheme)
		}
	}

	// Check common chunk metadata
	if _, ok := firstDoc.Metadata[source.MetaChunkIndex].(int); !ok {
		t.Errorf("expected %s metadata to be int, got %T", source.MetaChunkIndex, firstDoc.Metadata[source.MetaChunkIndex])
	}
	if _, ok := firstDoc.Metadata["trpc_ast_chunk_index"]; ok {
		t.Errorf("expected trpc_ast_chunk_index to be removed, got %v", firstDoc.Metadata["trpc_ast_chunk_index"])
	}
	if chunkSize, ok := firstDoc.Metadata[source.MetaChunkSize].(int); !ok || chunkSize <= 0 {
		t.Errorf("expected %s to be positive int, got %v", source.MetaChunkSize, firstDoc.Metadata[source.MetaChunkSize])
	}
	if contentLength, ok := firstDoc.Metadata[source.MetaContentLength].(int); !ok || contentLength <= 0 {
		t.Errorf("expected %s to be positive int, got %v", source.MetaContentLength, firstDoc.Metadata[source.MetaContentLength])
	}
}

func TestReadFromFile_NotFound(t *testing.T) {
	r := New()
	_, err := r.ReadFromFile("/nonexistent/path/file.proto")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReadFromFile_UnsupportedExtension(t *testing.T) {
	r := New()
	_, err := r.ReadFromFile("not_proto.txt")
	if err == nil {
		t.Fatal("expected unsupported extension error")
	}
	if !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadFromReader(t *testing.T) {
	protoContent := `syntax = "proto2";

package test;

message Simple {
  required string name = 1;
}
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read from reader: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Check metadata
	if syntax, ok := docs[0].Metadata["trpc_ast_syntax"]; !ok || syntax != "proto2" {
		t.Errorf("expected proto_syntax='proto2', got %v", syntax)
	}

	// Check common chunk metadata (available even for reader input)
	if _, ok := docs[0].Metadata[source.MetaChunkIndex].(int); !ok {
		t.Errorf("expected %s metadata to be int, got %T", source.MetaChunkIndex, docs[0].Metadata[source.MetaChunkIndex])
	}
	if chunkSize, ok := docs[0].Metadata[source.MetaChunkSize].(int); !ok || chunkSize <= 0 {
		t.Errorf("expected %s to be positive int, got %v", source.MetaChunkSize, docs[0].Metadata[source.MetaChunkSize])
	}
	if contentLength, ok := docs[0].Metadata[source.MetaContentLength].(int); !ok || contentLength <= 0 {
		t.Errorf("expected %s to be positive int, got %v", source.MetaContentLength, docs[0].Metadata[source.MetaContentLength])
	}
}

func TestReadFromReader_Error(t *testing.T) {
	r := New()
	_, err := r.ReadFromReader("test.proto", failingReader{})
	if err == nil {
		t.Fatal("expected read error")
	}
}

func TestReadFromURL(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;
message A { string id = 1; }
`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(protoContent))
	}))
	defer ts.Close()

	r := New()
	docs, err := r.ReadFromURL(ts.URL + "/api.proto")
	if err != nil {
		t.Fatalf("expected successful ReadFromURL, got error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected documents from URL")
	}
}

func TestReadFromURL_InvalidURL(t *testing.T) {
	r := New()
	_, err := r.ReadFromURL("://bad")
	if err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestReadFromURL_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	r := New()
	_, err := r.ReadFromURL(ts.URL + "/api.proto")
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReader_WithTransformers(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;
message A { string id = 1; }`

	tests := []struct {
		name        string
		transformer *mockProtoTransformer
		wantErr     string
	}{
		{
			name:        "success",
			transformer: &mockProtoTransformer{},
		},
		{
			name:        "preprocess error",
			transformer: &mockProtoTransformer{failPre: true},
			wantErr:     "failed to apply preprocess",
		},
		{
			name:        "postprocess error",
			transformer: &mockProtoTransformer{failPost: true},
			wantErr:     "failed to apply postprocess",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(reader.WithTransformers(tt.transformer))
			docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(docs) == 0 {
					t.Fatal("expected documents")
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestProcessContent_NoEntitiesFallbackToFileDocument(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;
import "google/protobuf/empty.proto";
`
	r := New().(*Reader)
	docs, err := r.ReadFromReader("empty.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected single file document, got %d", len(docs))
	}
	if typ, _ := docs[0].Metadata["trpc_ast_type"].(string); typ != "file" {
		t.Fatalf("expected fallback file document, got type=%q", typ)
	}
}

func TestHelperFunctions_EmptyBranches(t *testing.T) {
	if got := codeproto.QualifiedName("", "Foo"); got != "Foo" {
		t.Fatalf("qualifiedName empty package mismatch: %s", got)
	}
	if got := codeproto.QualifiedNameWithParent("Inner", ""); got != "Inner" {
		t.Fatalf("qualifiedNameWithParent empty parent mismatch: %s", got)
	}

	lines := []string{"line1", "line2"}
	if got := codeproto.ExtractCode(lines, 0, 1); got != "" {
		t.Fatalf("expected empty extractCode for invalid start, got %q", got)
	}
	if got := codeproto.ExtractCode(lines, 3, 4); got != "" {
		t.Fatalf("expected empty extractCode for out-of-range start, got %q", got)
	}
}

func TestExtractOptionString_NotFound(t *testing.T) {
	content := `syntax = "proto3";
package test;`
	if got := codeproto.ExtractOptionString(content, "go_package"); got != "" {
		t.Fatalf("expected empty option string, got %q", got)
	}
}

func TestBuildFileEmbeddingText_MinimalMetadata(t *testing.T) {
	text := codeproto.BuildFileEmbeddingText("message A {}", "a.proto", map[string]any{})

	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("failed to unmarshal embedding text: %v", err)
	}
	if got["id"] != "a.proto" {
		t.Fatalf("expected id=a.proto, got %v", got["id"])
	}
	if got["type"] != "file" {
		t.Fatalf("expected type=file, got %v", got["type"])
	}
	if got["file_path"] != "a.proto" {
		t.Fatalf("expected file_path=a.proto, got %v", got["file_path"])
	}
	if _, ok := got["entity_type"]; ok {
		t.Fatalf("entity_type should not be present")
	}
	if _, ok := got["entity_name"]; ok {
		t.Fatalf("entity_name should not be present")
	}
	if _, ok := got["messages"]; ok {
		t.Fatalf("messages should not be present")
	}
	if _, ok := got["services"]; ok {
		t.Fatalf("services should not be present")
	}
}

func TestExtractFileOptionsBranches(t *testing.T) {
	// nil options
	fd := &descriptorpb.FileDescriptorProto{}
	goPkg, javaPkg := codeproto.ExtractFileOptions(fd)
	if goPkg != "" || javaPkg != "" {
		t.Fatalf("expected empty options, got go=%q java=%q", goPkg, javaPkg)
	}

	// uninterpreted option with composite name should be skipped
	fd2 := &descriptorpb.FileDescriptorProto{
		Options: &descriptorpb.FileOptions{
			UninterpretedOption: []*descriptorpb.UninterpretedOption{
				{
					Name: []*descriptorpb.UninterpretedOption_NamePart{
						{NamePart: strPtr("foo"), IsExtension: boolPtr(false)},
						{NamePart: strPtr("bar"), IsExtension: boolPtr(false)},
					},
					StringValue: []byte("ignored"),
				},
				{
					Name: []*descriptorpb.UninterpretedOption_NamePart{
						{NamePart: strPtr("go_package"), IsExtension: boolPtr(false)},
					},
					StringValue: []byte("x/y/z"),
				},
			},
		},
	}
	goPkg, javaPkg = codeproto.ExtractFileOptions(fd2)
	if goPkg != "x/y/z" || javaPkg != "" {
		t.Fatalf("unexpected options: go=%q java=%q", goPkg, javaPkg)
	}
}

func TestExtractMessage_SkipsMapEntry(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;
message Holder {
  map<string, int32> attrs = 1;
}`
	r := New().(*Reader)
	docs, err := r.ReadFromReader("map.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range docs {
		if name, _ := d.Metadata["trpc_ast_name"].(string); strings.Contains(name, "AttrsEntry") {
			t.Fatalf("map entry synthetic message should be skipped, got %q", name)
		}
	}
}

func strPtr(v string) *string { return &v }
func boolPtr(v bool) *bool    { return &v }

func TestReadFromURL_FetchError(t *testing.T) {
	r := New()
	_, err := r.ReadFromURL("http://127.0.0.1:1/nope.proto")
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if !strings.Contains(err.Error(), "failed to fetch URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSupportedExtensions(t *testing.T) {
	r := New()
	exts := r.SupportedExtensions()
	if len(exts) != 1 || exts[0] != ".proto" {
		t.Errorf("expected ['.proto'], got %v", exts)
	}
}

func TestExtractMetadata(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected map[string]any
	}{
		{
			name: "extract syntax",
			content: `syntax = "proto3";
package test;`,
			expected: map[string]any{
				"syntax": "proto3",
			},
		},
		{
			name: "extract package",
			content: `syntax = "proto3";
package example.v1.service;`,
			expected: map[string]any{
				"package": "example.v1.service",
			},
		},
		{
			name: "extract imports",
			content: `import "google/protobuf/timestamp.proto";
import public "common.proto";
import weak "optional.proto";`,
			expected: map[string]any{
				"import_count": 3,
			},
		},
		{
			name: "extract services",
			content: `service UserService {
  rpc GetUser(Request) returns (Response);
}
service OrderService {}`,
			expected: map[string]any{
				"service_count": 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := codeproto.ExtractFileMetadata(tt.content)
			for key, expectedValue := range tt.expected {
				if actualValue, ok := metadata[key]; !ok {
					t.Errorf("expected metadata key %s not found", key)
				} else {
					switch expected := expectedValue.(type) {
					case string:
						if actual := actualValue.(string); actual != expected {
							t.Errorf("expected %s='%s', got '%s'", key, expected, actual)
						}
					case int:
						switch actual := actualValue.(type) {
						case int:
							if actual != expected {
								t.Errorf("expected %s=%d, got %d", key, expected, actual)
							}
						case int64:
							if int(actual) != expected {
								t.Errorf("expected %s=%d, got %d", key, expected, actual)
							}
						default:
							t.Errorf("unexpected type for %s: %T", key, actualValue)
						}
					}
				}
			}
		})
	}
}

func TestExtractFileNameFromURL(t *testing.T) {
	r := &Reader{}

	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.com/path/to/file.proto", "file"},
		{"https://example.com/file.proto?query=1", "file"},
		{"https://example.com/file.proto#fragment", "file"},
		{"https://example.com/path/", "proto_file"},
		{"https://example.com/some-name.proto", "some-name"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := r.extractFileNameFromURL(tt.url)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestChunkingDisabled(t *testing.T) {
	protoContent := strings.Repeat("message Test { string field = 1; }\n", 100)

	r := New(reader.WithChunk(false))
	docs, err := r.ReadFromReader("large.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	if len(docs) != 1 {
		t.Errorf("expected 1 document when chunking disabled, got %d", len(docs))
	}
}

func TestASTExtraction_LineNumbers(t *testing.T) {
	protoContent := `syntax = "proto3";

package test;

// User message comment
message User {
  string id = 1;
  string name = 2;
}

// Status enum comment
enum Status {
  UNKNOWN = 0;
  ACTIVE = 1;
}

// UserService comment
service UserService {
  rpc GetUser(GetUserRequest) returns (User);
}

message GetUserRequest {
  string id = 1;
}
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Should have: User (msg), Status (enum), UserService (svc), GetUser (rpc), GetUserRequest (msg)
	if len(docs) < 5 {
		t.Fatalf("expected at least 5 documents, got %d", len(docs))
	}

	// Find User message document
	var userDoc *document.Document
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_type"] == "message" &&
			doc.Metadata["trpc_ast_name"] == "User" {
			userDoc = doc
			break
		}
	}

	if userDoc == nil {
		t.Fatal("User message document not found")
	}

	// Check line numbers (User message starts at line 6)
	if startLine, ok := userDoc.Metadata["trpc_ast_line_start"].(int); !ok || startLine != 6 {
		t.Errorf("expected line_start=6 for User message, got %v", userDoc.Metadata["trpc_ast_line_start"])
	}
}

func TestASTExtraction_Comments(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;

// This is the User service
// It handles user operations
service UserService {
  // Get a user by ID
  rpc GetUser(GetUserRequest) returns (User);
}

message GetUserRequest {
  string id = 1;
}

message User {
  string id = 1;
}
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Find service document
	var svcDoc *document.Document
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_type"] == "service" {
			svcDoc = doc
			break
		}
	}

	if svcDoc == nil {
		t.Fatal("service document not found")
	}

	// Check that comment is included in embedding text
	if !strings.Contains(svcDoc.EmbeddingText, "User service") {
		t.Error("expected embedding text to contain 'User service' comment")
	}
}

func TestASTExtraction_EnumValues(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;

enum Status {
  UNKNOWN = 0;
  ACTIVE = 1;
  INACTIVE = 2;
}
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Find enum document
	var enumDoc *document.Document
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_type"] == "enum" {
			enumDoc = doc
			break
		}
	}

	if enumDoc == nil {
		t.Fatal("enum document not found")
	}

	// Check enum values
	values, ok := enumDoc.Metadata["trpc_ast_enum_values"].([]string)
	if !ok {
		t.Fatalf("expected enum values, got %T", enumDoc.Metadata["trpc_ast_enum_values"])
	}

	if len(values) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(values))
	}
}

func TestASTExtraction_StreamingRPC(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;

service ChatService {
  rpc Chat(stream ChatMessage) returns (stream ChatMessage);
  rpc SendMessage(ChatMessage) returns (Ack);
}

message ChatMessage {
  string text = 1;
}

message Ack {
  bool success = 1;
}
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Find streaming RPC
	var streamingRPC *document.Document
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_type"] == "rpc" &&
			doc.Metadata["trpc_ast_name"] == "Chat" {
			streamingRPC = doc
			break
		}
	}

	if streamingRPC == nil {
		t.Fatal("streaming RPC document not found")
	}

	// Check streaming flags
	if clientStreaming, ok := streamingRPC.Metadata["trpc_ast_client_streaming"].(bool); !ok || !clientStreaming {
		t.Error("expected Chat RPC to have client_streaming=true")
	}

	if serverStreaming, ok := streamingRPC.Metadata["trpc_ast_server_streaming"].(bool); !ok || !serverStreaming {
		t.Error("expected Chat RPC to have server_streaming=true")
	}

	// Check signature
	sig, ok := streamingRPC.Metadata["trpc_ast_signature"].(string)
	if !ok {
		t.Fatal("expected RPC signature in metadata")
	}

	if !strings.Contains(sig, "stream") {
		t.Errorf("expected signature to contain 'stream', got: %s", sig)
	}

	if _, ok := streamingRPC.Metadata["trpc_ast_services"]; ok {
		t.Errorf("expected rpc doc not to include trpc_ast_services, got %v", streamingRPC.Metadata["trpc_ast_services"])
	}
}

func TestASTExtraction_NestedMessages(t *testing.T) {
	protoContent := `syntax = "proto3";
package test;

message Outer {
  string name = 1;

  message Inner {
    int32 value = 1;
  }

  Inner inner = 2;
}
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Should have both Outer and Inner messages
	var outerFound, innerFound bool
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_type"] == "message" {
			if doc.Metadata["trpc_ast_name"] == "Outer" {
				outerFound = true
			}
			if doc.Metadata["trpc_ast_name"] == "Inner" {
				innerFound = true
			}
		}
	}

	if !outerFound {
		t.Error("Outer message not found")
	}
	if !innerFound {
		t.Error("Inner message not found")
	}
}

func TestEntityCount(t *testing.T) {
	protoContent := `syntax = "proto3";
package example.v1;

import "google/protobuf/timestamp.proto";

option go_package = "github.com/example/api";

// User represents a user
message User {
  string id = 1;
  string name = 2;
}

// Order represents an order
message Order {
  string id = 1;
  double amount = 2;
}

// Status enum
enum Status {
  UNKNOWN = 0;
  PENDING = 1;
  COMPLETED = 2;
}

// UserService provides user operations
service UserService {
  rpc GetUser(GetUserRequest) returns (User);
  rpc CreateUser(CreateUserRequest) returns (User);
}

// OrderService provides order operations
service OrderService {
  rpc GetOrder(GetOrderRequest) returns (Order);
}

message GetUserRequest { string id = 1; }
message CreateUserRequest { string name = 1; }
message GetOrderRequest { string id = 1; }
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Expected: User, Order, GetUserRequest, CreateUserRequest, GetOrderRequest (5 messages)
	// + Status (1 enum)
	// + UserService, OrderService (2 services)
	// + GetUser, CreateUser, GetOrder (3 RPCs)
	// Total: 11 entities

	entityCounts := map[string]int{
		"message": 0,
		"enum":    0,
		"service": 0,
		"rpc":     0,
	}

	for _, doc := range docs {
		entityType, ok := doc.Metadata["trpc_ast_type"].(string)
		if ok {
			entityCounts[entityType]++
		}
	}

	if entityCounts["message"] != 5 {
		t.Errorf("expected 5 messages, got %d", entityCounts["message"])
	}
	if entityCounts["enum"] != 1 {
		t.Errorf("expected 1 enum, got %d", entityCounts["enum"])
	}
	if entityCounts["service"] != 2 {
		t.Errorf("expected 2 services, got %d", entityCounts["service"])
	}
	if entityCounts["rpc"] != 3 {
		t.Errorf("expected 3 RPCs, got %d", entityCounts["rpc"])
	}
}

// TestEmbeddingTextTypeAndFilePath verifies that embedding text uses correct type values
// and file_path format (aligned with trpc-ast-rag conventions).
func TestEmbeddingTextTypeAndFilePath(t *testing.T) {
	protoContent := `syntax = "proto3";
package example.v1;

// User message
message User {
  string id = 1;
}

// Status enum
enum Status {
  UNKNOWN = 0;
}

// UserService service
service UserService {
  rpc GetUser(GetUserRequest) returns (User);
}

message GetUserRequest { string id = 1; }
`

	r := New()
	docs, err := r.ReadFromReader("test.proto", strings.NewReader(protoContent))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	for _, doc := range docs {
		if doc.EmbeddingText == "" {
			t.Errorf("expected embedding text for %s", doc.Metadata["trpc_ast_name"])
			continue
		}

		// Parse embedding text to verify type value
		var embeddingData map[string]string
		if err := json.Unmarshal([]byte(doc.EmbeddingText), &embeddingData); err != nil {
			t.Errorf("failed to parse embedding text: %v", err)
			continue
		}

		// Verify type values are capitalized (aligned with trpc-ast-rag)
		entityType := embeddingData["type"]
		switch entityType {
		case "Message", "Enum", "Service", "RPC":
			// Correct format
		case "message", "enum", "service", "rpc":
			t.Errorf("type should be capitalized, got: %s", entityType)
		default:
			if entityType != "file" {
				t.Errorf("unexpected type value: %s", entityType)
			}
		}

		// Verify file_path contains the file name
		if filePath, ok := embeddingData["file_path"]; !ok || filePath == "" {
			t.Errorf("file_path should be present in embedding text")
		} else if filePath != "test.proto" {
			t.Errorf("file_path should be 'test.proto', got: %s", filePath)
		}
	}
}

// TestEmbeddingTextFromFile verifies file_path uses full path when reading from file
func TestEmbeddingTextFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	protoFile := filepath.Join(tmpDir, "subdir", "api.proto")

	// Create subdirectory
	if err := os.MkdirAll(filepath.Dir(protoFile), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	protoContent := `syntax = "proto3";
package example.v1;

message User { string id = 1; }
`

	if err := os.WriteFile(protoFile, []byte(protoContent), 0644); err != nil {
		t.Fatalf("failed to write proto file: %v", err)
	}

	r := New()
	docs, err := r.ReadFromFile(protoFile)
	if err != nil {
		t.Fatalf("failed to read proto file: %v", err)
	}

	// Verify file_path contains full path
	for _, doc := range docs {
		if doc.EmbeddingText == "" {
			continue
		}

		var embeddingData map[string]string
		if err := json.Unmarshal([]byte(doc.EmbeddingText), &embeddingData); err != nil {
			t.Errorf("failed to parse embedding text: %v", err)
			continue
		}

		// Verify file_path contains the full path (not just filename)
		filePath := embeddingData["file_path"]
		if !strings.Contains(filePath, "subdir") {
			t.Errorf("file_path should contain 'subdir', got: %s", filePath)
		}
		if !strings.HasSuffix(filePath, "api.proto") {
			t.Errorf("file_path should end with 'api.proto', got: %s", filePath)
		}
	}
}
