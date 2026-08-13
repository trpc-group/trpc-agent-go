//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"testing"
)

func TestCleanResourcePathRejectsTraversalAndProviderLocators(t *testing.T) {
	for _, value := range []string{
		"/etc/passwd",
		"docs/../secret",
		"hdfs://cluster/secret",
		"file:/etc/passwd",
		"C:/secret",
		"./hdfs://cluster/secret",
		"./file:/etc/passwd",
		"./C:/secret",
	} {
		if cleaned, ok := cleanResourcePath(value); ok {
			t.Fatalf("cleanResourcePath(%q) = %q, want rejection", value, cleaned)
		}
	}
}
