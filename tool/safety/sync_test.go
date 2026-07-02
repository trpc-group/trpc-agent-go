//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestExampleFixturesInSync guards against drift between the unit-test
// fixtures under testdata/ and their copies shipped with the runnable example.
// The two copies exist on purpose — testdata/ is the Go-conventional fixture
// directory read by the tests, while examples/tool_safety_guard/ must be
// self-contained so `go run .` works standalone (and the two live in separate
// Go modules). This test keeps them byte-identical so a policy/sample change in
// one is never silently missed in the other.
func TestExampleFixturesInSync(t *testing.T) {
	exampleDir := filepath.Join("..", "..", "examples", "tool_safety_guard")
	for _, name := range []string{"tool_safety_policy.yaml", "samples.json"} {
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read testdata/%s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(exampleDir, name))
		if err != nil {
			t.Fatalf("read example/%s: %v", name, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s drifted between testdata/ and examples/tool_safety_guard/; re-sync the two copies", name)
		}
	}
}
