//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package document

import (
	"strings"
	"testing"
)

func TestGenerateDocumentID(t *testing.T) {
	name := "My Test Document"
	content := "test content"
	id := GenerateDocumentID(name, content)

	// Expect name spaces replaced with underscores followed by content hash and random bytes.
	if !strings.HasPrefix(id, "My_Test_Document_") {
		t.Fatalf("unexpected id prefix: %s", id)
	}

	// ID should not contain spaces.
	if strings.Contains(id, " ") {
		t.Fatalf("id should not contain spaces: %s", id)
	}

	// Generate another ID with same content - should be different due to random bytes.
	id2 := GenerateDocumentID(name, content)
	if id == id2 {
		t.Fatalf("IDs should be unique even for same content: %s == %s", id, id2)
	}
}

func TestCreateDocument(t *testing.T) {
	content := "Hello, world!"
	name := "Example Doc"
	doc := CreateDocument(content, name)

	if doc == nil {
		t.Fatalf("expected non-nil document")
	}
	if doc.Content != content {
		t.Errorf("content mismatch")
	}
	if doc.Name != name {
		t.Errorf("name mismatch")
	}
	if doc.ID == "" {
		t.Errorf("id should be set")
	}
	if doc.Metadata == nil {
		t.Errorf("metadata map should be initialized")
	}
}
