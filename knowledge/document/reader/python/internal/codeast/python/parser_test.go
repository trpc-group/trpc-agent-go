//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package python

import (
	"path/filepath"
	"runtime"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

func TestParseContent_Nodes(t *testing.T) {
	parser := NewParser()
	result, err := parser.ParseFileAt(testdataPath("sample.py"), "sample")
	if err != nil {
		t.Fatalf("ParseFileAt failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	nodesByName := make(map[string]*codeast.Node)
	for _, n := range result.Nodes {
		nodesByName[n.Name] = n
	}

	// Verify variables
	if n, ok := nodesByName["MAX_RETRIES"]; !ok {
		t.Error("missing Variable node: MAX_RETRIES")
	} else {
		if n.Type != codeast.EntityVariable {
			t.Errorf("MAX_RETRIES type = %s, want Variable", n.Type)
		}
		if n.Signature != "MAX_RETRIES: int" {
			t.Errorf("MAX_RETRIES signature = %q, want %q", n.Signature, "MAX_RETRIES: int")
		}
	}

	if _, ok := nodesByName["DEFAULT_TIMEOUT"]; !ok {
		t.Error("missing Variable node: DEFAULT_TIMEOUT")
	}

	// Verify classes
	if n, ok := nodesByName["BaseHandler"]; !ok {
		t.Error("missing Class node: BaseHandler")
	} else {
		if n.Type != codeast.EntityClass {
			t.Errorf("BaseHandler type = %s, want Class", n.Type)
		}
		if n.Language != codeast.LanguagePython {
			t.Errorf("BaseHandler language = %s, want python", n.Language)
		}
		// imports stay on the node (for metadata) even though no IMPORTS edge is emitted.
		if len(n.Imports) == 0 {
			t.Error("BaseHandler node.Imports should be populated")
		}
	}

	if n, ok := nodesByName["HTTPHandler"]; !ok {
		t.Error("missing Class node: HTTPHandler")
	} else if n.Type != codeast.EntityClass {
		t.Errorf("HTTPHandler type = %s, want Class", n.Type)
	}

	// Verify functions
	if n, ok := nodesByName["create_handler"]; !ok {
		t.Error("missing Function node: create_handler")
	} else if n.Type != codeast.EntityFunction {
		t.Errorf("create_handler type = %s, want Function", n.Type)
	}

	if n, ok := nodesByName["fetch_data"]; !ok {
		t.Error("missing Function node: fetch_data")
	} else {
		if n.Type != codeast.EntityFunction {
			t.Errorf("fetch_data type = %s, want Function", n.Type)
		}
		if n.Signature == "" {
			t.Error("fetch_data signature is empty")
		}
	}

	// Verify methods
	if n, ok := nodesByName["handle"]; !ok {
		t.Error("missing Method node: handle")
	} else if n.Type != codeast.EntityMethod {
		t.Errorf("handle type = %s, want Method", n.Type)
	}

	if n, ok := nodesByName["_send_request"]; !ok {
		t.Error("missing Method node: _send_request")
	} else if n.Type != codeast.EntityMethod {
		t.Errorf("_send_request type = %s, want Method", n.Type)
	}
}

func TestParseContent_Edges(t *testing.T) {
	parser := NewParser()
	result, err := parser.ParseFileAt(testdataPath("sample.py"), "sample")
	if err != nil {
		t.Fatalf("ParseFileAt failed: %v", err)
	}

	edgeTypes := make(map[string][]codeast.RelationType)
	for _, e := range result.Edges {
		key := e.FromID + " -> " + e.ToID
		edgeTypes[key] = append(edgeTypes[key], e.Type)
	}

	// Verify INHERITS edge: HTTPHandler -> BaseHandler
	// BaseHandler is in the same module so it won't be resolved with module prefix.
	inheritKey := "sample.HTTPHandler -> BaseHandler"
	if types, ok := edgeTypes[inheritKey]; !ok {
		t.Errorf("missing INHERITS edge: %s", inheritKey)
	} else {
		found := false
		for _, rt := range types {
			if rt == codeast.RelationInherits {
				found = true
			}
		}
		if !found {
			t.Errorf("edge %s has types %v, want INHERITS", inheritKey, types)
		}
	}

	// Verify METHOD edges
	methodKey := "sample.HTTPHandler -> sample.HTTPHandler.handle"
	if types, ok := edgeTypes[methodKey]; !ok {
		t.Errorf("missing METHOD edge: %s", methodKey)
	} else {
		found := false
		for _, rt := range types {
			if rt == codeast.RelationMethod {
				found = true
			}
		}
		if !found {
			t.Errorf("edge %s has types %v, want METHOD", methodKey, types)
		}
	}

	// Verify NO IMPORTS edges are emitted (aligns with Go reader; avoids
	// dangling external-module vertices). Imports stay on node.Imports only.
	for _, e := range result.Edges {
		if e.Type == codeast.RelationImports {
			t.Errorf("unexpected IMPORTS edge: %s -> %s", e.FromID, e.ToID)
		}
	}

	// Verify CALLS edges exist
	hasCalls := false
	for _, e := range result.Edges {
		if e.Type == codeast.RelationCalls {
			hasCalls = true
			break
		}
	}
	if !hasCalls {
		t.Error("no CALLS edges found")
	}
}

func TestBuildNodeEmbeddingText(t *testing.T) {
	node := &codeast.Node{
		ID:        "mymodule.MyClass",
		Type:      codeast.EntityClass,
		Name:      "MyClass",
		FullName:  "mymodule.MyClass",
		Package:   "mymodule",
		FilePath:  "mymodule.py",
		Signature: "class MyClass(BaseClass)",
		Comment:   "A test class.",
	}
	text := BuildNodeEmbeddingText(node)
	if text == "" {
		t.Fatal("embedding text is empty")
	}
	if len(text) < 10 {
		t.Errorf("embedding text too short: %s", text)
	}
}
