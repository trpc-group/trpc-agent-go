package document

import (
	"strings"
	"testing"
)

func TestGenerateDocumentID(t *testing.T) {
	name := "My Test Document"
	id := GenerateDocumentID(name)

	// Expect name spaces replaced with underscores followed by timestamp.
	if !strings.HasPrefix(id, "My_Test_Document_") {
		t.Fatalf("unexpected id prefix: %s", id)
	}

	// ID should not contain spaces.
	if strings.Contains(id, " ") {
		t.Fatalf("id should not contain spaces: %s", id)
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
